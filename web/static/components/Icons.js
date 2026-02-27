// Mitto Web Interface - Reusable Icon Components
// Centralizes SVG icons to reduce duplication and ensure consistency

const { html } = window.preact;

/**
 * Spinner icon for loading states
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function SpinnerIcon({ className = "w-4 h-4" }) {
  return html`
    <svg class="${className} animate-spin" fill="none" viewBox="0 0 24 24">
      <circle
        class="opacity-25"
        cx="12"
        cy="12"
        r="10"
        stroke="currentColor"
        stroke-width="4"
      ></circle>
      <path
        class="opacity-75"
        fill="currentColor"
        d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
      ></path>
    </svg>
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
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M13 10V3L4 14h7v7l9-11h-7z"
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
    <svg class="${className}" viewBox="0 0 24 24">
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
    <svg class="${className}" viewBox="0 0 24 24">
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
