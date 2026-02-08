.PHONY: build install test test-go test-js test-integration test-integration-go test-integration-cli test-integration-api test-integration-client test-ui test-ui-headed test-ui-debug test-ui-report test-all test-ci test-setup test-clean clean run fmt fmt-check fmt-docs fmt-docs-check lint deps-go deps-js deps build-mac-app clean-mac-app build-mock-acp ci homebrew-generate homebrew-test homebrew-test-style homebrew-test-install homebrew-test-cask homebrew-tap-setup homebrew-clean

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

# Run Go unit tests (excludes integration tests)
test-go:
	@echo "Running Go unit tests..."
	$(GOTEST) -v ./internal/... ./cmd/...

# Run JavaScript unit tests
test-js: deps-js
	@echo "Running JavaScript tests..."
	$(NPM) test

# =============================================================================
# Integration & UI Tests
# =============================================================================
# These tests use a mock ACP server for deterministic, repeatable testing.
# Build the mock server first with: make build-mock-acp
# =============================================================================

# Build mock ACP server
build-mock-acp:
	@echo "Building mock ACP server..."
	$(GOBUILD) -o tests/mocks/acp-server/mock-acp-server ./tests/mocks/acp-server

# Run legacy integration tests (bash scripts, requires real ACP server)
test-integration-legacy: build
	@echo "Running legacy integration tests..."
	@./tests/integration/run_all.sh

# Run Go-based integration tests (uses mock ACP server)
test-integration-go: build build-mock-acp
	@echo "Running Go integration tests..."
	$(GOTEST) -v -tags=integration ./tests/integration/...

# Run CLI integration tests only
test-integration-cli: build build-mock-acp
	@echo "Running CLI integration tests..."
	$(GOTEST) -v -tags=integration ./tests/integration/cli/...

# Run API integration tests only
test-integration-api: build build-mock-acp
	@echo "Running API integration tests..."
	$(GOTEST) -v -tags=integration ./tests/integration/api/...

# Run client integration tests only
test-integration-client: build build-mock-acp
	@echo "Running client integration tests..."
	$(GOTEST) -v -tags=integration ./tests/integration/client/...

# Run runner integration tests (platform-specific)
test-integration-runner: build
	@echo "Running runner integration tests..."
	$(GOTEST) -v ./internal/runner/... -run TestRunnerFallback

# Run all integration tests (Go-based, uses mock ACP)
test-integration: test-integration-go

# Run UI tests with Playwright
test-ui: build deps-js build-mock-acp
	@echo "Running UI tests..."
	npx playwright test --config=tests/ui/playwright.config.ts

# Run UI tests in headed mode (visible browser)
test-ui-headed: build deps-js build-mock-acp
	@echo "Running UI tests (headed)..."
	npx playwright test --config=tests/ui/playwright.config.ts --headed

# Run UI tests in debug mode
test-ui-debug: build deps-js build-mock-acp
	@echo "Running UI tests (debug)..."
	npx playwright test --config=tests/ui/playwright.config.ts --debug

# Show Playwright test report
test-ui-report:
	npx playwright show-report tests/ui/playwright-report

# Run all tests (unit + integration + UI)
test-all: test test-integration test-ui

# Setup test environment (install Playwright browsers, etc.)
test-setup: deps
	@echo "Setting up test environment..."
	npx playwright install chromium
	@echo "Test environment ready."

# Clean test artifacts
test-clean:
	@echo "Cleaning test artifacts..."
	rm -rf tests/ui/test-results
	rm -rf tests/ui/playwright-report
	rm -f tests/mocks/acp-server/mock-acp-server
	rm -rf /tmp/mitto-test-*

# Run tests in CI mode
test-ci: test-setup
	@echo "Running tests in CI mode..."
	CI=true $(MAKE) test
	CI=true $(MAKE) test-integration
	CI=true $(MAKE) test-ui

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

# Format documentation markdown files (requires prettier)
fmt-docs: deps-js
	@echo "Formatting documentation markdown files..."
	npx prettier --write "docs/**/*.md"

# Check documentation formatting (fails if files need formatting)
fmt-docs-check: deps-js
	@echo "Checking documentation formatting..."
	npx prettier --check "docs/**/*.md"

# Lint code (requires golangci-lint)
lint:
	golangci-lint run --timeout=5m

# =============================================================================
# CI Target
# =============================================================================
# Run all the same checks that run in GitHub Actions CI.
# This includes: formatting check, linting, unit tests, and integration tests.
# UI tests are excluded as they require a browser.
#
# Usage: make ci
# =============================================================================

ci: deps build-mock-acp build
	@echo "=============================================="
	@echo "Running CI checks locally..."
	@echo "=============================================="
	@echo ""
	@echo "Step 1/5: Checking Go formatting..."
	@$(MAKE) fmt-check
	@echo ""
	@echo "Step 2/5: Running golangci-lint..."
	@$(MAKE) lint
	@echo ""
	@echo "Step 3/5: Running Go unit tests..."
	@$(MAKE) test-go
	@echo ""
	@echo "Step 4/5: Running JavaScript unit tests..."
	@$(MAKE) test-js
	@echo ""
	@echo "Step 5/5: Running integration tests..."
	@MITTO_TEST_MODE=1 $(MAKE) test-integration
	@echo ""
	@echo "=============================================="
	@echo "✅ All CI checks passed!"
	@echo "=============================================="

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
	@# Generate app icon from source icon.png
	@echo "Generating app icon..."
	@platform/mac/generate-icon.sh
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
	@# Ad-hoc sign the app with entitlements (required for notifications)
	@echo "Signing app bundle..."
	@codesign --force --deep --sign - --entitlements platform/mac/Entitlements.plist "$(APP_BUNDLE)"
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

