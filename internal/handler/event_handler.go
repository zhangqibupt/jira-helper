package handler

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"jira_whisperer/internal/logger"
	"log"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Azure/azure-sdk-for-go/sdk/ai/azopenai"
	"github.com/gin-gonic/gin"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"jira_whisperer/internal/service/openai"
	"jira_whisperer/internal/storage"
)

type SlackHandler struct {
	api        *slack.Client
	mcpClient  *client.Client
	aiClient   *openai.Client
	tokenStore storage.TokenStore
}

// HistoryMessage represents a message in the conversation history
type HistoryMessage struct {
	Role    string // "user" or "assistant"
	Content string
}

func NewSlackHandler(token string, aiEndpoint string, aiKey string, aiDeployment string, tokenStore storage.TokenStore) (*SlackHandler, error) {
	// Ensure cache directory exists
	cacheDir := "/tmp/uvx-cache"
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %v", err)
	}

	logger.GetLogger().Info("Starting uvx client with debug logging enabled")

	// Create log file for uvx output
	logFile, err := os.OpenFile("/tmp/uvx.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create uvx log file: %v", err)
	}
	// Close the write-only log file as we'll reopen it for reading
	logFile.Close()

	mcpClient, err := client.NewStdioMCPClient(
		"uvx", []string{
			"UV_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple",
			"UV_CACHE_DIR=/tmp/uvx-cache",
		},
		"mcp-atlassian",
		"--jira-url=https://jira.freewheel.tv",
		"--jira-personal-token=MTE4MjU2NDAwNTU0Og/vNsKe89/quErTcSkk6XDr/u0O",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP client: %v", err)
	}

	// Set up a goroutine to monitor the log file
	go func() {
		// Open the file for reading in the monitoring goroutine
		readLogFile, err := os.OpenFile("/tmp/uvx.log", os.O_RDONLY, 0644)
		if err != nil {
			logger.GetLogger().Error("failed to open uvx log file for reading", zap.Error(err))
			return
		}
		defer readLogFile.Close()

		// Seek to the end of the file
		if _, err := readLogFile.Seek(0, 2); err != nil {
			logger.GetLogger().Error("failed to seek to end of uvx log file", zap.Error(err))
			return
		}

		// Create a scanner to read the log file
		scanner := bufio.NewScanner(readLogFile)
		for {
			for scanner.Scan() {
				logger.GetLogger().Debug("uvx log", zap.String("message", scanner.Text()))
			}
			if err := scanner.Err(); err != nil {
				logger.GetLogger().Error("error reading uvx log file", zap.Error(err))
				return
			}

			// Wait a short time before checking for new content
			time.Sleep(100 * time.Millisecond)

			// Check if there's new content
			currentPos, err := readLogFile.Seek(0, 1) // Get current position
			if err != nil {
				logger.GetLogger().Error("failed to get current file position", zap.Error(err))
				return
			}

			fileInfo, err := readLogFile.Stat()
			if err != nil {
				logger.GetLogger().Error("failed to get file info", zap.Error(err))
				return
			}

			if currentPos < fileInfo.Size() {
				// There's new content, continue reading
				continue
			}
		}
	}()

	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	logger.GetLogger().Info("Initializing MCP client")
	initResult, err := mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			logger.GetLogger().Fatal("MCP client initialization timed out")
		} else {
			logger.GetLogger().Fatal("MCP client initialization failed", zap.Error(err))
		}
	}
	log.Printf("Successfully initialized client: %v", initResult)

	aiClient, err := openai.NewClient(aiEndpoint, aiKey, aiDeployment)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAI client: %v", err)
	}

	return &SlackHandler{
		api:        slack.New(token),
		mcpClient:  mcpClient,
		aiClient:   aiClient,
		tokenStore: tokenStore,
	}, nil
}

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
			if err := h.handleEvent(event); err != nil {
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

const defaultErrorMessage = "Something went wrong while processing your request. Please try again later or contact @qzhang for help."

// handleMessageEvent handles direct messages and channel messages that mention the bot
func (h *SlackHandler) handleMessageEvent(ev *slackevents.MessageEvent) error {
	// Ignore messages from bots to prevent loops
	if ev.BotID != "" || ev.SubType == "bot_message" {
		return nil
	}

	// Only handle direct messages (DMs) or messages that mention the bot
	botInfo, err := h.api.AuthTest()
	if err != nil {
		return fmt.Errorf("failed to get bot info: %v", err)
	}

	// Check if this is a direct message (including multi-person IMs)
	isDM := ev.ChannelType == "im" || ev.ChannelType == "mpim"
	// Check if the message mentions the bot
	isBotMention := strings.Contains(ev.Text, fmt.Sprintf("<@%s>", botInfo.UserID))

	// Skip if not a DM and not mentioning the bot
	if !isDM && !isBotMention {
		return nil
	}

	// For non-DM channels, remove the bot mention from the text to clean up the query
	text := ev.Text
	if !isDM && isBotMention {
		text = strings.ReplaceAll(text, fmt.Sprintf("<@%s>", botInfo.UserID), "")
		text = strings.TrimSpace(text)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var history []HistoryMessage
	// If this is a message in a thread, get the thread history
	threadTS := ev.ThreadTimeStamp
	if threadTS == "" {
		// If the message is not in a thread, use the message's timestamp as the thread's start
		threadTS = ev.TimeStamp
	} else {
		// If the message is in a thread, get the thread history
		history, err = h.getThreadHistory(ev.Channel, threadTS)
		if err != nil {
			_ = h.sendSlackMessage(ev.Channel, defaultErrorMessage, threadTS)
			logger.GetLogger().Error(fmt.Sprintf("failed to get thread history: %v", err))
			return fmt.Errorf("failed to get thread history: %v", err)
		}
	}

	// Process the query with context
	response, err := h.processQuery(ctx, text, history, ev.Channel, threadTS)
	if err != nil {
		return fmt.Errorf("failed to process query: %v", err)
	}
	// Post the response in the thread
	_ = h.sendMarkdownMessage(ev.Channel, response, threadTS)

	return nil
}

// handleEvent handles app mention events
func (h *SlackHandler) handleEvent(ev *slackevents.AppMentionEvent) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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
			_ = h.sendSlackMessage(ev.Channel, defaultErrorMessage, threadTS)
			logger.GetLogger().Error(fmt.Sprintf("failed to get thread history: %v", err))
			return fmt.Errorf("failed to get thread history: %v", err)
		}
	}

	// Process the query with context
	response, err := h.processQuery(ctx, ev.Text, history, ev.Channel, threadTS)
	if err != nil {
		return fmt.Errorf("failed to process query: %v", err)
	}

	// Post the response in the thread
	_ = h.sendSlackMessage(ev.Channel, response, threadTS)

	return nil
}

