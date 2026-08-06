package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// suggestFixture builds a Handlers wired with a fresh temp Store and (optionally)
// a GetWorkspacePromptsAll stub, and returns the session ID it just created so
// tests can append events. Mirrors the newLoopStoreWithPrompts helper's shape
// but seeds a session up front so every case can drive the endpoint directly.
func suggestFixture(t *testing.T, workingDir string, prompts func(dir string) []configPkg.WebPrompt) (*session.Store, *Handlers, string) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	const sid = "suggest-test-session"
	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  "test-server",
		WorkingDir: workingDir,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	deps := Deps{Store: store}
	if prompts != nil {
		deps.GetWorkspacePromptsAll = prompts
	}
	return store, New(deps), sid
}

// appendUserPromptEvent appends a user_prompt event with the given PromptName
// (may be empty for free-text) using the store's normal write path.
func appendUserPromptEvent(t *testing.T, store *session.Store, sid, promptName, message string) {
	t.Helper()
	if err := store.AppendEvent(sid, session.Event{
		Type: session.EventTypeUserPrompt,
		Data: session.UserPromptData{
			Message:    message,
			PromptName: promptName,
		},
	}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
}

// callSuggest hits GET /api/sessions/{sid}/loop/suggest-from-recent through the
// full HandleSessionLoop dispatcher so the sub-path routing is exercised too.
func callSuggest(h *Handlers, sid string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sid+"/loop/suggest-from-recent", nil)
	w := httptest.NewRecorder()
	h.HandleSessionLoop(w, req, sid, "suggest-from-recent")
	return w
}

