package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"jira_whisperer/internal/logger"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"go.uber.org/zap"

	"github.com/Azure/azure-sdk-for-go/sdk/ai/azopenai"
	"github.com/gin-gonic/gin"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"jira_whisperer/internal/service/openai"
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

// Update processQuery to handle conversation history
func (h *SlackHandler) processQuery(ctx context.Context, query string, history []HistoryMessage, channelID string, threadTS string) (string, error) {
	// Initialize message array
	var slackMessageLines []string
	slackMessageLines = append(slackMessageLines, "⏳ Analyzing your request to determine the best way to help you...")

	// Send initial progress message and save its timestamp
	_, timestamp, err := h.api.PostMessage(
		channelID,
		slack.MsgOptionText(slackMessageLines[0], false),
		slack.MsgOptionTS(threadTS),
	)
	if err != nil {
		logger.GetLogger().Error("failed to send progress message", zap.Error(err))
		return "", err
	}

	tools, err := h.mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		h.sendMarkdownMessage(channelID, fmt.Sprintf(defaultErrorMessage, err.Error()), threadTS)
		return "", fmt.Errorf("failed to list tools: %v", err)
	}

	var openAITools []openai.Tool
	logger.GetLogger().Info("available tools", zap.Any("tools", tools.Tools))
	for _, tool := range tools.Tools {
		schemaBytes, _ := json.Marshal(tool.InputSchema)
		openAITools = append(openAITools, openai.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  string(ensureValidSchema(json.RawMessage(schemaBytes))),
		})
	}

	messages := h.createInitialMessages(query, history)

	maxRounds := 20
	currentRound := 0

	var finalResponse string
	for currentRound < maxRounds {
		maxMessages := 21 // 1 system prompt + 20 recent messages
		if len(messages) > maxMessages {
			systemPrompt := messages[0:1]
			rest := messages[len(messages)-20:]
			messages = append(systemPrompt, rest...)
		}

		response, err := h.aiClient.ChatWithTools(ctx, messages, openAITools)
		if err != nil {
			_ = h.sendMarkdownMessage(channelID, fmt.Sprintf(defaultErrorMessage, err.Error()), threadTS)
			return "", fmt.Errorf("failed to get chat completion: %v", err)
		}
		logger.GetLogger().Info("received response from AI", zap.Any("response", response))

		if response.IsComplete {
			finalResponse = response.Content
			break
		}

		if response.Content != "" {
			var updateErr error
			timestamp, updateErr = h.updateMessage(channelID, timestamp, slackMessageLines, response.Content)
			if updateErr != nil {
				logger.GetLogger().Error("failed to update message", zap.Error(updateErr))
			}
			slackMessageLines = append(slackMessageLines, response.Content)
		}

		for _, toolCall := range response.ToolCalls {
			// Add the tool_calls message to messages first
			messages = append(messages, &azopenai.ChatRequestAssistantMessage{
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

			// Update progress with current tool
			message := fmt.Sprintf("🔄 _Calling Tool *%s*_", toolCall.Name)
			if len(toolCall.Args) > 0 {
				message += fmt.Sprintf("\n>_%s_", printJSON(toolCall.Args))
			}
			var updateErr error
			timestamp, updateErr = h.updateMessage(channelID, timestamp, slackMessageLines, message)
			if updateErr != nil {
				logger.GetLogger().Error("failed to update message", zap.Error(updateErr))
			}
			slackMessageLines = append(slackMessageLines, message)

			request := mcp.CallToolRequest{}
			request.Method = toolCall.Name
			request.Params.Name = toolCall.Name
			request.Params.Arguments = toolCall.Args

			result, err := h.mcpClient.CallTool(ctx, request)
			if err != nil {
				// Add the error as a user message instead of a tool message
				messages = append(messages, &azopenai.ChatRequestToolMessage{
					ToolCallID: &toolCall.ID,
					Content:    azopenai.NewChatRequestToolMessageContent(err.Error()),
				})
				title := h.formatToolCallMessage(toolCall.Name, toolCall.Args, err)
				message := h.createCollapsibleBlocks(title, formatCallToolResult(err.Error()), true)
				timestamp, updateErr = h.updateMessage(channelID, timestamp, slackMessageLines, message)
				if updateErr != nil {
					logger.GetLogger().Error("failed to update message", zap.Error(updateErr))
				}
				slackMessageLines = append(slackMessageLines, message)
				continue
			}

			toolResultStr := printToolResult(result)
			toolResultStr, _ = h.summarizeIfTooLong(ctx, toolResultStr)

			// Add the tool response as a tool message
			messages = append(messages, &azopenai.ChatRequestToolMessage{
				ToolCallID: &toolCall.ID,
				Content:    azopenai.NewChatRequestToolMessageContent(toolResultStr),
			})

			title := h.formatToolCallMessage(toolCall.Name, toolCall.Args, nil)
			message = h.createCollapsibleBlocks(title, formatCallToolResult(toolResultStr), false)
			timestamp, updateErr = h.updateMessage(channelID, timestamp, slackMessageLines, message)
			if updateErr != nil {
				logger.GetLogger().Error("failed to update message", zap.Error(updateErr))
			}
			slackMessageLines = append(slackMessageLines, message)
		}

		currentRound++

		if currentRound >= maxRounds {
			warningMsg := "⚠️ Reached maximum number of steps. Providing partial response based on current progress..."
			_ = h.sendMarkdownMessage(channelID, fmt.Sprintf(defaultErrorMessage, warningMsg), threadTS)

			partialResponse := fmt.Sprintf("Reached maximum conversation rounds. Last response: %s", response.Content)
			return partialResponse, nil
		}
	}

	return finalResponse, nil
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
