package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerJiraTools registers all Jira-related tools with the server
func registerJiraTools(s *server.MCPServer) error {
	// Get Jira issue tool
	getJiraTool := mcp.NewTool("get_jira",
		mcp.WithDescription("Get details of a specific Jira issue"),
		mcp.WithString("issue_key",
			mcp.Required(),
			mcp.Description("Jira issue key (e.g., 'TVP-123')"),
		),
		mcp.WithString("fields",
			mcp.Description("Comma-separated fields to return in the results"),
		),
	)

	// Search Jira tool
	searchJiraTool := mcp.NewTool("search_jira",
		mcp.WithDescription("Search Jira issues using JQL"),
		mcp.WithString("jql",
			mcp.Required(),
			mcp.Description("JQL query string"),
		),
		mcp.WithString("fields",
			mcp.Description("Comma-separated fields to return in the results"),
		),
		mcp.WithNumber("max_results",
			mcp.Description("Maximum number of results to return"),
		),
	)

	// Register tools with handlers
	s.AddTool(getJiraTool, handleGetJira)
	s.AddTool(searchJiraTool, handleSearchJira)

	return nil
}

func handleGetJira(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	issueKey, ok := request.Params.Arguments["issue_key"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid issue_key parameter")
	}

	// Call the actual Jira API here
	// This is a placeholder that returns mock data
	result := map[string]any{
		"key": issueKey,
		"fields": map[string]any{
			"summary":    "Example Issue",
			"status":     "Open",
			"created":    "2024-01-01T00:00:00.000Z",
			"updated":    "2024-01-02T00:00:00.000Z",
			"assignee":   "john.doe",
			"reporter":   "jane.smith",
			"priority":   "High",
			"labels":     []string{"bug", "critical"},
			"components": []string{"frontend", "api"},
		},
	}
	// convert result to json string
	jsonResult, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %v", err)
	}

	return mcp.NewToolResultText(string(jsonResult)), nil
}

func handleSearchJira(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	jql, ok := request.Params.Arguments["jql"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid jql parameter")
	}

	// Get optional parameters
	fields := "summary,description,status"
	if f, ok := request.Params.Arguments["fields"].(string); ok && f != "" {
		fields = f
	}

	maxResults := 50
	if m, ok := request.Params.Arguments["max_results"].(float64); ok {
		maxResults = int(m)
	}

	// Call the actual Jira API here
	// This is a placeholder that returns mock data
	result := map[string]any{
		"jql":        jql,
		"fields":     fields,
		"maxResults": maxResults,
		"issues": []map[string]any{
			{
				"key": "PROJ-123",
				"fields": map[string]any{
					"summary": "First Issue",
					"status":  "Open",
				},
			},
			{
				"key": "PROJ-124",
				"fields": map[string]any{
					"summary": "Second Issue",
					"status":  "In Progress",
				},
			},
		},
		"total": 2,
	}

	// convert result to json string
	jsonResult, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %v", err)
	}

	return mcp.NewToolResultText(string(jsonResult)), nil
}
