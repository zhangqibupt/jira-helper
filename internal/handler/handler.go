package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"jira_whisperer/internal/logger"
	"strings"

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
			progressMsg := fmt.Sprintf("⚙️ Step %d/%d: `%s` with parameters: ```%s```", i+1, len(response.ToolCalls), toolCall.Name, prettyPrintJSON(toolCall.Args))
			_ = h.sendMarkdownMessage(channelID, progressMsg, threadTS)

			request := mcp.CallToolRequest{}
			request.Method = toolCall.Name
			request.Params.Name = toolCall.Name
			request.Params.Arguments = toolCall.Args

			result, err := h.mcpClient.CallTool(ctx, request)
			if err != nil {
				messages = append(messages, &azopenai.ChatRequestUserMessage{
					Content: azopenai.NewChatRequestUserMessageContent(fmt.Sprintf("Error occurred when calling tool *%s*: %v. Please suggest how to handle this error or try an alternative approach.", toolCall.Name, err)),
				})
				_ = h.sendMarkdownMessage(channelID, fmt.Sprintf("❌ Error occurred when calling tool *%s*: %v. Please suggest how to handle this error or try an alternative approach.", toolCall.Name, err), threadTS)
				continue
			}

			toolCall.Response = result
			toolResults = append(toolResults, fmt.Sprintf("%s: %s", toolCall.Name, printToolResult(result)))
			response := fmt.Sprintf("The tool *%s* was called with arguments ```%v``` successfully, the result is: %s", toolCall.Name, prettyPrintJSON(toolCall.Args), printToolResult(result))

			messages = append(messages, &azopenai.ChatRequestUserMessage{
				Content: azopenai.NewChatRequestUserMessageContent(response),
			})
			_ = h.sendMarkdownMessage(channelID, fmt.Sprintf("✅ The tool *%s* was called successfully, response is ```%s```.", toolCall.Name, printToolResult(result)), threadTS)
		}

		currentRound++

		if currentRound >= maxRounds {
			warningMsg := "⚠️ Reached maximum number of steps. Providing partial response based on current progress..."
			_ = h.sendMarkdownMessage(channelID, warningMsg, threadTS)

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

	formattedResponse, err := h.formatAIResponseForSlack(ctx, finalResponse)
	if err != nil {
		logger.GetLogger().Error("failed to format final response", zap.Error(err))
		return finalResponse, nil
	}

	return formattedResponse, nil
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

		When using Jira MCP APIs, ensure the following:
		- When you try to get jira issue, if you can not find expeceted information, you can try specifying fields: *all.

		Communication Guidelines:
		1. Be professional and clear in your responses
		2. Use slack markdown formatting for better readability
		3. When referencing Jira issues, always include the issue key (e.g., PROJ-123) as a clickable link
		4. Explain your actions before performing them
		5. If a request is unclear, ask for clarification
		6. Provide context when displaying search results

		When displaying Jira issue details:
		1. Format the output in a clean, easy-to-read way using slack markdown format
		2. Make issue keys and URLs clickable links
		3. Group related information together
		4. Highlight important fields like Status and Priority
		5. Remove unnecessary markdown symbols and formatting
		6. Skip avatar images and other non-essential visual elements
		7. Use emojis sparingly to highlight key information
		8. Present dates in a human-readable format

		Error Handling:
		1. If you are unsure about the answer to the USER's request or how to satiate their request, you should gather more information. This can be done by asking the USER for more information.
		2. Bias towards not asking the user for help if you can find the answer yourself.
		3. When we are talking about epic, "Epic Link" is used to represent the epic that the issue is linked to. The customfield_10006 is used to represent it.
		Start each interaction by understanding the user's needs and then utilize the appropriate tools to help them achieve their goals.
`),
	}

	//Please respond using only Slack-compatible Markdown syntax. This will be rendered inside a Slack Block Kit message using slack.NewTextBlockObject(slack.MarkdownType, ...), so your response must strictly follow Slack's supported Markdown rules:
	//1. Use *bold*, _italic_, and ~strikethrough~ only — no other formatting styles like #, ###, __, or backticks.
	//2. Use \n to break lines manually.
	//3. Use > to create block quotes (optional).
	//4. Use - or • for bullet lists.
	//5. Use <https://url|display_text> to format links.

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
