package mcp

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
)

type Client struct {
	mcpPath string
	apiKey  string
}

func NewClient(apiKey string) *Client {
	// MCP CLI 在 Lambda 环境中的路径
	mcpPath := filepath.Join("/var", "task", "bin", "mcp")
	return &Client{
		mcpPath: mcpPath,
		apiKey:  apiKey,
	}
}

// SearchJiraIssues searches for Jira issues using JQL
func (c *Client) SearchJiraIssues(jql string) (interface{}, error) {
	cmd := exec.Command(c.mcpPath, "jira", "search", "--jql", jql)

	// 设置环境变量
	cmd.Env = append(cmd.Env, fmt.Sprintf("MCP_API_KEY=%s", c.apiKey))

	// 执行命令并获取输出
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("error executing mcp command: %v, output: %s", err, string(output))
	}

	// 解析 JSON 输出
	var result interface{}
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("error parsing mcp output: %v", err)
	}

	return result, nil
}

// GetJiraIssue gets details of a specific Jira issue
func (c *Client) GetJiraIssue(issueKey string) (interface{}, error) {
	cmd := exec.Command(c.mcpPath, "jira", "get-issue", issueKey)
	cmd.Env = append(cmd.Env, fmt.Sprintf("MCP_API_KEY=%s", c.apiKey))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("error executing mcp command: %v, output: %s", err, string(output))
	}

	var result interface{}
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("error parsing mcp output: %v", err)
	}

	return result, nil
}

// GetProjectIssues gets all issues for a specific project
func (c *Client) GetProjectIssues(projectKey string) (interface{}, error) {
	cmd := exec.Command(c.mcpPath, "jira", "get-project-issues", projectKey)
	cmd.Env = append(cmd.Env, fmt.Sprintf("MCP_API_KEY=%s", c.apiKey))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("error executing mcp command: %v, output: %s", err, string(output))
	}

	var result interface{}
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("error parsing mcp output: %v", err)
	}

	return result, nil
}
