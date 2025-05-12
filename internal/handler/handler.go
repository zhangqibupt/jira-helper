package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"jira_helper/internal/logger"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"go.uber.org/zap"

	"github.com/Azure/azure-sdk-for-go/sdk/ai/azopenai"
	"github.com/gin-gonic/gin"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"jira_helper/internal/service/openai"
)

func (h *SlackHandler) HandleRequest(c *gin.Context) {
	logger := logger.GetLogger()

	// Read request body
	body, err := c.GetRawData()
	if err != nil || len(body) == 0 {
		logger.Error("empty request body")
		c.JSON(200, gin.H{"error": "empty request body"})
		return
	}

	// Parse the Slack event
	eventsAPIEvent, err := slackevents.ParseEvent(
		json.RawMessage(body),
		slackevents.OptionNoVerifyToken(),
	)
	if err != nil {
		logger.Error("failed to parse slack event", zap.Error(err))
		c.JSON(200, gin.H{"error": "failed to parse slack event"})
		return
	}

	// Handle URL verification challenge
	if eventsAPIEvent.Type == slackevents.URLVerification {
		var challenge *slackevents.ChallengeResponse
		if err := json.Unmarshal(body, &challenge); err != nil {
			logger.Error("failed to unmarshal challenge", zap.Error(err))
			c.JSON(400, gin.H{"error": "failed to parse challenge"})
			return
		}
		c.Header("Content-Type", "text/plain")
		c.String(200, string(body))
		return
	}

	// Handle event callbacks
	if eventsAPIEvent.Type == slackevents.CallbackEvent {
		innerEvent := eventsAPIEvent.InnerEvent
		switch event := innerEvent.Data.(type) {
		case *slackevents.MessageEvent:
			if err := h.handleMessageEvent(event); err != nil {
				logger.Error("failed to handle message event", zap.Error(err))
				c.JSON(200, gin.H{"error": "failed to handle message event"})
				return
			}
		case *slackevents.AppMentionEvent:
			if err := h.handleAppMentionEvent(event); err != nil {
				logger.Error("failed to handle message event", zap.Error(err))
				c.JSON(200, gin.H{"error": "failed to handle message event"})
				return
			}
		default:
			logger.Warn("unsupported event type", zap.String("event_type", fmt.Sprintf("%T", innerEvent.Data)))
		}
	}

	// Return success response
	c.JSON(200, gin.H{"status": "ok"})
}

// getUserPersonalToken retrieves the user's personal token from the token store.
func (h *SlackHandler) getUserPersonalToken(userID string) (string, error) {
	if userID == "" {
		return "", nil
	}
	return h.tokenStore.GetToken(userID)
}

// processQuery handles the main conversation flow with the AI model
func (h *SlackHandler) processQuery(ctx context.Context, query string, history []HistoryMessage, channelID string, threadTS string, userID string) (string, error) {
	// Initialize and send progress message
	initialMessage := "⏳ Analyzing your request to determine the best way to help you..."
	timestamp, _ := h.sendMarkdownMessage(channelID, initialMessage, threadTS)
	slackMessageLines := []string{initialMessage}

	// Prepare tools and messages
	openAITools, messages, err := h.prepareConversation(ctx, query, history)
	if err != nil {
		return "", err
	}

	// Fetch user's personal token if available
	userToken, err := h.getUserPersonalToken(userID)
	if err != nil {
		_, _ = h.sendMarkdownMessage(channelID, fmt.Sprintf(defaultErrorMessage, err.Error()), threadTS)
		return "", fmt.Errorf("failed to get user personal token: %v", err)
	}

	// Run the conversation loop with the user token
	return h.runConversationLoop(ctx, channelID, threadTS, timestamp, messages, openAITools, slackMessageLines, userToken)
}

// prepareConversation sets up the tools and initial messages for the conversation
func (h *SlackHandler) prepareConversation(ctx context.Context, query string, history []HistoryMessage) ([]openai.Tool, []azopenai.ChatRequestMessageClassification, error) {
	// Get available tools
	tools, err := h.defaultMcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list tools: %v", err)
	}

	logger.GetLogger().Info("Available tools", zap.Any("tools", tools.Tools))

	// Convert tools to OpenAI format
	openAITools := h.convertToolsToOpenAIFormat(tools.Tools)

	// Create initial messages
	messages := h.createInitialMessages(query, history)

	return openAITools, messages, nil
}

