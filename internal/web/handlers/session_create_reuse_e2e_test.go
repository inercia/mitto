package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// newReuseE2EHandlers builds a Handlers facade wired for end-to-end HTTP tests
// of the reuseIssue branch in HandleCreateSession. It returns a real Store, a
// minimally-wired SessionManager with a single workspace at workingDir, and the
// Handlers under test. Tests inject ResolvePromptReuseIssue / ResolvePromptSingleton
// closures directly on Deps to drive the guard/precedence flow.
func newReuseE2EHandlers(t *testing.T, workingDir string) (*session.Store, *Handlers) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	sm := conversation.NewSessionManager("", "test-server", false, nil)
	sm.SetStore(store)
	sm.SetWorkspaces([]configPkg.WorkspaceSettings{
		{UUID: "ws-a", WorkingDir: workingDir, ACPServer: "test-server", IsDefault: true},
	})

	h := New(Deps{
		Store:                   store,
		SessionManager:          sm,
		DefaultACPServer:        "test-server",
		NotifyQueueUpdate:       func(string, string, string) {},
		BroadcastSessionCreated: func(map[string]interface{}) {},
		BroadcastACPStartFailed: func(string, string, error, string) {},
	})
	return store, h
}

// postSession posts a SessionCreateRequest as JSON and returns the recorder.
func postSession(t *testing.T, h *Handlers, req SessionCreateRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	httpReq := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleCreateSession(w, httpReq)
	return w
}

// decodeSessionResponse decodes the response body as a generic map. It
// tolerates non-200 responses (returns nil), so miss-path tests can uniformly
// check "response is NOT {session_id: A, reused: true}" whether create fell
// through to 200-new or 500-failed.
func decodeSessionResponse(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	if w.Body.Len() == 0 {
		return nil
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		return nil
	}
	return resp
}

// assertNotReusedTo fails when resp claims {session_id: existingID, reused: true},
// which is the "wrong candidate" outcome the miss-path scenarios must never emit.
func assertNotReusedTo(t *testing.T, resp map[string]interface{}, existingID string) {
	t.Helper()
	if resp == nil {
		return // 500 / empty body is acceptable — it definitively is not "reused to A".
	}
	if resp["session_id"] == existingID && resp["reused"] == true {
		t.Fatalf("response reused pre-existing session %q, expected miss: %+v", existingID, resp)
	}
}

