.PHONY: build clean test zip all docker-build docker-push

# Go parameters
BUILD_DIR=build
DEPLOYMENT_PACKAGE=$(BUILD_DIR)/function.zip
BINARY_PATH=$(BUILD_DIR)/lambda/main

# Go build flags
GOOS=linux
GOARCH=arm64
GO_BUILD_FLAGS=-ldflags="-s -w"

all: clean build zip

build:
	make clean
	@echo "Building Lambda function..."
	@mkdir -p $(BUILD_DIR)/lambda
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 go build $(GO_BUILD_FLAGS) -o $(BINARY_PATH) ./cmd/lambda

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
	go run ./cmd/lambda

deps:
	@echo "Downloading dependencies..."
	go mod download
	go mod tidy

# Docker targets
docker-build: build
	@echo "Building Docker image..."
	docker build -t jira-helper .

docker-push: docker-build
	@echo "Pushing Docker image to ECR..."
	aws ecr get-login-password --region $(AWS_REGION) | docker login --username AWS --password-stdin $(AWS_ACCOUNT_ID).dkr.ecr.$(AWS_REGION).amazonaws.com
	docker tag jira-helper:latest $(AWS_ACCOUNT_ID).dkr.ecr.$(AWS_REGION).amazonaws.com/jira-helper:latest
	docker push $(AWS_ACCOUNT_ID).dkr.ecr.$(AWS_REGION).amazonaws.com/jira-helper:latest

# Show help
help:
	@echo "Available targets:"
	@echo "  build         - Build the Lambda function"
	@echo "  zip           - Create deployment package"
	@echo "  test          - Run tests"
	@echo "  clean         - Clean build directory"
	@echo "  all           - Clean, build, and create deployment package"
	@echo "  run-local     - Run the function locally"
	@echo "  deps          - Download and tidy dependencies"
	@echo "  docker-build  - Build Docker image"
	@echo "  docker-push   - Push Docker image to ECR"