func (h *SlackHandler) sendSlackMessage(channel string, message string, threadTS string) error {
	_, _, err := h.api.PostMessage(
		channel,
		slack.MsgOptionText(message, false),
		slack.MsgOptionTS(threadTS))
	if err != nil {
		logger.GetLogger().Error(fmt.Sprintf("failed to post message due to %s", err))
	}
	return err
}

func (h *SlackHandler) sendEphemeralSlackMessage(channel string, message string, threadTS string) error {
	_, _, err := h.api.PostMessage(
		channel,
		slack.MsgOptionText(message, false),
		slack.MsgOptionTS(threadTS))
	if err != nil {
		logger.GetLogger().Error(fmt.Sprintf("failed to post message due to %s", err))
	}
	return err
}

// sendMarkdownMessage sends a message to Slack with Markdown formatting enabled
func (h *SlackHandler) sendMarkdownMessage(channel string, message string, threadTS string) error {
	// Create a section block with markdown-enabled text
	blockText := slack.NewTextBlockObject(slack.MarkdownType, message, false, false)
	section := slack.NewSectionBlock(blockText, nil, nil)

	_, _, err := h.api.PostMessage(
		channel,
		slack.MsgOptionBlocks(section),
		slack.MsgOptionTS(threadTS))
	if err != nil {
		logger.GetLogger().Error(fmt.Sprintf("failed to post markdown message due to %s", err))
	}
	return err
}

