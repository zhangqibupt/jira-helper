package handler

import (
	"fmt"
	"strings"

	"jira_whisperer/internal/logger"

	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

// sendEphemeralSlackMessage sends an ephemeral message to Slack
func (h *SlackHandler) sendEphemeralSlackMessage(channel string, message string, threadTS string) error {
	_, _, err := h.api.PostMessage(
		channel,
		slack.MsgOptionText(message, false),
		slack.MsgOptionTS(threadTS))
	if err != nil {
		logger.GetLogger().Error(fmt.Sprintf("failed to post message due to %s", err))
	}
	return err
}

// sendMarkdownMessage sends a message to Slack with Markdown formatting enabled
func (h *SlackHandler) sendMarkdownMessage(channel string, message string, threadTS string) error {
	_, _, err := h.api.PostMessage(
		channel,
		slack.MsgOptionText(message, false),
		slack.MsgOptionTS(threadTS))
	if err != nil {
		logger.GetLogger().Error(fmt.Sprintf("failed to post markdown message due to %s", err))
	}
	return err
}

// updateMessage updates an existing Slack message with new content and returns the message timestamp
func (h *SlackHandler) updateMessage(channel string, timestamp string, existingLines []string, newLine string) (string, error) {
	// Add new line to existing lines
	existingLines = append(existingLines, newLine)
	message := strings.Join(existingLines, "\n\n")

	// Slack message length limit (approximately 40,000 characters)
	const maxMessageLength = 40000

	if len(message) > maxMessageLength {
		// If combined message is too long, send only the new message in the thread
		logger.GetLogger().Info("Combined message too long, sending new content as separate message",
			zap.Int("combinedLength", len(message)),
			zap.Int("maxLength", maxMessageLength))

		// Send new line as a new message in the thread
		_, newTimestamp, err := h.api.PostMessage(
			channel,
			slack.MsgOptionText(newLine, false),
			slack.MsgOptionTS(timestamp))
		if err != nil {
			logger.GetLogger().Error("failed to send new message", zap.Error(err))
			return timestamp, err
		}

		return newTimestamp, nil
	}

	// If message is not too long, update the existing message with all content
	_, _, _, err := h.api.UpdateMessage(
		channel,
		timestamp,
		slack.MsgOptionText(message, false),
	)
	if err != nil {
		logger.GetLogger().Error("failed to update message", zap.Error(err))
	}
	return timestamp, nil
}
