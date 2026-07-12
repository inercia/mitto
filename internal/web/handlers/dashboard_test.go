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

	// in_progress: a-1 and b-1, sorted by updated_at desc → a-1 first (2026-01-02) then b-1 (2026-01-01).
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
