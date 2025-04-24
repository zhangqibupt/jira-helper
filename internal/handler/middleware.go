package handler

import (
	"jira_whisperer/internal/logger"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// HandleSlackRetry is a middleware that handles Slack retry requests
func HandleSlackRetry() gin.HandlerFunc {
	return func(c *gin.Context) {
		retryNum := c.GetHeader("X-Slack-Retry-Num")
		retryReason := c.GetHeader("X-Slack-Retry-Reason")

		if retryNum != "" {
			logger.GetLogger().Info("slack retry request",
				zap.String("retry_num", retryNum),
				zap.String("retry_reason", retryReason))
			c.String(http.StatusOK, "ok (retry skipped)")
			c.Abort()
			return
		}
		c.Next()
	}
}
