# macOS Desktop App

This document covers building, running, and configuring the Mitto macOS desktop app.

> **Note:** Configuration settings in this document only apply when running the native macOS app (`Mitto.app`), not when using `mitto web` in a browser.

## Building

### Requirements

- macOS 10.15 (Catalina) or later
- Command Line Tools (`xcode-select --install`)
- Go 1.23 or later

### Build the App

```bash
make build-mac-app
```

This creates `Mitto.app` in the project root.

### Running

```bash
# Run directly
open Mitto.app

# Or install to Applications
cp -r Mitto.app /Applications/
```

### Environment Variables

Override settings when launching:

```bash
# Use a specific ACP server
MITTO_ACP_SERVER=claude-code open Mitto.app

# Use a specific working directory
MITTO_WORK_DIR=/path/to/project open Mitto.app

# Custom global hotkey
MITTO_HOTKEY="ctrl+shift+space" open Mitto.app

# Disable global hotkey
MITTO_HOTKEY=disabled open Mitto.app
```

## How It Works

The macOS app:
1. Starts the internal web server on a random localhost port
2. Opens a native WebView window pointing to that URL
3. Creates native menus with keyboard shortcuts
4. Registers a global hotkey (⌘+Control+M) for quick access
5. Shuts down the server when the window is closed

This reuses 100% of the web interface code while providing native macOS integration.

## Configuration

macOS settings are configured under the `ui.mac` section:

```yaml
ui:
  mac:
    hotkeys:
      show_hide:
        enabled: true
        key: "cmd+ctrl+m"
    notifications:
      sounds:
        agent_completed: true
```

## Hotkeys

The macOS app supports two types of hotkeys:

1. **Global hotkeys** - Work even when the app is not focused (configurable)
2. **Menu hotkeys** - Standard macOS menu shortcuts (fixed)

### Global Hotkeys

#### Show/Hide Hotkey

Toggle the app window visibility with a global hotkey:

```yaml
ui:
  mac:
    hotkeys:
      show_hide:
        enabled: true # Enable/disable the hotkey (default: true)
        key: "cmd+ctrl+m" # Hotkey combination (default: cmd+ctrl+m)
```

**Supported modifiers:**

- `cmd` - Command key (⌘)
- `ctrl` - Control key (⌃)
- `alt` - Option key (⌥)
- `shift` - Shift key (⇧)

**Supported keys:**

- Letters: `a-z`
- Numbers: `0-9`
- Special: `space`, `tab`, `return`, `escape`, `delete`
- Function keys: `f1-f12`

**Examples:**

```yaml
key: "cmd+shift+m"      # Default
key: "ctrl+alt+space"   # Alternative
key: "cmd+shift+."      # Using period
```

To disable the hotkey:

```yaml
ui:
  mac:
    hotkeys:
      show_hide:
        enabled: false
```

### Menu Hotkeys

The following keyboard shortcuts are available from the app menu:

**Conversations:**

| Shortcut | Action | Description |
|----------|--------|-------------|
| `⌘N` | New Conversation | Create a new conversation |
| `⌘W` | Close Conversation | Close the current conversation |
| `⌘1-9` | Switch Conversation | Switch to conversation 1-9 in the sidebar list |

**Navigation:**

| Shortcut | Action | Description |
|----------|--------|-------------|
| `⌘,` | Settings | Open the settings dialog |
| `⌘L` | Focus Input | Focus the chat input field |
| `⌘⇧S` | Toggle Sidebar | Show/hide the sidebar |

These shortcuts are fixed and cannot be customized.

### All Keyboard Shortcuts

Here's a complete reference of all keyboard shortcuts:

| Shortcut | Action | Notes |
|----------|--------|-------|
| `⌘⇧M` | Show/Hide Window | Global hotkey (works even when app is not focused) |
| `⌘N` | New Conversation | |
| `⌘W` | Close Conversation | |
| `⌘,` | Settings | Standard macOS preference shortcut |
| `⌘1-9` | Switch to Conversation 1-9 | Based on sidebar order |
| `⌘L` | Focus Input | |
| `⌘⇧S` | Toggle Sidebar | |

You can also view these shortcuts in the app by clicking the keyboard icon in the sidebar footer.

## Window Behavior

### Show in All Spaces

Make the Mitto window appear in all macOS Spaces (virtual desktops):

```yaml
ui:
  mac:
    show_in_all_spaces: true  # Show window in all Spaces (default: false)
```

When enabled, the Mitto window will be visible regardless of which Space you're currently in. This is useful if you frequently switch between Spaces and want quick access to Mitto.

> **Note:** This setting requires an app restart to take effect.

You can also configure this through the Settings dialog:

1. Open the Settings dialog (gear icon in sidebar)
2. Click the **UI** tab (only visible in macOS app)
3. Toggle **Show in All Spaces** under Window
4. Click **Save Changes**
5. Restart the app

## Notifications

Configure notification behavior for the macOS app.

### Sounds

#### Agent Completed Sound

Play a notification sound when the AI agent finishes its response:

```yaml
ui:
  mac:
    notifications:
      sounds:
        agent_completed: true # Play sound when agent finishes (default: false)
```

When enabled, a pleasant two-tone chime plays whenever:

- The agent completes a response in the active session
- A background session finishes processing

This is useful when:

- Running long-running tasks where you want to be notified when complete
- Working in another application while waiting for the agent

### Configuring via Settings Dialog

You can also configure these settings through the UI:

1. Open the Settings dialog (gear icon in sidebar)
2. Click the **UI** tab (only visible in macOS app)
3. Toggle **Agent Completed** under Notifications → Sounds
4. Click **Save Changes**

## Complete Example

```yaml
ui:
  mac:
    # Global hotkeys
    hotkeys:
      show_hide:
        enabled: true
        key: "cmd+shift+m"

    # Window behavior
    show_in_all_spaces: true  # Requires app restart

    # Notification settings
    notifications:
      sounds:
        agent_completed: true
```

## JSON Format

When using `settings.json`:

```json
{
  "ui": {
    "mac": {
      "hotkeys": {
        "show_hide": {
          "enabled": true,
          "key": "cmd+shift+m"
        }
      },
      "show_in_all_spaces": true,
      "notifications": {
        "sounds": {
          "agent_completed": true
        }
      }
    }
  }
}
```

## Related Documentation

- [Configuration Overview](config.md) - Main configuration documentation
- [Web Configuration](web.md) - Web server settings
