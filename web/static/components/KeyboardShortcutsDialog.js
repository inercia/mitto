// Mitto Web Interface - Keyboard Shortcuts Dialog Component
const { html, useEffect } = window.preact;

import { CloseIcon } from "./Icons.js";
import { KEYBOARD_SHORTCUTS } from "../constants.js";

// =============================================================================
// Keyboard Shortcuts Dialog
// =============================================================================

export function KeyboardShortcutsDialog({ isOpen, onClose }) {
  if (!isOpen) return null;

  // Check if running in the native macOS app
  const isMacApp = typeof window.mittoPickFolder === "function";

  // Handle Escape key to close dialog
  useEffect(() => {
    const handleKeyDown = (e) => {
      if (e.key === "Escape") {
        onClose();
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [onClose]);

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
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick=${onClose}
    >
      <div
        class="bg-mitto-sidebar rounded-xl p-6 w-[420px] md:w-[700px] shadow-2xl max-h-[80vh] overflow-y-auto"
        onClick=${(e) => e.stopPropagation()}
      >
        <div class="flex items-center justify-between mb-4">
          <h3 class="text-lg font-semibold">Keyboard Shortcuts</h3>
          <button
            onClick=${onClose}
            class="p-1 hover:bg-slate-700 rounded-lg transition-colors"
            title="Close"
          >
            <${CloseIcon} className="w-5 h-5 text-gray-400 hover:text-white" />
          </button>
        </div>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          ${Object.entries(sections)
            .sort((a, b) => b[1].length - a[1].length)
            .map(
              ([sectionName, shortcuts]) => html`
                <div key=${sectionName}>
                  <h4
                    class="text-xs font-medium text-gray-400 uppercase tracking-wide mb-2"
                  >
                    ${sectionName}
                  </h4>
                  <div class="space-y-1">
                    ${shortcuts.map(
                      (shortcut) => html`
                        <div
                          key=${shortcut.keys}
                          class="flex items-center justify-between py-2 px-3 rounded-lg bg-slate-700/30"
                        >
                          <div class="flex flex-col gap-0.5">
                            <div class="flex items-center gap-2">
                              <span class="text-gray-300"
                                >${shortcut.description}</span
                              >
                              ${shortcut.macOnly &&
                              html`
                                <span
                                  class="text-[10px] px-1.5 py-0.5 rounded bg-slate-600 text-gray-400"
                                  >macOS app</span
                                >
                              `}
                            </div>
                            ${shortcut.hint &&
                            html`
                              <span class="text-[11px] text-gray-500"
                                >${shortcut.hint}</span
                              >
                            `}
                          </div>
                          <kbd
                            class="px-2 py-1 text-sm font-mono bg-slate-700 rounded border border-slate-600 text-gray-200"
                          >
                            ${shortcut.keys}
                          </kbd>
                        </div>
                      `,
                    )}
                  </div>
                </div>
              `,
            )}
        </div>
        <div class="mt-4 pt-3 border-t border-slate-700 space-y-2">
          <p class="text-xs text-gray-500 text-center">
            On touch devices, swipe left/right to switch conversations
          </p>
          <p class="text-xs text-gray-500 text-center">Press Escape to close</p>
        </div>
      </div>
    </div>
  `;
}
