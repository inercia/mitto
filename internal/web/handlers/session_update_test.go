package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// seedArchivedLoopSession creates a session store + archived session with a
// loop config that was stopped for the given reason, mimicking the state
// left behind by an archive.
func seedArchivedLoopSession(t *testing.T, sid string, stoppedReason session.StoppedReason) (*session.Store, *session.LoopPrompt) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  "test-server",
		WorkingDir: t.TempDir(),
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	loop := &session.LoopPrompt{
		Prompt:    "do the thing",
		Arguments: map[string]string{"foo": "bar"},
		Trigger:   session.TriggerOnCompletion,
		Enabled:   true,
	}
	if err := store.Loop(sid).Set(loop); err != nil {
		t.Fatalf("Loop Set failed: %v", err)
	}
	if err := store.Loop(sid).MarkStopped(stoppedReason); err != nil {
		t.Fatalf("MarkStopped failed: %v", err)
	}

	// Mark the session as archived (metadata only; MarkStopped doesn't do this).
	if err := store.UpdateMetadata(sid, func(meta *session.Metadata) {
		meta.Archived = true
		meta.ArchiveReason = session.ArchiveReasonManual
	}); err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	return store, loop
}

// unarchiveViaHandler issues a PATCH {"archived": false} against the given
// session and returns the recorded response.
func unarchiveViaHandler(t *testing.T, h *Handlers, sid string) *httptest.ResponseRecorder {
	t.Helper()
	archived := false
	body, _ := json.Marshal(SessionUpdateRequest{Archived: &archived})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleUpdateSession(w, req, sid)
	return w
}

func TestRestoreLoopOnUnarchive_RoundTripsOriginalConfig(t *testing.T) {
	sid := "test-loop-unarchive-roundtrip"
	store, original := seedArchivedLoopSession(t, sid, session.StoppedReasonArchived)
	h := New(Deps{Store: store})

	w := unarchiveViaHandler(t, h, sid)
	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	got, err := store.Loop(sid).Get()
	if err != nil {
		t.Fatalf("Get() after unarchive error = %v", err)
	}
	if got.Prompt != original.Prompt {
		t.Errorf("Prompt = %q, want %q", got.Prompt, original.Prompt)
	}
	if got.Arguments["foo"] != original.Arguments["foo"] {
		t.Errorf("Arguments = %v, want %v", got.Arguments, original.Arguments)
	}
	if got.Trigger != original.Trigger {
		t.Errorf("Trigger = %q, want %q", got.Trigger, original.Trigger)
	}
	if !got.Enabled {
		t.Error("Enabled should be true after unarchive of an archive-stopped loop")
	}
	if got.StoppedReason != "" {
		t.Errorf("StoppedReason = %q, want empty", got.StoppedReason)
	}
}

func TestRestoreLoopOnUnarchive_AutoResumeArchived(t *testing.T) {
	sid := "test-loop-unarchive-archived"
	store, _ := seedArchivedLoopSession(t, sid, session.StoppedReasonArchived)

	var mu sync.Mutex
	var broadcastCount int
	var lastLoop *session.LoopPrompt
	h := New(Deps{
		Store: store,
		BroadcastLoopUpdated: func(_ string, loop *session.LoopPrompt) {
			mu.Lock()
			defer mu.Unlock()
			broadcastCount++
			lastLoop = loop
		},
	})

	w := unarchiveViaHandler(t, h, sid)
	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	got, err := store.Loop(sid).Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !got.Enabled {
		t.Error("Enabled should be true")
	}
	if got.StoppedReason != "" {
		t.Errorf("StoppedReason = %q, want empty", got.StoppedReason)
	}

	mu.Lock()
	defer mu.Unlock()
	if broadcastCount != 1 {
		t.Errorf("BroadcastLoopUpdated call count = %d, want 1", broadcastCount)
	}
	if lastLoop == nil {
		t.Error("BroadcastLoopUpdated called with nil loop")
	}
}

