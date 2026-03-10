# Sample Processors

This directory contains example processors for Mitto. Copy the processors you want to use to your Mitto processors directory:

- **macOS**: `~/Library/Application Support/Mitto/processors/`
- **Linux**: `~/.local/share/mitto/processors/`
- **Windows**: `%APPDATA%\Mitto\processors\`

> **Note:** The old `hooks/` directory name is still supported for backward compatibility.

## Available Processors

### git-status

Attaches git status when your message contains `@git:status`.

```
"Please review my changes @git:status"
```

**Files:** `git-status.yaml`, `git-status.sh`

### git-diff

Attaches git diff (uncommitted changes) when your message contains `@git:diff`.

```
"Can you review these changes? @git:diff"
```

**Files:** `git-diff.yaml`, `git-diff.sh`

### file-context

Attaches file contents when your message contains `@file:path/to/file`.

```
"Explain this code @file:main.go"
"Review these @file:src/app.js @file:src/utils.js"
```

**Files:** `file-context.yaml`, `file-context.sh`

### attach-image

Attaches images when your message contains `@image:path/to/image`. The image is sent as an ACP attachment, allowing the AI to see and analyze it.

```
"What's in this image? @image:screenshot.png"
"Compare these @image:before.png @image:after.png"
```

**Files:** `attach-image.yaml`, `attach-image.sh`

### timestamp

Adds current date/time to the first message of each conversation.

**Files:** `timestamp.yaml`, `timestamp.sh`

## Installation

```bash
# macOS example
PROCESSORS_DIR="$HOME/Library/Application Support/Mitto/processors"

# Copy a specific processor
cp git-status.yaml git-status.sh "$PROCESSORS_DIR/"

# Or copy all processors
cp *.yaml *.sh "$PROCESSORS_DIR/"

# Make scripts executable
chmod +x "$PROCESSORS_DIR"/*.sh
```

## Requirements

These processors require:
- `jq` - JSON processor (install via `brew install jq` or `apt install jq`)
- `git` - For git-related processors

## Creating Your Own Processors

See [docs/config/processors.md](../../docs/config/processors.md) for the full processor configuration reference.

Basic structure:
1. Create a YAML file defining the processor configuration
2. Create a companion script (bash, python, etc.)
3. The script receives JSON on stdin and outputs JSON on stdout

