package main

import (
	"context"
	"fmt"
	"jira_helper/internal/config"
	"jira_helper/internal/handler"
	"jira_helper/internal/logger"
	"jira_helper/internal/storage"
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
	if IsInLambda() {
		initConfig()
		if err := logger.Init(config.Get().LogLevel); err != nil {
			log.Fatalf("Failed to initialize logger: %v", err)
		}
		defer logger.Sync()

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

		os.Setenv("AZURE_OPENAI_ENDPOINT", "https://test-gpt-4o-mini-3.openai.azure.com/")
		os.Setenv("AZURE_OPENAI_KEY", "b26c102d092f4fae91026ba590c27501")
		os.Setenv("AZURE_OPENAI_DEPLOYMENT", "gpt-4o")

		os.Setenv("TOKEN_BUCKET_NAME", "jira-helper-tokens")
		os.Setenv("LOG_LEVEL", "DEBUG")
		os.Setenv("DEFAULT_JIRA_TOKEN", "MDA2NTg3NDg2MzE5OnYxc7AGtM6CeURV5ugPmmU6DGsv")

		// below are only needed for local testing, not in lambda
		os.Setenv("AWS_REGION", "us-east-1")
		os.Setenv("AWS_ACCESS_KEY_ID", "ASIA4LV4WG5VUVJFSLYM")
		os.Setenv("AWS_SECRET_ACCESS_KEY", "O2X39ICLpd1ORB+xKVsDvLD/DEqDSqaPc0n/NTbU")
		os.Setenv("AWS_SESSION_TOKEN", "IQoJb3JpZ2luX2VjECgaCXVzLWVhc3QtMSJFMEMCIAtAzqOx+2pzuQgWvTb0sP0w/6/2q8/Wq4YOI7jPpcmDAh8b/OL74K+ehihpBG9cwTcCrAGo651J+dVqYv/NhvtOKrMDCNH//////////wEQAxoMODQ5NzI1NTAzMzM5Igx7PMd1ydrrJDsddmUqhwOWBrL3Pqmi8TyFzEwa7JWV54pNiVPwhXXoUkaL6bHZZuQ++X9bS83o5BxsnqbWwR5+y+CVoUHOVPR7FW2CH9qf1mszC7Q9/mdrPJM1ekPoBaOyxjeHX9CqWdhYqtm/V1ym7HtcvY6EhGnjoy0VDTtYP+lUyPVSjqY331hchNGfuIiXnTw3q/MMdJ7VTkezQPzya8+Yvsm0RxeiU3uh+5pmoHRomvv4kL/snAGUiznVyX62AN+o8jKDJxaUfu8Qh61oNz6lBAQ6Sqh01VZm4zWWvkLwFuokp/qXcRf/415JHfrZ/t+qGtT6/vNFuNFnJEBZON6qW/hjFmYbVWrnrG09V5QB2Og2tj3CxH2wIYyG7KlKsD9Q+H4mM0jAYrvskLw9eKgCSBEalcUzFLMya1JwWLuQLI0IKSrV00j8lSgvX+EJTlCclza86D0+eZc3+ncpZNxaXzeWEn5ZAo3NrIlpcCddaP2crd6DWHgrrv+GKmEGRAw+HoLI8CTCuaQX1NydrNvjIpqQMPvNhsEGOqgBW2jF4cUmwOSIFE/FUrcy4TNj2rmF4QUxwBLyiVSSIKZTPqPVVT2GPtzLBpwTtoTFsWZ62ktSG4NUMobwryKTRndPT05ANAj1upGP9O9a7uPvpW5IKFxEBICwMMqIHxke0YIEda7YS+V2RvUZ+qc401+xYw2ZObz2EoDRALRAwIW5jlZPIIFZwUYhqtbTEl6V4248evdj3fNwAMaCFyyksRqmufoa8uqT")

		initConfig()
		if err := logger.Init(config.Get().LogLevel); err != nil {
			log.Fatalf("Failed to initialize logger: %v", err)
		}
		defer logger.Sync()
		if err := initSlackHandler(); err != nil {
			log.Fatalf("Failed to initialize slack handler: %v", err)
		}
		r := RouterEngine()
		if err := r.Run(":3000"); err != nil {
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
	r.Use(handler.HandleSlackRetry())
	r.Use(logger.GinLogMiddleware())

	// Create a group for Slack endpoints with retry handling
	slackGroup := r.Group("/")

	slackGroup.POST("/", slackHandler.HandleRequest)
	slackGroup.POST("/setup-personal-token", slackHandler.HandleSetupPersonalToken)

	return r
}

func IsInLambda() bool {
	return os.Getenv("AWS_LAMBDA_RUNTIME_API") != ""
}

var slackHandler *handler.SlackHandler

// 32-byte key for AES-256 encryption
var encryptionKey = []byte{
	0x0f, 0x71, 0x11, 0xee, 0x50, 0x74, 0x08, 0x3f,
	0x67, 0xe0, 0x0c, 0x23, 0xca, 0x6f, 0xe5, 0xde,
	0x75, 0x23, 0x7a, 0x0e, 0x7b, 0x61, 0xf4, 0x89,
	0xe3, 0x56, 0xed, 0x0f, 0x7a, 0x9d, 0xf4, 0x89,
}

func initSlackHandler() error {
	cfg := config.Get()

	// Initialize AWS config
	awsCfg, err := awsconfig.LoadDefaultConfig(context.TODO())
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %v", err)
	}

	// Create S3 client
	s3Client := s3.NewFromConfig(awsCfg)

	// Create token store with direct 32-byte key
	tokenStore := storage.NewS3TokenStore(
		s3Client,
		cfg.TokenBucketName,
		encryptionKey,
	)

	slackHandler, err = handler.NewSlackHandler(
		cfg.SlackBotToken,
		cfg.AzureOpenAIEndpoint,
		cfg.AzureOpenAIKey,
		cfg.AzureOpenAIDeployment,
		cfg.DefaultJiraToken,
		tokenStore,
	)
	if err != nil {
		return err
	}
	return nil
}