func TestRestoreLoopOnUnarchive_AutoResumeResumeFailures(t *testing.T) {
	sid := "test-loop-unarchive-resumefailures"
	store, _ := seedArchivedLoopSession(t, sid, session.StoppedReasonResumeFailures)

	var mu sync.Mutex
	var broadcastCount int
	h := New(Deps{
		Store: store,
		BroadcastLoopUpdated: func(_ string, _ *session.LoopPrompt) {
			mu.Lock()
			defer mu.Unlock()
			broadcastCount++
		},
	})

	w := unarchiveViaHandler(t, h, sid)
	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	got, err := store.Loop(sid).Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !got.Enabled {
		t.Error("Enabled should be true")
	}
	if got.StoppedReason != "" {
		t.Errorf("StoppedReason = %q, want empty", got.StoppedReason)
	}

	mu.Lock()
	defer mu.Unlock()
	if broadcastCount != 1 {
		t.Errorf("BroadcastLoopUpdated call count = %d, want 1", broadcastCount)
	}
}

func TestRestoreLoopOnUnarchive_NonArchivePauseStaysPaused(t *testing.T) {
	sid := "test-loop-unarchive-maxiterations"
	store, _ := seedArchivedLoopSession(t, sid, session.StoppedReasonMaxIterations)

	var mu sync.Mutex
	var broadcastCount int
	h := New(Deps{
		Store: store,
		BroadcastLoopUpdated: func(_ string, _ *session.LoopPrompt) {
			mu.Lock()
			defer mu.Unlock()
			broadcastCount++
		},
	})

	w := unarchiveViaHandler(t, h, sid)
	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	got, err := store.Loop(sid).Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Enabled {
		t.Error("Enabled should stay false for a non-archive-related stop reason")
	}
	if got.StoppedReason != session.StoppedReasonMaxIterations {
		t.Errorf("StoppedReason = %q, want %q", got.StoppedReason, session.StoppedReasonMaxIterations)
	}

	mu.Lock()
	defer mu.Unlock()
	if broadcastCount != 1 {
		t.Errorf("BroadcastLoopUpdated call count = %d, want 1 (config should still be surfaced)", broadcastCount)
	}
}

// TestHandleUpdateSession_UnarchiveResetsACPStartFailureCount is a regression
// test for mitto-wub: a session archived via ArchiveReasonACPFailures carries
// a non-zero ACPStartFailureCount into the unarchive. Today
// HandleUpdateSession's PATCH {"archived": false} path only clears
// Archived/ArchivedAt/ArchiveReason/AutoUnarchiveLastAttemptAt — it never
// resets ACPStartFailureCount. That means the very next transient ACP start
// failure after unarchive re-trips the ACPStartFailureThreshold immediately
// (count was already at/near threshold), re-archiving the session before it
// ever gets a chance at a successful start that would normally reset the
// counter (session_manager.go resets it only on success). This test seeds a
// session at ACPStartFailureThreshold-1 (the same "one failure from
// re-archiving" state seen in the bead's log excerpt: failure_count=4,
// threshold=3, i.e. already over on the very first post-unarchive attempt)
// and asserts the counter is back to 0 after unarchive.
func TestHandleUpdateSession_UnarchiveResetsACPStartFailureCount(t *testing.T) {
	sid := "test-unarchive-resets-acp-failure-count"
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  "test-server",
		WorkingDir: t.TempDir(),
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := store.UpdateMetadata(sid, func(m *session.Metadata) {
		m.Archived = true
		m.ArchiveReason = session.ArchiveReasonACPFailures
		m.ACPStartFailureCount = 4 // mirrors the bead's reproduced log: failure_count=4, threshold=3
	}); err != nil {
		t.Fatalf("UpdateMetadata (seed) failed: %v", err)
	}

	h := New(Deps{Store: store})

	w := unarchiveViaHandler(t, h, sid)
	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	got, err := store.GetMetadata(sid)
	if err != nil {
		t.Fatalf("GetMetadata after unarchive failed: %v", err)
	}
	if got.ACPStartFailureCount != 0 {
		t.Errorf("ACPStartFailureCount after unarchive = %d, want 0 (bug mitto-wub: counter carries over across archive/unarchive, so the very next transient failure immediately re-archives the session)", got.ACPStartFailureCount)
	}
}

func TestRestoreLoopOnUnarchive_NoLoopSessionIsNoop(t *testing.T) {
	sid := "test-no-loop-unarchive"
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  "test-server",
		WorkingDir: t.TempDir(),
		Archived:   true,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	var broadcastCount int
	h := New(Deps{
		Store: store,
		BroadcastLoopUpdated: func(_ string, _ *session.LoopPrompt) {
			broadcastCount++
		},
	})

	w := unarchiveViaHandler(t, h, sid)
	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	if _, err := store.Loop(sid).Get(); err != session.ErrLoopNotFound {
		t.Errorf("Loop Get() error = %v, want ErrLoopNotFound", err)
	}
	if broadcastCount != 0 {
		t.Errorf("BroadcastLoopUpdated call count = %d, want 0 for a non-loop session", broadcastCount)
	}
}

