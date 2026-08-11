package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestClient_PatchLoop_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPatch, "/mitto/api/sessions/sess-1/loop").
		RespondJSON(http.StatusOK, `{"prompt":"do it","frequency":{"value":5,"unit":"minutes"},"enabled":false,"triggers":["schedule"]}`)

	enabled := false
	config, err := f.Client().PatchLoop("sess-1", LoopPatchRequest{Enabled: &enabled})
	if err != nil {
		t.Fatalf("PatchLoop: %v", err)
	}
	var gotBody map[string]any
	_ = json.Unmarshal(f.LastRequest().Body, &gotBody)
	if gotBody["enabled"] != false {
		t.Errorf("request body enabled = %v, want false", gotBody["enabled"])
	}
	if config.Enabled || config.Prompt != "do it" || len(config.Triggers) != 1 {
		t.Errorf("LoopConfig = %+v, unexpected", config)
	}
}

func TestClient_PatchLoop_404_ReturnsTypedNotFoundError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPatch, "/mitto/api/sessions/sess-1/loop").RespondRaw(http.StatusNotFound, "", nil)

	_, err := f.Client().PatchLoop("sess-1", LoopPatchRequest{})
	assertAPIError(t, err, ErrNotFound, http.StatusNotFound, "")
}

func TestClient_RestoreLoop_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/sessions/sess-1/loop/restore").
		RespondJSON(http.StatusOK, `{"prompt":"restored","frequency":{"value":1,"unit":"hours"},"enabled":true}`)

	config, err := f.Client().RestoreLoop("sess-1")
	if err != nil {
		t.Fatalf("RestoreLoop: %v", err)
	}
	if !config.Enabled || config.Prompt != "restored" {
		t.Errorf("LoopConfig = %+v, unexpected", config)
	}
}

func TestClient_RestoreLoop_404_ReturnsTypedNotFoundError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/sessions/sess-1/loop/restore").RespondRaw(http.StatusNotFound, "", nil)

	_, err := f.Client().RestoreLoop("sess-1")
	assertAPIError(t, err, ErrNotFound, http.StatusNotFound, "")
}

func TestClient_RestoreLoop_409_ReturnsTypedConflictError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/sessions/sess-1/loop/restore").
		Fail(http.StatusConflict, "conflict", "loop already configured", nil)

	_, err := f.Client().RestoreLoop("sess-1")
	assertAPIError(t, err, ErrConflict, http.StatusConflict, "conflict")
}

func TestClient_AcknowledgeLoopStoppedReason_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/sessions/sess-1/loop/acknowledge-stopped-reason").
		RespondJSON(http.StatusOK, `{"prompt":"p","frequency":{"value":1,"unit":"hours"},"enabled":true,"stopped_reason":""}`)

	config, err := f.Client().AcknowledgeLoopStoppedReason("sess-1")
	if err != nil {
		t.Fatalf("AcknowledgeLoopStoppedReason: %v", err)
	}
	if config.StoppedReason != "" {
		t.Errorf("StoppedReason = %q, want empty", config.StoppedReason)
	}
}

func TestClient_AcknowledgeLoopStoppedReason_404_ReturnsTypedNotFoundError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/sessions/sess-1/loop/acknowledge-stopped-reason").RespondRaw(http.StatusNotFound, "", nil)

	_, err := f.Client().AcknowledgeLoopStoppedReason("sess-1")
	assertAPIError(t, err, ErrNotFound, http.StatusNotFound, "")
}

func TestClient_SuggestLoopFromRecent_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/sess-1/loop/suggest-from-recent").
		RespondJSON(http.StatusOK, `{"prompt_name":"nightly-cleanup","frequency":{"value":1,"unit":"days","at":"03:00"},"enabled":false}`)

	suggestion, err := f.Client().SuggestLoopFromRecent("sess-1")
	if err != nil {
		t.Fatalf("SuggestLoopFromRecent: %v", err)
	}
	if suggestion.PromptName != "nightly-cleanup" || suggestion.Enabled {
		t.Errorf("LoopSuggestion = %+v, unexpected", suggestion)
	}
}

func TestClient_SuggestLoopFromRecent_404_ReturnsTypedNotFoundError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/sess-1/loop/suggest-from-recent").
		Fail(http.StatusNotFound, "not_found", "no suggestion available", nil)

	_, err := f.Client().SuggestLoopFromRecent("sess-1")
	assertAPIError(t, err, ErrNotFound, http.StatusNotFound, "not_found")
}

