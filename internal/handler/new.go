package handler

import (
	"context"
	"fmt"
	"jira_helper/internal/logger"
	"jira_helper/internal/service/openai"
	"jira_helper/internal/storage"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/mark3labs/mcp-go/client"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

type SlackHandler struct {
	api              *slack.Client
	defaultMcpClient *client.Client // MCP client with default token
	aiClient         *openai.Client
	tokenStore       storage.TokenStore
	msgFormatter     *ToolMessageFormatter
	defaultJiraToken string // Default Jira token

	mcpInitOnce sync.Once
	mcpInitErr  error
}

// HistoryMessage represents a message in the conversation history
type HistoryMessage struct {
	Role    string // "user" or "assistant"
	Content string
}

func NewSlackHandler(token string, aiEndpoint string, aiKey string, aiDeployment string, defaultJiraToken string, tokenStore storage.TokenStore) (*SlackHandler, error) {
	logger.GetLogger().Info("Starting uvx client with debug logging enabled")
	aiClient, err := openai.NewClient(aiEndpoint, aiKey, aiDeployment)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAI client: %v", err)
	}

	return &SlackHandler{
		api:              slack.New(token),
		defaultMcpClient: nil, // 延迟初始化
		aiClient:         aiClient,
		tokenStore:       tokenStore,
		defaultJiraToken: defaultJiraToken,
	}, nil
}

// initializeMcpClient handles the common initialization logic for MCP clients
func (h *SlackHandler) initializeMcpClient(mcpClient *client.Client, timeout time.Duration) error {
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	logger.GetLogger().Info("Initializing MCP client")
	initResult, err := mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		logger.GetLogger().Fatal("MCP client initialization failed", zap.Error(err))
		return fmt.Errorf("failed to initialize MCP client: %v", err)
	}

	logger.GetLogger().Info(fmt.Sprintf("Successfully initialized client: %v", initResult))
	return nil
}

// CreateMcpClient creates a new MCP client with the given token
func (h *SlackHandler) CreateMcpClient(token string) (*client.Client, error) {
	return client.NewStdioMCPClient(
		"uvx",
		[]string{
			"UV_TOOL_DIR=/tmp/uvx-tool",
			"UV_CACHE_DIR=/tmp/uvx-cache",
			fmt.Sprintf("JIRA_API_TOKEN=%s", token),
			"JIRA_URL=https://jira.com", // TODO add your jira server link
		},
		"run",
		"mcp-atlassian",
	)
}

func (h *SlackHandler) ensureDefaultMcpClient() error {
	h.mcpInitOnce.Do(func() {
		defaultMcpClient, err := h.CreateMcpClient(h.defaultJiraToken)
		if err != nil {
			h.mcpInitErr = fmt.Errorf("failed to create default MCP client: %v", err)
			return
		}

		if err := h.initializeMcpClient(defaultMcpClient, 1*time.Minute); err != nil {
			h.mcpInitErr = err
			return
		}

		h.defaultMcpClient = defaultMcpClient
	})
	return h.mcpInitErr
}

func (h *SlackHandler) getMcpClient(userToken string) (*client.Client, func(), error) {
	if userToken == "" {
		if err := h.ensureDefaultMcpClient(); err != nil {
			return nil, nil, err
		}
		// Use the default client, no cleanup needed
		return h.defaultMcpClient, func() {}, nil
	}

	// Create a new MCP client with the user-supplied token
	mcpClient, err := h.CreateMcpClient(userToken)
	if err != nil {
		return nil, nil, err
	}

	if err := h.initializeMcpClient(mcpClient, 2*time.Minute); err != nil {
		return nil, nil, err
	}

	return mcpClient, func() {
		if err := mcpClient.Close(); err != nil {
			logger.GetLogger().Error("failed to close MCP client", zap.Error(err))
		}
	}, nil
}
