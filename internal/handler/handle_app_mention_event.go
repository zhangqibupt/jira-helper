package handler

import (
	"context"
	"fmt"
	"github.com/slack-go/slack/slackevents"
	"jira_whisperer/internal/logger"
	"time"
)

// handleAppMentionEvent handles app mention events
func (h *SlackHandler) handleAppMentionEvent(ev *slackevents.AppMentionEvent) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Ignore messages from bots to prevent loops
	if ev.BotID != "" {
		return nil
	}

	var history []HistoryMessage
	var err error

	// If this is a message in a thread, get the thread history
	threadTS := ev.ThreadTimeStamp
	if threadTS == "" {
		// If the message is not in a thread, use the message's timestamp as the thread's start
		threadTS = ev.TimeStamp
	} else {
		// If the message is in a thread, get the thread history
		history, err = h.getThreadHistory(ev.Channel, threadTS)
		if err != nil {
			_ = h.sendMarkdownMessage(ev.Channel, defaultErrorMessage, threadTS)
			logger.GetLogger().Error(fmt.Sprintf("failed to get thread history: %v", err))
			return fmt.Errorf("failed to get thread history: %v", err)
		}
	}

	// Process the query with context
	response, err := h.processQuery(ctx, ev.Text, history, ev.Channel, threadTS)
	if err != nil {
		return fmt.Errorf("failed to process query: %v", err)
	}

	// Post the response in the thread
	_ = h.sendMarkdownMessage(ev.Channel, response, threadTS)

	return nil
}