// TestClient_SetLoop_MultiTriggerSchema_RoundTrip pins the mitto-r6j.5 design
// decision recorded in the Implementation comment: SetLoopRequest serializes
// the new multi-trigger fields (triggers/child_events/etc.) and LoopConfig
// decodes the server's back-compat "trigger" alongside "triggers".
func TestClient_SetLoop_MultiTriggerSchema_RoundTrip(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPut, "/mitto/api/sessions/sess-1/loop").
		RespondJSON(http.StatusOK, `{"prompt":"p","frequency":{"value":1,"unit":"hours"},"enabled":true,"trigger":"onTasks","triggers":["onTasks","onCompletion"],"child_events":["anyEndResponse"]}`)

	config, err := f.Client().SetLoop("sess-1", SetLoopRequest{
		Prompt:      "p",
		Enabled:     true,
		Triggers:    []string{"onTasks", "onCompletion"},
		ChildEvents: []string{"anyEndResponse"},
	})
	if err != nil {
		t.Fatalf("SetLoop: %v", err)
	}
	var gotBody map[string]any
	_ = json.Unmarshal(f.LastRequest().Body, &gotBody)
	triggers, _ := gotBody["triggers"].([]any)
	if len(triggers) != 2 || triggers[0] != "onTasks" {
		t.Errorf("request body triggers = %v, want [onTasks onCompletion]", gotBody["triggers"])
	}
	if _, hasLegacyTrigger := gotBody["trigger"]; hasLegacyTrigger {
		t.Error("request body must not send the legacy scalar 'trigger' field")
	}
	if config.Trigger != "onTasks" || len(config.Triggers) != 2 || len(config.ChildEvents) != 1 {
		t.Errorf("LoopConfig = %+v, unexpected", config)
	}
}

// TestClient_GetLoop_DecodesFullServerShape guards the review-phase fix for
// LoopConfig dropping fields the server actually emits on session.LoopPrompt
// (arguments, stopped_at, acknowledged_stopped_reason, created_at/updated_at,
// first_run_at/last_sent_at, coalesce_during_busy, settle_window_seconds).
// Before the fix these were silently discarded on decode.
func TestClient_GetLoop_DecodesFullServerShape(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/sess-1/loop").RespondJSON(http.StatusOK, `{
		"prompt":"p","prompt_name":"pn","arguments":{"Commit":"true"},
		"frequency":{"value":1,"unit":"hours"},"enabled":true,
		"created_at":"2026-08-10T06:00:00Z","updated_at":"2026-08-10T06:30:00Z",
		"first_run_at":"2026-08-10T06:05:00Z","last_sent_at":"2026-08-10T06:25:00Z",
		"stopped_reason":"max_iterations","stopped_at":"2026-08-10T06:40:00Z",
		"acknowledged_stopped_reason":"max_duration",
		"coalesce_during_busy":false,"settle_window_seconds":15
	}`)

	cfg, err := f.Client().GetLoop("sess-1")
	if err != nil {
		t.Fatalf("GetLoop: %v", err)
	}
	if cfg.Arguments["Commit"] != "true" {
		t.Errorf("Arguments = %v, want Commit=true", cfg.Arguments)
	}
	if cfg.CreatedAt == "" || cfg.UpdatedAt == "" || cfg.FirstRunAt == "" || cfg.LastSentAt == "" {
		t.Errorf("timestamps not decoded: %+v", cfg)
	}
	if cfg.StoppedAt == "" || cfg.AcknowledgedStoppedReason != "max_duration" {
		t.Errorf("stopped fields not decoded: %+v", cfg)
	}
	if cfg.CoalesceDuringBusy == nil || *cfg.CoalesceDuringBusy {
		t.Errorf("CoalesceDuringBusy = %v, want false", cfg.CoalesceDuringBusy)
	}
	if cfg.SettleWindowSeconds == nil || *cfg.SettleWindowSeconds != 15 {
		t.Errorf("SettleWindowSeconds = %v, want 15", cfg.SettleWindowSeconds)
	}
}

// --- Coverage for previously-0%-tested RunLoopNow (mitto-rwxq.9) ---

func TestClient_RunLoopNow_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/sessions/sess-1/loop/run-now").RespondRaw(http.StatusOK, "", nil)

	if err := f.Client().RunLoopNow("sess-1", true); err != nil {
		t.Fatalf("RunLoopNow: %v", err)
	}
	var gotBody map[string]any
	_ = json.Unmarshal(f.LastRequest().Body, &gotBody)
	if gotBody["reset_timer"] != true {
		t.Errorf("request body reset_timer = %v, want true", gotBody["reset_timer"])
	}
}

func TestClient_RunLoopNow_404_ReturnsTypedNotFoundError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/sessions/missing/loop/run-now").
		Fail(http.StatusNotFound, CodeNotFound, "session not found", nil)

	err := f.Client().RunLoopNow("missing", false)
	assertAPIError(t, err, ErrNotFound, http.StatusNotFound, CodeNotFound)
}
