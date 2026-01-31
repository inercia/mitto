// Mitto Web Interface - Constants
// Centralized configuration values and constants

/**
 * Keyboard shortcuts configuration
 * Used by the KeyboardShortcutsDialog to display available shortcuts
 *
 * Note: Some shortcuts only work in the native macOS app (handled by native menu)
 */
export const KEYBOARD_SHORTCUTS = [
  // Global hotkey (works even when app is not focused - macOS app only)
  {
    keys: "⌘⌃M",
    description: "Show/hide window",
    macOnly: true,
    section: "Global",
  },
  // File menu shortcuts (native menu in macOS app, not available in browser)
  {
    keys: "⌘N",
    description: "New conversation",
    macOnly: true,
    section: "Conversations",
  },
  {
    keys: "⌘W",
    description: "Close conversation",
    macOnly: true,
    section: "Conversations",
  },
  // Web shortcuts (work in both macOS app and browser)
  {
    keys: "⌘1-9",
    description: "Switch to conversation 1-9",
    section: "Conversations",
  },
  {
    keys: "⌘⌃↑",
    description: "Previous conversation",
    macOnly: true,
    section: "Conversations",
  },
  {
    keys: "⌘⌃↓",
    description: "Next conversation",
    macOnly: true,
    section: "Conversations",
  },
  { keys: "⌘,", description: "Settings", section: "Navigation" },
  // View menu shortcuts (native menu in macOS app, not available in browser)
  {
    keys: "⌘L",
    description: "Focus input",
    macOnly: true,
    section: "Navigation",
  },
  {
    keys: "⌘⇧S",
    description: "Toggle sidebar",
    macOnly: true,
    section: "Navigation",
  },
  // Input shortcuts (work in both macOS app and browser)
  { keys: "⌃P", description: "Improve prompt (magic wand)", section: "Input" },
];
