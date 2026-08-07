// Canonical registry of Dashboard Activity-strip charts the user can toggle
// in Settings › Dashboard. Order and IDs mirror `buildChartSpecs()` in
// `components/dashboard/StatsCharts.js`; labels are surfaced verbatim in the
// Settings checkbox list.
//
// Backend counterpart: `KnownDashboardChartIDs` in
// `internal/web/handlers/ui_preferences.go`. The backend forward-declares
// `model_usage` for validation, but this frontend registry only exposes rows
// that actually render on the Dashboard today — flip `model_usage` on with a
// single uncomment once mitto-8wj Phase 3 ships the chart.
export const DASHBOARD_CHARTS = [
  { id: "tokens", label: "Tokens (input + output)" },
  { id: "tool_calls", label: "Tool calls" },
  { id: "prompts_vs_turns", label: "Prompts vs agent turns" },
  // { id: "model_usage",      label: "Model usage" },  // Uncomment once mitto-8wj Phase 3 lands.
  { id: "beads_activity", label: "Beads opened vs closed" },
  { id: "beads_cycle_time", label: "Beads: cycle time (claim → close)" },
];
