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

	"bytes"
	"os/exec"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"
	"github.com/gin-gonic/gin"
)

func main() {
	if IsInLambda() {
		logger.GetLogger().Info("Running in AWS Lambda")

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
		rawHandler := func(ctx context.Context, req events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
			return ginLambda.ProxyFunctionURLWithContext(ctx, req)
		}
		lambda.Start(rawHandler)
	} else {
		logger.GetLogger().Info("Running locally")
		os.Setenv("SLACK_BOT_TOKEN", "xxx")

		os.Setenv("AZURE_OPENAI_ENDPOINT", "xxx")
		os.Setenv("AZURE_OPENAI_KEY", "xxx")
		os.Setenv("AZURE_OPENAI_DEPLOYMENT", "gpt-4o")

		os.Setenv("TOKEN_BUCKET_NAME", "jira-helper-tokens")
		os.Setenv("LOG_LEVEL", "DEBUG")
		os.Setenv("DEFAULT_JIRA_TOKEN", "xxx")

		// below are only needed for local testing, not in lambda
		os.Setenv("AWS_REGION", "us-east-1")
		os.Setenv("AWS_ACCESS_KEY_ID", "xxx")
		os.Setenv("AWS_SECRET_ACCESS_KEY", "xx")
		os.Setenv("AWS_SESSION_TOKEN", "xxx")

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
	slackGroup.POST("/shell", ShellHandler)

	fmt.Println("Start create mcp client")
	if _, err := slackHandler.CreateMcpClient("xxxx"); err != nil {
		fmt.Println("error create mcp client", err.Error())
		panic(err.Error())
	} else {
		fmt.Println("create mcp client OK")
	}

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

// ShellHandler handles shell command execution
func ShellHandler(c *gin.Context) {
	// Only allow POST
	if c.Request.Method != "POST" {
		c.JSON(405, gin.H{"error": "Method not allowed, use POST"})
		return
	}

	// Parse JSON body
	var request struct {
		Command string `json:"command" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request format, must be JSON with 'command' field"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", request.Command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			c.JSON(408, gin.H{
				"error":   "Command execution timed out",
				"stdout":  stdout.String(),
				"stderr":  stderr.String(),
				"partial": true,
			})
			return
		}
		c.JSON(500, gin.H{"error": err.Error(), "stderr": stderr.String()})
		return
	}

	c.JSON(200, gin.H{
		"stdout": stdout.String(),
		"stderr": stderr.String(),
	})
}
