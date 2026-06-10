/**
 * Centralized selectors for Mitto UI tests.
 * Using a single source of truth for selectors makes tests more maintainable.
 */

/**
 * API prefix for all API endpoints.
 * The server uses /mitto as the default prefix for security through obscurity.
 */
export const API_PREFIX = "/mitto";

/**
 * Build an API URL with the configured prefix.
 * @param path - The API path (e.g., "/api/sessions")
 * @returns The full URL with prefix (e.g., "/mitto/api/sessions")
 */
export function apiUrl(path: string): string {
  // Ensure path starts with /
  if (!path.startsWith("/")) {
    path = "/" + path;
  }
  return API_PREFIX + path;
}

export const selectors = {
  // App container
  app: "#app",
  // App-readiness loader (the pre-mount "Loading Mitto..." screen in index.html).
  // Anchored on a stable data-testid so it stays decoupled from the spinner's
  // animation classes (the loader now uses daisyUI `loading`). This must NOT match
  // tool-call or reconnect spinners — only the initial app loader.
  loadingSpinner: '[data-testid="app-loading"]',

  // Chat input area
  chatInput: "textarea",
  // Send button is now icon-only (paper plane SVG), use type="submit" to identify it
  sendButton: 'button[type="submit"]',
  // Stop button appears when streaming (red square icon)
  stopButton: 'button[title="Stop streaming"]',
  cancelButton: 'button:has-text("Cancel")',

  // Messages
  userMessage: ".bg-mitto-user, .bg-blue-600",
  agentMessage: ".bg-mitto-agent, .prose",
  systemMessage: ".text-gray-500, .text-xs",
  errorMessage: ".text-red-500",
  thoughtMessage: ".text-gray-400",
  toolMessage: '.text-yellow-500:has-text("🔧")',
  // All messages in the chat (for ordering tests)
  allMessages: ".message-enter",
  // Messages container (for scroll operations) - the one with messages-container-reverse class
  messagesContainer: ".messages-container-reverse",

  // Sessions/Conversations sidebar
  // Note: The UI uses "Conversations" as the heading text
  conversationsHeader: 'h2:has-text("Conversations")',
  sessionsHeader: 'h2:has-text("Conversations")', // Alias for backwards compatibility
  // Session items are in containers with class "session-item-container"
  sessionsList: '.session-item-container',
  newSessionButton: 'button[title="New Conversation"]',
  sessionItem: (name: string) =>
    `.session-item-container:has-text("${name}")`,
  // Active session: the clickable inner div gets border-l-mitto-accent-500 when isActive
  activeSessionItem: '[data-session-id][class*="border-l-mitto-accent-500"]',
  activeSessionItemById: (id: string) =>
    `[data-session-id="${id}"][class*="border-l-mitto-accent-500"]`,

  // Dialogs
  settingsDialog: '[role="dialog"]',
  workspaceDialog: '[role="dialog"]:has-text("Workspace")',

  // Header
  header: "header",
  connectionStatus: '[class*="connection"]',

  // Body
  body: "body",
  darkTheme: ".bg-mitto-bg",

  // Queue dropdown
  queueToggleButton: '[data-queue-toggle]',
  queueDropdown: '.queue-dropdown',
  queueResizeHandle: '.queue-resize-handle',
  queueDropdownHeader: '.queue-dropdown-header',
  queueDropdownList: '.queue-dropdown-list',
  queueDropdownItem: '.queue-dropdown-item',
  queueDropdownEmpty: '.queue-dropdown-empty',

  // Scroll to bottom button
  scrollToBottomWrapper: '.scroll-to-bottom-wrapper',
  scrollToBottomButton: '.scroll-to-bottom-btn',

  // Message list — empty state
  // Shown when messages.length === 0 (no active session or brand-new session)
  emptyStateFavicon: 'img[alt="Mitto"]',
  emptyStateHeading: 'p.text-2xl',

  // Message list — date separator
  // Appears between messages of different calendar days (or at the oldest message)
  dateSeparator: '.date-separator',

  // Message list — error bubble and retry button
  errorMessageBubble: '.error-message-bubble',
  retryButton: 'button[title="Retry — resend the last prompt"]',
} as const;

/**
 * Common timeouts for different operations
 */
export const timeouts = {
  pageLoad: 10000,
  appReady: 10000,
  agentResponse: 60000,
  shortAction: 5000,
  animation: 500,
} as const;
