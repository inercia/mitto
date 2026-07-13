package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/inercia/mitto/internal/beads"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
)

// dashboardFakeClient is a beads.Client fake that returns per-directory
// canned List/Ready payloads, and records how many times each method was
// called per directory so tests can assert dedup. All other methods are
// inherited no-ops from stubBeadsClient.
type dashboardFakeClient struct {
	stubBeadsClient
	listByDir  map[string][]byte
	readyByDir map[string][]byte
	listErrBy  map[string]error
	readyErrBy map[string]error

	listCalls  int32
	readyCalls int32
}

func (c *dashboardFakeClient) List(_ context.Context, dir string) ([]byte, error) {
	atomic.AddInt32(&c.listCalls, 1)
	if err, ok := c.listErrBy[dir]; ok {
		return nil, err
	}
	if b, ok := c.listByDir[dir]; ok {
		return b, nil
	}
	return []byte(`[]`), nil
}

func (c *dashboardFakeClient) Ready(_ context.Context, dir string) ([]byte, error) {
	atomic.AddInt32(&c.readyCalls, 1)
	if err, ok := c.readyErrBy[dir]; ok {
		return nil, err
	}
	if b, ok := c.readyByDir[dir]; ok {
		return b, nil
	}
	return []byte(`[]`), nil
}

// newDashboardTestServer wires a Handlers with the given fake client and the
// given workspace records (may include duplicates by WorkingDir).
func newDashboardTestServer(t *testing.T, client beads.Client, wss []config.WorkspaceSettings) *Handlers {
	t.Helper()
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces(wss)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	return New(Deps{SessionManager: sm, BeadsClient: client, Logger: logger})
}

// decodeDashboard decodes a /api/dashboard response body into a typed shape
// that keeps the item maps as-is so tests can inspect working_dir + fields.
type decodedDashboard struct {
	Stats struct {
		IssuesInProgress       int `json:"issues_in_progress"`
		ConversationsPrompting int `json:"conversations_prompting"`
		LoopsActive            int `json:"loops_active"`
		LoopsStopped           int `json:"loops_stopped"`
	} `json:"stats"`
	Lists struct {
		InProgress []map[string]any `json:"in_progress"`
		Ready      []map[string]any `json:"ready"`
		Epics      []map[string]any `json:"epics"`
	} `json:"lists"`
}

func decodeDashboardBody(t *testing.T, w *httptest.ResponseRecorder) decodedDashboard {
	t.Helper()
	var d decodedDashboard
	if err := json.NewDecoder(w.Body).Decode(&d); err != nil {
		t.Fatalf("decode dashboard body: %v; raw=%s", err, w.Body.String())
	}
	return d
}

// --- HandleDashboard ---------------------------------------------------------

