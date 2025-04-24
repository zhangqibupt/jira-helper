package main

import (
	"context"
	"fmt"
	"jira_whisperer/internal/config"
	"jira_whisperer/internal/handler"
	"jira_whisperer/internal/logger"
	"jira_whisperer/internal/storage"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"
	"github.com/gin-gonic/gin"
)

func main() {
	// Initialize logger
	if err := logger.Init("INFO"); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Sync()

	if IsInLambda() {
		initConfig()

		if err := initSlackHandler(); err != nil {
			log.Fatalf("Failed to initialize slack handler: %v", err)
		}
		r := RouterEngine()
		ginLambda := ginadapter.New(r)
		rawHandler := func(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
			return ginLambda.ProxyWithContext(ctx, req)
		}
		lambda.Start(rawHandler)
	} else {
		os.Setenv("SLACK_BOT_TOKEN", "xoxb-4050481344-8783872636801-4fzL7TQ5iXne8BRIxKVvnmBI")

		os.Setenv("AZURE_OPENAI_ENDPOINT", "https://test-gpt-4o-mini-2.openai.azure.com/")
		os.Setenv("AZURE_OPENAI_KEY", "743bd60e3c9f4de9a667fdcf0fd342a6")
		os.Setenv("AZURE_OPENAI_DEPLOYMENT", "gpt-4o")

		os.Setenv("TOKEN_BUCKET_NAME", "jira-helper-tokens")

		initConfig()
		if err := initSlackHandler(); err != nil {
			log.Fatalf("Failed to initialize slack handler: %v", err)
		}
		r := RouterEngine()
		if err := r.Run("localhost:3000"); err != nil {
			log.Fatal("Server is shutting down due to ", err)
		}
	}
}

func initConfig() {
	_, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
}

func RouterEngine() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.Logger())

	// Create a group for Slack endpoints with retry handling
	slackGroup := r.Group("/", handler.HandleSlackRetry())

	slackGroup.POST("/", slackHandler.HandleRequest)
	slackGroup.POST("/setup-personal-token", slackHandler.HandleSetupPersonalToken)

	return r
}

func IsInLambda() bool {
	return os.Getenv("AWS_LAMBDA_RUNTIME_API") != ""
}

var slackHandler *handler.SlackHandler

const encryptionKey = "D3ER7lB0Cj9n4HzG0Azjym/l3l1edSN6Dnth9InjVu8="

func initSlackHandler() error {
	cfg := config.Get()

	// Initialize AWS config
	awsCfg, err := awsconfig.LoadDefaultConfig(context.TODO())
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %v", err)
	}

	// Create S3 client
	s3Client := s3.NewFromConfig(awsCfg)

	// Create token store
	tokenStore := storage.NewS3TokenStore(
		s3Client,
		cfg.TokenBucketName,
		[]byte(encryptionKey),
	)

	slackHandler, err = handler.NewSlackHandler(
		cfg.SlackBotToken,
		cfg.AzureOpenAIEndpoint,
		cfg.AzureOpenAIKey,
		cfg.AzureOpenAIDeployment,
		tokenStore,
	)
	if err != nil {
		return err
	}
	return nil
}
