package handler

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
	"jira_helper/internal/logger"
	"jira_helper/internal/service/openai"
	"jira_helper/internal/storage"
	"log"
	"os"
	"time"
)

type SlackHandler struct {
	api          *slack.Client
	mcpClient    *client.Client
	aiClient     *openai.Client
	tokenStore   storage.TokenStore
	msgFormatter *ToolMessageFormatter
}

// HistoryMessage represents a message in the conversation history
type HistoryMessage struct {
	Role    string // "user" or "assistant"
	Content string
}

func NewSlackHandler(token string, aiEndpoint string, aiKey string, aiDeployment string, tokenStore storage.TokenStore) (*SlackHandler, error) {
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
	// Close the write-only log file as we'll reopen it for reading
	logFile.Close()

	mcpClient, err := client.NewStdioMCPClient(
		"uvx", []string{
			"UV_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple",
			"UV_CACHE_DIR=/tmp/uvx-cache",
		},
		"mcp-atlassian",
		"--jira-url=https://jira.freewheel.tv",
		"--jira-personal-token=MTE4MjU2NDAwNTU0Og/vNsKe89/quErTcSkk6XDr/u0O",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP client: %v", err)
	}

	// Set up a goroutine to monitor the log file
	go func() {
		// Open the file for reading in the monitoring goroutine
		readLogFile, err := os.OpenFile("/tmp/uvx.log", os.O_RDONLY, 0644)
		if err != nil {
			logger.GetLogger().Error("failed to open uvx log file for reading", zap.Error(err))
			return
		}
		defer readLogFile.Close()

		// Seek to the end of the file
		if _, err := readLogFile.Seek(0, 2); err != nil {
			logger.GetLogger().Error("failed to seek to end of uvx log file", zap.Error(err))
			return
		}

		// Create a scanner to read the log file
		scanner := bufio.NewScanner(readLogFile)
		for {
			for scanner.Scan() {
				logger.GetLogger().Debug("uvx log", zap.String("message", scanner.Text()))
			}
			if err := scanner.Err(); err != nil {
				logger.GetLogger().Error("error reading uvx log file", zap.Error(err))
				return
			}

			// Wait a short time before checking for new content
			time.Sleep(100 * time.Millisecond)

			// Check if there's new content
			currentPos, err := readLogFile.Seek(0, 1) // Get current position
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
				// There's new content, continue reading
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
	initResult, err := mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			logger.GetLogger().Fatal("MCP client initialization timed out")
		} else {
			logger.GetLogger().Fatal("MCP client initialization failed", zap.Error(err))
		}
	}
	log.Printf("Successfully initialized client: %v", initResult)

	aiClient, err := openai.NewClient(aiEndpoint, aiKey, aiDeployment)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAI client: %v", err)
	}

	return &SlackHandler{
		api:        slack.New(token),
		mcpClient:  mcpClient,
		aiClient:   aiClient,
		tokenStore: tokenStore,
	}, nil
}
