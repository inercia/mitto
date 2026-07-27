package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// newLoopStore creates a temp store and returns it together with a Handlers
// wired with only the Store dependency. Broadcast/bootstrap deps are left nil
// (no-ops), which is sufficient for the loop REST handler tests.
func newLoopStore(t *testing.T) (*session.Store, *Handlers) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	h := New(Deps{Store: store})
	return store, h
}

// putLoopForTest is a helper that PUTs a loop config via the REST handler and
// returns the decoded response. It fails the test on a non-200 status.
func putLoopForTest(t *testing.T, h *Handlers, sid string, body LoopPromptRequest) session.LoopPrompt {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/sessions/"+sid+"/loop", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleSessionLoop(w, req, sid, "")
	if w.Code != http.StatusOK {
		t.Fatalf("PUT loop: Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var got session.LoopPrompt
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode PUT response: %v", err)
	}
	return got
}

// TestHandleRunLoopNow_TimeoutReturnsRetryable503 verifies that a slow
// TriggerLoopNow (e.g. a blocking auto-resume) does not block the handler
// past auxBackedRequestTimeout: it returns a fast retryable 503 with a
// Retry-After header and the canonical "unavailable" error code (mitto-n36h).
func TestHandleRunLoopNow_TimeoutReturnsRetryable503(t *testing.T) {
	// Lower the budget so the test completes quickly.
	old := auxBackedRequestTimeout
	auxBackedRequestTimeout = 20 * time.Millisecond
	defer func() { auxBackedRequestTimeout = old }()

	// Stub blocks past the shortened budget so the handler's deadline fires
	// first; the buffered result channel lets this goroutine finish without
	// leaking once the test releases it.
	release := make(chan struct{})
	defer close(release)
	stub := func(_ string, _ bool) error {
		<-release
		return nil
	}

	h := New(Deps{TriggerLoopNow: stub})

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/sid/loop/run-now", nil)
	w := httptest.NewRecorder()
	h.handleRunLoopNow(w, req, "sid")

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if ra := w.Header().Get("Retry-After"); ra == "" {
		t.Error("Retry-After header not set")
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code != "unavailable" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "unavailable")
	}
}

