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
- [Open In targets](#open-in-targets)

## Related Documentation

| Topic                   | Location                             |
| ----------------------- | ------------------------------------ |
| Configuration Overview  | [../overview.md](../overview.md)     |
| ACP Servers             | [../acp.md](../acp.md)               |
| Prompts & Quick Actions | [../prompts.md](../prompts.md)       |
| Command Processors      | [../processors.md](../processors.md) |
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

## Open In targets

The **Open In** section configures the list of external applications that can open a
workspace folder from the sidebar. Right-clicking a folder header in the sidebar shows
an **Open ▸** submenu whose entries come directly from this list; clicking an entry
runs its configured shell command with `${MITTO_WORKING_DIR}` substituted for the
folder's absolute path.

### Configuring via Settings Dialog

1. Open the Settings dialog (gear icon in sidebar)
2. Click the **UI** tab (only visible in macOS app)
3. Scroll to the **Open In** section
4. Toggle any built-in target on or off, or click the row to expand and edit its
   command
5. Click **+ Add custom…** to append a user-defined target
6. Click **Save Changes**

Only targets with **Enabled** turned on appear in the folder context-menu submenu.
When every target is disabled the **Open ▸** entry is hidden entirely.

### Built-in targets (macOS)

On macOS, the following seven targets are available out of the box. They are all
labeled as built-in in the UI and cannot be removed, only enabled/disabled or have
their command edited:

| ID         | Default label        | Default enabled | Command                                          |
| ---------- | -------------------- | --------------- | ------------------------------------------------ |
| `finder`   | Finder               | ✅ yes          | `open ${MITTO_WORKING_DIR}`                      |
| `terminal` | Terminal             | ✅ yes          | `open -a Terminal ${MITTO_WORKING_DIR}`          |
| `iterm`    | iTerm                | ❌ no           | `open -a iTerm ${MITTO_WORKING_DIR}`             |
| `vscode`   | Visual Studio Code   | ❌ no           | `open -a "Visual Studio Code" ${MITTO_WORKING_DIR}` |
| `cursor`   | Cursor               | ❌ no           | `open -a Cursor ${MITTO_WORKING_DIR}`            |
| `xcode`    | Xcode                | ❌ no           | `open -a Xcode ${MITTO_WORKING_DIR}`             |
| `goland`   | GoLand               | ❌ no           | `open -a GoLand ${MITTO_WORKING_DIR}`            |

### Adding a custom target

Click **+ Add custom…** in the Open In section, then fill in:

- **Label** — the text shown in the context menu (e.g. `Sublime Text`).
- **Command** — any shell command; use `${MITTO_WORKING_DIR}` where you want the
  workspace folder path substituted (it is quoted safely at exec time).

Custom targets get an auto-generated `id`; they are persisted alongside the
built-ins under `ui.mac.open_in.targets` in `settings.json` and can be removed
from the same row.

### JSON example

```json
{
  "ui": {
    "mac": {
      "open_in": {
        "targets": [
          {
            "id": "finder",
            "label": "Finder",
            "icon": "finder",
            "command": "open ${MITTO_WORKING_DIR}",
            "enabled": true,
            "builtin": true
          },
          {
            "id": "sublime",
            "label": "Sublime Text",
            "command": "open -a \"Sublime Text\" ${MITTO_WORKING_DIR}",
            "enabled": true
          }
        ]
      }
    }
  }
}
```

Merging rules:

- Any built-in target you do not list keeps its default label/command and stays
  enabled/disabled per the default table above.
- Listing a built-in target by its `id` overrides those fields; the entry
  remains marked `builtin: true` and cannot be deleted from the UI.
- Entries with an unknown or empty `id` are ignored.

### Legacy fallback

Older installations may still have `ui.mac.badge_click_action` and
`ui.mac.terminal_action` in `settings.json` (from before the Open In section
existed). When `ui.mac.open_in.targets` is absent or empty, Mitto synthesizes
a two-entry list — a Finder entry from `badge_click_action` and a Terminal
entry from `terminal_action` — so those installations keep working unchanged.
Once you save any Open In configuration through the Settings dialog, the new
`ui.mac.open_in.targets` list becomes authoritative and the legacy fields are
ignored.

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
