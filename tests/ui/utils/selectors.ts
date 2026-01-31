/**
 * Centralized selectors for Mitto UI tests.
 * Using a single source of truth for selectors makes tests more maintainable.
 */

/**
 * API prefix for all API endpoints.
 * The server uses /mitto as the default prefix for security through obscurity.
 */
export const API_PREFIX = '/mitto';

/**
 * Build an API URL with the configured prefix.
 * @param path - The API path (e.g., "/api/sessions")
 * @returns The full URL with prefix (e.g., "/mitto/api/sessions")
 */
export function apiUrl(path: string): string {
  // Ensure path starts with /
  if (!path.startsWith('/')) {
    path = '/' + path;
  }
  return API_PREFIX + path;
}

export const selectors = {
  // App container
  app: '#app',
  // Use a more specific selector for the app loading spinner (not tool call spinners)
  // The app loading spinner is in the center of the screen, not inside a message
  loadingSpinner: '#app > .animate-spin, .flex.items-center.justify-center > .animate-spin',

  // Chat input area
  chatInput: 'textarea',
  sendButton: 'button:has-text("Send")',
  cancelButton: 'button:has-text("Cancel")',

  // Messages
  userMessage: '.bg-mitto-user, .bg-blue-600',
  agentMessage: '.bg-mitto-agent, .prose',
  systemMessage: '.text-gray-500, .text-xs',
  errorMessage: '.text-red-500',
  thoughtMessage: '.text-gray-400',
  toolMessage: '.text-yellow-500:has-text("🔧")',
  // All messages in the chat (for ordering tests)
  allMessages: '.message-enter',

  // Sessions/Conversations sidebar
  // Note: The UI uses "Conversations" as the heading text
  conversationsHeader: 'h2:has-text("Conversations")',
  sessionsHeader: 'h2:has-text("Conversations")', // Alias for backwards compatibility
  sessionsList: '[class*="border-b"][class*="cursor-pointer"]',
  newSessionButton: 'button[title="New Conversation"]',
  sessionItem: (name: string) =>
    `[class*="border-b"][class*="cursor-pointer"]:has-text("${name}")`,

  // Dialogs
  settingsDialog: '[role="dialog"]',
  workspaceDialog: '[role="dialog"]:has-text("Workspace")',

  // Header
  header: 'header',
  connectionStatus: '[class*="connection"]',

  // Body
  body: 'body',
  darkTheme: '.bg-mitto-bg',
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

