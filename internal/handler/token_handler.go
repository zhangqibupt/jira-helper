package handler

import (
	"fmt"
	"net/http"

	"jira_whisperer/internal/logger"

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

		_ = h.sendEphemeralSlackMessage(req.ChannelID, defaultErrorMessage, "")
		c.JSON(http.StatusOK, gin.H{"error": "Invalid request body"})
		return
	}
	if err := h.validateToken(req.Text, req.ChannelID, c); err != nil {
		return
	}

	// Store the token in S3
	if err := h.tokenStore.SetToken(req.UserID, req.Text); err != nil {
		logger.GetLogger().Error("failed to store token", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to store token",
		})
		return
	}

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
