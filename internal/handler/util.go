package handler

import (
	"encoding/json"
	"fmt"
	"jira_helper/internal/logger"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

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

// add error emoji to the error message
const defaultErrorMessage = "❌ Something went wrong while processing your request. Please try again later or contact <@U0ZGB1ZLP> for help. ```Error: %s```"
