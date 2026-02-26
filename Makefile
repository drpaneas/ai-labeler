.PHONY: all build test clean run help lint coverage install-tools

# Variables
BINARY_NAME := ai-labeler
BINARY_PATH := cmd/ai-labeler
GO := go
GOFLAGS := 
LDFLAGS := -ldflags "-X main.version=$$(git describe --tags --always || echo dev) \
                     -X main.buildTime=$$(date -u +'%Y-%m-%d_%H:%M:%S') \
                     -X main.gitCommit=$$(git rev-parse --short HEAD || echo unknown)"

# Default target
all: clean test build

# Build the application
build:
	@echo "Building $(BINARY_NAME)..."
	$(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BINARY_NAME) ./$(BINARY_PATH)

# Run tests
test:
	@echo "Running tests..."
	$(GO) test -v ./...

# Run tests with coverage
coverage:
	@echo "Running tests with coverage..."
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Run linter
lint:
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed. Install with: make install-tools"; \
		exit 1; \
	fi

# Install development tools
install-tools:
	@echo "Installing development tools..."
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -f $(BINARY_NAME)
	rm -f coverage.out coverage.html

# Run the application (example usage)
run: build
	./$(BINARY_NAME) --help

# Run with example configuration
run-example: build
	@echo "Running with example configuration..."
	@echo "Make sure to set your API keys and update config.json"
	./$(BINARY_NAME) --config config-example.json --ticket 1 --dry-run

# Format code
fmt:
	@echo "Formatting code..."
	$(GO) fmt ./...

# Vet code
vet:
	@echo "Vetting code..."
	$(GO) vet ./...

# Run all checks (format, vet, lint, test)
check: fmt vet lint test

# Update dependencies
deps:
	@echo "Updating dependencies..."
	$(GO) mod tidy
	$(GO) mod verify

# Build for multiple platforms
build-all:
	@echo "Building for multiple platforms..."
	GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BINARY_NAME)-linux-amd64 ./$(BINARY_PATH)
	GOOS=darwin GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BINARY_NAME)-darwin-amd64 ./$(BINARY_PATH)
	GOOS=darwin GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BINARY_NAME)-darwin-arm64 ./$(BINARY_PATH)
	GOOS=windows GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BINARY_NAME)-windows-amd64.exe ./$(BINARY_PATH)

# Help target
help:
	@echo "Available targets:"
	@echo "  make build         - Build the application"
	@echo "  make test          - Run tests"
	@echo "  make coverage      - Run tests with coverage report"
	@echo "  make lint          - Run linter"
	@echo "  make clean         - Clean build artifacts"
	@echo "  make run           - Build and show help"
	@echo "  make run-example   - Run with example configuration in dry-run mode"
	@echo "  make fmt           - Format code"
	@echo "  make vet           - Vet code"
	@echo "  make check         - Run all checks (fmt, vet, lint, test)"
	@echo "  make deps          - Update dependencies"
	@echo "  make build-all     - Build for multiple platforms"
	@echo "  make install-tools - Install development tools"
	@echo "  make help          - Show this help message"
