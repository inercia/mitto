package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// newPeriodicStore creates a temp store and returns it together with a Handlers
// wired with only the Store dependency. Broadcast/bootstrap deps are left nil
// (no-ops), which is sufficient for the periodic REST handler tests.
func newPeriodicStore(t *testing.T) (*session.Store, *Handlers) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	h := New(Deps{Store: store})
	return store, h
}

// putPeriodicForTest is a helper that PUTs a periodic config via the REST handler and
// returns the decoded response. It fails the test on a non-200 status.
func putPeriodicForTest(t *testing.T, h *Handlers, sid string, body PeriodicPromptRequest) session.PeriodicPrompt {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/sessions/"+sid+"/periodic", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleSessionPeriodic(w, req, sid, "")
	if w.Code != http.StatusOK {
		t.Fatalf("PUT periodic: Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var got session.PeriodicPrompt
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode PUT response: %v", err)
	}
	return got
}

func TestHandleSessionPeriodic_ChildRejected(t *testing.T) {
	store, h := newPeriodicStore(t)
	tmpDir := t.TempDir()

	if err := store.Create(session.Metadata{
		SessionID:  "test-parent-periodic",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create parent failed: %v", err)
	}

	if err := store.Create(session.Metadata{
		SessionID:       "test-child-periodic",
		ACPServer:       "test-server",
		WorkingDir:      tmpDir,
		ParentSessionID: "test-parent-periodic",
	}); err != nil {
		t.Fatalf("Create child failed: %v", err)
	}

	// PUT periodic on child — should be rejected
	body, _ := json.Marshal(PeriodicPromptRequest{
		Prompt:    "check updates",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   true,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/sessions/test-child-periodic/periodic", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleSessionPeriodic(w, req, "test-child-periodic", "")

	if w.Code != http.StatusBadRequest {
		t.Errorf("PUT periodic on child: Status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	// GET should still work (not rejected as 400)
	req2 := httptest.NewRequest(http.MethodGet, "/api/sessions/test-child-periodic/periodic", nil)
	w2 := httptest.NewRecorder()

	h.HandleSessionPeriodic(w2, req2, "test-child-periodic", "")

	if w2.Code == http.StatusBadRequest {
		t.Error("GET periodic on child should NOT be rejected with 400")
	}
}

// TestHandleSessionPeriodic_TopLevelAllowed tests that setting periodic on a top-level session works.
func TestHandleSessionPeriodic_TopLevelAllowed(t *testing.T) {
	store, h := newPeriodicStore(t)
	tmpDir := t.TempDir()

	if err := store.Create(session.Metadata{
		SessionID:  "test-toplevel-periodic",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	body, _ := json.Marshal(PeriodicPromptRequest{
		Prompt:    "check updates",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   true,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/sessions/test-toplevel-periodic/periodic", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleSessionPeriodic(w, req, "test-toplevel-periodic", "")

	if w.Code != http.StatusOK {
		t.Errorf("PUT periodic on top-level: Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

// TestHandleSessionPeriodic_OnCompletionRoundTrip verifies that the on-completion trigger,
// completion delay, and max-duration fields round-trip through the PUT handler. A frequency
// is not required for the onCompletion trigger.
func TestHandleSessionPeriodic_OnCompletionRoundTrip(t *testing.T) {
	store, h := newPeriodicStore(t)
	tmpDir := t.TempDir()

	const sid = "test-oncompletion-roundtrip"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test-server", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got := putPeriodicForTest(t, h, sid, PeriodicPromptRequest{
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

// TestHandleSessionPeriodic_OnCompletionDelayClampedOnPut verifies that a delay below the
// global floor is clamped up to the floor on write (PUT). With no periodic runner configured,
// the floor is the package default.
func TestHandleSessionPeriodic_OnCompletionDelayClampedOnPut(t *testing.T) {
	store, h := newPeriodicStore(t)
	tmpDir := t.TempDir()

	const sid = "test-oncompletion-clamp-put"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test-server", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got := putPeriodicForTest(t, h, sid, PeriodicPromptRequest{
		Prompt:       "keep going",
		Enabled:      true,
		Trigger:      session.TriggerOnCompletion,
		DelaySeconds: 1, // below the default floor (5)
	})

	if got.DelaySeconds != h.periodicDelayFloor() {
		t.Errorf("DelaySeconds = %d, want clamped to floor %d", got.DelaySeconds, h.periodicDelayFloor())
	}
}

// TestHandleSessionPeriodic_PatchPartialPreservesOnCompletionFields verifies that a partial
// PATCH updating only max_duration_seconds does not clobber the trigger or delay.
func TestHandleSessionPeriodic_PatchPartialPreservesOnCompletionFields(t *testing.T) {
	store, h := newPeriodicStore(t)
	tmpDir := t.TempDir()

	const sid = "test-oncompletion-patch"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test-server", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Seed an onCompletion config with a delay and no duration cap.
	putPeriodicForTest(t, h, sid, PeriodicPromptRequest{
		Prompt:       "keep going",
		Enabled:      true,
		Trigger:      session.TriggerOnCompletion,
		DelaySeconds: 30,
	})

	// PATCH only max_duration_seconds.
	maxDur := 7200
	patchBody, _ := json.Marshal(PeriodicPromptPatchRequest{MaxDurationSeconds: &maxDur})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid+"/periodic", bytes.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleSessionPeriodic(w, req, sid, "")
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH periodic: Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	stored, err := store.Periodic(sid).Get()
	if err != nil {
		t.Fatalf("Get periodic after PATCH: %v", err)
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

// TestHandleSessionPeriodic_PatchResetCounters verifies that PATCHing with
// reset_counters=true (used when restoring a loop that hit its cap) re-enables the
// loop and resets IterationCount=0 and FirstRunAt=nil (elapsed time = 0).
func TestHandleSessionPeriodic_PatchResetCounters(t *testing.T) {
	store, h := newPeriodicStore(t)
	tmpDir := t.TempDir()

	const sid = "test-reset-counters-patch"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test-server", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Seed an onCompletion config with a duration cap.
	putPeriodicForTest(t, h, sid, PeriodicPromptRequest{
		Prompt:             "keep going",
		Enabled:            true,
		Trigger:            session.TriggerOnCompletion,
		DelaySeconds:       30,
		MaxDurationSeconds: 60,
	})

	// Simulate two completed runs, then auto-stop on the duration cap.
	ps := store.Periodic(sid)
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
	patchBody, _ := json.Marshal(PeriodicPromptPatchRequest{Enabled: &enabled, ResetCounters: &reset})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid+"/periodic", bytes.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleSessionPeriodic(w, req, sid, "")
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH periodic: Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	stored, err := ps.Get()
	if err != nil {
		t.Fatalf("Get periodic after PATCH: %v", err)
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

// TestHandleSessionPeriodic_PatchDelayClamped verifies that a PATCH lowering the delay below
// the floor on an onCompletion config is clamped up to the floor.
func TestHandleSessionPeriodic_PatchDelayClamped(t *testing.T) {
	store, h := newPeriodicStore(t)
	tmpDir := t.TempDir()

	const sid = "test-oncompletion-patch-clamp"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test-server", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	putPeriodicForTest(t, h, sid, PeriodicPromptRequest{
		Prompt:       "keep going",
		Enabled:      true,
		Trigger:      session.TriggerOnCompletion,
		DelaySeconds: 30,
	})

	belowFloor := 1
	patchBody, _ := json.Marshal(PeriodicPromptPatchRequest{DelaySeconds: &belowFloor})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid+"/periodic", bytes.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleSessionPeriodic(w, req, sid, "")
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH periodic: Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	stored, err := store.Periodic(sid).Get()
	if err != nil {
		t.Fatalf("Get periodic after PATCH: %v", err)
	}
	if stored.DelaySeconds != h.periodicDelayFloor() {
		t.Errorf("DelaySeconds after PATCH = %d, want clamped to floor %d", stored.DelaySeconds, h.periodicDelayFloor())
	}
}

// TestHandleSessionPeriodic_MakePeriodicDraft verifies the "Make periodic" frontend flow:
// PUT /api/sessions/{id}/periodic with a draft body (enabled:false, prompt:"(pending)")
// on an existing top-level session succeeds and stores the draft config.
func TestHandleSessionPeriodic_MakePeriodicDraft(t *testing.T) {
	store, h := newPeriodicStore(t)
	tmpDir := t.TempDir()

	if err := store.Create(session.Metadata{
		SessionID:  "test-make-periodic-draft",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Draft body — mirrors what handleMakePeriodic in app.js sends.
	body, _ := json.Marshal(PeriodicPromptRequest{
		Prompt:    "(pending)",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   false,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/sessions/test-make-periodic-draft/periodic", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleSessionPeriodic(w, req, "test-make-periodic-draft", "")

	if w.Code != http.StatusOK {
		t.Errorf("PUT periodic draft: Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify the stored periodic config reflects the draft state.
	ps := store.Periodic("test-make-periodic-draft")
	stored, err := ps.Get()
	if err != nil {
		t.Fatalf("Get periodic after PUT: %v", err)
	}
	if stored.Enabled {
		t.Errorf("Draft periodic should have Enabled=false, got true")
	}
	if stored.Prompt != "(pending)" {
		t.Errorf("Draft periodic prompt = %q, want %q", stored.Prompt, "(pending)")
	}
}

// TestHandleSessionPeriodic_DeleteRemovesConfig verifies the "Make non-periodic" frontend flow:
// PUT a draft config, confirm it exists, then DELETE it via HandleSessionPeriodic,
// assert HTTP 204, and confirm the config is gone from the store.
func TestHandleSessionPeriodic_DeleteRemovesConfig(t *testing.T) {
	store, h := newPeriodicStore(t)
	tmpDir := t.TempDir()

	const sid = "test-delete-periodic"
	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Step 1: PUT a draft periodic config so there is something to delete.
	putBody, _ := json.Marshal(PeriodicPromptRequest{
		Prompt:    "(pending)",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   false,
	})
	putReq := httptest.NewRequest(http.MethodPut, "/api/sessions/"+sid+"/periodic", bytes.NewReader(putBody))
	putReq.Header.Set("Content-Type", "application/json")
	putW := httptest.NewRecorder()
	h.HandleSessionPeriodic(putW, putReq, sid, "")
	if putW.Code != http.StatusOK {
		t.Fatalf("PUT periodic: Status = %d, want 200. Body: %s", putW.Code, putW.Body.String())
	}

	// Confirm the config exists before deleting.
	if _, err := store.Periodic(sid).Get(); err != nil {
		t.Fatalf("Get periodic before DELETE: %v", err)
	}

	// Step 2: DELETE — mirrors what handleMakeNonPeriodic in app.js sends.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/sessions/"+sid+"/periodic", nil)
	delW := httptest.NewRecorder()
	h.HandleSessionPeriodic(delW, delReq, sid, "")

	// handleDeletePeriodic calls writeNoContent → HTTP 204.
	if delW.Code != http.StatusNoContent {
		t.Errorf("DELETE periodic: Status = %d, want %d. Body: %s", delW.Code, http.StatusNoContent, delW.Body.String())
	}

	// Step 3: Confirm the config is gone.
	_, getErr := store.Periodic(sid).Get()
	if getErr == nil {
		t.Errorf("Expected error (config gone) after DELETE, got nil")
	}
}

// TestHandleSetPeriodic_PendingPlaceholderDoesNotBecomeTitle verifies that when a periodic
// prompt is set with a "(pending)" placeholder body plus a prompt_name, the generated title
// is derived from the resolved prompt body rather than the placeholder.
func TestHandleSetPeriodic_PendingPlaceholderDoesNotBecomeTitle(t *testing.T) {
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

	putPeriodicForTest(t, h, sid, PeriodicPromptRequest{
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
