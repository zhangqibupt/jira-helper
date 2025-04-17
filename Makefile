.PHONY: build clean test zip all

# Go parameters
BUILD_DIR=build
DEPLOYMENT_PACKAGE=$(BUILD_DIR)/function.zip

# Go build flags
GOOS=linux
GOARCH=amd64
GO_BUILD_FLAGS=-ldflags="-s -w"

all: clean build zip

build:
	make clean
	@echo "Building Lambda function..."
	@mkdir -p $(BUILD_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0  go build $(GO_BUILD_FLAGS) -o $(BUILD_DIR)/bootstrap cmd/lambda/main.go
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0  go build $(GO_BUILD_FLAGS) -o $(BUILD_DIR)/mcp-server cmd/server/main.go

zip: build
	@echo "Creating deployment package..."
	cd $(BUILD_DIR) && zip -r function.zip .
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