func TestHandleSessionLoop_ChildRejected(t *testing.T) {
	store, h := newLoopStore(t)
	tmpDir := t.TempDir()

	if err := store.Create(session.Metadata{
		SessionID:  "test-parent-loop",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create parent failed: %v", err)
	}

	if err := store.Create(session.Metadata{
		SessionID:       "test-child-loop",
		ACPServer:       "test-server",
		WorkingDir:      tmpDir,
		ParentSessionID: "test-parent-loop",
	}); err != nil {
		t.Fatalf("Create child failed: %v", err)
	}

	// PUT loop on child — should be rejected
	body, _ := json.Marshal(LoopPromptRequest{
		Prompt:    "check updates",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   true,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/sessions/test-child-loop/loop", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleSessionLoop(w, req, "test-child-loop", "")

	if w.Code != http.StatusBadRequest {
		t.Errorf("PUT loop on child: Status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("Failed to unmarshal error envelope: %v (body=%q)", err, w.Body.String())
	}
	if env.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "bad_request")
	}
	const wantMsg = "Cannot set loop on a child conversation. Only parent or top-level conversations can be loops."
	if env.Error.Message != wantMsg {
		t.Errorf("error.message = %q, want %q", env.Error.Message, wantMsg)
	}

	// GET should still work (not rejected as 400)
	req2 := httptest.NewRequest(http.MethodGet, "/api/sessions/test-child-loop/loop", nil)
	w2 := httptest.NewRecorder()

	h.HandleSessionLoop(w2, req2, "test-child-loop", "")

	if w2.Code == http.StatusBadRequest {
		t.Error("GET loop on child should NOT be rejected with 400")
	}
}

// TestHandleSessionLoop_TopLevelAllowed tests that setting loop on a top-level session works.
func TestHandleSessionLoop_TopLevelAllowed(t *testing.T) {
	store, h := newLoopStore(t)
	tmpDir := t.TempDir()

	if err := store.Create(session.Metadata{
		SessionID:  "test-toplevel-loop",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	body, _ := json.Marshal(LoopPromptRequest{
		Prompt:    "check updates",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   true,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/sessions/test-toplevel-loop/loop", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleSessionLoop(w, req, "test-toplevel-loop", "")

	if w.Code != http.StatusOK {
		t.Errorf("PUT loop on top-level: Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

// TestHandleSessionLoop_OnCompletionRoundTrip verifies that the on-completion trigger,
// completion delay, and max-duration fields round-trip through the PUT handler. A frequency
// is not required for the onCompletion trigger.
func TestHandleSessionLoop_OnCompletionRoundTrip(t *testing.T) {
	store, h := newLoopStore(t)
	tmpDir := t.TempDir()

	const sid = "test-oncompletion-roundtrip"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test-server", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got := putLoopForTest(t, h, sid, LoopPromptRequest{
		Prompt:             "keep going",
		Enabled:            true,
		Trigger:            session.TriggerOnCompletion,
		DelaySeconds:       30,
		MaxDurationSeconds: 3600,
	})

	if got.Trigger != session.TriggerOnCompletion {
		t.Errorf("Trigger = %q, want %q", got.Trigger, session.TriggerOnCompletion)
	}
	if got.DelaySeconds != 30 {
		t.Errorf("DelaySeconds = %d, want 30", got.DelaySeconds)
	}
	if got.MaxDurationSeconds != 3600 {
		t.Errorf("MaxDurationSeconds = %d, want 3600", got.MaxDurationSeconds)
	}
}

// TestHandleSessionLoop_OnCompletionDelayClampedOnPut verifies that a delay below the
// global floor is clamped up to the floor on write (PUT). With no loop runner configured,
// the floor is the package default.
func TestHandleSessionLoop_OnCompletionDelayClampedOnPut(t *testing.T) {
	store, h := newLoopStore(t)
	tmpDir := t.TempDir()

	const sid = "test-oncompletion-clamp-put"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test-server", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got := putLoopForTest(t, h, sid, LoopPromptRequest{
		Prompt:       "keep going",
		Enabled:      true,
		Trigger:      session.TriggerOnCompletion,
		DelaySeconds: 1, // below the default floor (5)
	})

	if got.DelaySeconds != h.loopDelayFloor() {
		t.Errorf("DelaySeconds = %d, want clamped to floor %d", got.DelaySeconds, h.loopDelayFloor())
	}
}

// TestHandleSessionLoop_PatchPartialPreservesOnCompletionFields verifies that a partial
// PATCH updating only max_duration_seconds does not clobber the trigger or delay.
func TestHandleSessionLoop_PatchPartialPreservesOnCompletionFields(t *testing.T) {
	store, h := newLoopStore(t)
	tmpDir := t.TempDir()

	const sid = "test-oncompletion-patch"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test-server", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Seed an onCompletion config with a delay and no duration cap.
	putLoopForTest(t, h, sid, LoopPromptRequest{
		Prompt:       "keep going",
		Enabled:      true,
		Trigger:      session.TriggerOnCompletion,
		DelaySeconds: 30,
	})

	// PATCH only max_duration_seconds.
	maxDur := 7200
	patchBody, _ := json.Marshal(LoopPromptPatchRequest{MaxDurationSeconds: &maxDur})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid+"/loop", bytes.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleSessionLoop(w, req, sid, "")
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH loop: Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	stored, err := store.Loop(sid).Get()
	if err != nil {
		t.Fatalf("Get loop after PATCH: %v", err)
	}
	if stored.Trigger != session.TriggerOnCompletion {
		t.Errorf("Trigger after PATCH = %q, want %q (must not be clobbered)", stored.Trigger, session.TriggerOnCompletion)
	}
	if stored.DelaySeconds != 30 {
		t.Errorf("DelaySeconds after PATCH = %d, want 30 (must not be clobbered)", stored.DelaySeconds)
	}
	if stored.MaxDurationSeconds != 7200 {
		t.Errorf("MaxDurationSeconds after PATCH = %d, want 7200", stored.MaxDurationSeconds)
	}
}

// TestHandleSessionLoop_PatchResetCounters verifies that PATCHing with
// reset_counters=true (used when restoring a loop that hit its cap) re-enables the
// loop and resets IterationCount=0 and FirstRunAt=nil (elapsed time = 0).
func TestHandleSessionLoop_PatchResetCounters(t *testing.T) {
	store, h := newLoopStore(t)
	tmpDir := t.TempDir()

	const sid = "test-reset-counters-patch"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test-server", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Seed an onCompletion config with a duration cap.
	putLoopForTest(t, h, sid, LoopPromptRequest{
		Prompt:             "keep going",
		Enabled:            true,
		Trigger:            session.TriggerOnCompletion,
		DelaySeconds:       30,
		MaxDurationSeconds: 60,
	})

	// Simulate two completed runs, then auto-stop on the duration cap.
	ps := store.Loop(sid)
	if err := ps.RecordSent(); err != nil {
		t.Fatalf("RecordSent: %v", err)
	}
	if err := ps.RecordSent(); err != nil {
		t.Fatalf("RecordSent: %v", err)
	}
	if err := ps.MarkStopped(session.StoppedReasonMaxDuration); err != nil {
		t.Fatalf("MarkStopped: %v", err)
	}

	// PATCH restore with reset_counters=true.
	enabled := true
	reset := true
	patchBody, _ := json.Marshal(LoopPromptPatchRequest{Enabled: &enabled, ResetCounters: &reset})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid+"/loop", bytes.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleSessionLoop(w, req, sid, "")
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH loop: Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	stored, err := ps.Get()
	if err != nil {
		t.Fatalf("Get loop after PATCH: %v", err)
	}
	if !stored.Enabled {
		t.Error("Enabled after restore = false, want true")
	}
	if stored.IterationCount != 0 {
		t.Errorf("IterationCount after reset = %d, want 0", stored.IterationCount)
	}
	if stored.FirstRunAt != nil {
		t.Errorf("FirstRunAt after reset = %v, want nil", stored.FirstRunAt)
	}
	// LastSentAt must be cleared so the restored loop looks never-sent and the
	// onCompletion first run fires immediately (no delay) instead of waiting out
	// the configured delay_seconds.
	if stored.LastSentAt != nil {
		t.Errorf("LastSentAt after reset = %v, want nil", stored.LastSentAt)
	}
	if stored.StoppedReason != "" {
		t.Errorf("StoppedReason after restore = %q, want empty", stored.StoppedReason)
	}
}

// TestHandleSessionLoop_PatchDelayClamped verifies that a PATCH lowering the delay below
// the floor on an onCompletion config is clamped up to the floor.
func TestHandleSessionLoop_PatchDelayClamped(t *testing.T) {
	store, h := newLoopStore(t)
	tmpDir := t.TempDir()

	const sid = "test-oncompletion-patch-clamp"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test-server", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	putLoopForTest(t, h, sid, LoopPromptRequest{
		Prompt:       "keep going",
		Enabled:      true,
		Trigger:      session.TriggerOnCompletion,
		DelaySeconds: 30,
	})

	belowFloor := 1
	patchBody, _ := json.Marshal(LoopPromptPatchRequest{DelaySeconds: &belowFloor})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid+"/loop", bytes.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleSessionLoop(w, req, sid, "")
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH loop: Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	stored, err := store.Loop(sid).Get()
	if err != nil {
		t.Fatalf("Get loop after PATCH: %v", err)
	}
	if stored.DelaySeconds != h.loopDelayFloor() {
		t.Errorf("DelaySeconds after PATCH = %d, want clamped to floor %d", stored.DelaySeconds, h.loopDelayFloor())
	}
}

// TestHandleSessionLoop_MakeLoopDraft verifies the "Make loop" frontend flow:
// PUT /api/sessions/{id}/loop with a draft body (enabled:false, prompt:"(pending)")
// on an existing top-level session succeeds and stores the draft config.
func TestHandleSessionLoop_MakeLoopDraft(t *testing.T) {
	store, h := newLoopStore(t)
	tmpDir := t.TempDir()

	if err := store.Create(session.Metadata{
		SessionID:  "test-make-loop-draft",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Draft body — mirrors what handleMakeLoop in app.js sends.
	body, _ := json.Marshal(LoopPromptRequest{
		Prompt:    "(pending)",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   false,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/sessions/test-make-loop-draft/loop", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleSessionLoop(w, req, "test-make-loop-draft", "")

	if w.Code != http.StatusOK {
		t.Errorf("PUT loop draft: Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify the stored loop config reflects the draft state.
	ps := store.Loop("test-make-loop-draft")
	stored, err := ps.Get()
	if err != nil {
		t.Fatalf("Get loop after PUT: %v", err)
	}
	if stored.Enabled {
		t.Errorf("Draft loop should have Enabled=false, got true")
	}
	if stored.Prompt != "(pending)" {
		t.Errorf("Draft loop prompt = %q, want %q", stored.Prompt, "(pending)")
	}
}

// TestHandleSessionLoop_DeleteRemovesConfig verifies the "Make non-loop" frontend flow:
// PUT a draft config, confirm it exists, then DELETE it via HandleSessionLoop,
// assert HTTP 204, and confirm the config is gone from the store.
func TestHandleSessionLoop_DeleteRemovesConfig(t *testing.T) {
	store, h := newLoopStore(t)
	tmpDir := t.TempDir()

	const sid = "test-delete-loop"
	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Step 1: PUT a draft loop config so there is something to delete.
	putBody, _ := json.Marshal(LoopPromptRequest{
		Prompt:    "(pending)",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   false,
	})
	putReq := httptest.NewRequest(http.MethodPut, "/api/sessions/"+sid+"/loop", bytes.NewReader(putBody))
	putReq.Header.Set("Content-Type", "application/json")
	putW := httptest.NewRecorder()
	h.HandleSessionLoop(putW, putReq, sid, "")
	if putW.Code != http.StatusOK {
		t.Fatalf("PUT loop: Status = %d, want 200. Body: %s", putW.Code, putW.Body.String())
	}

	// Confirm the config exists before deleting.
	if _, err := store.Loop(sid).Get(); err != nil {
		t.Fatalf("Get loop before DELETE: %v", err)
	}

	// Step 2: DELETE — mirrors what handleMakeNonLoop in app.js sends.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/sessions/"+sid+"/loop", nil)
	delW := httptest.NewRecorder()
	h.HandleSessionLoop(delW, delReq, sid, "")

	// handleDeleteLoop calls writeNoContent → HTTP 204.
	if delW.Code != http.StatusNoContent {
		t.Errorf("DELETE loop: Status = %d, want %d. Body: %s", delW.Code, http.StatusNoContent, delW.Body.String())
	}

	// Step 3: Confirm the config is gone.
	_, getErr := store.Loop(sid).Get()
	if getErr == nil {
		t.Errorf("Expected error (config gone) after DELETE, got nil")
	}
}

// TestHandleSessionLoop_UnloopRestoreRoundTrip verifies the un-loop → re-loop
// symmetric toggle: DELETE detaches the config (preserving settings), a
// subsequent POST /loop/restore brings it back with the same prompt and enabled
// state, and the saved slot is cleared afterwards.
func TestHandleSessionLoop_UnloopRestoreRoundTrip(t *testing.T) {
	store, h := newLoopStore(t)
	tmpDir := t.TempDir()

	const sid = "test-unloop-restore"
	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Step 1: configure an active loop.
	putLoopForTest(t, h, sid, LoopPromptRequest{
		Prompt:    "keep going",
		Frequency: session.Frequency{Value: 3, Unit: session.FrequencyHours},
		Enabled:   true,
	})

	// Step 2: un-loop (DELETE) — detaches, config gone but saved.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/sessions/"+sid+"/loop", nil)
	delW := httptest.NewRecorder()
	h.HandleSessionLoop(delW, delReq, sid, "")
	if delW.Code != http.StatusNoContent {
		t.Fatalf("DELETE loop: Status = %d, want %d. Body: %s", delW.Code, http.StatusNoContent, delW.Body.String())
	}
	if _, err := store.Loop(sid).Get(); err != session.ErrLoopNotFound {
		t.Fatalf("Get after un-loop = %v, want ErrLoopNotFound", err)
	}
	if _, err := store.Loop(sid).GetSaved(); err != nil {
		t.Fatalf("GetSaved after un-loop = %v, want nil (settings preserved)", err)
	}

	// Step 3: re-loop (POST /loop/restore) — restores config + enabled state.
	restReq := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sid+"/loop/restore", nil)
	restW := httptest.NewRecorder()
	h.HandleSessionLoop(restW, restReq, sid, "restore")
	if restW.Code != http.StatusOK {
		t.Fatalf("POST restore: Status = %d, want %d. Body: %s", restW.Code, http.StatusOK, restW.Body.String())
	}
	var restored session.LoopPrompt
	if err := json.Unmarshal(restW.Body.Bytes(), &restored); err != nil {
		t.Fatalf("decode restore response: %v", err)
	}
	if restored.Prompt != "keep going" {
		t.Errorf("restored.Prompt = %q, want %q", restored.Prompt, "keep going")
	}
	if !restored.Enabled {
		t.Errorf("restored.Enabled = false, want true (enabled state preserved)")
	}
	if restored.IterationCount != 0 {
		t.Errorf("restored.IterationCount = %d, want 0 (counters reset)", restored.IterationCount)
	}
	if restored.StoppedReason != "" {
		t.Errorf("restored.StoppedReason = %q, want empty", restored.StoppedReason)
	}

	// Active config is back.
	if _, err := store.Loop(sid).Get(); err != nil {
		t.Errorf("Get after restore = %v, want nil", err)
	}
	// Saved slot cleared after a successful restore.
	if _, err := store.Loop(sid).GetSaved(); err != session.ErrLoopNotFound {
		t.Errorf("GetSaved after restore = %v, want ErrLoopNotFound (cleared)", err)
	}
}

// TestHandleSessionLoop_Restore_NotFound verifies POST /loop/restore returns 404
// when there are no saved settings, so the frontend can fall back to a draft.
func TestHandleSessionLoop_Restore_NotFound(t *testing.T) {
	store, h := newLoopStore(t)
	tmpDir := t.TempDir()

	const sid = "test-restore-notfound"
	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sid+"/loop/restore", nil)
	w := httptest.NewRecorder()
	h.HandleSessionLoop(w, req, sid, "restore")
	if w.Code != http.StatusNotFound {
		t.Errorf("POST restore with nothing saved: Status = %d, want %d. Body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

// TestHandleSessionLoop_SetClearsSavedSlot verifies that a fresh make-loop (PUT)
// drops any previously-detached settings: the saved slot is cleared so a later
// restore cannot resurrect stale settings over the freshly-defined loop.
func TestHandleSessionLoop_SetClearsSavedSlot(t *testing.T) {
	store, h := newLoopStore(t)
	tmpDir := t.TempDir()

	const sid = "test-set-clears-saved"
	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Simulate a prior un-loop: seed a loop then Detach it to the saved slot.
	if err := store.Loop(sid).Set(&session.LoopPrompt{
		Prompt:    "old detached loop",
		Frequency: session.Frequency{Value: 5, Unit: session.FrequencyHours},
		Enabled:   true,
	}); err != nil {
		t.Fatalf("Set (seed) error = %v", err)
	}
	if err := store.Loop(sid).Detach(); err != nil {
		t.Fatalf("Detach error = %v", err)
	}
	if _, err := store.Loop(sid).GetSaved(); err != nil {
		t.Fatalf("GetSaved after Detach = %v, want nil (settings preserved)", err)
	}

	// Make a fresh loop via PUT.
	putLoopForTest(t, h, sid, LoopPromptRequest{
		Prompt:    "brand new loop",
		Frequency: session.Frequency{Value: 2, Unit: session.FrequencyHours},
		Enabled:   true,
	})

	// The stale saved slot must be cleared so a later restore cannot resurrect it.
	if _, err := store.Loop(sid).GetSaved(); err != session.ErrLoopNotFound {
		t.Errorf("GetSaved after PUT = %v, want ErrLoopNotFound (stale slot cleared)", err)
	}
}

// TestHandleSessionLoop_RestoreRejectedWhenLoopActive verifies that POST
// /loop/restore returns 409 Conflict when an active loop.json already exists
// alongside a saved loop.saved.json (e.g. a previous ClearSaved silently
// failed), and that the active loop's accumulated counters/anchors are NOT
// modified by the rejected restore. This guards against LoopStore.Set()'s
// update path silently preserving stale IterationCount/CreatedAt/FirstRunAt
// from the active config and clobbering it with saved settings.
func TestHandleSessionLoop_RestoreRejectedWhenLoopActive(t *testing.T) {
	store, h := newLoopStore(t)
	tmpDir := t.TempDir()

	const sid = "test-restore-rejected-active"
	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Step 1: seed and detach an initial loop so loop.saved.json exists.
	if err := store.Loop(sid).Set(&session.LoopPrompt{
		Prompt:    "old saved settings",
		Frequency: session.Frequency{Value: 5, Unit: session.FrequencyHours},
		Enabled:   true,
	}); err != nil {
		t.Fatalf("Set (saved seed) error = %v", err)
	}
	if err := store.Loop(sid).Detach(); err != nil {
		t.Fatalf("Detach error = %v", err)
	}
	if _, err := store.Loop(sid).GetSaved(); err != nil {
		t.Fatalf("GetSaved after Detach = %v, want nil", err)
	}

	// Step 2: install a fresh active loop directly via the store (bypassing
	// the handler on purpose so the saved slot is NOT cleared — this simulates
	// the "ClearSaved previously failed" state the reviewer is concerned about).
	if err := store.Loop(sid).Set(&session.LoopPrompt{
		Prompt:    "brand new active loop",
		Frequency: session.Frequency{Value: 2, Unit: session.FrequencyHours},
		Enabled:   true,
	}); err != nil {
		t.Fatalf("Set (active) error = %v", err)
	}
	// Bump the counters on the active loop so we can prove they survive the
	// rejected restore untouched.
	if err := store.Loop(sid).RecordSent(); err != nil {
		t.Fatalf("RecordSent error = %v", err)
	}
	if err := store.Loop(sid).RecordSent(); err != nil {
		t.Fatalf("RecordSent error = %v", err)
	}

	before, err := store.Loop(sid).Get()
	if err != nil {
		t.Fatalf("Get (before restore) error = %v", err)
	}
	if before.IterationCount != 2 {
		t.Fatalf("before.IterationCount = %d, want 2 (setup precondition)", before.IterationCount)
	}
	if before.FirstRunAt == nil || before.LastSentAt == nil {
		t.Fatalf("before anchors nil, want set (setup precondition)")
	}

	// Precondition: both files must exist for this test to be meaningful.
	if _, err := store.Loop(sid).GetSaved(); err != nil {
		t.Fatalf("GetSaved (before restore) = %v, want nil (both files present)", err)
	}

	// Step 3: POST /loop/restore — must be rejected with 409 Conflict.
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sid+"/loop/restore", nil)
	w := httptest.NewRecorder()
	h.HandleSessionLoop(w, req, sid, "restore")
	if w.Code != http.StatusConflict {
		t.Fatalf("POST restore with active loop: Status = %d, want %d. Body: %s",
			w.Code, http.StatusConflict, w.Body.String())
	}

	// Step 4: the active loop must be UNCHANGED — prompt, counters, and
	// anchors preserved (no silent clobber by Set()'s update path).
	after, err := store.Loop(sid).Get()
	if err != nil {
		t.Fatalf("Get (after restore) error = %v", err)
	}
	if after.Prompt != "brand new active loop" {
		t.Errorf("after.Prompt = %q, want %q (active loop clobbered by saved settings)",
			after.Prompt, "brand new active loop")
	}
	if after.IterationCount != before.IterationCount {
		t.Errorf("after.IterationCount = %d, want %d (counter reset by rejected restore)",
			after.IterationCount, before.IterationCount)
	}
	if after.FirstRunAt == nil || !after.FirstRunAt.Equal(*before.FirstRunAt) {
		t.Errorf("after.FirstRunAt = %v, want %v (anchor changed by rejected restore)",
			after.FirstRunAt, before.FirstRunAt)
	}
	if after.LastSentAt == nil || !after.LastSentAt.Equal(*before.LastSentAt) {
		t.Errorf("after.LastSentAt = %v, want %v (anchor changed by rejected restore)",
			after.LastSentAt, before.LastSentAt)
	}

	// The saved slot must still be there — a rejected restore does not clear it,
	// so the operator can resolve the conflict (DELETE + restore) without loss.
	if _, err := store.Loop(sid).GetSaved(); err != nil {
		t.Errorf("GetSaved (after rejected restore) = %v, want nil (saved slot cleared by rejected restore)", err)
	}
}

// TestHandleSetLoop_PendingPlaceholderDoesNotBecomeTitle verifies that when a loop
// prompt is set with a "(pending)" placeholder body plus a prompt_name, the generated title
// is derived from the resolved prompt body rather than the placeholder.
func TestHandleSetLoop_PendingPlaceholderDoesNotBecomeTitle(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	tmpDir := t.TempDir()
	const sid = "test-pending-placeholder-title"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// conversation.BackgroundSession with a promptResolver that returns a recognisable body.
	bs := conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{
		SessionID:  sid,
		WorkingDir: tmpDir,
		Store:      store,
		PromptResolver: func(name, dir string) (string, error) {
			return "The actual resolved body for " + name, nil
		},
	})

	sm := conversation.NewSessionManager("", "", false, nil)
	sm.AddSessionForTest(bs)

	h := New(Deps{Store: store, SessionManager: sm})

	putLoopForTest(t, h, sid, LoopPromptRequest{
		Prompt:     "(pending)",
		PromptName: "CGW: latest questions",
		Frequency:  session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:    true,
	})

	meta, err := store.GetMetadata(sid)
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if strings.Contains(strings.ToLower(meta.Name), "pending") {
		t.Errorf("title must not contain 'pending' when prompt_name is set; got %q", meta.Name)
	}
	if !strings.Contains(strings.ToLower(meta.Name), "actual") && !strings.Contains(strings.ToLower(meta.Name), "resolved") {
		t.Errorf("title should be derived from the resolved prompt body; got %q", meta.Name)
	}
}

// TestHandleSessionLoop_OnTasksRoundTrip verifies that the onTasks trigger and its
// condition/condition_preset/cooldown_seconds fields round-trip through PUT and PATCH,
// and that a frequency is not required for the onTasks trigger.
func TestHandleSessionLoop_OnTasksRoundTrip(t *testing.T) {
	store, h := newLoopStore(t)
	tmpDir := t.TempDir()

	const sid = "test-ontasks-roundtrip"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test-server", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	cond := `Tasks.Open > Prev.Open`
	preset := "any-open-increase"
	cooldown := 120

	got := putLoopForTest(t, h, sid, LoopPromptRequest{
		Prompt:          "review beads changes",
		Enabled:         true,
		Trigger:         session.TriggerOnTasks,
		Condition:       &cond,
		ConditionPreset: &preset,
		CooldownSeconds: &cooldown,
	})

	if got.Trigger != session.TriggerOnTasks {
		t.Errorf("Trigger = %q, want %q", got.Trigger, session.TriggerOnTasks)
	}
	if got.Condition != cond {
		t.Errorf("Condition = %q, want %q", got.Condition, cond)
	}
	if got.ConditionPreset != preset {
		t.Errorf("ConditionPreset = %q, want %q", got.ConditionPreset, preset)
	}
	if got.CooldownSeconds != cooldown {
		t.Errorf("CooldownSeconds = %d, want %d", got.CooldownSeconds, cooldown)
	}

	// PATCH: change only the condition; other onTasks fields must be preserved.
	newCond := `size(Changes.Reopened) > 0`
	patchBody, _ := json.Marshal(LoopPromptPatchRequest{Condition: &newCond})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid+"/loop", bytes.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleSessionLoop(w, req, sid, "")
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH loop: Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	stored, err := store.Loop(sid).Get()
	if err != nil {
		t.Fatalf("Get loop after PATCH: %v", err)
	}
	if stored.Condition != newCond {
		t.Errorf("Condition after PATCH = %q, want %q", stored.Condition, newCond)
	}
	if stored.ConditionPreset != preset {
		t.Errorf("ConditionPreset after PATCH = %q, want preserved %q", stored.ConditionPreset, preset)
	}
	if stored.CooldownSeconds != cooldown {
		t.Errorf("CooldownSeconds after PATCH = %d, want preserved %d", stored.CooldownSeconds, cooldown)
	}
	if !stored.IsOnTasks() {
		t.Errorf("Trigger after PATCH = %q, want onTasks (must not be clobbered)", stored.Trigger)
	}
}

// TestHandleSessionLoop_PatchInvalidConditionRejected verifies that an invalid CEL
// condition is rejected with a 400 Bad Request when session.ConditionValidator is wired.
// The real wiring (config.ValidateCondition) is owned by a sibling worker, so this test
// injects a fake rejecting validator to exercise the same seam in isolation.
func TestHandleSessionLoop_PatchInvalidConditionRejected(t *testing.T) {
	old := session.ConditionValidator
	session.ConditionValidator = func(expr string) error {
		return errors.New("simulated invalid CEL")
	}
	defer func() { session.ConditionValidator = old }()

	store, h := newLoopStore(t)
	tmpDir := t.TempDir()

	const sid = "test-ontasks-invalid-condition"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test-server", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	putLoopForTest(t, h, sid, LoopPromptRequest{
		Prompt:  "review beads changes",
		Enabled: true,
		Trigger: session.TriggerOnTasks,
	})

	badCond := "not valid cel("
	patchBody, _ := json.Marshal(LoopPromptPatchRequest{Condition: &badCond})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid+"/loop", bytes.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleSessionLoop(w, req, sid, "")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("PATCH invalid condition: Status = %d, want %d. Body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if !strings.Contains(env.Error.Message, "invalid condition") {
		t.Errorf("error.message = %q, want it to mention 'invalid condition'", env.Error.Message)
	}

	// Verify the rejected condition was not persisted.
	stored, err := store.Loop(sid).Get()
	if err != nil {
		t.Fatalf("Get loop after rejected PATCH: %v", err)
	}
	if stored.Condition == badCond {
		t.Errorf("rejected condition must not be persisted, got %q", stored.Condition)
	}
}

// TestHandleSessionLoop_PUT_ArgumentsPersisted verifies that Arguments supplied in a
// PUT request are stored in the loop config and returned by Get.
func TestHandleSessionLoop_PUT_ArgumentsPersisted(t *testing.T) {
	store, h := newLoopStore(t)
	tmpDir := t.TempDir()
	sid := "put-args-session"
	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  "test",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create session failed: %v", err)
	}

	args := map[string]string{"ISSUE_ID": "mitto-42", "ENV": "staging"}
	got := putLoopForTest(t, h, sid, LoopPromptRequest{
		PromptName: "check-status",
		Arguments:  args,
		Frequency:  session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:    true,
	})

	if len(got.Arguments) != len(args) {
		t.Fatalf("Arguments len = %d, want %d", len(got.Arguments), len(args))
	}
	for k, v := range args {
		if got.Arguments[k] != v {
			t.Errorf("Arguments[%q] = %q, want %q", k, got.Arguments[k], v)
		}
	}

	// Verify round-trip via the store directly.
	stored, err := store.Loop(sid).Get()
	if err != nil {
		t.Fatalf("Loop().Get() error = %v", err)
	}
	for k, v := range args {
		if stored.Arguments[k] != v {
			t.Errorf("Stored Arguments[%q] = %q, want %q", k, stored.Arguments[k], v)
		}
	}
}

// TestHandleSessionLoop_PATCH_ArgumentsPersisted verifies that Arguments supplied in a
// PATCH request replace the existing arguments and are returned by Get.
func TestHandleSessionLoop_PATCH_ArgumentsPersisted(t *testing.T) {
	store, h := newLoopStore(t)
	tmpDir := t.TempDir()
	sid := "patch-args-session"
	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  "test",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create session failed: %v", err)
	}

	// Seed via PUT with initial arguments.
	putLoopForTest(t, h, sid, LoopPromptRequest{
		PromptName: "check-status",
		Arguments:  map[string]string{"KEY": "initial"},
		Frequency:  session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:    true,
	})

	// PATCH with new arguments.
	newArgs := map[string]string{"KEY": "patched", "EXTRA": "yes"}
	body, _ := json.Marshal(LoopPromptPatchRequest{
		Arguments: &newArgs,
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid+"/loop", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleSessionLoop(w, req, sid, "")
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH loop status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got session.LoopPrompt
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode PATCH response: %v", err)
	}
	if got.Arguments["KEY"] != "patched" {
		t.Errorf("Arguments[KEY] = %q, want %q", got.Arguments["KEY"], "patched")
	}
	if got.Arguments["EXTRA"] != "yes" {
		t.Errorf("Arguments[EXTRA] = %q, want %q", got.Arguments["EXTRA"], "yes")
	}

	// Nil arguments in PATCH must leave stored map unchanged.
	body2, _ := json.Marshal(LoopPromptPatchRequest{}) // nil Arguments
	req2 := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid+"/loop", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	h.HandleSessionLoop(w2, req2, sid, "")
	if w2.Code != http.StatusOK {
		t.Fatalf("PATCH (nil args) status = %d. Body: %s", w2.Code, w2.Body.String())
	}
	stored, _ := store.Loop(sid).Get()
	if stored.Arguments["KEY"] != "patched" {
		t.Errorf("nil PATCH should not clear Arguments; KEY = %q", stored.Arguments["KEY"])
	}
}

// newLoopStoreWithPrompts is a variant of newLoopStore that also wires a
// GetWorkspacePromptsAll stub so the mitto-le4.1 frontmatter merge path can be
// exercised. Passing a nil returnPrompts function leaves the dep unset (nil),
// so the merge is skipped entirely.
func newLoopStoreWithPrompts(t *testing.T, returnPrompts func(workingDir string) []configPkg.WebPrompt) (*session.Store, *Handlers) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	deps := Deps{Store: store}
	if returnPrompts != nil {
		deps.GetWorkspacePromptsAll = returnPrompts
	}
	h := New(deps)
	return store, h
}

// TestHandleSessionLoop_PUT_MergesPromptDefaults_Baseline (T1) verifies that
// a PUT with only PromptName + Enabled fills empty fields from the resolved
// prompt's loop: frontmatter block.
func TestHandleSessionLoop_PUT_MergesPromptDefaults_Baseline(t *testing.T) {
	tr, fa := true, false
	promptStub := func(string) []configPkg.WebPrompt {
		return []configPkg.WebPrompt{{
			Name: "test-prompt",
			Loop: &configPkg.PromptLoop{
				Trigger:            "onTasks",
				FreshContext:       &tr,
				RunOnStart:         &tr,
				CoalesceDuringBusy: &fa,
			},
		}}
	}

	store, h := newLoopStoreWithPrompts(t, promptStub)
	sid := "put-merge-baseline"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "t", WorkingDir: t.TempDir()}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := putLoopForTest(t, h, sid, LoopPromptRequest{
		PromptName: "test-prompt",
		Enabled:    true,
	})

	if got.Trigger != session.TriggerOnTasks {
		t.Errorf("Trigger = %q, want %q", got.Trigger, session.TriggerOnTasks)
	}
	if !got.FreshContext {
		t.Errorf("FreshContext = false, want true (filled from frontmatter)")
	}
	if got.RunOnStart == nil || !*got.RunOnStart {
		t.Errorf("RunOnStart = %v, want non-nil *true", got.RunOnStart)
	}
	if got.CoalesceDuringBusy == nil || *got.CoalesceDuringBusy {
		t.Errorf("CoalesceDuringBusy = %v, want non-nil *false", got.CoalesceDuringBusy)
	}
}

// TestHandleSessionLoop_PUT_MergesPromptDefaults_ExplicitWins (T2) verifies
// that an explicit non-zero request field wins over the frontmatter default.
// FreshContext is intentionally omitted here — LoopPrompt.FreshContext is a
// plain bool so "explicit false" is indistinguishable from "unset"; the
// value-type quirk is documented in the helper's comment.
func TestHandleSessionLoop_PUT_MergesPromptDefaults_ExplicitWins(t *testing.T) {
	tr := true
	promptStub := func(string) []configPkg.WebPrompt {
		return []configPkg.WebPrompt{{
			Name: "test-prompt",
			Loop: &configPkg.PromptLoop{
				Trigger:       "onTasks",
				MaxIterations: 42,
				RunOnStart:    &tr,
			},
		}}
	}

	store, h := newLoopStoreWithPrompts(t, promptStub)
	sid := "put-merge-explicit"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "t", WorkingDir: t.TempDir()}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	fa := false
	got := putLoopForTest(t, h, sid, LoopPromptRequest{
		PromptName:    "test-prompt",
		Enabled:       true,
		Trigger:       session.TriggerOnCompletion, // explicit, must win
		DelaySeconds:  30,
		MaxIterations: 7,   // explicit, must win over frontmatter's 42
		RunOnStart:    &fa, // explicit *false must win over frontmatter *true
	})

	if got.Trigger != session.TriggerOnCompletion {
		t.Errorf("Trigger = %q, want %q (explicit request wins)", got.Trigger, session.TriggerOnCompletion)
	}
	if got.MaxIterations != 7 {
		t.Errorf("MaxIterations = %d, want 7 (explicit request wins)", got.MaxIterations)
	}
	if got.RunOnStart == nil || *got.RunOnStart {
		t.Errorf("RunOnStart = %v, want non-nil *false (explicit request wins)", got.RunOnStart)
	}
}

// TestHandleSessionLoop_PUT_MergesPromptDefaults_OptOut (T3) verifies that
// LoopApplyPromptDefaults: &false disables the merge entirely, so no
// frontmatter field leaks into the persisted loop.
func TestHandleSessionLoop_PUT_MergesPromptDefaults_OptOut(t *testing.T) {
	tr := true
	promptStub := func(string) []configPkg.WebPrompt {
		return []configPkg.WebPrompt{{
			Name: "test-prompt",
			Loop: &configPkg.PromptLoop{
				Trigger:       "onTasks",
				FreshContext:  &tr,
				RunOnStart:    &tr,
				MaxIterations: 42,
			},
		}}
	}

	store, h := newLoopStoreWithPrompts(t, promptStub)
	sid := "put-merge-optout"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "t", WorkingDir: t.TempDir()}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	fa := false
	got := putLoopForTest(t, h, sid, LoopPromptRequest{
		PromptName:              "test-prompt",
		Enabled:                 true,
		Trigger:                 session.TriggerOnCompletion, // required non-schedule so no Frequency needed
		DelaySeconds:            30,
		LoopApplyPromptDefaults: &fa,
	})

	if got.Trigger != session.TriggerOnCompletion {
		t.Errorf("Trigger = %q, want %q", got.Trigger, session.TriggerOnCompletion)
	}
	if got.FreshContext {
		t.Errorf("FreshContext = true, want false (merge opt-out honored)")
	}
	if got.RunOnStart != nil {
		t.Errorf("RunOnStart = %v, want nil (merge opt-out honored)", got.RunOnStart)
	}
	if got.MaxIterations != 0 {
		t.Errorf("MaxIterations = %d, want 0 (merge opt-out honored)", got.MaxIterations)
	}
}

// TestHandleSessionLoop_PUT_MergesPromptDefaults_NoPromptName (T4) verifies
// that a free-text prompt (no PromptName) does NOT trigger any prompt lookup;
// the stub panics if invoked.
func TestHandleSessionLoop_PUT_MergesPromptDefaults_NoPromptName(t *testing.T) {
	promptStub := func(string) []configPkg.WebPrompt {
		t.Fatalf("GetWorkspacePromptsAll must not be called when PromptName is empty")
		return nil
	}

	store, h := newLoopStoreWithPrompts(t, promptStub)
	sid := "put-merge-nopromptname"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "t", WorkingDir: t.TempDir()}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := putLoopForTest(t, h, sid, LoopPromptRequest{
		Prompt:    "free-text body",
		Enabled:   true,
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
	})

	if got.Prompt != "free-text body" {
		t.Errorf("Prompt = %q, want %q", got.Prompt, "free-text body")
	}
	if got.Trigger != "" {
		t.Errorf("Trigger = %q, want empty (no merge)", got.Trigger)
	}
}

