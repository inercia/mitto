// Mitto Web Interface - Keyboard Shortcuts Dialog Component
const { html } = window.preact;

import { Modal } from "./Modal.js";
import { KEYBOARD_SHORTCUTS } from "../constants.js";

// =============================================================================
// Keyboard Shortcuts Dialog
// =============================================================================

export function KeyboardShortcutsDialog({ isOpen, onClose }) {
  // Check if running in the native macOS app
  const isMacApp = typeof window.mittoPickFolder === "function";

  // Filter shortcuts based on environment and group by section
  // In browser (not macOS app), hide macOnly shortcuts since they're handled by native menu
  const sections = {};
  KEYBOARD_SHORTCUTS.forEach((shortcut) => {
    // Skip macOnly shortcuts when not in the macOS app
    if (shortcut.macOnly && !isMacApp) {
      return;
    }
    const section = shortcut.section || "General";
    if (!sections[section]) {
      sections[section] = [];
    }
    sections[section].push(shortcut);
  });

  return html`
    <${Modal}
      isOpen=${isOpen}
      onClose=${onClose}
      title="Keyboard Shortcuts"
      boxClass="max-w-3xl max-h-[70vh]"
      bodyClass="p-4 overflow-y-auto min-h-0"
    >
      <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
        ${Object.entries(sections)
          .sort((a, b) => b[1].length - a[1].length)
          .map(
            ([sectionName, shortcuts]) => html`
              <div key=${sectionName}>
                <h4
                  class="text-xs font-medium text-mitto-text-muted uppercase tracking-wide mb-2"
                >
                  ${sectionName}
                </h4>
                <div class="space-y-1">
                  ${shortcuts.map(
                    (shortcut) => html`
                      <div
                        key=${shortcut.keys}
                        class="flex items-center justify-between py-2 px-3 rounded-md bg-base-200"
                      >
                        <div class="flex flex-col gap-0.5">
                          <div class="flex items-center gap-2">
                            <span class="text-mitto-text-secondary"
                              >${shortcut.description}</span
                            >
                            ${shortcut.macOnly &&
                            html`
                              <span
                                class="badge badge-sm bg-mitto-surface-4 text-mitto-text-muted"
                                >macOS app</span
                              >
                            `}
                          </div>
                          ${shortcut.hint &&
                          html`
                            <span class="text-[11px] text-mitto-text-muted"
                              >${shortcut.hint}</span
                            >
                          `}
                        </div>
                        <kbd class="kbd kbd-sm">${shortcut.keys}</kbd>
                      </div>
                    `,
                  )}
                </div>
              </div>
            `,
          )}
      </div>
      <div class="mt-4 pt-3 border-t border-mitto-border-1 space-y-2">
        <p class="text-xs text-mitto-text-muted text-center">
          On touch devices, swipe left/right to switch conversations
        </p>
        <p class="text-xs text-mitto-text-muted text-center">Press Escape to close</p>
      </div>
    </${Modal}>
  `;
}