// Update processQuery to handle conversation history
func (h *SlackHandler) processQuery(ctx context.Context, query string, history []HistoryMessage, channelID string, threadTS string) (string, error) {
	// Send initial progress message
	if err := h.sendSlackMessage(channelID, "🤔 Analyzing your request to determine the best way to help...", threadTS); err != nil {
		logger.GetLogger().Error("failed to send progress message", zap.Error(err))
	}

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

	maxRounds := 10
	currentRound := 0

	var finalResponse string
	for currentRound < maxRounds {
		response, err := h.aiClient.ChatWithTools(ctx, messages, openAITools)
		if err != nil {
			return "", fmt.Errorf("failed to get chat completion: %v", err)
		}
		logger.GetLogger().Info("received response from AI", zap.Any("response", response))

		if response.IsComplete {
			finalResponse = response.Content
			break
		}

		var toolResults []string
		for i, toolCall := range response.ToolCalls {
			progressMsg := fmt.Sprintf("⚙️ Step %d/%d: %s with parameters: `%s`", i+1, len(response.ToolCalls), toolCall.Name, toolCall.Args)
			_ = h.sendSlackMessage(channelID, progressMsg, threadTS)

			request := mcp.CallToolRequest{}
			request.Method = toolCall.Name
			request.Params.Name = toolCall.Name
			request.Params.Arguments = toolCall.Args

			result, err := h.mcpClient.CallTool(ctx, request)
			if err != nil {
				messages = append(messages, &azopenai.ChatRequestUserMessage{
					Content: azopenai.NewChatRequestUserMessageContent(fmt.Sprintf("Error occurred when calling tool %s: %v. Please suggest how to handle this error or try an alternative approach.", toolCall.Name, err)),
				})
				_ = h.sendSlackMessage(channelID, fmt.Sprintf("Error occurred when calling tool %s: %v. Please suggest how to handle this error or try an alternative approach.", toolCall.Name, err), threadTS)
				continue
			}

			toolCall.Response = result
			toolResults = append(toolResults, fmt.Sprintf("%s: %s", toolCall.Name, printToolResult(result)))

			messages = append(messages, &azopenai.ChatRequestAssistantMessage{
				Content: azopenai.NewChatRequestAssistantMessageContent(fmt.Sprintf("The tool `%s` was called with arguments `%v`, and the result is: %s",
					toolCall.Name, toolCall.Args, printToolResult(result))),
			})
		}

		currentRound++

		if currentRound >= maxRounds {
			warningMsg := "⚠️ Reached maximum number of steps. Providing partial response based on current progress..."
			_ = h.sendSlackMessage(channelID, warningMsg, threadTS)

			partialResponse := fmt.Sprintf("Reached maximum conversation rounds. Last response: %s\nTool results: %s",
				response.Content, strings.Join(toolResults, "\n"))
			//formattedResponse, err := h.formatAIResponseForSlack(ctx, partialResponse)
			//if err != nil {
			//	logger.GetLogger().Error("failed to format partial response", zap.Error(err))
			//	return partialResponse, nil
			//}
			return partialResponse, nil
		}
	}

	// Format the final response
	//formattedResponse, err := h.formatAIResponseForSlack(ctx, finalResponse)
	//if err != nil {
	//	logger.GetLogger().Error("failed to format final response", zap.Error(err))
	//	return finalResponse, nil
	//}

	return finalResponse, nil
}

func printToolResult(result *mcp.CallToolResult) string {
	for _, content := range result.Content {
		if textContent, ok := content.(mcp.TextContent); ok {
			return textContent.Text
		} else {
			jsonBytes, _ := json.MarshalIndent(content, "", "  ")
			return string(jsonBytes)
		}
	}
	return ""
}

