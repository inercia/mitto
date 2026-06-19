// Mitto Web Interface - Reusable Icon Components
// Centralizes SVG icons to reduce duplication and ensure consistency

const { html } = window.preact;

/**
 * Spinner icon for loading states
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function SpinnerIcon({ className = "w-4 h-4" }) {
  // daisyUI `loading loading-spinner` animates itself; strip any legacy
  // `animate-spin` passed by callers to avoid a double animation.
  const cls = className.replace(/\banimate-spin\b/g, "").replace(/\s+/g, " ").trim();
  return html`
    <span class="loading loading-spinner ${cls}"></span>
  `;
}

/**
 * Close/X icon
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function CloseIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M6 18L18 6M6 6l12 12"
      />
    </svg>
  `;
}

/**
 * Settings gear icon
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function SettingsIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"
      />
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"
      />
    </svg>
  `;
}

/**
 * Duplicate/Copy icon for duplicating items
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function DuplicateIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <rect
        x="9"
        y="9"
        width="13"
        height="13"
        rx="2"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"
      />
    </svg>
  `;
}

/**
 * Plus icon for adding items
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function PlusIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M12 4v16m8-8H4"
      />
    </svg>
  `;
}

/**
 * Chevron up icon
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function ChevronUpIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M5 15l7-7 7 7"
      />
    </svg>
  `;
}

/**
 * Chevron down icon
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function ChevronDownIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M19 9l-7 7-7-7"
      />
    </svg>
  `;
}

/**
 * Hamburger menu icon
 * @param {string} className - CSS classes (default: 'w-6 h-6')
 */