func TestHandleDashboard_MethodNotAllowed(t *testing.T) {
	s := newDashboardTestServer(t, &stubBeadsClient{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/dashboard", nil)
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.HandleDashboard(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleDashboard_EmptyWorkspaces_EmptyListsNotNull(t *testing.T) {
	// No workspaces → all three list keys must serialize as [] (never null).
	s := newDashboardTestServer(t, &stubBeadsClient{}, nil)
	req := localhostRequest("/api/dashboard")
	w := httptest.NewRecorder()
	s.HandleDashboard(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	body := w.Body.String()
	for _, key := range []string{`"in_progress":[]`, `"ready":[]`, `"epics":[]`} {
		if !strings.Contains(body, key) {
			t.Errorf("body missing %q; got=%s", key, body)
		}
	}
	if strings.Contains(body, `null`) {
		t.Errorf("body must not contain null list values; got=%s", body)
	}
}

func TestHandleDashboard_AggregatesTwoWorkspaces_AttachesWorkingDir(t *testing.T) {
	// Each workspace returns distinct payloads for List and Ready. The
	// aggregated response must include items from both, and every item must
	// carry the working_dir of its source workspace.
	client := &dashboardFakeClient{
		listByDir: map[string][]byte{
			"/ws/a": []byte(`[
				{"id":"a-1","status":"in_progress","issue_type":"task","updated_at":"2026-01-02T00:00:00Z"},
				{"id":"a-2","status":"open","issue_type":"epic","updated_at":"2026-01-03T00:00:00Z"}
			]`),
			"/ws/b": []byte(`[
				{"id":"b-1","status":"in_progress","issue_type":"task","updated_at":"2026-01-01T00:00:00Z"}
			]`),
		},
		readyByDir: map[string][]byte{
			"/ws/a": []byte(`[{"id":"a-r1","status":"open","updated_at":"2026-01-04T00:00:00Z"}]`),
			"/ws/b": []byte(`[{"id":"b-r1","status":"open","updated_at":"2026-01-05T00:00:00Z"}]`),
		},
	}
	wss := []config.WorkspaceSettings{
		{WorkingDir: "/ws/a", ACPServer: "acp-a"},
		{WorkingDir: "/ws/b", ACPServer: "acp-b"},
	}
	s := newDashboardTestServer(t, client, wss)

	req := localhostRequest("/api/dashboard")
	w := httptest.NewRecorder()
	s.HandleDashboard(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	d := decodeDashboardBody(t, w)

	// in_progress: a-1 and b-1. Neither has a "priority" field, so both fall
	// into the missing-priority sentinel and sort tie-breaks on updated_at
	// desc → a-1 first (2026-01-02) then b-1 (2026-01-01).
	if got, want := len(d.Lists.InProgress), 2; got != want {
		t.Fatalf("in_progress length = %d, want %d", got, want)
	}
	if got := d.Lists.InProgress[0]["id"]; got != "a-1" {
		t.Errorf("in_progress[0].id = %v, want a-1", got)
	}
	if got := d.Lists.InProgress[1]["id"]; got != "b-1" {
		t.Errorf("in_progress[1].id = %v, want b-1", got)
	}
	// working_dir attached to every item.
	for i, item := range d.Lists.InProgress {
		if item["working_dir"] == "" || item["working_dir"] == nil {
			t.Errorf("in_progress[%d] missing working_dir; item=%v", i, item)
		}
	}
	if got, want := d.Lists.InProgress[0]["working_dir"], "/ws/a"; got != want {
		t.Errorf("in_progress[0].working_dir = %v, want %q", got, want)
	}
	if got, want := d.Lists.InProgress[1]["working_dir"], "/ws/b"; got != want {
		t.Errorf("in_progress[1].working_dir = %v, want %q", got, want)
	}

	// epics: only a-2. Comes from List filtered by issue_type=="epic".
	if got, want := len(d.Lists.Epics), 1; got != want {
		t.Fatalf("epics length = %d, want %d", got, want)
	}
	if got := d.Lists.Epics[0]["id"]; got != "a-2" {
		t.Errorf("epics[0].id = %v, want a-2", got)
	}
	if got, want := d.Lists.Epics[0]["working_dir"], "/ws/a"; got != want {
		t.Errorf("epics[0].working_dir = %v, want %q", got, want)
	}

	// ready: b-r1 first (2026-01-05), then a-r1 (2026-01-04).
	if got, want := len(d.Lists.Ready), 2; got != want {
		t.Fatalf("ready length = %d, want %d", got, want)
	}
	if got := d.Lists.Ready[0]["id"]; got != "b-r1" {
		t.Errorf("ready[0].id = %v, want b-r1", got)
	}
	if got, want := d.Lists.Ready[0]["working_dir"], "/ws/b"; got != want {
		t.Errorf("ready[0].working_dir = %v, want %q", got, want)
	}
	if got, want := d.Lists.Ready[1]["working_dir"], "/ws/a"; got != want {
		t.Errorf("ready[1].working_dir = %v, want %q", got, want)
	}

	// stats: issues_in_progress counts the aggregated in-progress items (2).
	if got, want := d.Stats.IssuesInProgress, 2; got != want {
		t.Errorf("stats.issues_in_progress = %d, want %d", got, want)
	}
}

// TestHandleDashboard_InProgressSortedByPriorityFirst verifies that the
// in_progress list follows the same priority-first ordering as ready/epics:
// a Critical (p=0) but older item must beat a Medium (p=2) more-recently-
// touched one. This locks in the fix for "dashboard lists are not showing
// the highest-priority tasks" — previously in_progress was recency-only,
// which could bury a Critical item behind lower-priority recent activity.
func TestHandleDashboard_InProgressSortedByPriorityFirst(t *testing.T) {
	client := &dashboardFakeClient{
		listByDir: map[string][]byte{
			"/ws/x": []byte(`[
				{"id":"x-medium-newer","status":"in_progress","issue_type":"task","priority":2,"updated_at":"2026-06-10T00:00:00Z"},
				{"id":"x-crit-older","status":"in_progress","issue_type":"task","priority":0,"updated_at":"2026-06-01T00:00:00Z"},
				{"id":"x-high-mid","status":"in_progress","issue_type":"task","priority":1,"updated_at":"2026-06-05T00:00:00Z"}
			]`),
		},
	}
	wss := []config.WorkspaceSettings{{WorkingDir: "/ws/x", ACPServer: "acp-x"}}
	s := newDashboardTestServer(t, client, wss)

	req := localhostRequest("/api/dashboard")
	w := httptest.NewRecorder()
	s.HandleDashboard(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	d := decodeDashboardBody(t, w)

	if got, want := len(d.Lists.InProgress), 3; got != want {
		t.Fatalf("in_progress length = %d, want %d", got, want)
	}
	wantOrder := []string{"x-crit-older", "x-high-mid", "x-medium-newer"}
	for i, want := range wantOrder {
		if got := d.Lists.InProgress[i]["id"]; got != want {
			t.Errorf("in_progress[%d].id = %v, want %q (priority-first sort broken?)", i, got, want)
		}
	}
}

func TestHandleDashboard_DuplicateWorkingDir_VisitedOnce(t *testing.T) {
	// Three workspace records share the same WorkingDir but differ by ACPServer.
	// The handler must scan the folder exactly once — twice the List/Ready
	// calls would double every item and every count.
	client := &dashboardFakeClient{
		listByDir: map[string][]byte{
			"/ws/shared": []byte(`[{"id":"s-1","status":"in_progress","updated_at":"2026-01-01T00:00:00Z"}]`),
		},
		readyByDir: map[string][]byte{
			"/ws/shared": []byte(`[{"id":"s-r1","status":"open","updated_at":"2026-01-02T00:00:00Z"}]`),
		},
	}
	wss := []config.WorkspaceSettings{
		{WorkingDir: "/ws/shared", ACPServer: "acp-opus"},
		{WorkingDir: "/ws/shared", ACPServer: "acp-sonnet"},
		{WorkingDir: "/ws/shared", ACPServer: "acp-claude"},
	}
	s := newDashboardTestServer(t, client, wss)

	req := localhostRequest("/api/dashboard")
	w := httptest.NewRecorder()
	s.HandleDashboard(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	d := decodeDashboardBody(t, w)

	if got, want := len(d.Lists.InProgress), 1; got != want {
		t.Errorf("in_progress length = %d, want %d (dedup by working_dir)", got, want)
	}
	if got, want := len(d.Lists.Ready), 1; got != want {
		t.Errorf("ready length = %d, want %d (dedup by working_dir)", got, want)
	}
	// List is called twice per unique dir (once for in_progress, once for epics filter),
	// Ready is called once per unique dir.
	if got := atomic.LoadInt32(&client.listCalls); got != 2 {
		t.Errorf("List call count = %d, want 2 (2 filters x 1 unique dir)", got)
	}
	if got := atomic.LoadInt32(&client.readyCalls); got != 1 {
		t.Errorf("Ready call count = %d, want 1 (1 unique dir)", got)
	}
}

func TestHandleDashboard_PerWorkspaceListError_IsSkippedAndLogged(t *testing.T) {
	// /ws/bad's List errors; /ws/good's List succeeds. The response must be
	// 200 with only /ws/good items, and the failure must be logged as a Warn.
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	client := &dashboardFakeClient{
		listByDir: map[string][]byte{
			"/ws/good": []byte(`[{"id":"g-1","status":"in_progress","updated_at":"2026-01-01T00:00:00Z"}]`),
		},
		listErrBy: map[string]error{
			"/ws/bad": errors.New("bd: exit 1: database is locked"),
		},
	}
	// Zero retries so the error surfaces immediately.
	oldRetries := beadsReadRetries
	beadsReadRetries = 0
	defer func() { beadsReadRetries = oldRetries }()

	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{WorkingDir: "/ws/good", ACPServer: "acp-a"},
		{WorkingDir: "/ws/bad", ACPServer: "acp-b"},
	})
	s := New(Deps{SessionManager: sm, BeadsClient: client, Logger: logger})

	req := localhostRequest("/api/dashboard")
	w := httptest.NewRecorder()
	s.HandleDashboard(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	d := decodeDashboardBody(t, w)
	if got, want := len(d.Lists.InProgress), 1; got != want {
		t.Fatalf("in_progress length = %d, want %d", got, want)
	}
	if got := d.Lists.InProgress[0]["id"]; got != "g-1" {
		t.Errorf("in_progress[0].id = %v, want g-1", got)
	}
	if got, want := d.Lists.InProgress[0]["working_dir"], "/ws/good"; got != want {
		t.Errorf("in_progress[0].working_dir = %v, want %q", got, want)
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "level=WARN") {
		t.Errorf("log output = %q, want it to contain %q", logged, "level=WARN")
	}
	if !strings.Contains(logged, "/ws/bad") {
		t.Errorf("log output = %q, want it to reference the failing working_dir", logged)
	}
	if !strings.Contains(logged, "dashboard") {
		t.Errorf("log output = %q, want it to mention dashboard", logged)
	}
}

func TestHandleDashboard_LimitQuery(t *testing.T) {
	// Build 10 in_progress items for a single workspace so the cap is
	// observable. updated_at ordering: item-09 newest, item-00 oldest.
	items := make([]map[string]any, 0, 10)
	for i := 0; i < 10; i++ {
		items = append(items, map[string]any{
			"id":         "n-" + string(rune('0'+i%10)),
			"status":     "in_progress",
			"updated_at": "2026-01-" + twoDigit(i+10) + "T00:00:00Z",
		})
	}
	payload, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	client := &dashboardFakeClient{
		listByDir: map[string][]byte{"/ws/x": payload},
	}
	wss := []config.WorkspaceSettings{{WorkingDir: "/ws/x", ACPServer: "acp"}}

	cases := []struct {
		name       string
		url        string
		wantLength int
	}{
		{"default is 5", "/api/dashboard", dashboardDefaultLimit},
		{"explicit small cap", "/api/dashboard?limit=2", 2},
		{"explicit large cap clamps to max", "/api/dashboard?limit=999", dashboardMaxLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newDashboardTestServer(t, client, wss)
			req := localhostRequest(tc.url)
			w := httptest.NewRecorder()
			s.HandleDashboard(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
			}
			d := decodeDashboardBody(t, w)
			// The list has exactly 10 items to cap; the max clamp maps to 10.
			expect := tc.wantLength
			if expect > 10 {
				expect = 10
			}
			if got := len(d.Lists.InProgress); got != expect {
				t.Errorf("in_progress length = %d, want %d", got, expect)
			}
		})
	}
}

// twoDigit returns a zero-padded two-digit decimal for small non-negative ints.
func twoDigit(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	if n < 100 {
		return string(rune('0'+n/10)) + string(rune('0'+n%10))
	}
	return "99"
}

// --- sortByPriorityThenUpdatedAtDesc -----------------------------------------
//
// Ready / Epics lists must sort by priority ascending (0=Critical first) with
// updated_at desc as the intra-priority tiebreaker, and id asc as the final
// stable tiebreaker.

// prioItem is a shorthand for building test items with just the fields the
// priority sort inspects. priority is set as float64 to mirror what
// json.Unmarshal into map[string]any produces from bd's integer output.
func prioItem(id string, priority int, updatedAt string) map[string]any {
	m := map[string]any{"id": id, "updated_at": updatedAt}
	if priority >= 0 {
		m["priority"] = float64(priority)
	}
	return m
}

func TestSortByPriorityThenUpdatedAtDesc_PriorityBeatsRecency(t *testing.T) {
	// A Low-priority item touched today must still sit below a
	// Critical-priority item touched last week.
	items := []map[string]any{
		prioItem("a", 3, "2026-07-12T00:00:00Z"), // Low, most recent
		prioItem("b", 0, "2026-07-05T00:00:00Z"), // Critical, oldest
		prioItem("c", 2, "2026-07-10T00:00:00Z"), // Medium
	}
	sortByPriorityThenUpdatedAtDesc(items)
	got := []string{items[0]["id"].(string), items[1]["id"].(string), items[2]["id"].(string)}
	want := []string{"b", "c", "a"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestSortByPriorityThenUpdatedAtDesc_RecencyBreaksTiesWithinPriority(t *testing.T) {
	items := []map[string]any{
		prioItem("older", 1, "2026-07-01T00:00:00Z"),
		prioItem("newest", 1, "2026-07-12T00:00:00Z"),
		prioItem("middle", 1, "2026-07-07T00:00:00Z"),
	}
	sortByPriorityThenUpdatedAtDesc(items)
	want := []string{"newest", "middle", "older"}
	for i, w := range want {
		if items[i]["id"].(string) != w {
			t.Errorf("position %d: got %q, want %q", i, items[i]["id"], w)
		}
	}
}

func TestSortByPriorityThenUpdatedAtDesc_IDBreaksTiesWhenAllElseEqual(t *testing.T) {
	items := []map[string]any{
		prioItem("mitto-c", 2, "2026-07-10T00:00:00Z"),
		prioItem("mitto-a", 2, "2026-07-10T00:00:00Z"),
		prioItem("mitto-b", 2, "2026-07-10T00:00:00Z"),
	}
	sortByPriorityThenUpdatedAtDesc(items)
	want := []string{"mitto-a", "mitto-b", "mitto-c"}
	for i, w := range want {
		if items[i]["id"].(string) != w {
			t.Errorf("position %d: got %q, want %q", i, items[i]["id"], w)
		}
	}
}

func TestSortByPriorityThenUpdatedAtDesc_MissingPrioritySortsLast(t *testing.T) {
	// itemPriority returns a large sentinel for missing/wrong-type priorities
	// so any real 0..3 value beats them.
	items := []map[string]any{
		prioItem("no-prio", -1, "2026-07-12T00:00:00Z"), // priority key omitted
		prioItem("low", 3, "2026-07-01T00:00:00Z"),
		prioItem("critical", 0, "2026-07-05T00:00:00Z"),
	}
	sortByPriorityThenUpdatedAtDesc(items)
	want := []string{"critical", "low", "no-prio"}
	for i, w := range want {
		if items[i]["id"].(string) != w {
			t.Errorf("position %d: got %q, want %q", i, items[i]["id"], w)
		}
	}
}

func TestItemPriority_HandlesFloatAndInt(t *testing.T) {
	// json.Unmarshal into map[string]any produces float64; hand-built maps in
	// tests occasionally use int. Both branches must yield the raw integer.
	if got := itemPriority(map[string]any{"priority": float64(2)}); got != 2 {
		t.Errorf("float64(2) → %d, want 2", got)
	}
	if got := itemPriority(map[string]any{"priority": 1}); got != 1 {
		t.Errorf("int(1) → %d, want 1", got)
	}
	// Missing / wrong-type must sort last, not be treated as priority 0.
	if got := itemPriority(map[string]any{}); got <= 3 {
		t.Errorf("missing priority → %d, want >3 (sort-last sentinel)", got)
	}
	if got := itemPriority(map[string]any{"priority": "high"}); got <= 3 {
		t.Errorf("string priority → %d, want >3 (sort-last sentinel)", got)
	}
}