// TestReuseIssueE2E_HitReturnsExistingSession — Scenario 1.
// Pre-populated session A with beads_issue=X and matching workingDir; the
// reuseIssue resolver returns true for the prompt. POST must return
// {session_id: A, reused: true} without invoking CreateSessionWithWorkspace.
func TestReuseIssueE2E_HitReturnsExistingSession(t *testing.T) {
	const workingDir = "/work-hit"
	store, h := newReuseE2EHandlers(t, workingDir)

	sessionID := "20260201-140000-hit"
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		Status:     "active",
		ACPServer:  "test-server",
		WorkingDir: workingDir,
		BeadsIssue: "mitto-100",
		UpdatedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	var reuseCalls, singletonCalls int32
	h.deps.ResolvePromptReuseIssue = func(promptName, wd string) bool {
		atomic.AddInt32(&reuseCalls, 1)
		return promptName == "cleanup-issue" && wd == workingDir
	}
	h.deps.ResolvePromptSingleton = func(string, string) bool {
		atomic.AddInt32(&singletonCalls, 1)
		return false
	}

	w := postSession(t, h, SessionCreateRequest{
		WorkingDir:       workingDir,
		BeadsIssue:       "mitto-100",
		OriginPromptName: "cleanup-issue",
		Arguments:        map[string]string{"K": "v"},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	resp := decodeSessionResponse(t, w)
	if resp == nil {
		t.Fatalf("nil response body")
	}
	if resp["session_id"] != sessionID {
		t.Errorf("session_id = %v, want %q", resp["session_id"], sessionID)
	}
	if resp["reused"] != true {
		t.Errorf("reused = %v, want true", resp["reused"])
	}
	if atomic.LoadInt32(&reuseCalls) == 0 {
		t.Errorf("ResolvePromptReuseIssue not called; expected the reuseIssue branch to evaluate the prompt")
	}
	// Reuse hit returned early — singleton scan must not have been consulted.
	if atomic.LoadInt32(&singletonCalls) != 0 {
		t.Errorf("ResolvePromptSingleton call count = %d after a reuse HIT, want 0", singletonCalls)
	}
	// The reused funnel enqueues the named prompt on the existing session.
	msgs, err := store.Queue(sessionID).List()
	if err != nil {
		t.Fatalf("Queue.List: %v", err)
	}
	if len(msgs) != 1 || msgs[0].PromptName != "cleanup-issue" {
		t.Errorf("queue = %+v, want 1 message with prompt_name=cleanup-issue", msgs)
	}
}

// TestReuseIssueE2E_PrecedenceSkipsSingletonScan — Scenario 2.
// Prompt P is BOTH reuseIssue-enabled AND singleton-enabled. Session A is
// pinned to beads_issue=X + prompt P. POSTing beads_issue=Y (same prompt,
// same working dir) must NOT collapse into session A — the reuseIssueEvaluated
// guard at session_create.go:166 must prevent the singleton scan from running
// after a reuseIssue miss. Spy asserts ResolvePromptSingleton is never called.
func TestReuseIssueE2E_PrecedenceSkipsSingletonScan(t *testing.T) {
	const workingDir = "/work-prec"
	store, h := newReuseE2EHandlers(t, workingDir)

	sessionA := "20260201-140000-preca"
	if err := store.Create(session.Metadata{
		SessionID:        sessionA,
		Status:           "active",
		ACPServer:        "test-server",
		WorkingDir:       workingDir,
		BeadsIssue:       "mitto-X",
		OriginPromptName: "shared-prompt",
		UpdatedAt:        time.Now(),
	}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	var singletonCalls int32
	h.deps.ResolvePromptReuseIssue = func(promptName, _ string) bool {
		return promptName == "shared-prompt"
	}
	h.deps.ResolvePromptSingleton = func(promptName, _ string) bool {
		atomic.AddInt32(&singletonCalls, 1)
		return promptName == "shared-prompt"
	}

	// beads_issue=Y — no session pinned to it. reuseIssue evaluates → misses.
	// The guard must then SKIP the singleton scan.
	w := postSession(t, h, SessionCreateRequest{
		WorkingDir:       workingDir,
		BeadsIssue:       "mitto-Y",
		OriginPromptName: "shared-prompt",
	})

	// Definitive precedence assertion: singleton path was never consulted.
	if got := atomic.LoadInt32(&singletonCalls); got != 0 {
		t.Fatalf("ResolvePromptSingleton call count = %d after reuseIssue miss, want 0 (precedence guard failed)", got)
	}
	// Additional safety net: whatever the create path returned, it must NOT be
	// {session_id: A, reused: true}. Without the guard, singleton would collapse
	// mitto-Y into session A.
	assertNotReusedTo(t, decodeSessionResponse(t, w), sessionA)
}

// TestReuseIssueE2E_EmptyBeadsIssueSkipsReuseLookup — Scenario 3.
// Empty beads_issue must short-circuit the reuseIssue branch at line 137 before
// the resolver is consulted, so ResolvePromptReuseIssue is never called and no
// per-issue lock is taken. The singleton path remains available on the same
// request (positive control: the fallback still routes).
func TestReuseIssueE2E_EmptyBeadsIssueSkipsReuseLookup(t *testing.T) {
	const workingDir = "/work-empty"
	store, h := newReuseE2EHandlers(t, workingDir)

	singletonID := "20260201-140000-single"
	if err := store.Create(session.Metadata{
		SessionID:        singletonID,
		Status:           "active",
		ACPServer:        "test-server",
		WorkingDir:       workingDir,
		OriginPromptName: "only-singleton",
		UpdatedAt:        time.Now(),
	}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	var reuseCalls, singletonCalls int32
	h.deps.ResolvePromptReuseIssue = func(string, string) bool {
		atomic.AddInt32(&reuseCalls, 1)
		return true // Would match, but must not be reached when beads_issue is empty.
	}
	h.deps.ResolvePromptSingleton = func(promptName, _ string) bool {
		atomic.AddInt32(&singletonCalls, 1)
		return promptName == "only-singleton"
	}

	w := postSession(t, h, SessionCreateRequest{
		WorkingDir:       workingDir,
		OriginPromptName: "only-singleton",
		// BeadsIssue intentionally omitted.
	})

	if got := atomic.LoadInt32(&reuseCalls); got != 0 {
		t.Fatalf("ResolvePromptReuseIssue call count = %d with empty beads_issue, want 0 (line-137 guard failed)", got)
	}
	if got := atomic.LoadInt32(&singletonCalls); got == 0 {
		t.Fatalf("ResolvePromptSingleton was never called; the singleton fallback must run when reuseIssue is skipped")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d (singleton positive-control should route); body: %s",
			w.Code, http.StatusOK, w.Body.String())
	}
	resp := decodeSessionResponse(t, w)
	if resp == nil || resp["session_id"] != singletonID || resp["reused"] != true {
		t.Fatalf("expected singleton reuse to route to %q; got %+v", singletonID, resp)
	}
}

// TestReuseIssueE2E_ArchivedCandidateIgnored — Scenario 4.
// The only session with matching beads_issue is archived. FindConversationByBeadsIssue
// skips archived rows, so the handler must fall through to normal creation and
// NOT return {session_id: A, reused: true}.
func TestReuseIssueE2E_ArchivedCandidateIgnored(t *testing.T) {
	const workingDir = "/work-arch"
	store, h := newReuseE2EHandlers(t, workingDir)

	sessionA := "20260201-140000-arch"
	if err := store.Create(session.Metadata{
		SessionID:  sessionA,
		Status:     "active",
		ACPServer:  "test-server",
		WorkingDir: workingDir,
		BeadsIssue: "mitto-arch",
		Archived:   true,
		UpdatedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	h.deps.ResolvePromptReuseIssue = func(string, string) bool { return true }
	h.deps.ResolvePromptSingleton = func(string, string) bool { return false }

	w := postSession(t, h, SessionCreateRequest{
		WorkingDir:       workingDir,
		BeadsIssue:       "mitto-arch",
		OriginPromptName: "any-prompt",
	})
	assertNotReusedTo(t, decodeSessionResponse(t, w), sessionA)
}

// TestReuseIssueE2E_DifferentWorkingDirCreatesNew — Scenario 5.
// Session A carries beads_issue=X in /workA. A request for the same beads_issue
// in /workB must NOT reuse A — reuseIssue scoping is per-workingDir.
func TestReuseIssueE2E_DifferentWorkingDirCreatesNew(t *testing.T) {
	const workDirA = "/work-A"
	const workDirB = "/work-B"

	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	sm := conversation.NewSessionManager("", "test-server", false, nil)
	sm.SetStore(store)
	sm.SetWorkspaces([]configPkg.WorkspaceSettings{
		{UUID: "ws-a", WorkingDir: workDirA, ACPServer: "test-server", IsDefault: true},
		{UUID: "ws-b", WorkingDir: workDirB, ACPServer: "test-server"},
	})
	h := New(Deps{
		Store:                   store,
		SessionManager:          sm,
		DefaultACPServer:        "test-server",
		NotifyQueueUpdate:       func(string, string, string) {},
		BroadcastSessionCreated: func(map[string]interface{}) {},
		BroadcastACPStartFailed: func(string, string, error, string) {},
		ResolvePromptReuseIssue: func(string, string) bool { return true },
		ResolvePromptSingleton:  func(string, string) bool { return false },
	})

	sessionA := "20260201-140000-wda"
	if err := store.Create(session.Metadata{
		SessionID:  sessionA,
		Status:     "active",
		ACPServer:  "test-server",
		WorkingDir: workDirA,
		BeadsIssue: "mitto-cross",
		UpdatedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	w := postSession(t, h, SessionCreateRequest{
		WorkingDir:       workDirB,
		BeadsIssue:       "mitto-cross",
		OriginPromptName: "any-prompt",
	})
	assertNotReusedTo(t, decodeSessionResponse(t, w), sessionA)
}
