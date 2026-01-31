#!/bin/bash
# File Context Hook Script
# Attaches file contents when the message contains @file:path/to/file
#
# Input (stdin): JSON with message, is_first_message, session_id, working_dir
# Output (stdout): JSON with transformed message

set -e

# Read JSON input from stdin
input=$(cat)

# Extract message and working directory
message=$(echo "$input" | jq -r '.message')
working_dir=$(echo "$input" | jq -r '.working_dir')

# Find all @file:path patterns
files=$(echo "$message" | grep -oE '@file:[^ ]+' | sed 's/@file://' || true)

# If no files found, return message unchanged
if [ -z "$files" ]; then
    jq -n --arg msg "$message" '{"message": $msg}'
    exit 0
fi

# Remove @file:path patterns from message
clean_message=$(echo "$message" | sed 's/@file:[^ ]*//g' | sed 's/  */ /g' | sed 's/^ *//;s/ *$//')

# Change to working directory
cd "$working_dir" 2>/dev/null || {
    jq -n --arg msg "$clean_message" '{"message": $msg}'
    exit 0
}

# Build file context
file_context="## File Contents"

for file in $files; do
    if [ -f "$file" ]; then
        # Detect file extension for syntax highlighting
        ext="${file##*.}"
        
        # Read file content (limit to 300 lines)
        content=$(head -300 "$file")
        line_count=$(wc -l < "$file")
        
        file_context="$file_context

### $file
\`\`\`$ext
$content
\`\`\`"
        
        if [ "$line_count" -gt 300 ]; then
            file_context="$file_context
*(truncated, showing first 300 of $line_count lines)*"
        fi
    else
        file_context="$file_context

### $file
*File not found*"
    fi
done

# Combine context with message
result="$file_context

---

$clean_message"

# Output JSON
jq -n --arg msg "$result" '{"message": $msg}'