// archiveViaHandler issues a PATCH {"archived": true} against the given
// session and returns the recorded response.
func archiveViaHandler(t *testing.T, h *Handlers, sid string) *httptest.ResponseRecorder {
	t.Helper()
	archived := true
	body, _ := json.Marshal(SessionUpdateRequest{Archived: &archived})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleUpdateSession(w, req, sid)
	return w
}

// TestArchive_FiresApplyOnCloseProcessors verifies that a manual archive PATCH
// invokes the ApplyOnCloseProcessors dep with the manual archive reason.
func TestArchive_FiresApplyOnCloseProcessors(t *testing.T) {
	sid := "test-close-fires"
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  "test-server",
		WorkingDir: t.TempDir(),
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	var mu sync.Mutex
	var calls []struct{ id, reason string }
	h := New(Deps{
		Store: store,
		ApplyOnCloseProcessors: func(sessionID, reason string) {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, struct{ id, reason string }{sessionID, reason})
		},
	})

	w := archiveViaHandler(t, h, sid)
	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("ApplyOnCloseProcessors call count = %d, want 1", len(calls))
	}
	if calls[0].id != sid {
		t.Errorf("sessionID = %q, want %q", calls[0].id, sid)
	}
	if calls[0].reason != string(session.ArchiveReasonManual) {
		t.Errorf("reason = %q, want %q", calls[0].reason, session.ArchiveReasonManual)
	}
}

// TestUnarchive_DoesNotFireApplyOnCloseProcessors verifies that a PATCH
// archived=false does NOT invoke the close-phase trigger.
func TestUnarchive_DoesNotFireApplyOnCloseProcessors(t *testing.T) {
	sid := "test-close-not-fires-on-unarchive"
	store, _ := seedArchivedLoopSession(t, sid, session.StoppedReasonArchived)

	var mu sync.Mutex
	var callCount int
	h := New(Deps{
		Store: store,
		ApplyOnCloseProcessors: func(_, _ string) {
			mu.Lock()
			defer mu.Unlock()
			callCount++
		},
	})

	w := unarchiveViaHandler(t, h, sid)
	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	mu.Lock()
	defer mu.Unlock()
	if callCount != 0 {
		t.Errorf("ApplyOnCloseProcessors call count = %d, want 0 on unarchive", callCount)
	}
}

// TestHandleUpdateSession_BackgroundColorSetAndClear verifies PATCH
// /api/sessions/{id} can set and then clear background_color (mitto-8sk),
// mirroring the existing beads_issue set/clear coverage.
func TestHandleUpdateSession_BackgroundColorSetAndClear(t *testing.T) {
	sid := "test-session-color"
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  "test-server",
		WorkingDir: t.TempDir(),
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	h := New(Deps{Store: store})

	// Set.
	color := "#E1BEE7"
	body, _ := json.Marshal(SessionUpdateRequest{BackgroundColor: &color})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleUpdateSession(w, req, sid)
	if w.Code != http.StatusOK {
		t.Fatalf("Set: Status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	meta, err := store.GetMetadata(sid)
	if err != nil {
		t.Fatalf("GetMetadata after set: %v", err)
	}
	if meta.BackgroundColor != color {
		t.Errorf("BackgroundColor after set = %q, want %q", meta.BackgroundColor, color)
	}

	// Clear (empty string).
	empty := ""
	body2, _ := json.Marshal(SessionUpdateRequest{BackgroundColor: &empty})
	req2 := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid, bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	h.HandleUpdateSession(w2, req2, sid)
	if w2.Code != http.StatusOK {
		t.Fatalf("Clear: Status = %d, want %d; body: %s", w2.Code, http.StatusOK, w2.Body.String())
	}
	cleared, err := store.GetMetadata(sid)
	if err != nil {
		t.Fatalf("GetMetadata after clear: %v", err)
	}
	if cleared.BackgroundColor != "" {
		t.Errorf("BackgroundColor after clear = %q, want empty", cleared.BackgroundColor)
	}
}
