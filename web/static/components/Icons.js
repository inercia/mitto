// Mitto Web Interface - Reusable Icon Components
// Centralizes SVG icons to reduce duplication and ensure consistency

const { html } = window.preact;

/**
 * Spinner icon for loading states
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function SpinnerIcon({ className = 'w-4 h-4' }) {
    return html`
        <svg class="${className} animate-spin" fill="none" viewBox="0 0 24 24">
            <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
            <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
        </svg>
    `;
}

/**
 * Close/X icon
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function CloseIcon({ className = 'w-5 h-5' }) {
    return html`
        <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
        </svg>
    `;
}

/**
 * Settings gear icon
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function SettingsIcon({ className = 'w-5 h-5' }) {
    return html`
        <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
        </svg>
    `;
}

/**
 * Plus icon for adding items
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function PlusIcon({ className = 'w-5 h-5' }) {
    return html`
        <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
        </svg>
    `;
}

/**
 * Chevron up icon
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function ChevronUpIcon({ className = 'w-4 h-4' }) {
    return html`
        <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 15l7-7 7 7" />
        </svg>
    `;
}

/**
 * Chevron down icon
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function ChevronDownIcon({ className = 'w-4 h-4' }) {
    return html`
        <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
        </svg>
    `;
}

/**
 * Hamburger menu icon
 * @param {string} className - CSS classes (default: 'w-6 h-6')
 */
export function MenuIcon({ className = 'w-6 h-6' }) {
    return html`
        <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h16M4 18h16" />
        </svg>
    `;
}

/**
 * Trash/delete icon
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function TrashIcon({ className = 'w-5 h-5' }) {
    return html`
        <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
        </svg>
    `;
}

/**
 * Edit/pencil icon
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function EditIcon({ className = 'w-4 h-4' }) {
    return html`
        <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15.232 5.232l3.536 3.536m-2.036-5.036a2.5 2.5 0 113.536 3.536L6.5 21.036H3v-3.572L16.732 3.732z" />
        </svg>
    `;
}

/**
 * Checkmark icon
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function CheckIcon({ className = 'w-4 h-4' }) {
    return html`
        <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" />
        </svg>
    `;
}

/**
 * Arrow down icon (for scroll to bottom)
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function ArrowDownIcon({ className = 'w-5 h-5' }) {
    return html`
        <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 14l-7 7m0 0l-7-7m7 7V3" />
        </svg>
    `;
}

/**
 * Save/download icon
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function SaveIcon({ className = 'w-4 h-4' }) {
    return html`
        <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 7H5a2 2 0 00-2 2v9a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-3m-1 4l-3 3m0 0l-3-3m3 3V4" />
        </svg>
    `;
}

/**
 * Magic wand/sparkles icon (for AI improve prompt)
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function MagicWandIcon({ className = 'w-5 h-5' }) {
    return html`
        <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 3v4M3 5h4M6 17v4m-2-2h4m5-16l2.286 6.857L21 12l-5.714 2.143L13 21l-2.286-6.857L5 12l5.714-2.143L13 3z" />
        </svg>
    `;
}

/**
 * Image/photo icon
 * @param {string} className - CSS classes (default: 'w-5 h-5')
 */
export function ImageIcon({ className = 'w-5 h-5' }) {
    return html`
        <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z" />
        </svg>
    `;
}

/**
 * Lightning bolt icon (for prompts)
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function LightningIcon({ className = 'w-4 h-4' }) {
    return html`
        <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z" />
        </svg>
    `;
}

/**
 * Stop/square icon (for cancel streaming)
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function StopIcon({ className = 'w-4 h-4' }) {
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
export function ErrorIcon({ className = 'w-4 h-4' }) {
    return html`
        <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
    `;
}

/**
 * Server icon
 * @param {string} className - CSS classes (default: 'w-4 h-4')
 */
export function ServerIcon({ className = 'w-4 h-4' }) {
    return html`
        <svg class="${className}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2" />
        </svg>
    `;
}

