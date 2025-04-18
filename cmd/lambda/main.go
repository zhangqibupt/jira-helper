package main

import (
	"context"
	"jira_whisperer/internal/logger"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"
	"github.com/gin-gonic/gin"
)

func main() {
	if err := logger.Init("info"); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Sync()

	r := RouterEngine()
	if IsInLambda() {
		if err := initSlackHandler(); err != nil {
			log.Fatalf("Failed to initialize slack handler: %v", err)
		}

		ginLambda := ginadapter.New(r)
		rawHandler := func(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
			return ginLambda.ProxyWithContext(ctx, req)
		}
		lambda.Start(rawHandler)
	} else {
		os.Setenv("SLACK_BOT_TOKEN", "xoxb-4050481344-8783872636801-4fzL7TQ5iXne8BRIxKVvnmBI")
		os.Setenv("OPENAI_API_BASE", "https://test-gpt-4o-mini-2.openai.azure.com/")
		os.Setenv("OPENAI_API_KEY", "743bd60e3c9f4de9a667fdcf0fd342a6")
		os.Setenv("OPENAI_MODEL", "gpt-4o")
		os.Setenv("MCP_PATH", "/Users/qzhang/workspace/github/jira_whisperer/build/mcp-server")

		if err := initSlackHandler(); err != nil {
			log.Fatalf("Failed to initialize slack handler: %v", err)
		}
		if err := r.Run("localhost:3000"); err != nil {
			log.Fatal("Server is shutting down due to ", err)
		}
	}
}

func RouterEngine() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.Logger())
	r.POST("/", handleRequest)
	return r
}

func IsInLambda() bool {
	return os.Getenv("AWS_LAMBDA_RUNTIME_API") != ""
}