// TestHandleSessionLoop_PUT_MergesPromptDefaults_UnknownPromptName (T5)
// verifies that a PromptName that does not resolve to any prompt is a
// graceful no-op — the request persists as-is (matches the MCP path when
// .Loop == nil).
func TestHandleSessionLoop_PUT_MergesPromptDefaults_UnknownPromptName(t *testing.T) {
	promptStub := func(string) []configPkg.WebPrompt {
		return nil // empty prompt list
	}

	store, h := newLoopStoreWithPrompts(t, promptStub)
	sid := "put-merge-unknown"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "t", WorkingDir: t.TempDir()}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := putLoopForTest(t, h, sid, LoopPromptRequest{
		PromptName: "does-not-exist",
		Enabled:    true,
		Frequency:  session.Frequency{Value: 1, Unit: session.FrequencyHours},
	})

	if got.PromptName != "does-not-exist" {
		t.Errorf("PromptName = %q, want %q", got.PromptName, "does-not-exist")
	}
	if got.Trigger != "" {
		t.Errorf("Trigger = %q, want empty (unknown prompt is graceful fallback)", got.Trigger)
	}
	if got.FreshContext {
		t.Errorf("FreshContext = true, want false (unknown prompt is graceful fallback)")
	}
}

// TestHandleSessionLoop_Restore_StoppedSavedConfig_ClearsReasonAndResetsCounters
// verifies the D2 two-step restore for mitto-uun: when the saved config was
// preserved from an auto-stopped loop (Enabled=false + StoppedReason set +
// IterationCount>0), the restore path (a) clears the stopped-reason so Set()
// writes a resumable config, (b) calls ResetCounters() as a separate step so
// IterationCount / FirstRunAt / LastSentAt land at zero, and (c) preserves the
// saved Enabled value (a stopped loop restores as paused; the user must
// explicitly re-enable via Update). This decouples the counter-reset intent
// from Set()'s field-preservation semantics.
func TestHandleSessionLoop_Restore_StoppedSavedConfig_ClearsReasonAndResetsCounters(t *testing.T) {
	store, h := newLoopStore(t)
	tmpDir := t.TempDir()

	const sid = "test-restore-stopped-saved"
	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Step 1: configure an active loop, record several deliveries, then
	// auto-stop it — mirrors the shape of a loop that hit its cap.
	loop := store.Loop(sid)
	if err := loop.Set(&session.LoopPrompt{
		Prompt:        "keep going",
		Frequency:     session.Frequency{Value: 3, Unit: session.FrequencyHours},
		Enabled:       true,
		MaxIterations: 3,
	}); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := loop.RecordSent(); err != nil {
			t.Fatalf("RecordSent()[%d] error = %v", i, err)
		}
	}
	if err := loop.MarkStopped(session.StoppedReasonMaxIterations); err != nil {
		t.Fatalf("MarkStopped() error = %v", err)
	}

	// Step 2: un-loop (detach) — the stopped state is preserved in the saved
	// slot, and loop.json is removed.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/sessions/"+sid+"/loop", nil)
	delW := httptest.NewRecorder()
	h.HandleSessionLoop(delW, delReq, sid, "")
	if delW.Code != http.StatusNoContent {
		t.Fatalf("DELETE loop: Status = %d, want %d. Body: %s", delW.Code, http.StatusNoContent, delW.Body.String())
	}
	savedBeforeRestore, err := loop.GetSaved()
	if err != nil {
		t.Fatalf("GetSaved() before restore = %v, want nil", err)
	}
	// Sanity: the saved slot really does carry the auto-stopped state — that
	// is the input this test is exercising.
	if savedBeforeRestore.Enabled {
		t.Fatalf("saved.Enabled = true before restore; test setup is wrong (expected auto-stopped shape)")
	}
	if savedBeforeRestore.StoppedReason != session.StoppedReasonMaxIterations {
		t.Fatalf("saved.StoppedReason = %q, want %q (test setup is wrong)",
			savedBeforeRestore.StoppedReason, session.StoppedReasonMaxIterations)
	}
	if savedBeforeRestore.IterationCount != 3 {
		t.Fatalf("saved.IterationCount = %d, want 3 (test setup is wrong)",
			savedBeforeRestore.IterationCount)
	}

	// Step 3: POST /loop/restore — must clear StoppedReason, reset counters,
	// and preserve the saved Enabled state (paused stays paused).
	restReq := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sid+"/loop/restore", nil)
	restW := httptest.NewRecorder()
	h.HandleSessionLoop(restW, restReq, sid, "restore")
	if restW.Code != http.StatusOK {
		t.Fatalf("POST restore: Status = %d, want %d. Body: %s", restW.Code, http.StatusOK, restW.Body.String())
	}
	var restored session.LoopPrompt
	if err := json.Unmarshal(restW.Body.Bytes(), &restored); err != nil {
		t.Fatalf("decode restore response: %v", err)
	}

	// D2 acceptance criteria:
	if restored.StoppedReason != "" {
		t.Errorf("restored.StoppedReason = %q, want empty (D2 pre-Set clear)", restored.StoppedReason)
	}
	if restored.StoppedAt != nil {
		t.Errorf("restored.StoppedAt = %v, want nil (D2 pre-Set clear)", restored.StoppedAt)
	}
	if restored.IterationCount != 0 {
		t.Errorf("restored.IterationCount = %d, want 0 (D2 explicit ResetCounters)",
			restored.IterationCount)
	}
	if restored.FirstRunAt != nil {
		t.Errorf("restored.FirstRunAt = %v, want nil (D2 explicit ResetCounters)",
			restored.FirstRunAt)
	}
	if restored.LastSentAt != nil {
		t.Errorf("restored.LastSentAt = %v, want nil (D2 explicit ResetCounters)",
			restored.LastSentAt)
	}
	// Enabled must reflect the saved value — a stopped loop restores as paused
	// (the D2 comment: "paused draft comes back paused"). The user re-enables
	// via a subsequent Update.
	if restored.Enabled {
		t.Errorf("restored.Enabled = true, want false (auto-stopped saved config must restore as paused)")
	}
	// Mutable config fields must survive the round-trip.
	if restored.Prompt != "keep going" {
		t.Errorf("restored.Prompt = %q, want %q", restored.Prompt, "keep going")
	}
}
