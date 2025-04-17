package config

import "os"

type Config struct {
	// Slack configuration
	SlackBotToken      string
	SlackSigningSecret string

	// Azure OpenAI configuration
	AzureOpenAIKey        string
	AzureOpenAIEndpoint   string
	AzureOpenAIDeployment string

	// MCP configuration
	MCPServerURL string
	MCPAPIKey    string
}

func NewConfig() *Config {
	return &Config{
		SlackBotToken:         os.Getenv("SLACK_BOT_TOKEN"),
		SlackSigningSecret:    os.Getenv("SLACK_SIGNING_SECRET"),
		AzureOpenAIKey:        os.Getenv("AZURE_OPENAI_KEY"),
		AzureOpenAIEndpoint:   os.Getenv("AZURE_OPENAI_ENDPOINT"),
		AzureOpenAIDeployment: os.Getenv("AZURE_OPENAI_DEPLOYMENT"),
		MCPServerURL:          os.Getenv("MCP_SERVER_URL"),
		MCPAPIKey:             os.Getenv("MCP_API_KEY"),
	}
}