// TestHandleSuggestLoopFromRecent covers all documented paths of the
// GET /api/sessions/{id}/loop/suggest-from-recent handler (mitto-qff).
func TestHandleSuggestLoopFromRecent(t *testing.T) {
	// --- case 1: empty event log -> 404 -------------------------------------
	t.Run("EmptyEventLog404", func(t *testing.T) {
		tmp := t.TempDir()
		_, h, sid := suggestFixture(t, tmp, func(string) []configPkg.WebPrompt { return nil })
		w := callSuggest(h, sid)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})

	// --- case 2: GetMetadata error (nonexistent session) -> 404 -------------
	t.Run("GetMetadataError404", func(t *testing.T) {
		_, h, _ := suggestFixture(t, t.TempDir(), func(string) []configPkg.WebPrompt { return nil })
		w := callSuggest(h, "no-such-session")
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})

	// --- case 3: empty WorkingDir -> 404 ------------------------------------
	t.Run("EmptyWorkingDir404", func(t *testing.T) {
		_, h, sid := suggestFixture(t, "", func(string) []configPkg.WebPrompt { return nil })
		appendUserPromptEvent(t, storeFromHandler(t, h), sid, "some-prompt", "hi")
		w := callSuggest(h, sid)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})

	// --- case 5: most-recent user prompt is free-text (PromptName == "") ---
	t.Run("FreeTextMostRecent404", func(t *testing.T) {
		tmp := t.TempDir()
		store, h, sid := suggestFixture(t, tmp, func(string) []configPkg.WebPrompt { return nil })
		appendUserPromptEvent(t, store, sid, "", "hello there")
		w := callSuggest(h, sid)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})

	// --- case 6: named prompt not in workspace list -> 404 ------------------
	t.Run("NamedPromptNotInWorkspaceList404", func(t *testing.T) {
		tmp := t.TempDir()
		store, h, sid := suggestFixture(t, tmp, func(string) []configPkg.WebPrompt {
			return []configPkg.WebPrompt{{Name: "some-other-prompt"}}
		})
		appendUserPromptEvent(t, store, sid, "missing-prompt", "hi")
		w := callSuggest(h, sid)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})

	// --- case 7: named prompt found but .Loop == nil -> 404 -----------------
	t.Run("NamedPromptNoLoopBlock404", func(t *testing.T) {
		tmp := t.TempDir()
		store, h, sid := suggestFixture(t, tmp, func(string) []configPkg.WebPrompt {
			return []configPkg.WebPrompt{{Name: "plain-prompt" /* Loop: nil */}}
		})
		appendUserPromptEvent(t, store, sid, "plain-prompt", "hi")
		w := callSuggest(h, sid)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})

	// --- case 8: happy path with a fully-populated loop: block -> 200 -------
	t.Run("HappyPathMergesEveryField200", func(t *testing.T) {
		tmp := t.TempDir()
		tr, fa := true, false
		promptStub := func(string) []configPkg.WebPrompt {
			return []configPkg.WebPrompt{{
				Name: "seed-prompt",
				Loop: &configPkg.PromptLoop{
					Trigger:       []string{"onCompletion"},
					OnCompletion:  &configPkg.PromptLoopOnCompletion{Delay: 45},
					MaxIterations: 7,
					MaxDuration:   "2h",
					OnTasks:       &configPkg.PromptLoopOnTasks{Condition: "tasks.changed()", CoalesceDuringBusy: &fa},
					FreshContext:  &tr,
					RunOnStart:    &tr,
				},
			}}
		}
		store, h, sid := suggestFixture(t, tmp, promptStub)
		appendUserPromptEvent(t, store, sid, "seed-prompt", "hi")

		w := callSuggest(h, sid)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
		}

		var got session.LoopPrompt
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if got.PromptName != "seed-prompt" {
			t.Errorf("PromptName = %q, want %q", got.PromptName, "seed-prompt")
		}
		if got.Enabled {
			t.Errorf("Enabled = true, want false (draft must land paused)")
		}
		if got.Trigger != session.TriggerOnCompletion {
			t.Errorf("Trigger = %q, want %q", got.Trigger, session.TriggerOnCompletion)
		}
		if got.DelaySeconds != 45 {
			t.Errorf("DelaySeconds = %d, want 45", got.DelaySeconds)
		}
		if got.MaxIterations != 7 {
			t.Errorf("MaxIterations = %d, want 7", got.MaxIterations)
		}
		if got.MaxDurationSeconds != 7200 {
			t.Errorf("MaxDurationSeconds = %d, want 7200 (2h)", got.MaxDurationSeconds)
		}
		if got.Condition != "tasks.changed()" {
			t.Errorf("Condition = %q, want %q", got.Condition, "tasks.changed()")
		}
		if !got.FreshContext {
			t.Errorf("FreshContext = false, want true")
		}
		if got.RunOnStart == nil || !*got.RunOnStart {
			t.Errorf("RunOnStart = %v, want *true", got.RunOnStart)
		}
		if got.CoalesceDuringBusy == nil || *got.CoalesceDuringBusy {
			t.Errorf("CoalesceDuringBusy = %v, want *false", got.CoalesceDuringBusy)
		}
	})

	// --- case 9: merged scaffold fails Validate() -> 404 --------------------
	t.Run("MergedScaffoldFailsValidate404", func(t *testing.T) {
		tmp := t.TempDir()
		// Frontmatter with an invalid trigger enum — leaks through the merge
		// helper into LoopPrompt.Trigger, so Validate() returns ErrInvalidTrigger.
		promptStub := func(string) []configPkg.WebPrompt {
			return []configPkg.WebPrompt{{
				Name: "bad-loop",
				Loop: &configPkg.PromptLoop{Trigger: []string{"not-a-real-trigger"}},
			}}
		}
		store, h, sid := suggestFixture(t, tmp, promptStub)
		appendUserPromptEvent(t, store, sid, "bad-loop", "hi")

		w := callSuggest(h, sid)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d. Body: %s", w.Code, http.StatusNotFound, w.Body.String())
		}
	})

	// --- case 10: non-GET method -> 405 -------------------------------------
	t.Run("NonGetMethod405", func(t *testing.T) {
		tmp := t.TempDir()
		_, h, sid := suggestFixture(t, tmp, func(string) []configPkg.WebPrompt { return nil })
		for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			req := httptest.NewRequest(m, "/api/sessions/"+sid+"/loop/suggest-from-recent", nil)
			w := httptest.NewRecorder()
			h.HandleSessionLoop(w, req, sid, "suggest-from-recent")
			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("method %s: status = %d, want %d", m, w.Code, http.StatusMethodNotAllowed)
			}
		}
	})

	// --- case 11: case-insensitive prompt-name match -> 200 -----------------
	t.Run("CaseInsensitiveMatch200", func(t *testing.T) {
		tmp := t.TempDir()
		promptStub := func(string) []configPkg.WebPrompt {
			return []configPkg.WebPrompt{{
				Name: "someprompt", // lower-case in workspace list
				Loop: &configPkg.PromptLoop{Trigger: []string{"onCompletion"}, OnCompletion: &configPkg.PromptLoopOnCompletion{Delay: 10}},
			}}
		}
		store, h, sid := suggestFixture(t, tmp, promptStub)
		appendUserPromptEvent(t, store, sid, "SoMePrompt" /* mixed case */, "hi")

		w := callSuggest(h, sid)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
		}
		var got session.LoopPrompt
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.PromptName != "someprompt" {
			t.Errorf("PromptName = %q, want workspace-cased %q", got.PromptName, "someprompt")
		}
	})

	// --- case 12: newest reverse-order named prompt wins ---------------------
	t.Run("FirstReverseOrderPromptWins200", func(t *testing.T) {
		tmp := t.TempDir()
		promptStub := func(string) []configPkg.WebPrompt {
			return []configPkg.WebPrompt{
				{Name: "older-prompt", Loop: &configPkg.PromptLoop{Trigger: []string{"schedule"}, Schedule: &configPkg.PromptLoopSchedule{Value: 5, Unit: "hours"}}},
				{Name: "newer-prompt", Loop: &configPkg.PromptLoop{Trigger: []string{"onCompletion"}, OnCompletion: &configPkg.PromptLoopOnCompletion{Delay: 99}, MaxIterations: 42}},
			}
		}
		store, h, sid := suggestFixture(t, tmp, promptStub)
		// Order matters: older first, newer last. ReadEventsLastReverse
		// returns newest-first, so newer-prompt must be picked.
		appendUserPromptEvent(t, store, sid, "older-prompt", "first")
		appendUserPromptEvent(t, store, sid, "newer-prompt", "second")

		w := callSuggest(h, sid)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
		}
		var got session.LoopPrompt
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.PromptName != "newer-prompt" {
			t.Errorf("PromptName = %q, want %q (newest reverse-order named prompt)", got.PromptName, "newer-prompt")
		}
		if got.Trigger != session.TriggerOnCompletion {
			t.Errorf("Trigger = %q, want onCompletion (from newer-prompt)", got.Trigger)
		}
		if got.MaxIterations != 42 {
			t.Errorf("MaxIterations = %d, want 42 (from newer-prompt)", got.MaxIterations)
		}
	})

	// --- case 13: read-only - a 200 must not create loop.json ---------------
	t.Run("ReadOnlyNoLoopWrite200", func(t *testing.T) {
		tmp := t.TempDir()
		promptStub := func(string) []configPkg.WebPrompt {
			return []configPkg.WebPrompt{{
				Name: "ro-prompt",
				Loop: &configPkg.PromptLoop{Trigger: []string{"onCompletion"}, OnCompletion: &configPkg.PromptLoopOnCompletion{Delay: 30}},
			}}
		}
		store, h, sid := suggestFixture(t, tmp, promptStub)
		appendUserPromptEvent(t, store, sid, "ro-prompt", "hi")

		// Precondition: no loop configured yet.
		if _, err := store.Loop(sid).Get(); err != session.ErrLoopNotFound {
			t.Fatalf("precondition failed: Get = %v, want ErrLoopNotFound", err)
		}

		w := callSuggest(h, sid)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
		}

		// Post-condition: still nothing persisted — the endpoint is read-only.
		if _, err := store.Loop(sid).Get(); err != session.ErrLoopNotFound {
			t.Errorf("post-call Get = %v, want ErrLoopNotFound (endpoint must not write)", err)
		}
		if _, err := store.Loop(sid).GetSaved(); err != session.ErrLoopNotFound {
			t.Errorf("post-call GetSaved = %v, want ErrLoopNotFound (endpoint must not write saved slot)", err)
		}
	})
}

// storeFromHandler pulls the Store back out of a Handlers built by
// suggestFixture. Used by cases that need a second AppendEvent after the
// handler has been created but only kept the *Handlers value.
func storeFromHandler(t *testing.T, h *Handlers) *session.Store {
	t.Helper()
	if h.deps.Store == nil {
		t.Fatalf("handler has nil Store")
	}
	return h.deps.Store
}
