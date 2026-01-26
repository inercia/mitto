.PHONY: build install test test-go test-js test-integration clean run fmt fmt-check lint deps-go deps-js deps

# Binary name
BINARY_NAME=mitto

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt

# Node parameters
NPM=npm

# Build flags
LDFLAGS=-ldflags "-s -w"

# Main build target
build:
	$(GOBUILD) $(LDFLAGS) -o $(BINARY_NAME) ./cmd/mitto

# Install to GOPATH/bin
install:
	$(GOCMD) install ./cmd/mitto

# Run all unit tests (Go + JavaScript)
test: test-go test-js

# Run Go unit tests
test-go:
	@echo "Running Go tests..."
	$(GOTEST) -v ./...

# Run JavaScript unit tests
test-js: deps-js
	@echo "Running JavaScript tests..."
	$(NPM) test

# Run integration tests (requires ACP server like auggie)
test-integration: build
	@echo "Running integration tests..."
	@./tests/integration/run_all.sh

# Run all tests (unit + integration)
test-all: test test-integration

# Clean build artifacts
clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -rf node_modules

# Run the application (pass ARGS to provide command line arguments)
run: build
	./$(BINARY_NAME) $(ARGS)

# Format Go code
fmt:
	$(GOFMT) ./...

# Check Go code formatting (fails if files need formatting)
fmt-check:
	@echo "Checking Go code formatting..."
	@test -z "$$(gofmt -l .)" || (echo "The following files need formatting:" && gofmt -l . && exit 1)
	@echo "All Go files are properly formatted."

# Lint code (requires golangci-lint)
lint:
	golangci-lint run

# Download Go dependencies
deps-go:
	$(GOMOD) download
	$(GOMOD) tidy

# Install JavaScript dependencies
deps-js:
	@if [ ! -d "node_modules" ]; then \
		echo "Installing JavaScript dependencies..."; \
		$(NPM) install; \
	fi

# Download all dependencies
deps: deps-go deps-js