// convertToolsToOpenAIFormat converts MCP tools to OpenAI tool format
func (h *SlackHandler) convertToolsToOpenAIFormat(tools []mcp.Tool) []openai.Tool {
	var openAITools []openai.Tool
	for _, tool := range tools {
		schemaBytes, _ := json.Marshal(tool.InputSchema)
		openAITools = append(openAITools, openai.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  string(ensureValidSchema(json.RawMessage(schemaBytes))),
		})
	}
	return openAITools
}

var writableJiraTools = []string{
	"jira_create_issue",
	"jira_batch_create_issues",
	"jira_update_issue",
	"jira_delete_issue",
	"jira_add_comment",
	"jira_add_worklog",
	"jira_link_to_epic",
	"jira_create_issue_link",
	"jira_remove_issue_link",
	"jira_transition_issue",
	"jira_create_sprint",
	"jira_update_sprint",
	"confluence_add_label",
	"confluence_create_page",
	"confluence_update_page",
	"confluence_delete_page",
}

// runConversationLoop handles the main conversation loop with the AI model
func (h *SlackHandler) runConversationLoop(ctx context.Context, channelID, threadTS, timestamp string, messages []azopenai.ChatRequestMessageClassification, openAITools []openai.Tool, slackMessageLines []string, userToken string) (string, error) {
	maxRounds := 20
	currentRound := 0

	// Get the appropriate MCP client for this user
	mcpClient, cleanup, err := h.getMcpClient(userToken)
	if err != nil {
		_, _ = h.sendMarkdownMessage(channelID, fmt.Sprintf(defaultErrorMessage, err.Error()), threadTS)
		return "", fmt.Errorf("failed to get MCP client: %v", err)
	}
	defer cleanup()

	for currentRound < maxRounds {
		// Trim messages if needed
		messages = h.trimMessages(messages)

		// Get AI response
		response, err := h.aiClient.ChatWithTools(ctx, messages, openAITools)
		if err != nil {
			_, _ = h.sendMarkdownMessage(channelID, fmt.Sprintf(defaultErrorMessage, err.Error()), threadTS)
			return "", fmt.Errorf("failed to get chat completion: %v", err)
		}

		// Handle complete response
		if response.IsComplete {
			return response.Content, nil
		}

		// Update progress with AI response
		if response.Content != "" {
			slackMessageLines = append(slackMessageLines, response.Content)
			_ = h.updateMessage(channelID, timestamp, strings.Join(slackMessageLines, "\n\n"))
		}

		// Handle tool calls
		for _, toolCall := range response.ToolCalls {
			// If the tool is in below list and userToken is empty, should not call and return error
			if slices.Contains(writableJiraTools, toolCall.Name) && userToken == "" {
				_, _ = h.sendMarkdownMessage(channelID, fmt.Sprintf("❌ Permission denied. You should set your personal token first to use `%s`", toolCall.Name), threadTS)
				return "", fmt.Errorf("you don't have permission to use this tool")
			}

			// Add tool call to messages
			messages = h.addToolCallToMessages(messages, toolCall)

			// Update progress with current tool
			slackMessage := fmt.Sprintf("🔄 _Calling Tool *%s*_", toolCall.Name)
			if len(toolCall.Args) > 0 {
				slackMessage += fmt.Sprintf("\n>_%s_", printJSON(toolCall.Args))
			}

			slackMessageLines = append(slackMessageLines, slackMessage)
			_ = h.updateMessage(channelID, timestamp, strings.Join(slackMessageLines, "\n\n"))

			// Execute tool and handle response
			toolResult, err := h.executeToolWithClient(ctx, toolCall, mcpClient)
			if err != nil {
				messages = append(messages, &azopenai.ChatRequestToolMessage{
					ToolCallID: &toolCall.ID,
					Content:    azopenai.NewChatRequestToolMessageContent(err.Error()),
				})
				continue
			}

			// Process successful tool result
			messages, timestamp, slackMessageLines = h.processToolResult(ctx, channelID, timestamp, threadTS, slackMessageLines, toolCall, toolResult, messages)
		}

		currentRound++

		// Check for maximum rounds
		if currentRound >= maxRounds {
			return h.handleMaxRoundsReached(channelID, threadTS, response.Content)
		}
	}

	return "", nil
}