export function MenuIcon({ className = "w-6 h-6" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M4 6h16M4 12h16M4 18h16"
      />
    </svg>
  `;
}

/**
 * Trash/delete icon
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function TrashIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"
      />
    </svg>
  `;
}

/**
 * Broom icon (used for "clean up" actions)
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function BroomIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      fill="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        d="M19.36 2.72l1.42 1.42-5.72 5.71c1.07 1.54 1.22 3.39.32 4.59L9.06 8.12c1.2-.9 3.05-.75 4.59.32l5.71-5.72M5.93 17.57c-2.01-2.01-3.24-4.41-3.58-6.65l4.88-2.09 7.44 7.44-2.09 4.88c-2.24-.34-4.64-1.57-6.65-3.58z"
      />
    </svg>
  `;
}

/**
 * Edit/pencil icon
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function EditIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M15.232 5.232l3.536 3.536m-2.036-5.036a2.5 2.5 0 113.536 3.536L6.5 21.036H3v-3.572L16.732 3.732z"
      />
    </svg>
  `;
}

/**
 * Clipboard/copy icon for "Copy as Markdown" affordances
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function CopyIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2"
      />
    </svg>
  `;
}

/**
 * Checkmark icon
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function CheckIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M5 13l4 4L19 7"
      />
    </svg>
  `;
}

/**
 * Hollow circle icon (used for the "open" status filter)
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function CircleIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <circle cx="12" cy="12" r="8" stroke-width="2" />
    </svg>
  `;
}

/**
 * Arrow down icon (for scroll to bottom)
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function ArrowDownIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M19 14l-7 7m0 0l-7-7m7 7V3"
      />
    </svg>
  `;
}

export function ArrowUpIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M5 10l7-7m0 0l7 7m-7-7v18"
      />
    </svg>
  `;
}

export function SortIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M3 7.5 7.5 3m0 0L12 7.5M7.5 3v13.5m13.5 0L16.5 21m0 0L12 16.5m4.5 4.5V7.5"
      />
    </svg>
  `;
}

/**
 * Save/download icon
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function SaveIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M8 7H5a2 2 0 00-2 2v9a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-3m-1 4l-3 3m0 0l-3-3m3 3V4"
      />
    </svg>
  `;
}

/**
 * Magic wand/sparkles icon (for AI improve prompt)
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function MagicWandIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M5 3v4M3 5h4M6 17v4m-2-2h4m5-16l2.286 6.857L21 12l-5.714 2.143L13 21l-2.286-6.857L5 12l5.714-2.143L13 3z"
      />
    </svg>
  `;
}

/**
 * Image/photo icon
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function ImageIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z"
      />
    </svg>
  `;
}

/**
 * Lightning bolt icon (for prompts)
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function LightningIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="currentColor"
      viewBox="0 0 24 24"
    >
      <path d="M13 10V3L4 14h7v7l9-11h-7z" />
    </svg>
  `;
}

/**
 * Robot icon for MCP-spawned child conversations
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function RobotIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M9 3v1m6-1v1M9 19v1m6-1v1M5 8h14a2 2 0 012 2v6a2 2 0 01-2 2H5a2 2 0 01-2-2v-6a2 2 0 012-2zM9 13h.01M15 13h.01"
      />
    </svg>
  `;
}

/**
 * Link/chain icon (for linked items and auto-created child conversations)
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function LinkIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M13.19 8.688a4.5 4.5 0 011.242 7.244l-4.5 4.5a4.5 4.5 0 01-6.364-6.364l1.757-1.757m13.35-.622l1.757-1.757a4.5 4.5 0 00-6.364-6.364l-4.5 4.5a4.5 4.5 0 001.242 7.244"
      />
    </svg>
  `;
}

/**
 * Person icon for human-created child conversations
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function PersonIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z"
      />
    </svg>
  `;
}

/**
 * Hourglass icon for parent conversations waiting for children
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function HourglassIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M5 3h14M5 21h14M7 3v4l5 5-5 5v4M17 3v4l-5 5 5 5v4"
      />
    </svg>
  `;
}

/**
 * Question mark circle icon for conversations waiting for user input
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function QuestionMarkIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M8.228 9c.549-1.165 2.03-2 3.772-2 2.21 0 4 1.343 4 3 0 1.4-1.278 2.575-3.006 2.907-.542.104-.994.54-.994 1.093m0 3h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
      />
    </svg>
  `;
}

/**
 * Stop/square icon (for cancel streaming)
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function StopIcon({ className = "w-4 h-4" }) {
  return html`
    <svg class="${className}" fill="currentColor" viewBox="0 0 24 24">
      <rect x="6" y="6" width="12" height="12" rx="2" />
    </svg>
  `;
}

/**
 * Error/warning circle icon
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function ErrorIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
      />
    </svg>
  `;
}

/**
 * Server icon
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function ServerIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2"
      />
    </svg>
  `;
}

/**
 * Server with dots icon (for empty server state)
 * @param {string} className - CSS classes (default: 'w-12 h-12')
 */
export function ServerEmptyIcon({ className = "w-12 h-12" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2m-2-4h.01M17 16h.01"
      />
    </svg>
  `;
}

/**
 * Folder icon
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function FolderIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z"
      />
    </svg>
  `;
}

/**
 * Keyboard icon
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function KeyboardIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <rect x="2" y="4" width="20" height="16" rx="2" ry="2"></rect>
      <path d="M6 8h.001"></path>
      <path d="M10 8h.001"></path>
      <path d="M14 8h.001"></path>
      <path d="M18 8h.001"></path>
      <path d="M8 12h.001"></path>
      <path d="M12 12h.001"></path>
      <path d="M16 12h.001"></path>
      <path d="M7 16h10"></path>
    </svg>
  `;
}

/**
 * Sun icon (for light theme)
 * @param {string} className - CSS classes (default: '')
 */
export function SunIcon({ className = "" }) {
  return html`
    <svg
      class="${className}"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <circle cx="12" cy="12" r="4"></circle>
      <path d="M12 2v2"></path>
      <path d="M12 20v2"></path>
      <path d="m4.93 4.93 1.41 1.41"></path>
      <path d="m17.66 17.66 1.41 1.41"></path>
      <path d="M2 12h2"></path>
      <path d="M20 12h2"></path>
      <path d="m6.34 17.66-1.41 1.41"></path>
      <path d="m19.07 4.93-1.41 1.41"></path>
    </svg>
  `;
}

/**
 * Moon icon (for dark theme)
 * @param {string} className - CSS classes (default: '')
 */
export function MoonIcon({ className = "" }) {
  return html`
    <svg
      class="${className}"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"></path>
    </svg>
  `;
}

/**
 * Drag handle icon (six dots / grip)
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function DragHandleIcon({ className = "w-4 h-4" }) {
  return html`
    <svg class="${className}" fill="currentColor" viewBox="0 0 24 24">
      <circle cx="9" cy="6" r="1.5" />
      <circle cx="15" cy="6" r="1.5" />
      <circle cx="9" cy="12" r="1.5" />
      <circle cx="15" cy="12" r="1.5" />
      <circle cx="9" cy="18" r="1.5" />
      <circle cx="15" cy="18" r="1.5" />
    </svg>
  `;
}

/**
 * Queue icon - Arrow pointing up with stacked layers (like "arrow_upward_alt" + stack)
 * Represents messages queued/stacked waiting to be sent
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function QueueIcon({ className = "w-4 h-4" }) {
  return html`
    <svg class="${className}" fill="currentColor" viewBox="0 0 24 24">
      <!-- Top arrow -->
      <path d="M12 3L6 9h4v4h4V9h4L12 3z" />
      <!-- Stacked layers below -->
      <path d="M6 15h12v2H6v-2z" opacity="0.7" />
      <path d="M6 19h12v2H6v-2z" opacity="0.4" />
    </svg>
  `;
}

/**
 * Horizontal grip/handle icon for resize operations
 * Shows a pill-shaped horizontal line that indicates "drag here"
 * @param {string} className - CSS classes (default: 'w-8 h-1')
 */
export function GripIcon({ className = "w-8 h-1" }) {
  return html`
    <svg class="${className}" viewBox="0 0 32 6" fill="currentColor">
      <rect x="0" y="0" width="32" height="6" rx="3" />
    </svg>
  `;
}

/**
 * Pin icon (outline) for unpinned state
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function PinIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
      stroke-width="2"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        d="M16 3.5l-4 4-5-1.5-1.5 1.5 4 4-5 6.5 6.5-5 4 4 1.5-1.5-1.5-5 4-4-3-3z"
      />
    </svg>
  `;
}

/**
 * Pin filled icon for pinned state
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function PinFilledIcon({ className = "w-4 h-4" }) {
  return html`
    <svg class="${className}" fill="currentColor" viewBox="0 0 24 24">
      <path
        d="M16 3.5l-4 4-5-1.5-1.5 1.5 4 4-5 6.5 6.5-5 4 4 1.5-1.5-1.5-5 4-4-3-3z"
      />
    </svg>
  `;
}

/**
 * Archive icon (outline) for unarchived state
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function ArchiveIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
      stroke-width="2"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        d="M5 8h14M5 8a2 2 0 110-4h14a2 2 0 110 4M5 8v10a2 2 0 002 2h10a2 2 0 002-2V8m-9 4h4"
      />
    </svg>
  `;
}

export function FilterIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      xmlns="http://www.w3.org/2000/svg"
      fill="none"
      viewBox="0 0 24 24"
      stroke-width="1.5"
      stroke="currentColor"
      class="${className}"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        d="M3.792 2.938A49.069 49.069 0 0 1 12 2.25c2.797 0 5.54.236 8.209.688a1.857 1.857 0 0 1 1.541 1.836v1.044a3 3 0 0 1-.879 2.121l-6.182 6.182a1.5 1.5 0 0 0-.439 1.061v2.927a3 3 0 0 1-1.658 2.684l-1.757.878A.75.75 0 0 1 9.75 21v-5.818a1.5 1.5 0 0 0-.44-1.06L3.13 7.938a3 3 0 0 1-.879-2.121V4.774c0-.897.64-1.683 1.542-1.836Z"
      />
    </svg>
  `;
}

/**
 * Archive filled icon for archived state
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function ArchiveFilledIcon({ className = "w-4 h-4" }) {
  return html`
    <svg class="${className}" fill="currentColor" viewBox="0 0 24 24">
      <path
        d="M20 2H4c-1.1 0-2 .9-2 2v2c0 .55.45 1 1 1h18c.55 0 1-.45 1-1V4c0-1.1-.9-2-2-2z"
      />
      <path d="M19 8H5v10c0 1.1.9 2 2 2h10c1.1 0 2-.9 2-2V8zm-7 8h-2v-4h2v4z" />
    </svg>
  `;
}

/**
 * Periodic/repeat icon (circular arrow) for recurring conversations
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function PeriodicIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"
      />
    </svg>
  `;
}

/**
 * Periodic filled icon for active periodic state
 * Shows a filled circular badge with contrasting white arrows inside
 * Creates an "inverted" look compared to the outline PeriodicIcon
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function PeriodicFilledIcon({ className = "w-4 h-4" }) {
  return html`
    <svg class="${className}" viewBox="0 0 24 24">
      <!-- Filled circle background using currentColor (will be blue/colored) -->
      <circle cx="12" cy="12" r="11" fill="currentColor" />
      <!-- Refresh/sync arrows in white for contrast against colored circle -->
      <g
        fill="none"
        stroke="white"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      >
        <!-- Top-right arrow pointing down-left (refresh from top) -->
        <path d="M16.5 8.5A5 5 0 0 0 8 8" />
        <polyline points="17 5 17 9 13 9" />
        <!-- Bottom-left arrow pointing up-right (refresh from bottom) -->
        <path d="M7.5 15.5A5 5 0 0 0 16 16" />
        <polyline points="7 19 7 15 11 15" />
      </g>
    </svg>
  `;
}

/**
 * Play filled icon for "run now" action
 * Shows a filled circular badge with a white triangle (play arrow) inside
 * Similar style to PeriodicFilledIcon but indicates "run/play" action
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function PlayFilledIcon({ className = "w-4 h-4" }) {
  return html`
    <svg class="${className}" viewBox="0 0 24 24">
      <!-- Filled circle background using currentColor (will be blue/colored) -->
      <circle cx="12" cy="12" r="11" fill="currentColor" />
      <!-- Play triangle in white for contrast against colored circle -->
      <polygon points="9,6 19,12 9,18" fill="white" />
    </svg>
  `;
}

/**
 * Pause filled icon for "pause" action
 * Shows a filled circular badge with two white vertical bars inside
 * Similar style to PlayFilledIcon but indicates "pause" action
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function PauseFilledIcon({ className = "w-4 h-4" }) {
  return html`
    <svg class="${className}" viewBox="0 0 24 24">
      <!-- Filled circle background using currentColor (will be blue/colored) -->
      <circle cx="12" cy="12" r="11" fill="currentColor" />
      <!-- Two pause bars in white for contrast against colored circle -->
      <rect x="8" y="7" width="3" height="10" rx="1" fill="white" />
      <rect x="13" y="7" width="3" height="10" rx="1" fill="white" />
    </svg>
  `;
}

/**
 * List/no-grouping icon (horizontal lines)
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function ListIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M4 6h16M4 12h16M4 18h16"
      />
    </svg>
  `;
}

/**
 * Chevron right icon (for collapsed groups)
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function ChevronRightIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M9 5l7 7-7 7"
      />
    </svg>
  `;
}

// Lock icon (closed padlock) - for locked periodic prompt state
export function LockIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <!-- Closed padlock: shackle is closed (connected to body) -->
      <rect
        x="5"
        y="11"
        width="14"
        height="10"
        rx="2"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M8 11V7a4 4 0 018 0v4"
      />
      <circle cx="12" cy="16" r="1" fill="currentColor" />
    </svg>
  `;
}

// Unlock icon (open padlock) - for unlocked periodic prompt state
export function UnlockIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <!-- Open padlock: shackle is open (right side lifted up) -->
      <rect
        x="5"
        y="11"
        width="14"
        height="10"
        rx="2"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
      <!-- Open shackle: left side goes down to body, right side is lifted -->
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M8 11V7a4 4 0 017.5-2"
      />
      <circle cx="12" cy="16" r="1" fill="currentColor" />
    </svg>
  `;
}

/**
 * Globe icon (for web/external access settings)
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function GlobeIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <circle cx="12" cy="12" r="10" stroke-width="2" />
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M2 12h20M12 2a15.3 15.3 0 014 10 15.3 15.3 0 01-4 10 15.3 15.3 0 01-4-10 15.3 15.3 0 014-10z"
      />
    </svg>
  `;
}

/**
 * Chat bubble icon (for conversations tab)
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function ChatBubbleIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z"
      />
    </svg>
  `;
}

/**
 * Sliders/adjustments icon (for UI settings)
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function SlidersIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3M1 14h6M9 8h6M17 16h6"
      />
    </svg>
  `;
}

/**
 * Shield icon (for permissions settings)
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function ShieldIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z"
      />
    </svg>
  `;
}

/**
 * Layers/stacked icon (for workspace grouping - folder + agent combination)
 * Shows stacked rectangles representing a workspace concept
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function LayersIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M19 11H5m14 0a2 2 0 012 2v6a2 2 0 01-2 2H5a2 2 0 01-2-2v-6a2 2 0 012-2m14 0V9a2 2 0 00-2-2M5 11V9a2 2 0 012-2m0 0V5a2 2 0 012-2h6a2 2 0 012 2v2M7 7h10"
      />
    </svg>
  `;
}


/**
 * Search / magnifying glass icon
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function SearchIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"
      />
    </svg>
  `;
}

/**
 * Refresh icon (arrow-path / Heroicons)
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function RefreshIcon({ className = "w-4 h-4" }) {
  return html`
    <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class=${className}>
      <path stroke-linecap="round" stroke-linejoin="round" d="M16.023 9.348h4.992v-.001M2.985 19.644v-4.992m0 0h4.992m-4.992 0 3.181 3.183a8.25 8.25 0 0 0 13.803-3.7M4.031 9.865a8.25 8.25 0 0 1 13.803-3.7l3.181 3.182M21.015 4.356v4.992" />
    </svg>
  `;
}

/**
 * Sync icon (bidirectional up/down arrows) — used for upstream sync to visually
 * distinguish it from the circular RefreshIcon used to reload a list. The
 * vertical arrows pair naturally with the pull (down) and push (up) buttons.
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function SyncIcon({ className = "w-4 h-4" }) {
  return html`
    <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class=${className}>
      <path stroke-linecap="round" stroke-linejoin="round" d="M3 7.5 7.5 3m0 0L12 7.5M7.5 3v13.5m13.5 0L16.5 21m0 0L12 16.5m4.5 4.5V7.5" />
    </svg>
  `;
}

/**
 * Tag icon (for user data / metadata)
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function TagIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M7 7h.01M7 3h5c.512 0 1.024.195 1.414.586l7 7a2 2 0 010 2.828l-7 7a2 2 0 01-2.828 0l-7-7A2 2 0 013 12V7a4 4 0 014-4z"
      />
    </svg>
  `;
}


export function SidePanelIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <rect x="3" y="3" width="18" height="18" rx="2" ry="2" />
      <line x1="15" y1="3" x2="15" y2="21" />
    </svg>
  `;
}

/**
 * Expand / enter-fullscreen icon (Heroicons arrows-pointing-out)
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function ExpandIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <path d="M3.75 3.75v4.5m0-4.5h4.5m-4.5 0L9 9M3.75 20.25v-4.5m0 4.5h4.5m-4.5 0L9 15M20.25 3.75h-4.5m4.5 0v4.5m0-4.5L15 9m5.25 11.25h-4.5m4.5 0v-4.5m0 4.5L15 15" />
    </svg>
  `;
}

/**
 * Collapse / exit-fullscreen icon (Heroicons arrows-pointing-in)
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function CollapseIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      class="${className}"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <path d="M9 9V4.5M9 9H4.5M9 9 3.75 3.75M9 15v4.5M9 15H4.5M9 15l-5.25 5.25M15 9h4.5M15 9V4.5M15 9l5.25-5.25M15 15h4.5M15 15v4.5m0-4.5 5.25 5.25" />
    </svg>
  `;
}


/**
 * Terminal/command prompt icon (Heroicons terminal-window)
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function TerminalIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      xmlns="http://www.w3.org/2000/svg"
      fill="none"
      viewBox="0 0 24 24"
      stroke-width="1.5"
      stroke="currentColor"
      class=${className}
    >
      <path stroke-linecap="round" stroke-linejoin="round" d="m6.75 7.5 3 2.25-3 2.25m4.5 0h3m-9 8.25h13.5A2.25 2.25 0 0 0 21 17.25V6.75A2.25 2.25 0 0 0 18.75 4.5H5.25A2.25 2.25 0 0 0 3 6.75v10.5A2.25 2.25 0 0 0 5.25 20.25Z" />
    </svg>
  `;
}

/**
 * Folder open icon (Heroicons folder-open) for opening workspace folder
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function FolderOpenIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      xmlns="http://www.w3.org/2000/svg"
      fill="none"
      viewBox="0 0 24 24"
      stroke-width="1.5"
      stroke="currentColor"
      class=${className}
    >
      <path stroke-linecap="round" stroke-linejoin="round" d="M3.75 9.776c.112-.017.227-.026.344-.026h15.812c.117 0 .232.009.344.026m-16.5 0a2.25 2.25 0 0 0-1.883 2.542l.857 6a2.25 2.25 0 0 0 2.227 1.932H19.05a2.25 2.25 0 0 0 2.227-1.932l.857-6a2.25 2.25 0 0 0-1.883-2.542m-16.5 0V6.228c0-1.168.895-2.128 2.033-2.216a48.394 48.394 0 0 1 5.274-.166c1.045.044 2.062.262 2.987.678l.724.33c.925.416 1.943.634 2.987.678a48.54 48.54 0 0 1 5.274.166 2.252 2.252 0 0 1 2.033 2.216v3.548" />
    </svg>
  `;
}

/**
 * Beads issue tracker icon (rounded bead/bullet list shape)
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function BeadsIcon({ className = "w-5 h-5" }) {
  return html`
    <svg
      xmlns="http://www.w3.org/2000/svg"
      fill="none"
      viewBox="0 0 24 24"
      stroke-width="1.5"
      stroke="currentColor"
      class=${className}
    >
      <path stroke-linecap="round" stroke-linejoin="round" d="M8.25 6.75h7.5M8.25 12h7.5m-7.5 5.25h7.5M3.75 6.75h.007v.008H3.75V6.75Zm.375 0a.375.375 0 1 1-.75 0 .375.375 0 0 1 .75 0ZM3.75 12h.007v.008H3.75V12Zm.375 0a.375.375 0 1 1-.75 0 .375.375 0 0 1 .75 0Zm-.375 5.25h.007v.008H3.75v-.008Zm.375 0a.375.375 0 1 1-.75 0 .375.375 0 0 1 .75 0Z" />
    </svg>
  `;
}

/**
 * Balloon icon for regular (one-off) conversations
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function BalloonIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
      stroke-width="2"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        d="M12 3c-3.31 0-6 2.69-6 6 0 4 4.5 7.5 6 8.5 1.5-1 6-4.5 6-8.5 0-3.31-2.69-6-6-6z"
      />
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        d="M12 17.5v1.5m0 0l-1.5 2m1.5-2l1.5 2"
      />
    </svg>
  `;
}

/**
 * Mitto icon for regular (one-off) conversations — a rounded speech bubble
 * with three dots, mirroring the Mitto app logo.
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function MittoIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
      stroke-width="2"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        d="M4 5a1 1 0 0 1 1-1h14a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1H10l-4 4v-4H5a1 1 0 0 1-1-1z"
      />
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        d="M8 10h.01M12 10h.01M16 10h.01"
      />
    </svg>
  `;
}

/**
 * Clock icon for periodic (recurring) conversations
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function ClockIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
      stroke-width="2"
    >
      <circle cx="12" cy="12" r="9" />
      <path stroke-linecap="round" stroke-linejoin="round" d="M12 7v5l3 2" />
    </svg>
  `;
}

/**
 * Ellipsis (three-dot) icon for per-item overflow menus
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function EllipsisIcon({ className = "w-4 h-4" }) {
  return html`
    <svg class="${className}" fill="currentColor" viewBox="0 0 24 24">
      <circle cx="5" cy="12" r="2" />
      <circle cx="12" cy="12" r="2" />
      <circle cx="19" cy="12" r="2" />
    </svg>
  `;
}

/**
 * Dashboard icon (2x2 grid) for the top-level Dashboard node
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function DashboardIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
      stroke-width="2"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        d="M4 4h7v7H4V4zM13 4h7v7h-7V4zM13 13h7v7h-7v-7zM4 13h7v7H4v-7z"
      />
    </svg>
  `;
}

/**
 * Home icon for the top-level Dashboard node
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function HomeIcon({ className = "w-4 h-4" }) {
  return html`
    <svg
      class="${className}"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
      stroke-width="2"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        d="M3 12l9-9 9 9M5 10v10a1 1 0 001 1h4v-6a1 1 0 011-1h2a1 1 0 011 1v6h4a1 1 0 001-1V10"
      />
    </svg>
  `;
}

// PROMPT_ICONS maps the optional "icon" front-matter value of a prompt to an
// icon component. Names are matched case-insensitively. Keep names stable and
// kebab-case so they can be referenced from prompt markdown files.
export const PROMPT_ICONS = {
  beads: BeadsIcon,
  settings: SettingsIcon,
  sliders: SlidersIcon,
  search: SearchIcon,
  edit: EditIcon,
  trash: TrashIcon,
  broom: BroomIcon,
  save: SaveIcon,
  "magic-wand": MagicWandIcon,
  lightning: LightningIcon,
  robot: RobotIcon,
  person: PersonIcon,
  image: ImageIcon,
  folder: FolderIcon,
  "folder-open": FolderOpenIcon,
  terminal: TerminalIcon,
  server: ServerIcon,
  globe: GlobeIcon,
  "chat-bubble": ChatBubbleIcon,
  shield: ShieldIcon,
  layers: LayersIcon,
  list: ListIcon,
  tag: TagIcon,
  check: CheckIcon,
  question: QuestionMarkIcon,
  error: ErrorIcon,
  plus: PlusIcon,
  hourglass: HourglassIcon,
  refresh: RefreshIcon,
  sync: SyncIcon,
  keyboard: KeyboardIcon,
  duplicate: DuplicateIcon,
  pin: PinIcon,
  archive: ArchiveIcon,
  periodic: PeriodicIcon,
  queue: QueueIcon,
  play: PlayFilledIcon,
};

// getPromptIcon returns the icon component for a given prompt icon name, or
// null if the name is empty or unknown. Matching is case-insensitive.
export function getPromptIcon(name) {
  if (!name || typeof name !== "string") return null;
  return PROMPT_ICONS[name.trim().toLowerCase()] || null;
}

// getPromptIconOrDefault returns the icon component for a prompt's icon name,
// falling back to the default lightning icon when the name is empty or unknown.
// Use this in menus that always want to render an icon for every prompt.
export function getPromptIconOrDefault(name) {
  return getPromptIcon(name) || LightningIcon;
}

// ---- Markdown editor toolbar icons ------------------------------------------

/**
 * Bold text icon (B)
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function BoldIcon({ className = "w-4 h-4" }) {
  return html`
    <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2.5"
        d="M6 4h8a4 4 0 010 8H6V4zm0 8h9a4 4 0 010 8H6v-8z" />
    </svg>
  `;
}

/**
 * Italic text icon (I)
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function ItalicIcon({ className = "w-4 h-4" }) {
  return html`
    <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
        d="M19 4h-9m4 16H5M15 4L9 20" />
    </svg>
  `;
}

/**
 * Strikethrough text icon
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function StrikethroughIcon({ className = "w-4 h-4" }) {
  return html`
    <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
        d="M9 15a4 4 0 007.5-2H4m8-9c-2.2 0-4 1.3-4 3s1.8 3 4 3" />
    </svg>
  `;
}

/**
 * Inline code icon (monospace brackets)
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function InlineCodeIcon({ className = "w-4 h-4" }) {
  return html`
    <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
        d="M10 20l4-16m4 4l4 4-4 4M6 16l-4-4 4-4" />
    </svg>
  `;
}

/**
 * Code block icon (fenced code block)
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function CodeBlockIcon({ className = "w-4 h-4" }) {
  return html`
    <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
        d="M8 9l-3 3 3 3m8-6l3 3-3 3M3 5h18a1 1 0 011 1v12a1 1 0 01-1 1H3a1 1 0 01-1-1V6a1 1 0 011-1z" />
    </svg>
  `;
}

/**
 * Numbered list icon
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function NumberedListIcon({ className = "w-4 h-4" }) {
  return html`
    <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
        d="M9 6h11M9 12h11M9 18h11M4 6h1m-1 6h1m-1 6h1" />
    </svg>
  `;
}

/**
 * Heading icon (H with lines)
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function HeadingIcon({ className = "w-4 h-4" }) {
  return html`
    <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
        d="M4 6h16M4 12h10M4 18h6" />
    </svg>
  `;
}

/**
 * Blockquote icon (vertical bar with text lines)
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function QuoteIcon({ className = "w-4 h-4" }) {
  return html`
    <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
        d="M3 6h18M3 10h18M3 14h18M3 18h18" />
      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2.5"
        d="M2 4v16" />
    </svg>
  `;
}
