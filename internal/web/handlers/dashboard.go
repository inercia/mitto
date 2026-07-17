package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"sync"

	"github.com/inercia/mitto/internal/beads"
)

// dashboardDefaultLimit is the default cap applied to each dashboard list.
// It matches the UX spec (epic mitto-aqo): each sidebar list shows at most 5
// items, sorted by a sensible recency signal (updated_at desc).
const dashboardDefaultLimit = 5

// dashboardMaxLimit caps the ?limit= query so a caller cannot force the
// server to serialise an unbounded number of items per list.
const dashboardMaxLimit = 50

// dashboardStats is the aggregate summary block of the dashboard response.
// conversations_prompting / loops_active / loops_stopped are session-derived;
// issues_in_progress is the sum across every known workspace working directory.
type dashboardStats struct {
	IssuesInProgress       int `json:"issues_in_progress"`
	ConversationsPrompting int `json:"conversations_prompting"`
	LoopsActive            int `json:"loops_active"`
	LoopsStopped           int `json:"loops_stopped"`
}

// dashboardLists holds the three issue lists surfaced by the dashboard. Each
// list is capped at the request limit (default dashboardDefaultLimit) and
// carries the working directory that produced the item, so the frontend can
// route the click back to the right workspace's task viewer.
type dashboardLists struct {
	InProgress       []map[string]any `json:"in_progress"`
	Ready            []map[string]any `json:"ready"`
	RecentlyModified []map[string]any `json:"recently_modified"`
}

// dashboardResponse is the JSON payload of GET /api/dashboard.
type dashboardResponse struct {
	Stats dashboardStats `json:"stats"`
	Lists dashboardLists `json:"lists"`
}

// HandleDashboard handles GET /api/dashboard.
//
// It aggregates issue counts and lists across every unique workspace working
// directory (session-manager view), plus session-derived counts (prompting
// conversations, active vs stopped loops). Per-workspace bd failures are
// logged but do not fail the whole request; empty results are surfaced as
// [] rather than null.
//
// Query parameters:
//   - limit (optional, default 5, max 50): cap for each of the three lists
//
// Requires authentication via the standard auth middleware.
func (h *Handlers) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	limit := dashboardDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			if n > dashboardMaxLimit {
				n = dashboardMaxLimit
			}
			limit = n
		}
	}

	resp := dashboardResponse{
		Lists: dashboardLists{
			InProgress:       make([]map[string]any, 0),
			Ready:            make([]map[string]any, 0),
			RecentlyModified: make([]map[string]any, 0),
		},
	}

	if h.deps.SessionManager == nil {
		writeJSONOK(w, resp)
		return
	}

	// Session-derived stats read directly from the session manager. No bd
	// dependency, so they always succeed even when every beads DB is broken.
	resp.Stats.ConversationsPrompting, resp.Stats.LoopsActive, resp.Stats.LoopsStopped = h.dashboardSessionStats()

	// Enumerate workspaces and de-duplicate by WorkingDir: multiple workspace
	// records may point at the same folder with different ACP servers, but the
	// beads database lives on the folder — scanning it twice would double the
	// issue counts.
	seen := make(map[string]struct{})
	dirs := make([]string, 0)
	for _, ws := range h.deps.SessionManager.GetWorkspaces() {
		if ws.WorkingDir == "" {
			continue
		}
		if _, ok := seen[ws.WorkingDir]; ok {
			continue
		}
		seen[ws.WorkingDir] = struct{}{}
		dirs = append(dirs, ws.WorkingDir)
	}

	ctx, cancel := context.WithTimeout(r.Context(), auxBackedRequestTimeout)
	defer cancel()

	inProgress := h.dashboardCollect(ctx, dirs, "in_progress", func(ctx context.Context, dir string) ([]byte, error) {
		return h.beadsClient().List(ctx, dir)
	}, func(item map[string]any) bool {
		return itemStatus(item) == "in_progress"
	})
	ready := h.dashboardCollect(ctx, dirs, "ready", func(ctx context.Context, dir string) ([]byte, error) {
		return h.beadsClient().Ready(ctx, dir)
	}, nil)
	recentlyModified := h.dashboardCollect(ctx, dirs, "recently_modified", func(ctx context.Context, dir string) ([]byte, error) {
		return h.beadsClient().List(ctx, dir)
	}, nil)

	// Global timeout: if the shared context expired before the collectors
	// finished, surface a retryable 503 rather than a truncated result.
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		writeRetryableUnavailable(w, "Dashboard aggregation is busy. Please try again in a few seconds.", 5)
		return
	}

	resp.Stats.IssuesInProgress = len(inProgress)

	// InProgress and Ready sort priority-first (Critical -> Low) then
	// updated_at DESC within a priority band, so the top-N cap surfaces the
	// most important items rather than merely the most recently touched.
	// RecentlyModified sorts by updated_at DESC only — recency IS the panel's
	// whole point, and a priority-first sort would defeat it by burying a
	// Low-priority item the user just touched behind stale Critical work.
	resp.Lists.InProgress = capItems(sortByPriorityThenUpdatedAtDesc(inProgress), limit)
	resp.Lists.Ready = capItems(sortByPriorityThenUpdatedAtDesc(ready), limit)
	resp.Lists.RecentlyModified = capItems(sortByUpdatedAtDesc(recentlyModified), limit)

	writeJSONOK(w, resp)
}

