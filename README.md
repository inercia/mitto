# Mitto

Mitto is a command-line interface for interacting with [Agent Communication Protocol (ACP)](https://agentcommunicationprotocol.dev/) servers. It allows you to communicate with AI coding agents like auggie, claude-code, and others that implement ACP.

## Installation

### From Source

```bash
# Clone the repository
git clone https://github.com/inercia/mitto.git
cd mitto

# Build
make build

# Or install to your GOPATH/bin
make install
```

### Dependencies

Mitto requires Go 1.23 or later.

## Configuration

Mitto uses a YAML configuration file to define ACP servers. 

### Configuration File Location

- **macOS/Linux**: `~/.mittorc`
- **Windows**: `%APPDATA%\.mittorc`

You can override the location using the `MITTORC` environment variable:

```bash
export MITTORC=/path/to/custom/config.yaml
```

### Configuration Format

```yaml
acp:
  - auggie:
      command: auggie --acp
  - claude-code:
      command: npx -y @zed-industries/claude-code-acp@latest
```

The first server in the list is the default. Each server entry is a map with:
- **name** (key): A unique identifier for the server
- **command**: The shell command to start the ACP server

## Usage

### Interactive CLI

Start an interactive session with the default ACP server:

```bash
mitto cli
```

Use a specific ACP server:

```bash
mitto cli --acp claude-code
```

Auto-approve permission requests (use with caution):

```bash
mitto cli --auto-approve
```

### CLI Commands

Once in the interactive CLI, you can use these commands:

| Command | Description |
|---------|-------------|
| `/quit`, `/exit`, `/q` | Exit the CLI |
| `/cancel` | Cancel the current operation |
| `/help`, `/h`, `/?` | Show available commands |

### Global Flags

| Flag | Description |
|------|-------------|
| `--acp <name>` | Select which ACP server to use |
| `--config <path>` | Use a custom configuration file |
| `--auto-approve` | Automatically approve all permission requests |
| `--debug` | Enable debug logging |

## Adding New ACP Servers

To add a new ACP server, edit your `~/.mittorc` file and add a new entry:

```yaml
acp:
  - auggie:
      command: auggie -acp
  - my-new-agent:
      command: /path/to/my-agent --acp-mode
```

The command should start an ACP-compatible server that communicates via stdin/stdout.

## macOS Desktop App

Mitto can be built as a native macOS application that displays the web interface in a native window.

### Requirements

- macOS 10.15 (Catalina) or later
- Command Line Tools (`xcode-select --install`)
- Go 1.23 or later

### Building the App

```bash
# Build the macOS app bundle
make build-mac-app
```

This creates `Mitto.app` in the project root.

### Running the App

```bash
# Run directly
open Mitto.app

# Or install to Applications
cp -r Mitto.app /Applications/
```

### Configuration

The desktop app uses the same `.mittorc` configuration file as the CLI. You can also use environment variables:

```bash
# Override the ACP server
MITTO_ACP_SERVER=claude open Mitto.app

# Override the working directory
MITTO_WORK_DIR=/path/to/project open Mitto.app
```

### Keyboard Shortcuts

The macOS app provides native keyboard shortcuts:

| Shortcut | Action |
|----------|--------|
| **⌘+N** | New Conversation |
| **⌘+L** | Focus Input |
| **⌘+Shift+S** | Toggle Sidebar |
| **⌘+Shift+M** | Toggle App Visibility (global) |

The **⌘+Shift+M** hotkey works system-wide, even when Mitto is not the active application:
- Press once to hide the app
- Press again to show and focus the app

#### Configuring the Hotkey

You can customize or disable the hotkey in your `.mittorc` file:

```yaml
ui:
  mac:
    hotkeys:
      show_hide:
        key: "ctrl+alt+m"  # Custom hotkey
```

To disable the hotkey:

```yaml
ui:
  mac:
    hotkeys:
      show_hide:
        enabled: false
```

**Supported modifiers:** `cmd`, `ctrl`, `alt` (or `option`), `shift`

**Supported keys:** `a-z`, `0-9`, `f1-f12`, `space`, `tab`, `return`, `escape`, `delete`, arrow keys

You can also use the `MITTO_HOTKEY` environment variable:

```bash
# Custom hotkey
MITTO_HOTKEY="ctrl+shift+space" open Mitto.app

# Disable hotkey
MITTO_HOTKEY=disabled open Mitto.app
```

### How It Works

The macOS app:
1. Starts the internal web server on a random localhost port
2. Opens a native WebView window pointing to that URL
3. Creates native menus with keyboard shortcuts that trigger actions in the web UI
4. Registers a global hotkey (⌘+Shift+M) for quick access
5. Shuts down the server when the window is closed

This approach reuses 100% of the existing web interface code while providing a native app experience with proper macOS integration.

## Development

```bash
# Run tests
make test

# Format code
make fmt

# Build and run
make run ARGS="cli"

# Build macOS app
make build-mac-app

# Clean all build artifacts (including macOS app)
make clean
```

## License

MIT

