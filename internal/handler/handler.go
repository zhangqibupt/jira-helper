package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"jira_whisperer/internal/logger"
	"strings"

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

const defaultErrorMessage = "Something went wrong while processing your request. Please try again later or contact @qzhang for help. Error: %s"

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
	_, _, err := h.api.PostMessage(
		channel,
		slack.MsgOptionText(message, false),
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
func printJSON(v interface{}) string {
	prettyJSON, err := json.Marshal(v)
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

// updateMessage updates an existing Slack message with new content
func (h *SlackHandler) updateMessage(channel string, timestamp string, message string) error {
	_, _, _, err := h.api.UpdateMessage(
		channel,
		timestamp,
		slack.MsgOptionText(message, false),
	)
	if err != nil {
		logger.GetLogger().Error(fmt.Sprintf("failed to update message due to %s", err))
	}
	return nil
}

// Update processQuery to handle conversation history
func (h *SlackHandler) processQuery(ctx context.Context, query string, history []HistoryMessage, channelID string, threadTS string) (string, error) {
	// Initialize message array
	var slackMessageLines []string
	slackMessageLines = append(slackMessageLines, "⏳ Analyzing your request to determine the best way to help you...")

	// Send initial progress message and save its timestamp
	_, timestamp, err := h.api.PostMessage(
		channelID,
		slack.MsgOptionText(strings.Join(slackMessageLines, "\n\n"), false),
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
			slackMessageLines = append(slackMessageLines, response.Content)
			_ = h.updateMessage(channelID, timestamp, strings.Join(slackMessageLines, "\n\n"))
		}

		for _, toolCall := range response.ToolCalls {
			// Update progress with current tool
			message := fmt.Sprintf("🔄 _Calling Tool *%s*_", toolCall.Name)
			if len(toolCall.Args) > 0 {
				message += fmt.Sprintf("\n>_%s_", printJSON(toolCall.Args))
			}
			slackMessageLines = append(slackMessageLines, message)
			_ = h.updateMessage(channelID, timestamp, strings.Join(slackMessageLines, "\n\n"))

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
				blocks := h.createCollapsibleBlocks(title, formatCallToolResult(err.Error()), true)
				_, _ = h.sendBlockKitMessage(channelID, threadTS, blocks)
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
			blocks := h.createCollapsibleBlocks(title, formatCallToolResult(toolResultStr), false)
			_, _ = h.sendBlockKitMessage(channelID, threadTS, blocks)
		}

		currentRound++

		if currentRound >= maxRounds {
			warningMsg := "⚠️ Reached maximum number of steps. Providing partial response based on current progress..."
			slackMessageLines = append(slackMessageLines, warningMsg)
			_ = h.updateMessage(channelID, timestamp, strings.Join(slackMessageLines, "\n\n"))

			partialResponse := fmt.Sprintf("Reached maximum conversation rounds. Last response: %s", response.Content)
			return partialResponse, nil
		}
	}

	return finalResponse, nil
}

func formatCallToolResult(result string) string {
	lines := strings.Split(result, "\n")
	for i, line := range lines {
		lines[i] = fmt.Sprintf(">_%s_", line)
	}
	return strings.Join(lines, "\n")
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
- If users ask for similar issues, you should only search for issues in the same project
- Use Slack-supported markdown (e.g. *bold*, > quote), but avoid unsupported formatting (like headers #, tables, or HTML)

When using Jira MCP APIs:
- When using tool jira_get_issue, you should always use fields: *all as parameter
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

// formatToolCallMessage formats tool call messages in a more natural language way
func (h *SlackHandler) formatToolCallMessage(toolName string, args map[string]interface{}, err error) (title string) {
	if err != nil {
		// For error case
		switch toolName {
		case "jira_get_issue":
			issueKey, _ := args["issue_key"].(string)
			return fmt.Sprintf("Failed to retrieve details for issue %s", issueKey)
		case "jira_search":
			jql, _ := args["jql"].(string)
			return fmt.Sprintf("Failed to search with JQL '%s'", jql)
		case "jira_search_fields":
			keyword, _ := args["keyword"].(string)
			if keyword != "" {
				return fmt.Sprintf("Failed to find fields matching '%s'", keyword)
			}
			return fmt.Sprintf("Failed to retrieve available fields")
		case "jira_get_project_issues":
			projectKey, _ := args["project_key"].(string)
			return fmt.Sprintf("Failed to retrieve issues for project %s", projectKey)
		case "jira_get_epic_issues":
			epicKey, _ := args["epic_key"].(string)
			return fmt.Sprintf("Failed to retrieve issues linked to epic %s", epicKey)
		case "jira_get_transitions":
			issueKey, _ := args["issue_key"].(string)
			return fmt.Sprintf("Failed to get transitions for %s", issueKey)
		case "jira_get_worklog":
			issueKey, _ := args["issue_key"].(string)
			return fmt.Sprintf("Failed to get worklog for %s", issueKey)
		case "jira_download_attachments":
			issueKey, _ := args["issue_key"].(string)
			return fmt.Sprintf("Failed to download attachments from %s", issueKey)
		case "jira_get_agile_boards":
			boardName, _ := args["board_name"].(string)
			boardType, _ := args["board_type"].(string)
			projectKey, _ := args["project_key"].(string)
			searchCriteria := ""
			if boardName != "" {
				searchCriteria += fmt.Sprintf("name: %s, ", boardName)
			}
			if boardType != "" {
				searchCriteria += fmt.Sprintf("type: %s, ", boardType)
			}
			if projectKey != "" {
				searchCriteria += fmt.Sprintf("project: %s, ", projectKey)
			}
			return fmt.Sprintf("Failed to retrieve agile boards (%s)", strings.TrimRight(searchCriteria, ", "))
		case "jira_get_board_issues":
			boardId, _ := args["board_id"].(string)
			return fmt.Sprintf("Failed to retrieve issues from board %s", boardId)
		case "jira_get_sprints_from_board":
			boardId, _ := args["board_id"].(string)
			return fmt.Sprintf("Failed to retrieve sprints from board %s", boardId)
		case "jira_create_sprint":
			sprintName, _ := args["sprint_name"].(string)
			return fmt.Sprintf("Failed to create sprint '%s'", sprintName)
		case "jira_get_sprint_issues":
			sprintId, _ := args["sprint_id"].(string)
			return fmt.Sprintf("Failed to retrieve issues from sprint %s", sprintId)
		case "jira_update_sprint":
			sprintId, _ := args["sprint_id"].(string)
			return fmt.Sprintf("Failed to update sprint %s", sprintId)
		case "jira_create_issue":
			issueType, _ := args["issue_type"].(string)
			projectKey, _ := args["project_key"].(string)
			return fmt.Sprintf("Failed to create %s in project %s", issueType, projectKey)
		case "jira_batch_create_issues":
			return fmt.Sprintf("Failed to create issues")
		case "jira_update_issue":
			issueKey, _ := args["issue_key"].(string)
			return fmt.Sprintf("Failed to update issue %s", issueKey)
		case "jira_delete_issue":
			issueKey, _ := args["issue_key"].(string)
			return fmt.Sprintf("Failed to delete issue %s", issueKey)
		case "jira_add_comment":
			issueKey, _ := args["issue_key"].(string)
			return fmt.Sprintf("Failed to add comment to %s", issueKey)
		case "jira_add_worklog":
			issueKey, _ := args["issue_key"].(string)
			return fmt.Sprintf("Failed to add worklog to %s", issueKey)
		case "jira_link_to_epic":
			issueKey, _ := args["issue_key"].(string)
			epicKey, _ := args["epic_key"].(string)
			return fmt.Sprintf("Failed to link issue %s to epic %s", issueKey, epicKey)
		case "jira_create_issue_link":
			inwardIssue, _ := args["inward_issue_key"].(string)
			outwardIssue, _ := args["outward_issue_key"].(string)
			linkType, _ := args["link_type"].(string)
			return fmt.Sprintf("Failed to create %s link between %s and %s", linkType, inwardIssue, outwardIssue)
		case "jira_remove_issue_link":
			linkId, _ := args["link_id"].(string)
			return fmt.Sprintf("Failed to remove issue link %s", linkId)
		case "jira_get_link_types":
			return fmt.Sprintf("Failed to get link types")
		case "jira_transition_issue":
			issueKey, _ := args["issue_key"].(string)
			return fmt.Sprintf("Failed to transition issue %s", issueKey)
		default:
			return "Operation failed"
		}
	}

	// Success case
	switch toolName {
	case "jira_get_issue":
		issueKey, _ := args["issue_key"].(string)
		return fmt.Sprintf("Retrieved details for issue %s", issueKey)
	case "jira_search":
		jql, _ := args["jql"].(string)
		limit, _ := args["limit"].(float64)
		return fmt.Sprintf("Search %d results for JQL '%s'", int(limit), jql)
	case "jira_search_fields":
		keyword, _ := args["keyword"].(string)
		limit, _ := args["limit"].(float64)
		if keyword != "" {
			return fmt.Sprintf("Found %d fields matching '%s'", int(limit), keyword)
		}
		return fmt.Sprintf("Retrieved %d available fields", int(limit))
	case "jira_get_project_issues":
		projectKey, _ := args["project_key"].(string)
		limit, _ := args["limit"].(float64)
		return fmt.Sprintf("Retrieved %d issues for project %s", int(limit), projectKey)
	case "jira_get_epic_issues":
		epicKey, _ := args["epic_key"].(string)
		limit, _ := args["limit"].(float64)
		return fmt.Sprintf("Retrieved %d issues linked to epic %s", int(limit), epicKey)
	case "jira_get_transitions":
		issueKey, _ := args["issue_key"].(string)
		return fmt.Sprintf("Available status transitions for %s", issueKey)
	case "jira_get_worklog":
		issueKey, _ := args["issue_key"].(string)
		return fmt.Sprintf("Worklog entries for %s", issueKey)
	case "jira_download_attachments":
		issueKey, _ := args["issue_key"].(string)
		targetDir, _ := args["target_dir"].(string)
		return fmt.Sprintf("Downloaded attachments from %s to %s", issueKey, targetDir)
	case "jira_get_agile_boards":
		boardName, _ := args["board_name"].(string)
		boardType, _ := args["board_type"].(string)
		projectKey, _ := args["project_key"].(string)
		limit, _ := args["limit"].(float64)
		searchCriteria := ""
		if boardName != "" {
			searchCriteria += fmt.Sprintf("name: %s, ", boardName)
		}
		if boardType != "" {
			searchCriteria += fmt.Sprintf("type: %s, ", boardType)
		}
		if projectKey != "" {
			searchCriteria += fmt.Sprintf("project: %s, ", projectKey)
		}
		return fmt.Sprintf("Retrieved %d agile boards (%s)", int(limit), strings.TrimRight(searchCriteria, ", "))
	case "jira_get_board_issues":
		boardId, _ := args["board_id"].(string)
		jql, _ := args["jql"].(string)
		limit, _ := args["limit"].(float64)
		return fmt.Sprintf("Retrieved %d issues from board %s (JQL: '%s')", int(limit), boardId, jql)
	case "jira_get_sprints_from_board":
		boardId, _ := args["board_id"].(string)
		state, _ := args["state"].(string)
		limit, _ := args["limit"].(float64)
		if state != "" {
			return fmt.Sprintf("Retrieved %d %s sprints from board %s", int(limit), state, boardId)
		}
		return fmt.Sprintf("Retrieved %d sprints from board %s", int(limit), boardId)
	case "jira_create_sprint":
		sprintName, _ := args["sprint_name"].(string)
		boardId, _ := args["board_id"].(string)
		return fmt.Sprintf("Created new sprint '%s' for board %s", sprintName, boardId)
	case "jira_get_sprint_issues":
		sprintId, _ := args["sprint_id"].(string)
		limit, _ := args["limit"].(float64)
		return fmt.Sprintf("Retrieved %d issues from sprint %s", int(limit), sprintId)
	case "jira_update_sprint":
		sprintId, _ := args["sprint_id"].(string)
		sprintName, _ := args["sprint_name"].(string)
		state, _ := args["state"].(string)
		updates := ""
		if sprintName != "" {
			updates += fmt.Sprintf("name: %s, ", sprintName)
		}
		if state != "" {
			updates += fmt.Sprintf("state: %s, ", state)
		}
		return fmt.Sprintf("Updated sprint %s (%s)", sprintId, strings.TrimRight(updates, ", "))
	case "jira_create_issue":
		issueType, _ := args["issue_type"].(string)
		projectKey, _ := args["project_key"].(string)
		summary, _ := args["summary"].(string)
		return fmt.Sprintf("Created new %s in project %s: '%s'", issueType, projectKey, summary)
	case "jira_batch_create_issues":
		issues, _ := args["issues"].(string)
		var issuesList []map[string]interface{}
		json.Unmarshal([]byte(issues), &issuesList)
		return fmt.Sprintf("Created %d issues", len(issuesList))
	case "jira_update_issue":
		issueKey, _ := args["issue_key"].(string)
		fields, _ := args["fields"].(string)
		var fieldsMap map[string]interface{}
		json.Unmarshal([]byte(fields), &fieldsMap)
		if epicLink, ok := fieldsMap["customfield_10006"].(string); ok {
			return fmt.Sprintf("Moved issue %s to epic %s", issueKey, epicLink)
		}
		return fmt.Sprintf("Updated issue %s with fields", issueKey)
	case "jira_delete_issue":
		issueKey, _ := args["issue_key"].(string)
		return fmt.Sprintf("Deleted issue %s", issueKey)
	case "jira_add_comment":
		issueKey, _ := args["issue_key"].(string)
		comment, _ := args["comment"].(string)
		return fmt.Sprintf("Added comment to %s: %s", issueKey, comment)
	case "jira_add_worklog":
		issueKey, _ := args["issue_key"].(string)
		timeSpent, _ := args["time_spent"].(string)
		comment, _ := args["comment"].(string)
		msg := fmt.Sprintf("Added worklog (%s) to %s", timeSpent, issueKey)
		if comment != "" {
			msg += fmt.Sprintf(" with comment: %s", comment)
		}
		return msg
	case "jira_link_to_epic":
		issueKey, _ := args["issue_key"].(string)
		epicKey, _ := args["epic_key"].(string)
		return fmt.Sprintf("Linked issue %s to epic %s", issueKey, epicKey)
	case "jira_create_issue_link":
		inwardIssue, _ := args["inward_issue_key"].(string)
		outwardIssue, _ := args["outward_issue_key"].(string)
		linkType, _ := args["link_type"].(string)
		return fmt.Sprintf("Created %s link between %s and %s", linkType, inwardIssue, outwardIssue)
	case "jira_remove_issue_link":
		linkId, _ := args["link_id"].(string)
		return fmt.Sprintf("Removed issue link %s", linkId)
	case "jira_get_link_types":
		return fmt.Sprintf("Available link types")
	case "jira_transition_issue":
		issueKey, _ := args["issue_key"].(string)
		transitionId, _ := args["transition_id"].(string)
		comment, _ := args["comment"].(string)
		msg := fmt.Sprintf("Transitioned issue %s (transition ID: %s)", issueKey, transitionId)
		if comment != "" {
			msg += fmt.Sprintf(" with comment: %s", comment)
		}
		return msg
	default:
		return "Operation completed"
	}
}

// createCollapsibleBlocks creates a collapsible message using Block Kit
func (h *SlackHandler) createCollapsibleBlocks(title string, content string, isError bool) []slack.Block {
	// Create emoji based on message type
	emoji := "✅️"
	if isError {
		emoji = "❌"
	}

	// Create the title block as a regular section
	titleBlock := slack.NewSectionBlock(
		&slack.TextBlockObject{
			Type: slack.MarkdownType,
			Text: fmt.Sprintf("%s _%s_", emoji, title),
		},
		nil,
		nil,
	)

	// Create the content block with context formatting
	contentBlock := slack.NewSectionBlock(
		&slack.TextBlockObject{
			Type: slack.MarkdownType,
			Text: content,
		},
		nil,
		nil,
	)

	// Create a divider for better visual separation
	// dividerBlock := slack.NewDividerBlock()

	return []slack.Block{titleBlock, contentBlock}
}

// sendBlockKitMessage sends a message using Block Kit
func (h *SlackHandler) sendBlockKitMessage(channelID string, threadTS string, blocks []slack.Block) (string, error) {
	_, timestamp, err := h.api.PostMessage(
		channelID,
		slack.MsgOptionBlocks(blocks...),
		slack.MsgOptionTS(threadTS),
	)
	if err != nil {
		logger.GetLogger().Error("failed to send block kit message", zap.Error(err))
		return "", err
	}
	return timestamp, nil
}
