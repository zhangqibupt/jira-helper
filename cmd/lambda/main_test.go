package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

func Test_handleRequest(t *testing.T) {
	// Create a fake Slack message event

	// os.Getenv("SLACK_BOT_TOKEN"),
	// 	os.Getenv("OPENAI_API_BASE"),
	// 	os.Getenv("OPENAI_API_KEY"),
	// 	os.Getenv("OPENAI_MODEL"),

	os.Setenv("SLACK_BOT_TOKEN", "xapp-1-AQQ5GNLAF-8766904573877-f4852aff4f7b84928677dada293ad078a17f154d4a76fd01e706d320297a77a5")
	os.Setenv("OPENAI_API_BASE", "https://test-gpt-4o-mini-2.openai.azure.com")
	os.Setenv("OPENAI_API_KEY", "743bd60e3c9f4de9a667fdcf0fd342a6")
	os.Setenv("OPENAI_MODEL", "gpt-4o")
	os.Setenv("MCP_PATH", "/Users/qzhang/workspace/github/jira_whisperer/mcp-server")

	slackEvent := map[string]interface{}{
		"type": "event_callback",
		"event": map[string]interface{}{
			"type":    "message",
			"text":    "Hello bot!",
			"user":    "U123456",
			"channel": "C123456",
			"ts":      "1234567890.123456",
		},
	}

	// Convert to JSON
	eventJSON, err := json.Marshal(slackEvent)
	if err != nil {
		t.Fatalf("Failed to marshal event: %v", err)
	}

	// Create API Gateway request
	req := events.APIGatewayProxyRequest{
		Body: string(eventJSON),
	}

	// Call handler
	if err := initSlackHandler(); err != nil {
		t.Fatalf("Failed to initialize slack handler: %v", err)
	}

	resp, err := handleRequest(req)
	if err != nil {
		t.Fatalf("Handler failed: %v", err)
	}

	// Verify response
	if resp.StatusCode != 200 {
		t.Errorf("Expected status code 200, got %d", resp.StatusCode)
	}
	if resp.Body != "ok" {
		t.Errorf("Expected body 'ok', got %s", resp.Body)
	}
}
