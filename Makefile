# Makefile for grove-hooks (hooks)

BINARY_NAME=hooks
E2E_BINARY_NAME=tend-hooks
BIN_DIR=bin
VERSION_PKG=github.com/mattsolo1/grove-core/version

# --- Versioning ---
# For dev builds, we construct a version string from git info.
# For release builds, VERSION is passed in by the CI/CD pipeline (e.g., VERSION=v1.2.3)
GIT_COMMIT ?= $(shell git rev-parse --short HEAD)
GIT_BRANCH ?= $(shell git rev-parse --abbrev-ref HEAD)
GIT_DIRTY  ?= $(shell test -n "`git status --porcelain`" && echo "-dirty")
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# If VERSION is not set, default to a dev version string
VERSION ?= $(GIT_BRANCH)-$(GIT_COMMIT)$(GIT_DIRTY)

# Go LDFLAGS to inject version info at compile time
LDFLAGS = -ldflags="\
-X '$(VERSION_PKG).Version=$(VERSION)' \
-X '$(VERSION_PKG).Commit=$(GIT_COMMIT)' \
-X '$(VERSION_PKG).Branch=$(GIT_BRANCH)' \
-X '$(VERSION_PKG).BuildDate=$(BUILD_DATE)'"

.PHONY: all build test clean fmt vet lint run check dev build-all help

all: build

build:
	@mkdir -p $(BIN_DIR)
	@echo "Building $(BINARY_NAME) version $(VERSION)..."
	@go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME) .

test:
	@echo "Running unit tests..."
	@go test -v ./...
	@echo "Running E2E tests..."
	@make test-e2e

clean:
	@echo "Cleaning..."
	@go clean
	@rm -rf $(BIN_DIR)
	@rm -f $(BINARY_NAME)
	@rm -f $(E2E_BINARY_NAME)
	@rm -f coverage.out

fmt:
	@echo "Formatting code..."
	@go fmt ./...

vet:
	@echo "Running go vet..."
	@go vet ./...

lint:
	@echo "Running linter..."
	@if which golangci-lint > /dev/null; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed, skipping..."; \
	fi

run: build
	@echo "Running $(BINARY_NAME)..."
	@./$(BIN_DIR)/$(BINARY_NAME)

check: fmt vet lint test

# Build with race detector for development
dev:
	@mkdir -p $(BIN_DIR)
	@echo "Building $(BINARY_NAME) with race detector..."
	@go build -race $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME) .

# Cross-compile for multiple platforms
build-all:
	@mkdir -p $(BIN_DIR)
	@echo "Building for Linux AMD64..."
	@GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME)-linux-amd64 .
	@echo "Building for Darwin AMD64..."
	@GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME)-darwin-amd64 .
	@echo "Building for Darwin ARM64..."
	@GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME)-darwin-arm64 .

# --- E2E Testing ---
# Build the custom tend binary for grove-hooks E2E tests.
test-e2e-build:
	@echo "Building E2E test binary $(E2E_BINARY_NAME)..."
	@go build $(LDFLAGS) -o $(BIN_DIR)/$(E2E_BINARY_NAME) ./tests/e2e

# Run E2E tests. Depends on the main 'hooks' binary and the test runner.
test-e2e: build test-e2e-build
	@echo "Running E2E tests for hooks..."
	@HOOKS_BINARY=$(abspath $(BIN_DIR)/$(BINARY_NAME)) $(BIN_DIR)/$(E2E_BINARY_NAME) run

help:
	@echo "Grove Hooks Makefile"
	@echo "==================="
	@echo ""
	@echo "Available targets:"
	@echo "  make build       - Build the binary"
	@echo "  make test        - Run tests"
	@echo "  make clean       - Clean build artifacts"
	@echo "  make fmt         - Format code"
	@echo "  make vet         - Run go vet"
	@echo "  make lint        - Run linter (if available)"
	@echo "  make run         - Build and run the binary"
	@echo "  make check       - Run all checks (fmt, vet, lint, test)"
	@echo "  make dev         - Build with race detector"
	@echo "  make build-all   - Cross-compile for multiple platforms"
	@echo "  make test-e2e-build   - Build the E2E test runner binary"
	@echo "  make test-e2e    - Run E2E tests"
	@echo "  make help        - Show this help"