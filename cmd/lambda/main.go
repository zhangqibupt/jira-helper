package main

import (
	"encoding/json"

	"jira_whisperer/internal/handler"
	"jira_whisperer/internal/logger"

	"github.com/slack-go/slack/slackevents"

	"log"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"go.uber.org/zap"
)

var slackHandler *handler.SlackHandler

func init() {
	// cfg := config.NewConfig()

	// Initialize clients
	// mcpClient := mcp.NewClient(cfg.MCPAPIKey)
	// openaiClient := openai.NewClient(cfg.AzureOpenAIKey, cfg.AzureOpenAIEndpoint, cfg.AzureOpenAIDeployment)

	// // Initialize handler
	// slackHandler = handler.NewSlackHandler(mcpClient, openaiClient)

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

func main() {
	err := logger.Init("info")
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Sync()
	lambda.Start(handleRequest)
}
