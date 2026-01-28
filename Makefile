.PHONY: build install test test-go test-js test-integration clean run fmt fmt-check lint deps-go deps-js deps build-mac-app clean-mac-app

# Binary name
BINARY_NAME=mitto

# macOS app bundle settings
APP_NAME=Mitto
APP_BUNDLE=$(APP_NAME).app
APP_BINARY=mitto-app

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
clean: clean-mac-app
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

# =============================================================================
# macOS Desktop App
# =============================================================================
# Build the macOS app bundle (Mitto.app)
#
# Requirements:
#   - macOS with Command Line Tools installed (xcode-select --install)
#   - CGO is required for the webview library
#
# The resulting Mitto.app can be:
#   - Run directly: open Mitto.app
#   - Copied to /Applications for permanent installation
#   - Distributed as a .dmg or .zip file
#
# Environment variables:
#   MITTO_ACP_SERVER - Override the default ACP server
#   MITTO_WORK_DIR   - Override the working directory for ACP sessions
# =============================================================================

build-mac-app: deps-go
	@echo "Building macOS app bundle..."
	@# Ensure the icon exists
	@if [ ! -f platform/mac/AppIcon.icns ]; then \
		echo "Generating app icon..."; \
		cd platform/mac && ICONSET_DIR="./AppIcon.iconset" ./generate-icon.sh; \
	fi
	@# Create app bundle structure
	@mkdir -p "$(APP_BUNDLE)/Contents/MacOS"
	@mkdir -p "$(APP_BUNDLE)/Contents/Resources"
	@# Build the Go binary with CGO enabled (required for webview)
	@echo "Compiling $(APP_BINARY)..."
	CGO_ENABLED=1 $(GOBUILD) $(LDFLAGS) -o "$(APP_BUNDLE)/Contents/MacOS/$(APP_BINARY)" ./cmd/mitto-app
	@# Copy Info.plist
	@cp platform/mac/Info.plist "$(APP_BUNDLE)/Contents/"
	@# Copy icon
	@cp platform/mac/AppIcon.icns "$(APP_BUNDLE)/Contents/Resources/"
	@echo ""
	@echo "✅ Built $(APP_BUNDLE)"
	@echo ""
	@echo "To run the app:"
	@echo "  open $(APP_BUNDLE)"
	@echo ""
	@echo "To install to Applications:"
	@echo "  cp -r $(APP_BUNDLE) /Applications/"
	@echo ""

# Clean macOS app bundle
clean-mac-app:
	rm -rf "$(APP_BUNDLE)"

