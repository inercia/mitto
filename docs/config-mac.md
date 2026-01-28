# macOS Configuration

This document covers macOS-specific configuration options for the Mitto desktop app.

> **Note:** These settings only apply when running the native macOS app (`Mitto.app`),
> not when using `mitto web` in a browser.

## Configuration

macOS settings are configured under the `ui.mac` section:

```yaml
ui:
  mac:
    hotkeys:
      show_hide:
        enabled: true
        key: "cmd+shift+m"
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
        key: "cmd+shift+m" # Hotkey combination (default: cmd+shift+m)
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
| `⌘1-9` | Switch to Conversation 1-9 | Based on sidebar order |
| `⌘L` | Focus Input | |
| `⌘⇧S` | Toggle Sidebar | |

You can also view these shortcuts in the app by clicking the keyboard icon in the sidebar footer.

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
- [Web Configuration](config-web.md) - Web server settings
