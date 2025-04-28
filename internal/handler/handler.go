package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"jira_whisperer/internal/logger"

	"go.uber.org/zap"

	"github.com/Azure/azure-sdk-for-go/sdk/ai/azopenai"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
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

const defaultErrorMessage = "Something went wrong while processing your request. Please try again later or contact @qzhang for help."

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

// prettyPrintJSON formats any value as a pretty-printed JSON string
func prettyPrintJSON(v interface{}) string {
	prettyJSON, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		logger.GetLogger().Error("failed to marshal to JSON", zap.Error(err))
		return fmt.Sprintf("%v", v) // fallback to string representation if marshaling fails
	}
	return string(prettyJSON)
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
You are a summarization assistant. Your job is to condense lengthy tool results into concise, key information that is easy to read in Slack.

Guidelines:
- Focus on the most important and relevant details for the user's request.
- Use clean, professional Slack markdown formatting.
- For each item, only include key fields (e.g., key, summary, status, assignee), depend on which field is most relevant to user's request.
- Remove unnecessary technical details, raw JSON, or verbose metadata.
- If the content is too long, group or summarize similar items.
- Always keep the summary under 3000 characters if possible.
- Add a note if some content is omitted due to length.

Format your output for direct posting in Slack.
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

// Update processQuery to handle conversation history
func (h *SlackHandler) processQuery(ctx context.Context, query string, history []HistoryMessage, channelID string, threadTS string) (string, error) {
	// Send initial progress message
	if err := h.sendMarkdownMessage(channelID, "⏳ Analyzing your request to determine the best way...", threadTS); err != nil {
		logger.GetLogger().Error("failed to send progress message", zap.Error(err))
	}

	tools, err := h.mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return "", fmt.Errorf("failed to list tools: %v", err)
	}

	var openAITools []openai.Tool
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
		// 裁剪消息，防止过长
		maxMessages := 21 // 1 system prompt + 20 recent messages
		if len(messages) > maxMessages {
			systemPrompt := messages[0:1]
			rest := messages[len(messages)-20:]
			messages = append(systemPrompt, rest...)
		}

		response, err := h.aiClient.ChatWithTools(ctx, messages, openAITools)
		if err != nil {
			_ = h.sendMarkdownMessage(channelID, fmt.Sprintf("❌ Error occurred when processing your request: %s", defaultErrorMessage), threadTS)
			return "", fmt.Errorf("failed to get chat completion: %v", err)
		}
		logger.GetLogger().Info("received response from AI", zap.Any("response", response))

		if response.IsComplete {
			finalResponse = response.Content
			break
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

			progressMsg := fmt.Sprintf("⚙️ Calling tool *%s* with parameters: ```%s```", toolCall.Name, prettyPrintJSON(toolCall.Args))
			_ = h.sendMarkdownMessage(channelID, progressMsg, threadTS)

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
				_ = h.sendMarkdownMessage(channelID, fmt.Sprintf("❌ Error occurred when calling tool *%s*: %v. Please suggest how to handle this error or try an alternative approach.", toolCall.Name, err), threadTS)
				continue
			}

			toolResultStr := printToolResult(result)
			toolResultStr, _ = h.summarizeIfTooLong(ctx, toolResultStr)

			// Add the tool response as a tool message
			messages = append(messages, &azopenai.ChatRequestToolMessage{
				ToolCallID: &toolCall.ID,
				Content:    azopenai.NewChatRequestToolMessageContent(toolResultStr),
			})
			_ = h.sendMarkdownMessage(channelID, fmt.Sprintf("✅ The tool *%s* was called successfully, response is ```%s```.", toolCall.Name, toolResultStr), threadTS)
		}

		currentRound++

		if currentRound >= maxRounds {
			warningMsg := "⚠️ Reached maximum number of steps. Providing partial response based on current progress..."
			_ = h.sendMarkdownMessage(channelID, warningMsg, threadTS)

			partialResponse := fmt.Sprintf("Reached maximum conversation rounds. Last response: %s", response.Content)
			return partialResponse, nil
		}
	}
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
			`You are a Jira assistant that helps users manage Jira issues, projects, and workflows using tools provided by the MCP server.

Your main tasks:
- Create, update, and search for Jira issues
- Manage epics and link issues to epics
- Guide users through issue transitions and workflows
- Retrieve and summarize issue details, comments, and worklogs
- For ESC tickets, suggest similar tickets

When using Jira MCP APIs:
- If you can't find expected information, try specifying fields: *all
- When using the search tool, try to use pagination to avoid too many results
- If batch operations is involved, you should use the batch tool first

Communication guidelines:
- Be professional and clear
- Use Slack markdown for formatting
- Always include clickable Jira issue keys (e.g., <https://jira.freewheel.tv/browse/PROJ-123|PROJ-123>)
- Explain your actions before performing them
- Ask for clarification if a request is unclear
- Provide context for search results

When displaying Jira issue details:
- Use clean, easy-to-read Slack markdown
- Make issue keys and URLs clickable
- Group related information together
- Highlight important fields like Status and Priority
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
		Limit:     20, // messages per page
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
				"You are a Slack message formatter. Your job is to take raw responses and decide what to return to the users. If message is just a notification of success or fail, you can just return a user-friendly message. " +
					"If the result contains data user needs, you need to format them to meet the slack markdown format.\n\n" +
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
			Content: azopenai.NewChatRequestUserMessageContent("" + response),
		},
	}

	logger.GetLogger().Info("sending formatting prompt to AI", zap.Any("prompt", formattingPrompt))
	// Get formatted response
	formattedResponse, err := h.aiClient.Chat(ctx, formattingPrompt)
	if err != nil {
		return response, fmt.Errorf("failed to format response: %v", err)
	}
	logger.GetLogger().Info("received formatted response from AI", zap.String("formatted_response", formattedResponse))

	return formattedResponse, nil
}

func ensureValidSchema(schema json.RawMessage) json.RawMessage {
	var m map[string]interface{}
	if err := json.Unmarshal(schema, &m); err != nil {
		// fallback: return {"type":"object","properties":{}}
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	if m["type"] != "object" {
		m["type"] = "object"
	}
	if _, ok := m["properties"]; !ok {
		m["properties"] = map[string]interface{}{}
	}
	b, _ := json.Marshal(m)
	return b
}