// createInitialMessages creates the initial message list
func (h *SlackHandler) createInitialMessages(query string, history []HistoryMessage) []azopenai.ChatRequestMessageClassification {
	// Create system message
	systemMessage := &azopenai.ChatRequestSystemMessage{
		Content: azopenai.NewChatRequestSystemMessageContent(
			`You are a specialized Jira assistant, designed to help users manage and interact with Jira issues, projects, and workflows effectively. You have access to various Jira-related tools through the MCP server that allow you to perform actions like searching issues, creating tickets, managing epics, and more.

		Your primary responsibilities:

		1. Issue Management
		- Help users create, update, and search for Jira issues
		- Assist with epic management and linking issues to epics
		- Guide users through issue transitions and workflow states
		- Help retrieve and analyze issue details, comments, and worklog entries
		- For ESC ticket, you can search simliar tickets to provide suggestions.

		Communication Guidelines:
		1. Be professional and clear in your responses
		2. Use markdown formatting for better readability
		3. When referencing Jira issues, always include the issue key (e.g., PROJ-123) as a clickable link
		4. Explain your actions before performing them
		5. If a request is unclear, ask for clarification
		6. Provide context when displaying search results

		When displaying Jira issue details:
		1. Format the output in a clean, easy-to-read way using markdown
		2. Make issue keys and URLs clickable links
		3. Group related information together
		4. Highlight important fields like Status and Priority
		5. Remove unnecessary markdown symbols and formatting
		6. Skip avatar images and other non-essential visual elements
		7. Use emojis sparingly to highlight key information
		8. Present dates in a human-readable format

		Best Practices:
		1. Always verify issue keys before performing actions

		Error Handling:
		1. If you are unsure about the answer to the USER's request or how to satiate their request, you should gather more information. This can be done by asking the USER for more information.
		2. Bias towards not asking the user for help if you can find the answer yourself.

		When using these tools:
		1. Always validate input parameters
		2. Use appropriate error handling
		3. Respect rate limits and system constraints
		4. Follow Jira best practices


		Please respond using only Slack-compatible Markdown syntax. This will be rendered inside a Slack Block Kit message using slack.NewTextBlockObject(slack.MarkdownType, ...), so your response must strictly follow Slack’s supported Markdown rules:
		1. Use *bold*, _italic_, and ~strikethrough~ only — no other formatting styles like #, ###, __, or backticks.
		2. Use \n to break lines manually.
		3. Use > to create block quotes (optional).
		4. Use - or • for bullet lists.
		5. Use <https://url|display_text> to format links.

		Start each interaction by understanding the user's needs and then utilize the appropriate tools to help them achieve their goals.`),
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

// formatAIResponseForSlack formats the AI response to be more user-friendly for Slack
func (h *SlackHandler) formatAIResponseForSlack(ctx context.Context, response string) (string, error) {
	// Create a formatting prompt
	formattingPrompt := []azopenai.ChatRequestMessageClassification{
		&azopenai.ChatRequestSystemMessage{
			Content: azopenai.NewChatRequestSystemMessageContent(
				"You are a Slack message formatter. Your job is to take raw responses and format them for optimal readability in Slack.\n\n" +
					"Follow these formatting rules:\n" +
					"1. Clean and Simple:\n" +
					"   - Remove unnecessary symbols like underscores, dashes, and extra newlines\n" +
					"   - Remove redundant section markers (---, ===, etc.)\n" +
					"   - Keep only one blank line between sections\n" +
					"2. Headers and Sections:\n" +
					"   - Use *bold* for section headers\n" +
					"   - Don't use underscores or dashes for section separation\n" +
					"   - Format headers as '*Section Name*'\n" +
					"3. Content Formatting:\n" +
					"   - Use bullet points (•) instead of dashes for lists\n" +
					"   - Remove redundant bold markers (**) when not needed\n" +
					"   - Format field names in bold: '*Field:* Value'\n" +
					"   - Keep dates and times in their original format\n" +
					"4. Links and References:\n" +
					"   - Format Jira tickets as <https://jira.freewheel.tv/browse/TICKET-123|TICKET-123>\n" +
					"   - Format URLs as <URL|text>\n" +
					"   - Format email addresses as <mailto:email@domain.com|email@domain.com>\n" +
					"5. Special Formatting:\n" +
					"   - Use emojis strategically: 📋 for summaries, 👤 for people, 📅 for dates\n" +
					"   - Use `code` blocks only for actual code or technical values\n" +
					"   - Maintain proper spacing around sections\n\n" +
					"Your output should be clean, professional, and free of unnecessary formatting symbols.",
			),
		},
		&azopenai.ChatRequestUserMessage{
			Content: azopenai.NewChatRequestUserMessageContent("Please format this response for Slack: " + response),
		},
	}

	// Get formatted response
	formattedResponse, err := h.aiClient.Chat(ctx, formattingPrompt)
	if err != nil {
		return response, fmt.Errorf("failed to format response: %v", err)
	}

	return formattedResponse, nil
}