// dashboardSessionStats returns the session-derived counts used by the
// dashboard: currently-prompting conversations, and loops split by enabled
// state (Enabled true → active, Enabled false with a saved loop config →
// stopped). Sessions without any loop config are ignored.
func (h *Handlers) dashboardSessionStats() (prompting, loopsActive, loopsStopped int) {
	sm := h.deps.SessionManager
	if sm == nil {
		return 0, 0, 0
	}
	store := h.deps.Store

	for _, sid := range sm.ListRunningSessions() {
		bs := sm.GetSession(sid)
		if bs == nil {
			continue
		}
		if bs.IsPrompting() {
			prompting++
		}
		if store == nil {
			continue
		}
		lp, err := store.Loop(sid).Get()
		if err != nil || lp == nil {
			continue
		}
		if lp.Enabled {
			loopsActive++
		} else {
			loopsStopped++
		}
	}
	return prompting, loopsActive, loopsStopped
}

// dashboardCollect runs fetch against every directory, decodes the JSON array,
// optionally filters items with keep, attaches working_dir to each surviving
// item, and returns the combined slice. Per-directory failures are logged and
// skipped so a single broken beads DB does not blank the whole dashboard.
func (h *Handlers) dashboardCollect(
	ctx context.Context,
	dirs []string,
	listName string,
	fetch func(context.Context, string) ([]byte, error),
	keep func(map[string]any) bool,
) []map[string]any {
	if len(dirs) == 0 {
		return nil
	}

	type result struct {
		items []map[string]any
	}

	results := make([]result, len(dirs))
	var wg sync.WaitGroup
	for i, dir := range dirs {
		if ctx.Err() != nil {
			break
		}
		i, dir := i, dir
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := h.runBeadsRead(ctx, func(ctx context.Context) ([]byte, error) {
				return fetch(ctx, dir)
			})
			if err != nil {
				if h.deps.Logger != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
					h.deps.Logger.Warn("dashboard: bd query failed for workspace; skipping",
						"list", listName, "working_dir", dir, "error", err, "stderr", beads.StderrOf(err))
				}
				return
			}
			var raw []map[string]any
			if err := json.Unmarshal(out, &raw); err != nil {
				if h.deps.Logger != nil {
					h.deps.Logger.Warn("dashboard: bd output was not a JSON array; skipping",
						"list", listName, "working_dir", dir, "error", err)
				}
				return
			}
			kept := make([]map[string]any, 0, len(raw))
			for _, item := range raw {
				if item == nil {
					continue
				}
				if keep != nil && !keep(item) {
					continue
				}
				item["working_dir"] = dir
				kept = append(kept, item)
			}
			results[i] = result{items: kept}
		}()
	}
	wg.Wait()

	total := 0
	for _, r := range results {
		total += len(r.items)
	}
	out := make([]map[string]any, 0, total)
	for _, r := range results {
		out = append(out, r.items...)
	}
	return out
}

// itemStatus returns the "status" field of an issue map as a string, or "" if
// missing or of the wrong type.
func itemStatus(item map[string]any) string {
	s, _ := item["status"].(string)
	return s
}

// itemUpdatedAt returns the "updated_at" field of an issue map as a string.
// bd emits RFC 3339 timestamps so lexicographic comparison matches chronology.
func itemUpdatedAt(item map[string]any) string {
	s, _ := item["updated_at"].(string)
	return s
}

// itemPriority returns the "priority" field of an issue map as an int
// (0=Critical, 1=High, 2=Medium, 3=Low — lower number is higher priority).
// bd emits integers, but json.Unmarshal into map[string]any decodes numbers
// as float64, so both branches are handled. Missing / wrong-type priorities
// sort last by returning a large sentinel.
func itemPriority(item map[string]any) int {
	switch v := item["priority"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		// Unknown / missing priority sorts after any real value (0..3).
		return 1 << 30
	}
}

// sortByUpdatedAtDesc sorts items by updated_at descending in place and
// returns the same slice for chaining. Ties break by id ascending for a
// stable order across repeated calls.
func sortByUpdatedAtDesc(items []map[string]any) []map[string]any {
	sort.SliceStable(items, func(i, j int) bool {
		ui, uj := itemUpdatedAt(items[i]), itemUpdatedAt(items[j])
		if ui != uj {
			return ui > uj
		}
		idI, _ := items[i]["id"].(string)
		idJ, _ := items[j]["id"].(string)
		return idI < idJ
	})
	return items
}

// sortByPriorityThenUpdatedAtDesc sorts items so higher-priority (lower
// numeric priority) items come first, then falls back to updated_at desc
// within the same priority band, and finally to id ascending for stability.
// Sorts in place and returns the same slice for chaining. Used for the
// In-progress and Ready backlogs where the top-N cap should surface the
// most important items rather than merely the most recently touched.
func sortByPriorityThenUpdatedAtDesc(items []map[string]any) []map[string]any {
	sort.SliceStable(items, func(i, j int) bool {
		pi, pj := itemPriority(items[i]), itemPriority(items[j])
		if pi != pj {
			return pi < pj
		}
		ui, uj := itemUpdatedAt(items[i]), itemUpdatedAt(items[j])
		if ui != uj {
			return ui > uj
		}
		idI, _ := items[i]["id"].(string)
		idJ, _ := items[j]["id"].(string)
		return idI < idJ
	})
	return items
}

// capItems truncates items to at most limit entries. Guarantees a non-nil
// slice so JSON marshaling produces [] instead of null.
func capItems(items []map[string]any, limit int) []map[string]any {
	if items == nil {
		return make([]map[string]any, 0)
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}
