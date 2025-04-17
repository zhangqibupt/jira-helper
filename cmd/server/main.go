package main

import (
	"fmt"
	"log"

	mcpserver "jira_whisperer/internal/service/mcp-server"
)

func main() {
	// Create new MCP server
	server, err := mcpserver.NewServer()
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// Start server
	fmt.Println("Starting Jira Whisperer MCP server...")
	if err := mcpserver.Serve(server); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
