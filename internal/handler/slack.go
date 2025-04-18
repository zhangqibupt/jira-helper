package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/ai/azopenai"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"jira_whisperer/internal/service/openai"
)

type SlackHandler struct {
	api       *slack.Client
	mcpClient *client.Client
	aiClient  *openai.Client
}

// HistoryMessage represents a message in the conversation history
type HistoryMessage struct {
	Role    string // "user" or "assistant"
	Content string
}

func NewSlackHandler(token string, aiEndpoint string, aiKey string, aiDeployment string, mcpPath string) (*SlackHandler, error) {
	mcpClient, err := client.NewStdioMCPClient(
		mcpPath,
		[]string{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP client: %v", err)
	}

	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}
	initResult, err := mcpClient.Initialize(context.Background(), initRequest)
	if err != nil {
		log.Fatalf("Failed to initialize: %v", err)
	}
	log.Printf("Successfully initialized client: %v", initResult)

	aiClient, err := openai.NewClient(aiEndpoint, aiKey, aiDeployment)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAI client: %v", err)
	}

	return &SlackHandler{
		api:       slack.New(token),
		mcpClient: mcpClient,
		aiClient:  aiClient,
	}, nil
}

func (h *SlackHandler) HandleEvent(ev *slackevents.MessageEvent) error {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	// Ignore messages from bots to prevent loops
	if ev.BotID != "" {
		return nil
	}

	var history []HistoryMessage
	var err error

	// If this is a message in a thread, get the thread history
	threadTS := ev.ThreadTimeStamp
	if threadTS == "" {
		// If the message is not in a thread, use the message's timestamp as the thread's start
		threadTS = ev.TimeStamp
	} else {
		// If the message is in a thread, get the thread history
		history, err = h.getThreadHistory(ev.Channel, threadTS)
		if err != nil {
			return fmt.Errorf("failed to get thread history: %v", err)
		}
	}

	// Process the query with context
	response, err := h.processQuery(ctx, ev.Text, history)
	if err != nil {
		return fmt.Errorf("failed to process query: %v", err)
	}

	// Post the response in the thread
	_, _, err = h.api.PostMessage(
		ev.Channel,
		slack.MsgOptionText(response, false),
		slack.MsgOptionTS(threadTS),
	)
	if err != nil {
		return fmt.Errorf("failed to post message: %v", err)
	}

	return nil
}

// Update processQuery to handle conversation history
func (h *SlackHandler) processQuery(ctx context.Context, query string, history []HistoryMessage) (string, error) {
	tools, err := h.mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return "", fmt.Errorf("failed to list tools: %v", err)
	}

	var openAITools []openai.Tool
	for _, tool := range tools.Tools {
		openAITools = append(openAITools, openai.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  string(mustMarshal(tool.InputSchema)),
		})
	}

	messages := h.createInitialMessages(query, history)

	maxRounds := 5
	currentRound := 0

	var finalResponse string
	for currentRound < maxRounds {
		response, err := h.aiClient.ChatWithTools(ctx, messages, openAITools)
		if err != nil {
			return "", fmt.Errorf("failed to get chat completion: %v", err)
		}

		if response.IsComplete {
			finalResponse = response.Content
			break
		}

		var toolResults []string
		for _, toolCall := range response.ToolCalls {
			// Execute tool call
			request := mcp.CallToolRequest{}
			request.Params.Name = toolCall.Name
			request.Params.Arguments = toolCall.Args

			result, err := h.mcpClient.CallTool(ctx, request)
			if err != nil {
				return "", fmt.Errorf("failed to call tool %s: %v", toolCall.Name, err)
			}

			// Save tool call result
			toolCall.Response = result

			// Add tool call result to message history
			messages = append(messages, &azopenai.ChatRequestFunctionMessage{
				Content: to.Ptr(response.Content),
			})
		}

		currentRound++

		// If maximum rounds reached but not complete, return partial result
		if currentRound >= maxRounds {
			return fmt.Sprintf("Reached maximum conversation rounds. Last response: %s\nTool results: %s",
				response.Content, strings.Join(toolResults, "\n")), nil
		}
	}

	return finalResponse, nil
}

// createInitialMessages creates the initial message list
func (h *SlackHandler) createInitialMessages(query string, history []HistoryMessage) []azopenai.ChatRequestMessageClassification {
	// Create system message
	systemMessage := &azopenai.ChatRequestSystemMessage{
		Content: azopenai.NewChatRequestSystemMessageContent(
			"You are a helpful assistant. You have access to tools to help answer questions. " +
				"Use them when appropriate. You can use multiple tools if needed to complete a task. " +
				"Always explain your actions and the results clearly."),
	}

	// Create message array
	var messages []azopenai.ChatRequestMessageClassification
	messages = append(messages, systemMessage)

	// Add history messages
	for _, msg := range history {
		switch msg.Role {
		case "user":
			messages = append(messages, &azopenai.ChatRequestUserMessage{
				Content: azopenai.NewChatRequestUserMessageContent(msg.Content),
			})
		case "assistant":
			messages = append(messages, &azopenai.ChatRequestAssistantMessage{
				Content: azopenai.NewChatRequestAssistantMessageContent(msg.Content),
			})
		}
	}

	// Add current query
	messages = append(messages, &azopenai.ChatRequestUserMessage{
		Content: azopenai.NewChatRequestUserMessageContent(query),
	})

	return messages
}

// mustMarshal helper function for JSON serialization
func mustMarshal(v interface{}) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

// getThreadHistory retrieves the conversation history from a thread
func (h *SlackHandler) getThreadHistory(channelID, threadTS string) ([]HistoryMessage, error) {
	var allMessages []slack.Message
	params := &slack.GetConversationRepliesParameters{
		ChannelID: channelID,
		Timestamp: threadTS,
		Limit:     100, // messages per page
	}

	for {
		messages, hasMore, nextCursor, err := h.api.GetConversationReplies(params)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch thread history: %v", err)
		}

		allMessages = append(allMessages, messages...)

		if !hasMore {
			break
		}
		params.Cursor = nextCursor
	}

	// Format messages for context, excluding bot messages
	var historyMessages []HistoryMessage
	for _, msg := range allMessages {
		role := "user"
		if msg.BotID != "" {
			role = "assistant"
		}
		historyMessages = append(historyMessages, HistoryMessage{
			Role:    role,
			Content: msg.Text,
		})
	}

	return historyMessages, nil
}
