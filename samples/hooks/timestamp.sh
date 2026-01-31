#!/bin/bash
# Timestamp Hook Script
# Adds current date/time context to the conversation
#
# Input (stdin): None (input: none in config)
# Output (stdout): JSON with text to prepend

# Get current date and time
current_date=$(date "+%Y-%m-%d")
current_time=$(date "+%H:%M %Z")
day_of_week=$(date "+%A")

# Build context text
text="*Current time: $day_of_week, $current_date at $current_time*

"

# Output JSON for prepend
jq -n --arg text "$text" '{"text": $text}'

