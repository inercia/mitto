package handlers

// KnownDashboardChartIDs is the canonical, ordered list of chart IDs that may
// appear on the Dashboard's Activity strip. It is the single source of truth
// for backend validation of UIPreferences.DashboardHiddenCharts.
//
// MIRRORED: keep in sync with the same list in web/static/utils/storage.js
// (see the DASHBOARD_HIDDEN_CHARTS_KEY cluster). The bead (mitto-e2u) explicitly
// chose static Go const + static JS const over a runtime API endpoint; the
// small drift risk is accepted in exchange for not adding a new endpoint.
//
// "model_usage" is listed even though the Model Usage chart (mitto-8wj) has not
// landed yet, so the backend accepts the ID from day one and Phase 3 can wire
// cleanly. Harmless if that chart never ships.
var KnownDashboardChartIDs = []string{
	"tokens",
	"tool_calls",
	"prompts_vs_turns",
	"model_usage",
	"beads_activity",
	"beads_cycle_time",
}

// filterKnownChartIDs returns the subset of ids that appear in
// KnownDashboardChartIDs, preserving input order and de-duplicating. Unknown
// or repeated IDs are silently dropped — the handler is defensive against
// stale clients that may still send retired chart IDs after an upgrade.
func filterKnownChartIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	known := make(map[string]struct{}, len(KnownDashboardChartIDs))
	for _, id := range KnownDashboardChartIDs {
		known[id] = struct{}{}
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := known[id]; !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
