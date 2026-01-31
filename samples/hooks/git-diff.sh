#!/bin/bash
# Git Diff Hook Script
# Attaches git diff when the message contains @git:diff
#
# Input (stdin): JSON with message, is_first_message, session_id, working_dir
# Output (stdout): JSON with transformed message

set -e

# Read JSON input from stdin
input=$(cat)

# Extract message and working directory
message=$(echo "$input" | jq -r '.message')
working_dir=$(echo "$input" | jq -r '.working_dir')

# Check if message contains @git:diff trigger
if [[ "$message" != *"@git:diff"* ]]; then
    # No trigger found, return message unchanged
    jq -n --arg msg "$message" '{"message": $msg}'
    exit 0
fi

# Remove the @git:diff trigger from the message
clean_message=$(echo "$message" | sed 's/@git:diff//g' | sed 's/  */ /g' | sed 's/^ *//;s/ *$//')

# Change to working directory
cd "$working_dir" 2>/dev/null || {
    jq -n --arg msg "$clean_message" '{"message": $msg}'
    exit 0
}

# Check if this is a git repository
if ! git rev-parse --git-dir > /dev/null 2>&1; then
    result="$clean_message

---
Note: Not a git repository"
    jq -n --arg msg "$result" '{"message": $msg}'
    exit 0
fi

# Get the diff (staged + unstaged)
diff_output=$(git diff HEAD 2>/dev/null || git diff 2>/dev/null || echo "")

# If no diff, check for staged changes only
if [ -z "$diff_output" ]; then
    diff_output=$(git diff --cached 2>/dev/null || echo "")
fi

# Build context
if [ -z "$diff_output" ]; then
    git_context="## Git Diff

No uncommitted changes found."
else
    # Truncate if too long (keep first 500 lines)
    line_count=$(echo "$diff_output" | wc -l)
    if [ "$line_count" -gt 500 ]; then
        diff_output=$(echo "$diff_output" | head -500)
        diff_output="$diff_output

... (truncated, showing first 500 of $line_count lines)"
    fi
    
    git_context="## Git Diff

\`\`\`diff
$diff_output
\`\`\`"
fi

# Combine context with message
result="$git_context

---

$clean_message"

# Output JSON
jq -n --arg msg "$result" '{"message": $msg}'