// trimMessages ensures messages array doesn't exceed maximum size
func (h *SlackHandler) trimMessages(messages []azopenai.ChatRequestMessageClassification) []azopenai.ChatRequestMessageClassification {
	maxMessages := 21 // 1 system prompt + 20 recent messages
	if len(messages) > maxMessages {
		systemPrompt := messages[0:1]
		rest := messages[len(messages)-20:]
		return append(systemPrompt, rest...)
	}
	return messages
}

// addToolCallToMessages adds a tool call to the messages array
func (h *SlackHandler) addToolCallToMessages(messages []azopenai.ChatRequestMessageClassification, toolCall openai.ToolCall) []azopenai.ChatRequestMessageClassification {
	return append(messages, &azopenai.ChatRequestAssistantMessage{
		Content: azopenai.NewChatRequestAssistantMessageContent(""),
		ToolCalls: []azopenai.ChatCompletionsToolCallClassification{
			&azopenai.ChatCompletionsFunctionToolCall{
				ID:   to.Ptr(toolCall.ID),
				Type: to.Ptr("function"),
				Function: &azopenai.FunctionCall{
					Name:      to.Ptr(toolCall.Name),
					Arguments: to.Ptr(prettyPrintJSON(toolCall.Args)),
				},
			},
		},
	})
}

// executeToolWithClient executes a tool call using the provided MCP client.
func (h *SlackHandler) executeToolWithClient(ctx context.Context, toolCall openai.ToolCall, mcpClient interface {
	CallTool(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
}) (*mcp.CallToolResult, error) {
	request := mcp.CallToolRequest{
		Params: struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments,omitempty"`
			Meta      *struct {
				ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
			} `json:"_meta,omitempty"`
		}{
			Name:      toolCall.Name,
			Arguments: toolCall.Args,
		},
	}
	return mcpClient.CallTool(ctx, request)
}

// processToolResult handles a successful tool execution result
func (h *SlackHandler) processToolResult(ctx context.Context, channelID, timestamp string, threadTS string, slackMessageLines []string, toolCall openai.ToolCall, result *mcp.CallToolResult, messages []azopenai.ChatRequestMessageClassification) ([]azopenai.ChatRequestMessageClassification, string, []string) {
	// Format and summarize tool result
	toolResultStr := printToolResult(result)
	toolResultStr, _ = h.summarizeIfTooLong(ctx, toolResultStr)

	// Add tool response to messages
	messages = append(messages, &azopenai.ChatRequestToolMessage{
		ToolCallID: &toolCall.ID,
		Content:    azopenai.NewChatRequestToolMessageContent(toolResultStr),
	})

	// Update progress message
	title := h.formatToolCallMessage(toolCall.Name, toolCall.Args, nil)
	slackMessage := h.createCollapsibleBlocks(title, formatCallToolResult(toolResultStr), false)

	if h.shouldCreateNewMessage(slackMessageLines, slackMessage) {
		timestamp, _ = h.sendMarkdownMessage(channelID, slackMessage, threadTS)
		slackMessageLines = []string{}
	} else {
		slackMessageLines = append(slackMessageLines, slackMessage)
		_ = h.updateMessage(channelID, timestamp, strings.Join(slackMessageLines, "\n\n"))
	}
	slackMessageLines = append(slackMessageLines, slackMessage)

	return messages, timestamp, slackMessageLines
}

// handleMaxRoundsReached handles the case when maximum conversation rounds are reached
func (h *SlackHandler) handleMaxRoundsReached(channelID, threadTS, lastResponse string) (string, error) {
	warningMsg := "⚠️ Reached maximum number of steps. Providing partial response based on current progress..."
	_, _ = h.sendMarkdownMessage(channelID, warningMsg, threadTS)
	return fmt.Sprintf("Reached maximum conversation rounds. Last response: %s. \nDo you want me to continue?", lastResponse), nil
}

