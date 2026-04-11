#!/bin/bash
# Install Qwen Code
# Input: {} (none required)
# Output: {"success": bool, "message": "...", "version": "..."}

if ! command -v npm &> /dev/null; then
    echo '{"success": false, "message": "npm is required to install Qwen Code. Please install Node.js first.", "version": ""}'
    exit 1
fi

npm install -g @qwen-code/qwen-code >&2 2>&1
echo '{"success": true, "message": "Qwen Code installed", "version": ""}'
