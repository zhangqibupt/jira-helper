package mcpserver

import (
	"github.com/mark3labs/mcp-go/server"
)

// NewServer creates a new MCP server instance
func NewServer() (*server.MCPServer, error) {
	// Create MCP server
	s := server.NewMCPServer(
		"jira helper",
		"1.0.0",
	)

	// Add Jira tools
	if err := registerJiraTools(s); err != nil {
		return nil, err
	}

	return s, nil
}

// Serve starts the MCP server
func Serve(s *server.MCPServer) error {
	return server.ServeStdio(s)
}
