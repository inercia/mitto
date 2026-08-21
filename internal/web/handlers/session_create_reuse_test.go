package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// TestReuseIssue_NotLoadedIdle_EnqueuesWithoutDispatch mirrors
// TestReuseSingletonSession_NotLoadedIdle_EnqueuesWithoutDispatch but exercises
// the reuseIssue funnel path: a request that would land on an existing beads-
// linked conversation should reuse it and enqueue the seed prompt directly
// (no SessionManager → no live dispatch), returning reused:true.
func TestReuseIssue_NotLoadedIdle_EnqueuesWithoutDispatch(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewStore(dir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	sessionID := "20260201-140000-reuseissue"
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		Status:     "active",
		WorkingDir: "/work",
		BeadsIssue: "mitto-123",
		UpdatedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	h := New(Deps{Store: store})

	// Same helper used by the reuseIssue block in HandleCreateSession.
	w := httptest.NewRecorder()
	h.reuseSingletonSession(w, sessionID, "cleanup-issue", map[string]string{"A": "b"})

	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp["session_id"] != sessionID {
		t.Errorf("session_id = %v, want %q", resp["session_id"], sessionID)
	}
	if resp["reused"] != true {
		t.Errorf("reused = %v, want true", resp["reused"])
	}

	queue := store.Queue(sessionID)
	messages, err := queue.List()
	if err != nil {
		t.Fatalf("Queue.List failed: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(messages))
	}
	if messages[0].PromptName != "cleanup-issue" {
		t.Errorf("PromptName = %q, want %q", messages[0].PromptName, "cleanup-issue")
	}
}

// TestReuseIssueLock_ReentryOnSameKey verifies that lockReuseIssue returns
// distinct-per-key mutexes and that the same key blocks concurrent holders —
// the same locking contract HandleCreateSession relies on to make the
// scan+create sequence atomic per (workingDir, beadsIssue).
func TestReuseIssueLock_ReentryOnSameKey(t *testing.T) {
	h := New(Deps{})

	unlock1 := h.lockReuseIssue("/work\x00mitto-123")
	// Different key must not block.
	done := make(chan struct{})
	go func() {
		unlock2 := h.lockReuseIssue("/work\x00mitto-999")
		unlock2()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lockReuseIssue blocked on a different key")
	}

	// Same key must block until the first holder unlocks.
	sameKeyDone := make(chan struct{})
	go func() {
		unlock3 := h.lockReuseIssue("/work\x00mitto-123")
		unlock3()
		close(sameKeyDone)
	}()
	select {
	case <-sameKeyDone:
		t.Fatal("lockReuseIssue on the same key should have blocked")
	case <-time.After(50 * time.Millisecond):
		// Expected — still blocked.
	}
	unlock1()
	select {
	case <-sameKeyDone:
	case <-time.After(time.Second):
		t.Fatal("lockReuseIssue did not release after first unlock")
	}
}

// TestReuseTitle_NotLoadedIdle_EnqueuesWithoutDispatch is the reuseTitle
// analog of TestReuseIssue_NotLoadedIdle_EnqueuesWithoutDispatch: a request
// that would land on an existing title-matched conversation should reuse it
// and enqueue the seed prompt directly (no SessionManager → no live dispatch),
// returning reused:true.
func TestReuseTitle_NotLoadedIdle_EnqueuesWithoutDispatch(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewStore(dir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	sessionID := "20260201-140000-reusetitle"
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		Status:     "active",
		WorkingDir: "/work",
		Name:       "Weekly triage",
		UpdatedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	h := New(Deps{Store: store})

	// The reuseTitle block in HandleCreateSession funnels through the same
	// helper as reuseIssue and singleton.
	w := httptest.NewRecorder()
	h.reuseSingletonSession(w, sessionID, "weekly-triage", map[string]string{"A": "b"})

	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp["session_id"] != sessionID {
		t.Errorf("session_id = %v, want %q", resp["session_id"], sessionID)
	}
	if resp["reused"] != true {
		t.Errorf("reused = %v, want true", resp["reused"])
	}

	queue := store.Queue(sessionID)
	messages, err := queue.List()
	if err != nil {
		t.Fatalf("Queue.List failed: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(messages))
	}
	if messages[0].PromptName != "weekly-triage" {
		t.Errorf("PromptName = %q, want %q", messages[0].PromptName, "weekly-triage")
	}
}

// TestReuseTitleLock_ReentryOnSameKey verifies that lockReuseTitle returns
// distinct-per-key mutexes and that the same key blocks concurrent holders —
// the same locking contract HandleCreateSession relies on to make the
// scan+create sequence atomic per (workingDir, title).
func TestReuseTitleLock_ReentryOnSameKey(t *testing.T) {
	h := New(Deps{})

	unlock1 := h.lockReuseTitle("/work\x00Weekly triage")
	// Different key must not block.
	done := make(chan struct{})
	go func() {
		unlock2 := h.lockReuseTitle("/work\x00Daily standup")
		unlock2()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lockReuseTitle blocked on a different key")
	}

	// Same key must block until the first holder unlocks.
	sameKeyDone := make(chan struct{})
	go func() {
		unlock3 := h.lockReuseTitle("/work\x00Weekly triage")
		unlock3()
		close(sameKeyDone)
	}()
	select {
	case <-sameKeyDone:
		t.Fatal("lockReuseTitle on the same key should have blocked")
	case <-time.After(50 * time.Millisecond):
		// Expected — still blocked.
	}
	unlock1()
	select {
	case <-sameKeyDone:
	case <-time.After(time.Second):
		t.Fatal("lockReuseTitle did not release after first unlock")
	}
}
