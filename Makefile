.PHONY: help build run test clean docker deploy

help:
	@echo "Available commands:"
	@echo "  make build       - Build all binaries"
	@echo "  make run-server  - Run orchestrator server"
	@echo "  make run-agent   - Run agent service"
	@echo "  make test        - Run tests"
	@echo "  make clean       - Clean build artifacts"
	@echo "  make docker      - Build docker images"
	@echo "  make deploy      - Deploy to kubernetes"

build:
	@echo "Building binaries..."
	go build -o bin/server ./cmd/server
	go build -o bin/agent ./cmd/agent
	go build -o bin/cli ./cmd/cli

run-server:
	@echo "Starting orchestrator server..."
	go run ./cmd/server/main.go

run-agent:
	@echo "Starting agent service..."
	go run ./cmd/agent/main.go

test:
	@echo "Running tests..."
	go test -v ./...

test-coverage:
	@echo "Running tests with coverage..."
	go test -v -coverprofile=coverage.txt -covermode=atomic ./...

lint:
	@echo "Running linters..."
	golangci-lint run ./...

clean:
	@echo "Cleaning build artifacts..."
	rm -rf bin/
	rm -rf dist/
	go clean

docker:
	@echo "Building Docker images..."
	docker build -f deployments/docker/Dockerfile -t ai-agent-arrange:latest .

docker-compose-up:
	@echo "Starting services with docker-compose..."
	docker-compose -f deployments/docker-compose.yml up -d

docker-compose-down:
	@echo "Stopping services..."
	docker-compose -f deployments/docker-compose.yml down

deploy:
	@echo "Deploying to Kubernetes..."
	kubectl apply -f deployments/k8s/

proto:
	@echo "Generating protobuf files..."
	protoc --go_out=. --go-grpc_out=. api/grpc/proto/*.proto

migrate-up:
	@echo "Running database migrations..."
	./scripts/migrate.sh up

migrate-down:
	@echo "Rolling back database migrations..."
	./scripts/migrate.sh down

.DEFAULT_GOAL := help