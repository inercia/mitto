#!/bin/bash
# Generate the Mitto macOS app icon from the source icon.png
#
# This script converts the root icon.png to a macOS .icns file by:
# 1. Scaling the source image to all required sizes
# 2. Creating an iconset directory with properly named files
# 3. Using iconutil to create the final .icns file
#
# Requirements:
#   - macOS with sips and iconutil (included in Command Line Tools)
#   - Source image: icon.png at repository root
#
# Usage:
#   ./generate-icon.sh              # Run from platform/mac/
#   platform/mac/generate-icon.sh   # Run from repository root
#   ./generate-icon.sh /path/to/icon.png  # Use custom source image

set -e

# Determine script and repository directories
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Source image - can be overridden via command line argument
SOURCE_ICON="${1:-$REPO_ROOT/icon.png}"

# Output paths
ICONSET_DIR="$SCRIPT_DIR/AppIcon.iconset"
OUTPUT_ICON="$SCRIPT_DIR/AppIcon.icns"

# Validate source image exists
if [ ! -f "$SOURCE_ICON" ]; then
    echo "Error: Source icon not found: $SOURCE_ICON"
    echo "Usage: $0 [path/to/source/icon.png]"
    exit 1
fi

echo "Generating macOS icon from: $SOURCE_ICON"

# Create iconset directory
mkdir -p "$ICONSET_DIR"

# Icon sizes required for macOS app icons
# Format: size filename
SIZES=(
    "16 icon_16x16.png"
    "32 icon_16x16@2x.png"
    "32 icon_32x32.png"
    "64 icon_32x32@2x.png"
    "128 icon_128x128.png"
    "256 icon_128x128@2x.png"
    "256 icon_256x256.png"
    "512 icon_256x256@2x.png"
    "512 icon_512x512.png"
    "1024 icon_512x512@2x.png"
)

# Generate icons at each required size using sips (macOS built-in)
for entry in "${SIZES[@]}"; do
    size=$(echo "$entry" | cut -d' ' -f1)
    filename=$(echo "$entry" | cut -d' ' -f2)
    output_path="$ICONSET_DIR/$filename"

    # Copy and resize the source image
    # sips will maintain aspect ratio and center the image
    sips -z "$size" "$size" "$SOURCE_ICON" --out "$output_path" > /dev/null 2>&1

    if [ ! -f "$output_path" ]; then
        echo "Error: Failed to generate $filename"
        exit 1
    fi
done

echo "Generated icon sizes in $ICONSET_DIR"

# Convert iconset to icns using iconutil
if [ -d "$ICONSET_DIR" ] && [ "$(ls -A "$ICONSET_DIR")" ]; then
    iconutil -c icns "$ICONSET_DIR" -o "$OUTPUT_ICON"
    echo "Created $OUTPUT_ICON"

    # Clean up iconset directory
    rm -rf "$ICONSET_DIR"
else
    echo "Error: No icons were generated"
    exit 1
fi

