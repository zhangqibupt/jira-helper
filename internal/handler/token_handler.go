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
	var req TokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.GetLogger().Error("invalid request body", zap.Error(err))
		_ = h.sendEphemeralSlackMessage(req.ChannelID, fmt.Sprintf(defaultErrorMessage, err.Error()), "")
		c.JSON(http.StatusOK, gin.H{"error": "Invalid request body"})
		return
	}
	if err := h.validateToken(req.Text, req.ChannelID, c); err != nil {
		logger.GetLogger().Error("invalid token", zap.Error(err))
		_ = h.sendEphemeralSlackMessage(req.ChannelID, err.Error(), "")
		c.JSON(http.StatusOK, gin.H{"error": "Invalid token"})
		return
	}

	// Store the token in S3
	if err := h.tokenStore.SetToken(req.UserID, req.Text); err != nil {
		logger.GetLogger().Error("failed to store token", zap.Error(err))
		_ = h.sendEphemeralSlackMessage(req.ChannelID, fmt.Sprintf("failed to store token: %v", err), "")
		c.JSON(http.StatusOK, gin.H{"error": "Failed to store token"})
		return
	}

	_ = h.sendEphemeralSlackMessage(req.ChannelID, "Token successfully stored", "")

	// Return success response
	c.JSON(http.StatusOK, gin.H{
		"message": "Token successfully stored",
	})
}

func (h *SlackHandler) validateToken(token string, channelID string, c *gin.Context) error {
	// Validate token format
	if len(token) < 8 {
		logger.GetLogger().Error("token too short")
		_ = h.sendEphemeralSlackMessage(channelID, "Token must be at least 8 characters long", "")

		return fmt.Errorf("token too short")
	}
	return nil
}
