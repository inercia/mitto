# macOS Desktop App Configuration

> **Note:** This documentation applies to the **native macOS Desktop App** (`Mitto.app`) only. These settings do not apply when using `mitto web` in a browser. For web interface configuration, see [Linux CLI & Web Interface](../linux/README.md).

## Overview

The Mitto macOS app is a native application that embeds the web interface in a WebView, providing:

- Global hotkeys for quick access
- Native macOS menus with keyboard shortcuts
- Notification sounds when agents complete tasks
- Window behavior settings (show in all Spaces)

## Table of Contents

- [Building](#building)
- [Installation from GitHub Releases](#installation-from-github-releases)
- [Window Behavior](#window-behavior)
- [Notifications](#notifications)

## Related Documentation

| Topic                   | Location                             |
| ----------------------- | ------------------------------------ |
| Configuration Overview  | [../overview.md](../overview.md)     |
| ACP Servers             | [../acp.md](../acp.md)               |
| Prompts & Quick Actions | [../prompts.md](../prompts.md)       |
| Message Hooks           | [../hooks.md](../hooks.md)           |
| Web Interface           | [../web/README.md](../web/README.md) |

---

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

## Installation from GitHub Releases

When downloading Mitto.app from GitHub Releases, macOS Gatekeeper will show a security
warning because the app is not signed with an Apple Developer certificate.

### First Launch Warning

On first launch, you'll see a dialog saying:

> "Mitto" cannot be opened because it is from an unidentified developer.

### How to Open the App

**Option 1: Right-click to Open (Recommended)**

1. Right-click (or Control-click) on `Mitto.app`
2. Select **Open** from the context menu
3. Click **Open** in the dialog that appears

This only needs to be done once. After that, you can open the app normally.

**Option 2: System Settings**

1. Try to open `Mitto.app` (it will be blocked)
2. Open **System Settings** → **Privacy & Security**
3. Scroll down to find the message about Mitto being blocked
4. Click **Open Anyway**
5. Enter your password if prompted

### Why This Happens

The Mitto macOS app is **ad-hoc signed** with entitlements (required for
features like native notifications), but it's not signed with an Apple
Developer ID certificate. This means:

- ✅ All features work correctly (notifications, hotkeys, etc.)
- ✅ The app is safe to run
- ⚠️ macOS Gatekeeper shows a warning on first launch

Proper code signing with an Apple Developer ID ($99/year) would eliminate this warning, but is not currently implemented.

## How It Works

The macOS app:

1. Starts the internal web server on a random localhost port
2. Opens a native WebView window pointing to that URL
3. Creates native menus with keyboard shortcuts
4. Registers a global hotkey (⌘+Control+M) for quick access
5. Shuts down the server when the window is closed

This reuses 100% of the web interface code while providing native macOS integration.

### Global Hotkeys

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

### Menu Hotkeys

The following keyboard shortcuts are available from the app menu:

**Conversations:**

| Shortcut | Action              | Description                                    |
| -------- | ------------------- | ---------------------------------------------- |
| `⌘N`     | New Conversation    | Create a new conversation                      |
| `⌘W`     | Close Conversation  | Close the current conversation                 |
| `⌘1-9`   | Switch Conversation | Switch to conversation 1-9 in the sidebar list |

**Navigation:**

| Shortcut | Action         | Description                |
| -------- | -------------- | -------------------------- |
| `⌘,`     | Settings       | Open the settings dialog   |
| `⌘L`     | Focus Input    | Focus the chat input field |
| `⌘⇧S`    | Toggle Sidebar | Show/hide the sidebar      |

These shortcuts are fixed and cannot be customized.

### All Keyboard Shortcuts

Here's a complete reference of all keyboard shortcuts:

| Shortcut | Action                     | Notes                                              |
| -------- | -------------------------- | -------------------------------------------------- |
| `⌘⇧M`    | Show/Hide Window           | Global hotkey (works even when app is not focused) |
| `⌘N`     | New Conversation           |                                                    |
| `⌘W`     | Close Conversation         |                                                    |
| `⌘,`     | Settings                   | Standard macOS preference shortcut                 |
| `⌘1-9`   | Switch to Conversation 1-9 | Based on sidebar order                             |
| `⌘L`     | Focus Input                |                                                    |
| `⌘⇧S`    | Toggle Sidebar             |                                                    |

You can also view these shortcuts in the app by clicking the keyboard icon in the sidebar footer.

## Window Behavior

### Configuring via Settings Dialog

1. Open the Settings dialog (gear icon in sidebar)
2. Click the **UI** tab (only visible in macOS app)
3. Toggle **Start at Login** under macOS Settings
4. Click **Save Changes**

The setting takes effect immediately—no restart required.

## Notifications

Configure notification behavior for the macOS app.

### Native Notifications

Display notifications in the macOS Notification Center instead of in-app toasts. When enabled:

- Notifications appear in the top-right corner of the screen (Notification Center)
- Notifications are visible even when the app is in the background
- Clicking a notification brings the app to the foreground and switches to that session
- Notifications are grouped by session
- **Auto-dismiss**: Notifications automatically disappear after 5 seconds to keep Notification Center clean
- **Auto-cleanup**: Notifications are removed when you switch to that session

**Note:** The first time you enable this, macOS will prompt you to allow notifications. If you deny the permission, you can enable it later in System Settings → Notifications → Mitto.

### Configuring via Settings Dialog

You can also configure these settings through the UI:

1. Open the Settings dialog (gear icon in sidebar)
2. Click the **UI** tab (only visible in macOS app)
3. Toggle **Native notifications** to use macOS Notification Center
4. Toggle **Play sound when agent completes** for audio feedback
5. Click **Save Changes**

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
      "start_at_login": true,
      "notifications": {
        "native_enabled": true,
        "sounds": {
          "agent_completed": true
        }
      }
    }
  }
}
```
