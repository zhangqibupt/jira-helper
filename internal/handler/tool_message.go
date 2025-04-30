package handler

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ToolMessageFormatter handles formatting of tool-related messages
type ToolMessageFormatter struct{}

// NewToolMessageFormatter creates a new ToolMessageFormatter
func NewToolMessageFormatter() *ToolMessageFormatter {
	return &ToolMessageFormatter{}
}

// FormatToolCallMessage formats tool call messages in a more natural language way
func (f *ToolMessageFormatter) FormatToolCallMessage(toolName string, args map[string]interface{}, err error) string {
	if err != nil {
		return f.formatErrorToolCall(toolName, args)
	}
	return f.formatSuccessToolCall(toolName, args)
}

// formatErrorToolCall formats error messages for tool calls
func (f *ToolMessageFormatter) formatErrorToolCall(toolName string, args map[string]interface{}) string {
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
		return "Failed to retrieve available fields"
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
		return f.formatAgileBoardsError(args)
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
		return "Failed to create issues"
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
		return "Failed to get link types"
	case "jira_transition_issue":
		issueKey, _ := args["issue_key"].(string)
		return fmt.Sprintf("Failed to transition issue %s", issueKey)
	default:
		return "Operation failed"
	}
}

// formatSuccessToolCall formats success messages for tool calls
func (f *ToolMessageFormatter) formatSuccessToolCall(toolName string, args map[string]interface{}) string {
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
		return f.formatAgileBoardsSuccess(args)
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
		return "Available link types"
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

// formatAgileBoardsError formats error messages for agile boards
func (f *ToolMessageFormatter) formatAgileBoardsError(args map[string]interface{}) string {
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
}

// formatAgileBoardsSuccess formats success messages for agile boards
func (f *ToolMessageFormatter) formatAgileBoardsSuccess(args map[string]interface{}) string {
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
}