// createInitialMessages creates the initial message list
func (h *SlackHandler) createInitialMessages(query string, history []HistoryMessage) []azopenai.ChatRequestMessageClassification {
	// Create system message
	systemMessage := &azopenai.ChatRequestSystemMessage{
		Content: azopenai.NewChatRequestSystemMessageContent(
			`You are a Jira assistant that helps users manage Jira issues, projects, and workflows using tools provided by the MCP server.

Your main tasks:
- Create, update, and search for Jira issues
- Manage epics and link issues to epics
- Guide users through issue transitions and workflows
- Retrieve and summarize issue details, comments, and worklogs
- If users ask for similar issues, you should only search for issues in the same project
- Use Slack-supported markdown (e.g. *bold*, > quote), but avoid unsupported formatting (like headers #, tables, or HTML)

When using Jira MCP APIs:
- When using tool jira_get_issue, you should always use 'fields: *all' as parameter
- When using the search tool, try to use pagination to avoid too many results
- If batch operations is involved, you should use the batch tool first

Communication guidelines:
- Be professional and clear
- Use Slack markdown for formatting
- Always include clickable Jira issue keys (e.g., <https://jira.freewheel.tv/browse/PROJ-123|PROJ-123>)
- Explain your actions before performing them
- Ask for clarification if a request is unclear
- Provide context for search results

Thinking process:
1. Always explain your thought process before taking any action
2. When planning to use tools:
   - Explain why you need to use each tool
   - Describe what information you expect to get
   - Outline your plan for using the results
3. When encountering errors:
   - Explain what went wrong
   - Suggest possible solutions
   - Ask for clarification if needed
4. When making decisions:
   - Explain your reasoning
   - Consider alternatives
   - Justify your choices

When displaying Jira issue details:
- Use clean, easy-to-read Slack markdown
- Make issue keys and URLs clickable
- Group related information together
- Highlight important fields like Status and Priority using * instead of **
- Avoid unnecessary markdown and images
- Use emojis sparingly for emphasis
- Show dates in a human-readable format

Error handling:
- If unsure, gather more information or ask the user
- Prefer finding answers yourself before asking the user
- For epics, "Epic Link" refers to the epic an issue is linked to (customfield_10006)

Start by understanding the user's needs, then use the appropriate tools to help them.`),
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

// getThreadHistory retrieves the conversation history from a thread
func (h *SlackHandler) getThreadHistory(channelID, threadTS string) ([]HistoryMessage, error) {
	var allMessages []slack.Message
	params := &slack.GetConversationRepliesParameters{
		ChannelID: channelID,
		Timestamp: threadTS,
		Limit:     20,   // messages per page
		Inclusive: true, // Include the message with the specified timestamp
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

// formatToolCallMessage formats tool call messages in a more natural language way
func (h *SlackHandler) formatToolCallMessage(toolName string, args map[string]interface{}, err error) string {
	return h.msgFormatter.FormatToolCallMessage(toolName, args, err)
}

// createCollapsibleBlocks creates a collapsible message using Block Kit
func (h *SlackHandler) createCollapsibleBlocks(title string, content string, isError bool) string {
	// Create emoji based on message type
	emoji := "✅️"
	if isError {
		emoji = "❌"
	}

	// Format the message with title and content
	return fmt.Sprintf("%s _%s_\n%s", emoji, title, content)
}

// summarizeIfTooLong checks if the content is too long and summarizes it using the AI model if necessary.
func (h *SlackHandler) summarizeIfTooLong(ctx context.Context, content string) (string, error) {
	maxLen := 2000 // character threshold for summarization
	if len(content) <= maxLen {
		return content, nil
	}

	summaryPrompt := []azopenai.ChatRequestMessageClassification{
		&azopenai.ChatRequestSystemMessage{
			Content: azopenai.NewChatRequestSystemMessageContent(`
You are a summarization assistant. Your job is to condense lengthy tool results into plain-text key information that is easy to read in Slack.

Guidelines:
- Only output raw text. Do not use any Markdown syntax like **bold**, _italic_, > quote, or lists with bullets/symbols.
- Keep formatting plain and simple. For example, use "Status: IN PROGRESS", not "**Status**: IN PROGRESS".
- Remove all characters used for formatting or decoration.
- Group or summarize if content is too long, and note if anything is omitted.
- Always keep the summary under 2000 characters.

Output the result as plain text, suitable for direct posting in Slack *without* markdown.

			`),
		},
		&azopenai.ChatRequestUserMessage{
			Content: azopenai.NewChatRequestUserMessageContent(content),
		},
	}
	summarized, err := h.aiClient.Chat(ctx, summaryPrompt)
	if err != nil || summarized == "" {
		// If summarization fails, return the original content
		return content, err
	}
	return summarized, nil
}
