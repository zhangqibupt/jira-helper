package handler

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"jira_helper/internal/logger"
	"jira_helper/internal/service/openai"
	"jira_helper/internal/storage"
	"log"
	"os"
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
}

// HistoryMessage represents a message in the conversation history
type HistoryMessage struct {
	Role    string // "user" or "assistant"
	Content string
}

func NewSlackHandler(token string, aiEndpoint string, aiKey string, aiDeployment string, defaultJiraToken string, tokenStore storage.TokenStore) (*SlackHandler, error) {
	// Ensure cache directory exists
	cacheDir := "/tmp/uvx-cache"
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %v", err)
	}

	logger.GetLogger().Info("Starting uvx client with debug logging enabled")

	// Create log file for uvx output
	logFile, err := os.OpenFile("/tmp/uvx.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create uvx log file: %v", err)
	}
	logFile.Close()

	// Initialize the default MCP client with the default token
	defaultMcpClient, err := client.NewStdioMCPClient(
		"uvx", []string{
			"UV_CACHE_DIR=/tmp/uvx-cache",
			"UV_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple",
		},
		"mcp-atlassian",
		"--jira-url=https://jira.freewheel.tv",
		fmt.Sprintf("--jira-personal-token=%s", defaultJiraToken),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create default MCP client: %v", err)
	}

	// Set up a goroutine to monitor the log file
	go func() {
		readLogFile, err := os.OpenFile("/tmp/uvx.log", os.O_RDONLY, 0644)
		if err != nil {
			logger.GetLogger().Error("failed to open uvx log file for reading", zap.Error(err))
			return
		}
		defer readLogFile.Close()

		if _, err := readLogFile.Seek(0, 2); err != nil {
			logger.GetLogger().Error("failed to seek to end of uvx log file", zap.Error(err))
			return
		}

		scanner := bufio.NewScanner(readLogFile)
		for {
			for scanner.Scan() {
				logger.GetLogger().Debug("uvx log", zap.String("message", scanner.Text()))
			}
			if err := scanner.Err(); err != nil {
				logger.GetLogger().Error("error reading uvx log file", zap.Error(err))
				return
			}
			time.Sleep(100 * time.Millisecond)
			currentPos, err := readLogFile.Seek(0, 1)
			if err != nil {
				logger.GetLogger().Error("failed to get current file position", zap.Error(err))
				return
			}
			fileInfo, err := readLogFile.Stat()
			if err != nil {
				logger.GetLogger().Error("failed to get file info", zap.Error(err))
				return
			}
			if currentPos < fileInfo.Size() {
				continue
			}
		}
	}()

	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	logger.GetLogger().Info("Initializing MCP client")
	initResult, err := defaultMcpClient.Initialize(ctx, initRequest)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			logger.GetLogger().Fatal("MCP client initialization timed out")
		} else {
			logger.GetLogger().Fatal("MCP client initialization failed", zap.Error(err))
		}
		return nil, fmt.Errorf("failed to initialize MCP client: %v", err)
	}

	log.Printf("Successfully initialized client: %v", initResult)

	aiClient, err := openai.NewClient(aiEndpoint, aiKey, aiDeployment)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAI client: %v", err)
	}

	return &SlackHandler{
		api:              slack.New(token),
		defaultMcpClient: defaultMcpClient,
		aiClient:         aiClient,
		tokenStore:       tokenStore,
		defaultJiraToken: defaultJiraToken,
	}, nil
}

// getMcpClient returns the default MCP client if userToken is empty, otherwise creates a new MCP client with the user token.
// The returned cleanup function should be called after using the client if it is not the default client.
func (h *SlackHandler) getMcpClient(userToken string) (*client.Client, func(), error) {
	if userToken == "" {
		// Use the default client, no cleanup needed
		return h.defaultMcpClient, func() {}, nil
	}
	// Create a new MCP client with the user-supplied token
	mcpClient, err := client.NewStdioMCPClient(
		"uvx", []string{
			//"UV_OFFLINE=1", // The required dependency for uvx should be cached in docker image
		},
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
