package handler

import (
	"fmt"
	"strings"

	"jira_helper/internal/logger"

	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

// sendEphemeralSlackMessage sends an ephemeral message to Slack
func (h *SlackHandler) sendEphemeralSlackMessage(channel string, message string, threadTS string) error {
	if message == "" {
		return nil
	}
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
func (h *SlackHandler) sendMarkdownMessage(channel string, message string, threadTS string) (string, error) {
	if message == "" {
		return "", nil
	}

	_, timestamp, err := h.api.PostMessage(
		channel,
		slack.MsgOptionText(message, false),
		slack.MsgOptionTS(threadTS))
	if err != nil {
		logger.GetLogger().Error(fmt.Sprintf("failed to post markdown message due to %s", err))
	}
	return timestamp, err
}

// updateMessage updates an existing Slack message with new content and returns the message timestamp
func (h *SlackHandler) updateMessage(channel string, timestamp string, message string) error {
	// Update the existing message with all content
	_, _, _, err := h.api.UpdateMessage(
		channel,
		timestamp,
		slack.MsgOptionText(message, false),
	)
	if err != nil {
		logger.GetLogger().Error("failed to update message", zap.Error(err))
	}
	return nil
}

// shouldCreateNewMessage determines if a new message should be created instead of updating the existing one
func (h *SlackHandler) shouldCreateNewMessage(existingLines []string, newLine string) bool {
	// Add new line to existing lines
	combinedLines := append(existingLines, newLine)
	message := strings.Join(combinedLines, "\n\n")

	// Slack message length limit (approximately 40,000 characters)
	const maxMessageLength = 40000

	return len(message) > maxMessageLength
}

// createNewMessage creates a new message in a thread and returns its timestamp
func (h *SlackHandler) createNewMessage(channel string, threadTS string, message string) (string, error) {
	if message == "" {
		return "", nil
	}
	_, timestamp, err := h.api.PostMessage(
		channel,
		slack.MsgOptionText(message, false),
		slack.MsgOptionTS(threadTS))
	if err != nil {
		logger.GetLogger().Error("failed to send new message", zap.Error(err))
		return "", err
	}
	return timestamp, nil
}
