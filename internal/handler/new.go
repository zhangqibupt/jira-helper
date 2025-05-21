package handler

import (
	"context"
	"errors"
	"fmt"
	"jira_helper/internal/logger"
	"jira_helper/internal/service/openai"
	"jira_helper/internal/storage"
	"log"
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

// 延迟初始化 defaultMcpClient
func (h *SlackHandler) ensureDefaultMcpClient() error {
	h.mcpInitOnce.Do(func() {
		defaultMcpClient, err := client.NewStdioMCPClient(
			"uvx",
			[]string{},
			"--offline",
			"mcp-atlassian",
			"-v",
			"--jira-url=https://jira.freewheel.tv",
			fmt.Sprintf("--jira-personal-token=%s", h.defaultJiraToken),
		)
		if err != nil {
			h.mcpInitErr = fmt.Errorf("failed to create default MCP client: %v", err)
			return
		}
		initRequest := mcp.InitializeRequest{}
		initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
		initRequest.Params.ClientInfo = mcp.Implementation{
			Name:    "test-client",
			Version: "1.0.0",
		}
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancel()
		logger.GetLogger().Info("Initializing MCP client (lazy)")
		initResult, err := defaultMcpClient.Initialize(ctx, initRequest)
		if err != nil {
			h.mcpInitErr = fmt.Errorf("failed to initialize MCP client: %v", err)
			return
		}
		log.Printf("Successfully initialized client: %v", initResult)
		h.defaultMcpClient = defaultMcpClient
	})
	return h.mcpInitErr
}

// getMcpClient returns the default MCP client if userToken is empty, otherwise creates a new MCP client with the user token.
// The returned cleanup function should be called after using the client if it is not the default client.
func (h *SlackHandler) getMcpClient(userToken string) (*client.Client, func(), error) {
	if userToken == "" {
		if err := h.ensureDefaultMcpClient(); err != nil {
			return nil, nil, err
		}
		// Use the default client, no cleanup needed
		return h.defaultMcpClient, func() {}, nil
	}
	// Create a new MCP client with the user-supplied token
	mcpClient, err := client.NewStdioMCPClient(
		"uvx", []string{},
		"mcp-atlassian",
		"--jira-url=https://jira.freewheel.tv",
		fmt.Sprintf("--jira-personal-token=%s", userToken),
	)
	if err != nil {
		return nil, nil, err
	}

	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	logger.GetLogger().Info("Initializing MCP client")
	initResult, err := mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			logger.GetLogger().Fatal("MCP client initialization timed out")
		} else {
			logger.GetLogger().Fatal("MCP client initialization failed", zap.Error(err))
		}
		return nil, nil, fmt.Errorf("failed to initialize MCP client: %v", err)
	}

	logger.GetLogger().Info(fmt.Sprintf("Successfully initialized client: %v", initResult))
	return mcpClient, func() {
		if err := mcpClient.Close(); err != nil {
			logger.GetLogger().Error("failed to close MCP client", zap.Error(err))
		}
	}, nil
}
