package handler

import (
	"fmt"
	"net/http"

	"jira_helper/internal/logger"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type TokenRequest struct {
	UserID    string `json:"user_id" binding:"required"`
	Text      string `json:"text" binding:"required"`
	ChannelID string `json:"channel_id" binding:"required"`
}

// HandleSetupPersonalToken handles the POST request to /setup-personal-token
func (h *SlackHandler) HandleSetupPersonalToken(c *gin.Context) {
	userID := c.PostForm("user_id")
	text := c.PostForm("text")
	channelID := c.PostForm("channel_id")

	if userID == "" || text == "" || channelID == "" {
		logger.GetLogger().Error("missing required fields")
		// _ = h.sendEphemeralSlackMessage(channelID, "Missing required fields", "")
		c.JSON(http.StatusOK, gin.H{"error": "Missing required fields"})
		return
	}

	if err := h.validateToken(text); err != nil {
		logger.GetLogger().Error("invalid token", zap.Error(err))
		// _ = h.sendEphemeralSlackMessage(channelID, err.Error(), "")
		c.JSON(http.StatusOK, gin.H{"error": fmt.Sprintf("Validation failed due to %s", err.Error())})
		return
	}

	// Store the token in S3
	if err := h.tokenStore.SetToken(userID, text); err != nil {
		logger.GetLogger().Error("failed to store token", zap.Error(err))
		// _ = h.sendEphemeralSlackMessage(channelID, fmt.Sprintf("failed to store token: %v", err), "")
		c.JSON(http.StatusOK, gin.H{"error": fmt.Sprintf("Failed to store token due to %s", err.Error())})
		return
	}

	// _ = h.sendEphemeralSlackMessage(channelID, "Token successfully stored", "")

	// Return success response
	c.JSON(http.StatusOK, gin.H{
		"message": "Token successfully stored",
	})
}

func (h *SlackHandler) validateToken(token string) error {
	// Validate token format
	if len(token) < 8 {
		logger.GetLogger().Error("token too short")
		//_ = h.sendEphemeralSlackMessage(channelID, "Token must be at least 8 characters long", "")

		return fmt.Errorf("token too short")
	}
	return nil
}
