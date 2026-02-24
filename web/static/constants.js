// Mitto Web Interface - Constants
// Centralized configuration values and constants

// =============================================================================
// Periodic Progress Indicator Configuration
// =============================================================================

/**
 * Progress indicator style for periodic sessions in the sidebar.
 * The background of the session item shows elapsed/remaining time until next run.
 *
 * Available styles:
 * - "progress_bar": Full-width bar as item background (clear fill, default)
 * - "dark_overlay": Darker shade for elapsed time (subtle)
 * - "accent_tint": Green tint for elapsed, shifts to red when urgent
 * - "gradient_fade": Smooth gradient transition
 * - "none": Disable progress indicator
 */
export const PERIODIC_PROGRESS_STYLE = "progress_bar";

/**
 * Color configurations for each progress style.
 * Each style defines colors for light and dark themes.
 */
export const PERIODIC_PROGRESS_COLORS = {
  // Progress bar: full-width fill as item background (uses slate-700 gray like ACP cards)
  progress_bar: {
    light: {
      elapsed: "rgba(51, 65, 85, 0.20)", // slate-700 at 20% - matches ACP card bg
      remaining: "transparent",
      urgentElapsed: "rgba(220, 38, 38, 0.25)", // red-600 when urgent (>= 95% elapsed)
    },
    dark: {
      elapsed: "rgba(51, 65, 85, 0.30)", // slate-700 at 30% - slightly more visible in dark mode
      remaining: "transparent",
      urgentElapsed: "rgba(220, 38, 38, 0.35)", // red-600 when urgent (>= 95% elapsed)
    },
  },
  // Style 1: Subtle dark overlay - just darkens the elapsed portion
  dark_overlay: {
    light: {
      elapsed: "rgba(0, 0, 0, 0.08)", // Slightly darker
      remaining: "transparent",
      urgentElapsed: "rgba(220, 38, 38, 0.12)", // Red tint when < 25% remaining
    },
    dark: {
      elapsed: "rgba(0, 0, 0, 0.25)", // Darker overlay
      remaining: "transparent",
      urgentElapsed: "rgba(220, 38, 38, 0.2)", // Red tint when < 25% remaining
    },
  },
  // Style 2: Accent color tint - green for normal, red when urgent
  accent_tint: {
    light: {
      elapsed: "rgba(34, 197, 94, 0.12)", // Green tint
      remaining: "transparent",
      urgentElapsed: "rgba(220, 38, 38, 0.15)", // Red tint when < 25% remaining
    },
    dark: {
      elapsed: "rgba(34, 197, 94, 0.15)", // Green tint
      remaining: "transparent",
      urgentElapsed: "rgba(220, 38, 38, 0.2)", // Red tint when < 25% remaining
    },
  },
  // Style 3: Gradient fade - smooth transition
  gradient_fade: {
    light: {
      elapsed: "rgba(59, 130, 246, 0.1)", // Blue tint
      remaining: "rgba(59, 130, 246, 0.02)", // Very faint
      urgentElapsed: "rgba(249, 115, 22, 0.15)", // Orange when urgent
    },
    dark: {
      elapsed: "rgba(59, 130, 246, 0.15)", // Blue tint
      remaining: "rgba(59, 130, 246, 0.03)", // Very faint
      urgentElapsed: "rgba(249, 115, 22, 0.2)", // Orange when urgent
    },
  },
};

/**
 * Threshold for "urgent" state (percentage of time remaining).
 * When remaining time is below this threshold, the urgent color is used.
 * At 0.05 (5%), urgent triggers when >= 95% of the interval has elapsed.
 */
export const PERIODIC_PROGRESS_URGENT_THRESHOLD = 0.05; // 5% remaining = 95% elapsed

// =============================================================================
// Conversation Cycling
// =============================================================================

/**
 * Conversation cycling mode constants.
 * Determines which conversations are included when cycling with keyboard shortcuts or gestures.
 */
export const CYCLING_MODE = {
  /** Cycle through all non-archived conversations (default) */
  ALL: "all",
  /** Cycle only through conversations in expanded/open groups */
  VISIBLE_GROUPS: "visible_groups",
};

/**
 * Conversation cycling mode options for the settings UI dropdown.
 */
export const CYCLING_MODE_OPTIONS = [
  { value: CYCLING_MODE.ALL, label: "All conversations" },
  { value: CYCLING_MODE.VISIBLE_GROUPS, label: "Visible groups only" },
];

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
  {
    keys: "↵",
    description: "Send message",
    section: "Input",
  },
  {
    keys: "⇧↵",
    description: "Insert newline",
    section: "Input",
  },
  {
    keys: "⌘↵",
    description: "Add message to queue",
    section: "Input",
    hint: "Use Ctrl+Enter on Windows/Linux",
  },
  {
    keys: "⌃P",
    description: "Improve prompt (magic wand)",
    section: "Input",
  },
];
