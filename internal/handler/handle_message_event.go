package handler

import (
	"context"
	"fmt"
	"jira_whisperer/internal/logger"
	"strings"
	"time"

	"github.com/slack-go/slack/slackevents"
)

// handleMessageEvent handles direct messages and channel messages that mention the bot
func (h *SlackHandler) handleMessageEvent(ev *slackevents.MessageEvent) error {
	// Ignore messages from bots to prevent loops
	if ev.BotID != "" || ev.SubType == "bot_message" || ev.SubType == "message_changed" {
		return nil
	}

	// Only handle direct messages (DMs) or messages that mention the bot
	botInfo, err := h.api.AuthTest()
	if err != nil {
		return fmt.Errorf("failed to get bot info: %v", err)
	}

	// Check if this is a direct message (including multi-person IMs)
	isDM := ev.ChannelType == "im" || ev.ChannelType == "mpim"
	// Check if the message mentions the bot
	isBotMention := strings.Contains(ev.Text, fmt.Sprintf("<@%s>", botInfo.UserID))

	// Skip if not a DM and not mentioning the bot
	if !isDM && !isBotMention {
		return nil
	}

	// For non-DM channels, remove the bot mention from the text to clean up the query
	text := ev.Text
	if !isDM && isBotMention {
		text = strings.ReplaceAll(text, fmt.Sprintf("<@%s>", botInfo.UserID), "")
		text = strings.TrimSpace(text)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var history []HistoryMessage
	// If this is a message in a thread, get the thread history
	threadTS := ev.ThreadTimeStamp
	if threadTS == "" {
		// If the message is not in a thread, use the message's timestamp as the thread's start
		threadTS = ev.TimeStamp
	} else {
		// If the message is in a thread, get the thread history
		history, err = h.getThreadHistory(ev.Channel, threadTS)
		if err != nil {
			_, _ = h.sendMarkdownMessage(ev.Channel, fmt.Sprintf(defaultErrorMessage, err.Error()), threadTS)
			logger.GetLogger().Error(fmt.Sprintf("failed to get thread history: %v", err))
			return fmt.Errorf("failed to get thread history: %v", err)
		}
	}

	// Process the query with context
	response, err := h.processQuery(ctx, text, history, ev.Channel, threadTS)
	if err != nil {
		return fmt.Errorf("failed to process query: %v", err)
	}
	// Post the response in the thread
	_, _ = h.sendMarkdownMessage(ev.Channel, response, threadTS)

	return nil
}
