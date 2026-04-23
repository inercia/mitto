#!/usr/bin/env bash
# Install Goose
# Input: {} (none required)
# Output: {"success": bool, "message": "...", "version": "..."}

if command -v brew &> /dev/null; then
    brew install block/tap/goose >&2 2>&1
    if command -v goose &> /dev/null; then
        VERSION=$(goose --version 2>/dev/null | head -1 | tr -d '\n')
        echo "{\"success\": true, \"message\": \"Goose installed via Homebrew\", \"version\": \"$VERSION\"}"
    else
        echo '{"success": false, "message": "Homebrew installation failed", "version": ""}'
        exit 1
    fi
else
    echo '{"success": false, "message": "Homebrew not found. Download Goose from https://github.com/block/goose/releases", "version": ""}'
    exit 1
fi
