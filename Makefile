.PHONY: build clean test zip all

# Go parameters
BINARY_NAME=bootstrap
LAMBDA_HANDLER=cmd/lambda/main.go
BUILD_DIR=build
DEPLOYMENT_PACKAGE=$(BUILD_DIR)/function.zip

# Go build flags
GOOS=linux
GOARCH=amd64
GO_BUILD_FLAGS=-ldflags="-s -w"

# MCP CLI location
MCP_CLI_PATH=/path/to/mcp  # 需要替换为实际的 MCP CLI 路径

all: clean build zip

build:
	@echo "Building Lambda function..."
	@mkdir -p $(BUILD_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build $(GO_BUILD_FLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(LAMBDA_HANDLER)

zip: build
	@echo "Creating deployment package..."
	@mkdir -p $(BUILD_DIR)/bin
	@cp $(MCP_CLI_PATH) $(BUILD_DIR)/bin/mcp
	@cd $(BUILD_DIR) && zip -r function.zip $(BINARY_NAME) bin/
	@echo "Deployment package created at $(DEPLOYMENT_PACKAGE)"

test:
	@echo "Running tests..."
	go test ./...

clean:
	@echo "Cleaning up..."
	@rm -rf $(BUILD_DIR)

# Development helpers
run-local:
	@echo "Building and running locally..."
	go run $(LAMBDA_HANDLER)

deps:
	@echo "Downloading dependencies..."
	go mod download
	go mod tidy

# Show help
help:
	@echo "Available targets:"
	@echo "  build       - Build the Lambda function"
	@echo "  zip         - Create deployment package"
	@echo "  test        - Run tests"
	@echo "  clean       - Clean build directory"
	@echo "  all         - Clean, build, and create deployment package"
	@echo "  run-local   - Run the function locally"
	@echo "  deps        - Download and tidy dependencies" 