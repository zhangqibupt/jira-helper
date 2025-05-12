package config

import (
	"fmt"
	"os"
	"strings"
)

// Environment represents the running environment of the application
type Environment string

// Config holds all configuration for the application
type Config struct {
	// Environment is the current running environment (development, production, test)
	Environment Environment

	// Slack configuration
	SlackBotToken string // Required: Slack bot user OAuth token

	// Azure OpenAI configuration
	AzureOpenAIKey        string // Required: Azure OpenAI API key
	AzureOpenAIEndpoint   string // Required: Azure OpenAI endpoint URL
	AzureOpenAIDeployment string // Required: Azure OpenAI model deployment name

	// S3 configuration for token storage
	TokenBucketName string // Required: S3 bucket name for storing tokens

	// Jira configuration
	DefaultJiraToken string //

	// Log level
	LogLevel string // Required: Log level
}

var (
	// instance holds the singleton config instance
	instance *Config
)

// Get returns the singleton config instance
func Get() *Config {
	if instance == nil {
		panic("config not initialized")
	}
	return instance
}

// Load creates a new Config instance from environment variables
func Load() (*Config, error) {
	cfg := &Config{}

	// Load required values
	requiredVars := map[string]*string{
		"SLACK_BOT_TOKEN": &cfg.SlackBotToken,

		"AZURE_OPENAI_KEY":      &cfg.AzureOpenAIKey,
		"AZURE_OPENAI_ENDPOINT": &cfg.AzureOpenAIEndpoint,

		"AZURE_OPENAI_DEPLOYMENT": &cfg.AzureOpenAIDeployment,
		"TOKEN_BUCKET_NAME":       &cfg.TokenBucketName,

		"DEFAULT_JIRA_TOKEN": &cfg.DefaultJiraToken,

		"LOG_LEVEL": &cfg.LogLevel,
	}

	var missingVars []string
	for env, ptr := range requiredVars {
		*ptr = os.Getenv(env)
		if *ptr == "" {
			missingVars = append(missingVars, env)
		}
	}

	if len(missingVars) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %s", strings.Join(missingVars, ", "))
	}

	// Store the instance
	instance = cfg

	return cfg, nil
}
