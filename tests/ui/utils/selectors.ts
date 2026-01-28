/**
 * Centralized selectors for Mitto UI tests.
 * Using a single source of truth for selectors makes tests more maintainable.
 */

export const selectors = {
  // App container
  app: '#app',
  loadingSpinner: '.animate-spin',

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

  // Sessions/Conversations sidebar
  sessionsHeader: 'h2:has-text("Conversations")',
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

