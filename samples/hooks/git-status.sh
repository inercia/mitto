#!/bin/bash
# Git Status Hook Script
# Attaches git status when the message contains @git:status
#
# Input (stdin): JSON with message, is_first_message, session_id, working_dir
# Output (stdout): JSON with transformed message

set -e

# Read JSON input from stdin
input=$(cat)

# Extract message and working directory
message=$(echo "$input" | jq -r '.message')
working_dir=$(echo "$input" | jq -r '.working_dir')

# Check if message contains @git:status trigger
if [[ "$message" != *"@git:status"* ]]; then
    # No trigger found, return message unchanged
    jq -n --arg msg "$message" '{"message": $msg}'
    exit 0
fi

# Remove the @git:status trigger from the message
clean_message=$(echo "$message" | sed 's/@git:status//g' | sed 's/  */ /g' | sed 's/^ *//;s/ *$//')

# Change to working directory
cd "$working_dir" 2>/dev/null || {
    # Not a valid directory, return original message
    jq -n --arg msg "$clean_message" '{"message": $msg}'
    exit 0
}

# Check if this is a git repository
if ! git rev-parse --git-dir > /dev/null 2>&1; then
    # Not a git repository, return message with note
    result="$clean_message

---
Note: Not a git repository"
    jq -n --arg msg "$result" '{"message": $msg}'
    exit 0
fi

# Gather git information
branch=$(git branch --show-current 2>/dev/null || echo "detached")
status=$(git status --short 2>/dev/null || echo "Unable to get status")
last_commit=$(git log -1 --oneline 2>/dev/null || echo "No commits")

# Count changes
staged=$(echo "$status" | grep -c "^[MADRC]" 2>/dev/null || echo "0")
modified=$(echo "$status" | grep -c "^.M" 2>/dev/null || echo "0")
untracked=$(echo "$status" | grep -c "^??" 2>/dev/null || echo "0")

# Build git context
git_context="## Git Status

**Branch:** $branch
**Last commit:** $last_commit
**Changes:** $staged staged, $modified modified, $untracked untracked"

# Add file list if there are changes
if [ -n "$status" ] && [ "$status" != "" ]; then
    git_context="$git_context

\`\`\`
$status
\`\`\`"
fi

# Combine context with message
result="$git_context

---

$clean_message"

# Output JSON
jq -n --arg msg "$result" '{"message": $msg}'

