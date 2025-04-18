package main

import (
	"encoding/json"
	"fmt"
	"os"

	"jira_whisperer/internal/handler"
	"jira_whisperer/internal/logger"

	"github.com/slack-go/slack/slackevents"

	"log"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"go.uber.org/zap"
)

var slackHandler *handler.SlackHandler

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

func main() {
	if err := logger.Init("info"); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Sync()

	if err := initSlackHandler(); err != nil {
		log.Fatalf("Failed to initialize slack handler: %v", err)
	}

	lambda.Start(handleRequest)
}

func handleRequest(req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	logger := logger.GetLogger()

	// Verify request body is not empty
	if req.Body == "" {
		logger.Error("empty request body")
		return events.APIGatewayProxyResponse{
			StatusCode: 400,
			Body:       "empty request body",
		}, nil
	}

	// Parse the Slack event
	eventsAPIEvent, err := slackevents.ParseEvent(
		json.RawMessage(req.Body),
		slackevents.OptionNoVerifyToken(),
	)
	if err != nil {
		logger.Error("failed to parse slack event", zap.Error(err))
		return events.APIGatewayProxyResponse{
			StatusCode: 400,
			Body:       "failed to parse slack event",
		}, nil
	}

	// Handle URL verification challenge
	if eventsAPIEvent.Type == slackevents.URLVerification {
		var challenge *slackevents.ChallengeResponse
		if err := json.Unmarshal([]byte(req.Body), &challenge); err != nil {
			logger.Error("failed to unmarshal challenge", zap.Error(err))
			return events.APIGatewayProxyResponse{
				StatusCode: 400,
				Body:       "failed to parse challenge",
			}, nil
		}
		return events.APIGatewayProxyResponse{
			StatusCode: 200,
			Headers: map[string]string{
				"Content-Type": "text/plain",
			},
			Body: req.Body,
		}, nil
	}

	// Handle event callbacks
	if eventsAPIEvent.Type == slackevents.CallbackEvent {
		innerEvent := eventsAPIEvent.InnerEvent
		switch event := innerEvent.Data.(type) {
		case *slackevents.MessageEvent:
			if err := slackHandler.HandleEvent(event); err != nil {
				logger.Error("failed to handle message event", zap.Error(err))
				return events.APIGatewayProxyResponse{
					StatusCode: 500,
					Body:       "failed to handle message event",
				}, nil
			}
		}
	}

	// Return success response
	return events.APIGatewayProxyResponse{
		StatusCode: 200,
		Body:       "ok",
	}, nil
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
