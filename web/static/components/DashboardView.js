// Mitto Web Interface - Dashboard View Component
// Top-level Dashboard landing view. Intentionally empty for now; the sidebar
// "Dashboard" entry switches the main content area to this view. Mirrors the
// header layout of the conversation/beads views so the mobile sidebar toggle
// stays accessible.

const { html } = window.preact;

import { MenuIcon } from "./Icons.js";

export function DashboardView({ onShowSidebar }) {
  return html`
    <div class="flex-1 flex flex-col min-w-0 overflow-hidden bg-mitto-bg">
      <!-- Header -->
      <div
        class="relative p-4 bg-mitto-sidebar border-b border-mitto-border-1 flex items-center gap-3 shrink-0"
      >
        <button
          class="md:hidden p-2 hover:bg-mitto-surface-hover rounded-lg transition-colors"
          onClick=${() => onShowSidebar && onShowSidebar()}
        >
          <${MenuIcon} className="w-6 h-6" />
        </button>
        <h1 class="font-bold text-xl truncate">Dashboard</h1>
      </div>

      <!-- Body (empty for now) -->
      <div class="flex-1 min-h-0 overflow-y-auto"></div>
    </div>
  `;
}
