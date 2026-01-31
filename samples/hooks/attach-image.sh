#!/bin/bash
# Attach Image Hook Script
# Attaches images when the message contains @image:path/to/image
#
# Input (stdin): JSON with message, is_first_message, session_id, working_dir
# Output (stdout): JSON with message and attachments array

set -e

# Read JSON input from stdin
input=$(cat)

# Extract message and working directory
message=$(echo "$input" | jq -r '.message')
working_dir=$(echo "$input" | jq -r '.working_dir')

# Find all @image:path patterns
images=$(echo "$message" | grep -oE '@image:[^ ]+' | sed 's/@image://' || true)

# If no images found, return message unchanged
if [ -z "$images" ]; then
    jq -n --arg msg "$message" '{"message": $msg}'
    exit 0
fi

# Remove @image:path patterns from message
clean_message=$(echo "$message" | sed 's/@image:[^ ]*//g' | sed 's/  */ /g' | sed 's/^ *//;s/ *$//')

# Change to working directory
cd "$working_dir" 2>/dev/null || {
    jq -n --arg msg "$clean_message" '{"message": $msg}'
    exit 0
}

# Build attachments array
attachments="[]"
for image in $images; do
    if [ -f "$image" ]; then
        # Detect MIME type from extension
        ext="${image##*.}"
        case "$ext" in
            png)  mime="image/png" ;;
            jpg|jpeg) mime="image/jpeg" ;;
            gif)  mime="image/gif" ;;
            webp) mime="image/webp" ;;
            *)    mime="application/octet-stream" ;;
        esac
        
        # Add to attachments array (path-based, will be resolved by Mitto)
        attachments=$(echo "$attachments" | jq --arg path "$image" --arg type "image" --arg mime "$mime" --arg name "$(basename "$image")" \
            '. + [{"type": $type, "path": $path, "mime_type": $mime, "name": $name}]')
    else
        # File not found - add note to message
        clean_message="$clean_message

Note: Image not found: $image"
    fi
done

# Output JSON with message and attachments
jq -n --arg msg "$clean_message" --argjson att "$attachments" '{"message": $msg, "attachments": $att}'

