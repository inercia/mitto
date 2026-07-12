// Mitto Web Interface - Global Dashboard view (mitto-aqo).
// Placeholder stub delivered by mitto-aqo.3 (routing). The real UI lands in
// mitto-aqo.4; keep this minimal so .4 can replace the body wholesale without
// touching the routing wiring in app.js / SessionList.js.
const { html } = window.preact;

export function Dashboard() {
  return html`
    <div
      class="flex-1 flex flex-col min-w-0 overflow-hidden bg-mitto-bg items-center justify-center p-6"
    >
      <div class="bg-mitto-surface-2 rounded-2xl shadow-lg p-8 max-w-md w-full">
        <div class="text-mitto-text-strong text-lg font-semibold text-center">
          Dashboard (coming soon)
        </div>
      </div>
    </div>
  `;
}
