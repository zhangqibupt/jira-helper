package main

import (
	"encoding/json"
	"fmt"
	"jira_whisperer/internal/handler"
	"jira_whisperer/internal/logger"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/slack-go/slack/slackevents"
	"go.uber.org/zap"
)

var slackHandler *handler.SlackHandler

func handleRequest(c *gin.Context) {
	retryNum := c.GetHeader("X-Slack-Retry-Num")
	retryReason := c.GetHeader("X-Slack-Retry-Reason")

	if retryNum != "" {
		logger.GetLogger().Info("slack retry request", zap.String("retry_num", retryNum), zap.String("retry_reason", retryReason))
		// 直接返回 200，防止重复处理
		c.String(http.StatusOK, "ok (retry skipped)")
		return
	}

	logger := logger.GetLogger()

	// Read request body
	body, err := c.GetRawData()
	if err != nil || len(body) == 0 {
		logger.Error("empty request body")
		c.JSON(200, gin.H{"error": "empty request body"})
		return
	}

	// Parse the Slack event
	eventsAPIEvent, err := slackevents.ParseEvent(
		json.RawMessage(body),
		slackevents.OptionNoVerifyToken(),
	)
	if err != nil {
		logger.Error("failed to parse slack event", zap.Error(err))
		c.JSON(200, gin.H{"error": "failed to parse slack event"})
		return
	}

	// Handle URL verification challenge
	if eventsAPIEvent.Type == slackevents.URLVerification {
		var challenge *slackevents.ChallengeResponse
		if err := json.Unmarshal(body, &challenge); err != nil {
			logger.Error("failed to unmarshal challenge", zap.Error(err))
			c.JSON(400, gin.H{"error": "failed to parse challenge"})
			return
		}
		c.Header("Content-Type", "text/plain")
		c.String(200, string(body))
		return
	}

	// Handle event callbacks
	if eventsAPIEvent.Type == slackevents.CallbackEvent {
		innerEvent := eventsAPIEvent.InnerEvent
		switch event := innerEvent.Data.(type) {
		case *slackevents.MessageEvent:
		case *slackevents.AppMentionEvent:
			if err := slackHandler.HandleEvent(event); err != nil {
				logger.Error("failed to handle message event", zap.Error(err))
				c.JSON(200, gin.H{"error": "failed to handle message event"})
				return
			}
		default:
			logger.Warn("unsupported event type", zap.String("event_type", fmt.Sprintf("%T", innerEvent.Data)))
		}
	}

	// Return success response
	c.JSON(200, gin.H{"status": "ok"})
}

func initSlackHandler() error {
	if err := validateRequiredEnvVars(); err != nil {
		return err
	}

	var err error
	slackHandler, err = handler.NewSlackHandler(
		os.Getenv("SLACK_BOT_TOKEN"),
		os.Getenv("OPENAI_API_BASE"),
		os.Getenv("OPENAI_API_KEY"),
		os.Getenv("OPENAI_MODEL"),
		os.Getenv("MCP_PATH"),
	)
	if err != nil {
		return err
	}
	return nil
}

func validateRequiredEnvVars() error {
	required := []string{
		"SLACK_BOT_TOKEN",
		"OPENAI_API_BASE",
		"OPENAI_API_KEY",
		"OPENAI_MODEL",
		"MCP_PATH",
	}

	for _, env := range required {
		if os.Getenv(env) == "" {
			return fmt.Errorf("required environment variable %s is not set", env)
		}
	}
	return nil
}
