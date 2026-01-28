#!/bin/bash
# Generate the Mitto app icon - a speech balloon with three dots
#
# This script creates the Mitto icon: a rounded speech bubble with three
# horizontal dots inside, representing a chat/conversation interface.
#
# Requirements: macOS with sips and iconutil (included in Command Line Tools)

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ICONSET_DIR="$SCRIPT_DIR/AppIcon.iconset"
OUTPUT_ICON="$SCRIPT_DIR/AppIcon.icns"

# Create iconset directory
mkdir -p "$ICONSET_DIR"

# Generate the speech balloon icon using Python
# Creates a speech bubble with three dots inside
python3 << 'EOF'
import os
import subprocess
import math

script_dir = os.path.dirname(os.path.abspath(__file__)) if '__file__' in dir() else os.getcwd()
iconset_dir = os.environ.get('ICONSET_DIR', 'platform/mac/AppIcon.iconset')

# Icon sizes required for macOS app icons
sizes = [
    (16, "icon_16x16.png"),
    (32, "icon_16x16@2x.png"),
    (32, "icon_32x32.png"),
    (64, "icon_32x32@2x.png"),
    (128, "icon_128x128.png"),
    (256, "icon_128x128@2x.png"),
    (256, "icon_256x256.png"),
    (512, "icon_256x256@2x.png"),
    (512, "icon_512x512.png"),
    (1024, "icon_512x512@2x.png"),
]

try:
    from PIL import Image, ImageDraw

    for size, filename in sizes:
        img = Image.new('RGBA', (size, size), (0, 0, 0, 0))
        draw = ImageDraw.Draw(img)

        # Calculate dimensions relative to icon size
        margin = size * 0.08
        bubble_left = margin
        bubble_right = size - margin
        bubble_top = margin
        bubble_bottom = size * 0.75
        radius = size * 0.15

        # Draw the main speech bubble (rounded rectangle)
        draw.rounded_rectangle(
            [bubble_left, bubble_top, bubble_right, bubble_bottom],
            radius=radius,
            fill=(59, 130, 246, 255)  # Blue color (#3B82F6)
        )

        # Draw the speech bubble tail (bottom-left pointing triangle)
        tail_width = size * 0.15
        tail_height = size * 0.18
        tail_left = bubble_left + size * 0.15
        tail_points = [
            (tail_left, bubble_bottom - 1),                    # Top-left
            (tail_left + tail_width, bubble_bottom - 1),       # Top-right
            (tail_left, bubble_bottom + tail_height - 1),      # Bottom point
        ]
        draw.polygon(tail_points, fill=(59, 130, 246, 255))

        # Draw three dots inside the bubble
        dot_radius = size * 0.06
        dot_y = (bubble_top + bubble_bottom) / 2
        dot_spacing = size * 0.16
        center_x = size / 2

        for i in [-1, 0, 1]:
            dot_x = center_x + i * dot_spacing
            draw.ellipse(
                [dot_x - dot_radius, dot_y - dot_radius,
                 dot_x + dot_radius, dot_y + dot_radius],
                fill=(255, 255, 255, 255)  # White dots
            )

        img.save(os.path.join(iconset_dir, filename))

    print("Speech balloon icons generated successfully using PIL")

except ImportError:
    # Fallback: create simple colored squares with message to install PIL
    print("PIL not available. Install it with: pip3 install Pillow")
    print("Creating simple placeholder icons instead...")

    for size, filename in sizes:
        # Create a simple PPM file (no dependencies needed)
        filepath = os.path.join(iconset_dir, filename.replace('.png', '.ppm'))
        with open(filepath, 'wb') as f:
            f.write(f"P6\n{size} {size}\n255\n".encode())
            # Blue color
            for _ in range(size * size):
                f.write(bytes([59, 130, 246]))

        # Convert to PNG using sips
        png_path = os.path.join(iconset_dir, filename)
        subprocess.run(['sips', '-s', 'format', 'png', filepath, '--out', png_path],
                      capture_output=True)
        os.remove(filepath)

    print("Simple placeholder icons created (install Pillow for proper speech balloon)")
EOF

# Convert iconset to icns
if [ -d "$ICONSET_DIR" ] && [ "$(ls -A "$ICONSET_DIR")" ]; then
    iconutil -c icns "$ICONSET_DIR" -o "$OUTPUT_ICON"
    echo "Created $OUTPUT_ICON"
    
    # Clean up iconset directory
    rm -rf "$ICONSET_DIR"
else
    echo "Error: No icons were generated"
    exit 1
fi