# =============================================================================
# Homebrew Packaging
# =============================================================================
# Test Homebrew formula and cask generation and installation.
# Requires: brew (Homebrew) to be installed.
#
# Usage:
#   make homebrew-generate          # Generate formula and cask files
#   make homebrew-test              # Full test: generate, style, install, verify
#   make homebrew-test-style        # Only run style checks
#   make homebrew-test-install      # Test installation (requires generate first)
#   make homebrew-clean             # Clean up test artifacts and tap
# =============================================================================

HOMEBREW_VERSION ?= latest
HOMEBREW_TAP_NAME = local/mitto-test
HOMEBREW_TAP_PATH = /opt/homebrew/Library/Taps/local/homebrew-mitto-test
HOMEBREW_GEN_SCRIPT = platform/homebrew/generate-formula.sh

# Generate Homebrew formula and cask
homebrew-generate:
	@echo "Generating Homebrew formula and cask for version: $(HOMEBREW_VERSION)"
	@mkdir -p build/homebrew
	@cd build/homebrew && $(CURDIR)/$(HOMEBREW_GEN_SCRIPT) --all $(HOMEBREW_VERSION)
	@echo ""
	@echo "✅ Generated files in build/homebrew/:"
	@ls -la build/homebrew/mitto*.rb

# Run style checks on generated formula and cask
homebrew-test-style: homebrew-generate
	@echo ""
	@echo "Running Homebrew style checks..."
	@echo ""
	@echo "Formula style check:"
	@brew style build/homebrew/mitto.rb
	@echo ""
	@echo "Cask style check:"
	@brew style build/homebrew/mitto.cask.rb
	@echo ""
	@echo "✅ All style checks passed!"

# Set up local tap for testing
homebrew-tap-setup: homebrew-generate
	@echo "Setting up local tap for testing..."
	@if brew tap | grep -q "$(HOMEBREW_TAP_NAME)"; then \
		echo "Tap already exists, cleaning up first..."; \
		$(MAKE) homebrew-clean; \
	fi
	@brew tap-new --no-git $(HOMEBREW_TAP_NAME)
	@mkdir -p $(HOMEBREW_TAP_PATH)/Casks
	@cp build/homebrew/mitto.rb $(HOMEBREW_TAP_PATH)/Formula/
	@cp build/homebrew/mitto.cask.rb $(HOMEBREW_TAP_PATH)/Casks/mitto.rb
	@echo "✅ Local tap ready: $(HOMEBREW_TAP_NAME)"

# Test formula installation
homebrew-test-install: homebrew-tap-setup
	@echo ""
	@echo "Testing CLI formula installation..."
	@brew install --formula $(HOMEBREW_TAP_NAME)/mitto
	@echo ""
	@echo "Running formula test..."
	@brew test $(HOMEBREW_TAP_NAME)/mitto
	@echo ""
	@echo "Verifying CLI installation..."
	@which mitto
	@mitto --help | head -3
	@echo ""
	@echo "✅ CLI formula installation test passed!"

# Test cask installation (includes CLI as dependency)
homebrew-test-cask: homebrew-tap-setup
	@echo ""
	@echo "Testing cask installation (includes CLI dependency)..."
	@brew install --cask $(HOMEBREW_TAP_NAME)/mitto
	@echo ""
	@echo "Verifying installations..."
	@echo "CLI: $$(which mitto)"
	@echo "App: $$(ls -d /Applications/Mitto.app 2>/dev/null || echo 'not found')"
	@mitto --help | head -3
	@test -d /Applications/Mitto.app || (echo "❌ Mitto.app not found in /Applications" && exit 1)
	@echo ""
	@echo "✅ Cask installation test passed!"

# Full Homebrew test: generate, style check, install formula, install cask
homebrew-test: homebrew-clean
	@echo "=============================================="
	@echo "       HOMEBREW PACKAGING FULL TEST"
	@echo "=============================================="
	@echo ""
	@$(MAKE) homebrew-test-style
	@echo ""
	@$(MAKE) homebrew-test-cask
	@echo ""
	@echo "=============================================="
	@echo "✅ All Homebrew packaging tests passed!"
	@echo "=============================================="
	@$(MAKE) homebrew-clean

# Clean up Homebrew test artifacts
homebrew-clean:
	@echo "Cleaning up Homebrew test artifacts..."
	@-brew uninstall --cask mitto 2>/dev/null || true
	@-brew uninstall --formula mitto 2>/dev/null || true
	@-brew untap $(HOMEBREW_TAP_NAME) 2>/dev/null || true
	@rm -rf build/homebrew
	@echo "✅ Cleanup complete"

