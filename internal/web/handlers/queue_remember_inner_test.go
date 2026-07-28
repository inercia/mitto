package handlers

import (
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/prompts"
)

// buildInnerRememberedHandlers builds a Handlers wired with:
//   - a real SessionManager with a minimal BackgroundSession pre-registered so
//     GetWorkspaceUUID/GetWorkingDir return the test fixtures;
//   - a GetWorkspacePromptsAll closure returning the caller-supplied prompts;
//   - a RememberFolderArgs closure that captures writes into an in-memory map
//     keyed by prompt name (mirroring rememberedargs.Store.Set merge semantics).
//
// Returns the handlers, session ID, and the captured writes map. Tests inspect
// the writes map to assert what was persisted per prompt name (mitto-47y.3).
func buildInnerRememberedHandlers(t *testing.T, ws []config.WebPrompt) (*Handlers, string, map[string]map[string]string) {
	t.Helper()
	const (
		sessionID     = "20260201-140000-test47y3a"
		workspaceUUID = "ws-inner"
		workingDir    = "/wd"
	)
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: workspaceUUID, WorkingDir: workingDir, ACPServer: "srv"},
	})
	bs := conversation.NewMinimalBackgroundSession(sessionID, workingDir, workspaceUUID)
	sm.AddSessionForTest(bs)

	writes := make(map[string]map[string]string)
	h := New(Deps{
		SessionManager: sm,
		GetWorkspacePromptsAll: func(string) []config.WebPrompt {
			return ws
		},
		RememberFolderArgs: func(uuid, name string, args map[string]string) error {
			if uuid != workspaceUUID {
				t.Errorf("RememberFolderArgs uuid=%q want %q", uuid, workspaceUUID)
			}
			// Merge to mirror rememberedargs.Store.Set semantics.
			existing := writes[name]
			if existing == nil {
				existing = make(map[string]string, len(args))
			}
			for k, v := range args {
				existing[k] = v
			}
			writes[name] = existing
			return nil
		},
	})
	return h, sessionID, writes
}

// TestRememberFolderArgsForQueueAdd_PersistsInnerUnderInnerName pins the core
// mitto-47y.3 acceptance criterion: when an outer prompt X picks an inner
// prompt Y and Y declares a remember:folder param, the write is keyed under
// Y's name (not X's), so a later outer prompt Z that picks Y sees the same
// remembered inner value.
func TestRememberFolderArgsForQueueAdd_PersistsInnerUnderInnerName(t *testing.T) {
	inner := config.WebPrompt{
		Name: "Commit",
		Parameters: []config.PromptParameter{
			{Name: "Msg", Type: "text", Remember: prompts.RememberFolder},
		},
	}
	outer := config.WebPrompt{
		Name: "Wrap",
		Parameters: []config.PromptParameter{
			{Name: "Picked", Type: "prompts"},
		},
	}
	h, sessionID, writes := buildInnerRememberedHandlers(t,
		[]config.WebPrompt{outer, inner})

	h.rememberFolderArgsForQueueAdd(sessionID, "Wrap", map[string]string{
		"Picked":      "Commit",
		"Picked_Args": `{"Msg":"hello"}`,
	})

	// Inner write MUST be keyed under the inner prompt's name, not the outer.
	if got, ok := writes["Commit"]; !ok || got["Msg"] != "hello" {
		t.Fatalf("inner write under Commit = %v; want {Msg: hello}", got)
	}
	if _, ok := writes["Wrap"]; ok {
		t.Errorf("outer prompt Wrap declares no remember:folder param yet was written to: %v", writes["Wrap"])
	}

	// A different outer prompt that picks the same inner prompt must observe
	// the shared inner value on subsequent reads via the store (mirrored by
	// the writes map's merge semantics).
	h.rememberFolderArgsForQueueAdd(sessionID, "Wrap", map[string]string{
		"Picked":      "Commit",
		"Picked_Args": `{"Msg":"second"}`,
	})
	if got := writes["Commit"]["Msg"]; got != "second" {
		t.Errorf("after second outer prompt, Commit.Msg = %q; want %q", got, "second")
	}
}

// TestRememberFolderArgsForQueueAdd_PersistsOuterAndInner verifies that the
// outer prompt's own remember:folder args are still written under the outer
// prompt name (mitto-x8v behavior) while inner writes go under the inner
// prompt's name (mitto-47y.3), in a single call.
func TestRememberFolderArgsForQueueAdd_PersistsOuterAndInner(t *testing.T) {
	inner := config.WebPrompt{
		Name: "Commit",
		Parameters: []config.PromptParameter{
			{Name: "Msg", Type: "text", Remember: prompts.RememberFolder},
		},
	}
	outer := config.WebPrompt{
		Name: "Wrap",
		Parameters: []config.PromptParameter{
			{Name: "Label", Type: "text", Remember: prompts.RememberFolder},
			{Name: "Picked", Type: "prompts"},
		},
	}
	h, sessionID, writes := buildInnerRememberedHandlers(t,
		[]config.WebPrompt{outer, inner})

	h.rememberFolderArgsForQueueAdd(sessionID, "Wrap", map[string]string{
		"Label":       "outer-value",
		"Picked":      "Commit",
		"Picked_Args": `{"Msg":"inner-value"}`,
	})

	if got := writes["Wrap"]["Label"]; got != "outer-value" {
		t.Errorf("Wrap.Label = %q; want %q", got, "outer-value")
	}
	if got := writes["Commit"]["Msg"]; got != "inner-value" {
		t.Errorf("Commit.Msg = %q; want %q", got, "inner-value")
	}
}

// TestRememberFolderArgsForQueueAdd_EmptyPickedSkipsInner: when the picker's
// value is empty (no inner prompt picked), no inner write occurs even if the
// outer arguments map carries a stale "<Picker>_Args" blob.
func TestRememberFolderArgsForQueueAdd_EmptyPickedSkipsInner(t *testing.T) {
	inner := config.WebPrompt{
		Name: "Commit",
		Parameters: []config.PromptParameter{
			{Name: "Msg", Type: "text", Remember: prompts.RememberFolder},
		},
	}
	outer := config.WebPrompt{
		Name: "Wrap",
		Parameters: []config.PromptParameter{
			{Name: "Picked", Type: "prompts"},
		},
	}
	h, sessionID, writes := buildInnerRememberedHandlers(t,
		[]config.WebPrompt{outer, inner})

	h.rememberFolderArgsForQueueAdd(sessionID, "Wrap", map[string]string{
		"Picked":      "",
		"Picked_Args": `{"Msg":"ignored"}`,
	})

	if len(writes) != 0 {
		t.Errorf("expected no writes when picked value is empty; got %v", writes)
	}
}

// TestRememberFolderArgsForQueueAdd_MalformedInnerArgsIsBestEffort verifies
// that a malformed _Args JSON blob is logged and skipped without failing the
// enqueue path (best-effort contract). The outer write MUST still succeed.
func TestRememberFolderArgsForQueueAdd_MalformedInnerArgsIsBestEffort(t *testing.T) {
	inner := config.WebPrompt{
		Name: "Commit",
		Parameters: []config.PromptParameter{
			{Name: "Msg", Type: "text", Remember: prompts.RememberFolder},
		},
	}
	outer := config.WebPrompt{
		Name: "Wrap",
		Parameters: []config.PromptParameter{
			{Name: "Label", Type: "text", Remember: prompts.RememberFolder},
			{Name: "Picked", Type: "prompts"},
		},
	}
	h, sessionID, writes := buildInnerRememberedHandlers(t,
		[]config.WebPrompt{outer, inner})

	h.rememberFolderArgsForQueueAdd(sessionID, "Wrap", map[string]string{
		"Label":       "outer-still-lands",
		"Picked":      "Commit",
		"Picked_Args": `not-json`,
	})

	if got := writes["Wrap"]["Label"]; got != "outer-still-lands" {
		t.Errorf("outer write missing after malformed inner JSON: %v", writes["Wrap"])
	}
	if _, ok := writes["Commit"]; ok {
		t.Errorf("Commit unexpectedly written despite malformed _Args: %v", writes["Commit"])
	}
}

// TestRememberFolderArgsForQueueAdd_InnerParamWithoutRememberIsFiltered
// verifies that inner values whose declared parameter does NOT have
// remember:folder are not persisted, even when carried on the wire.
func TestRememberFolderArgsForQueueAdd_InnerParamWithoutRememberIsFiltered(t *testing.T) {
	inner := config.WebPrompt{
		Name: "Commit",
		Parameters: []config.PromptParameter{
			{Name: "Msg", Type: "text", Remember: prompts.RememberFolder},
			{Name: "Secret", Type: "text"}, // no remember
			{Name: "Legacy", Type: "text", Remember: prompts.RememberGlobal},
		},
	}
	outer := config.WebPrompt{
		Name: "Wrap",
		Parameters: []config.PromptParameter{
			{Name: "Picked", Type: "prompts"},
		},
	}
	h, sessionID, writes := buildInnerRememberedHandlers(t,
		[]config.WebPrompt{outer, inner})

	h.rememberFolderArgsForQueueAdd(sessionID, "Wrap", map[string]string{
		"Picked":      "Commit",
		"Picked_Args": `{"Msg":"kept","Secret":"leaked","Legacy":"old"}`,
	})

	got := writes["Commit"]
	if got == nil {
		t.Fatalf("no write recorded for Commit")
	}
	if got["Msg"] != "kept" {
		t.Errorf("Commit.Msg = %q; want %q", got["Msg"], "kept")
	}
	if _, ok := got["Secret"]; ok {
		t.Errorf("Secret should not be persisted (no remember): %v", got)
	}
	if _, ok := got["Legacy"]; ok {
		t.Errorf("Legacy should not be persisted (remember=global, not folder): %v", got)
	}
}

// TestRememberFolderArgsForQueueAdd_UnknownInnerPromptSkips verifies that if
// the picked inner prompt name cannot be resolved in the workspace prompts
// list, the write is silently skipped (best-effort contract).
func TestRememberFolderArgsForQueueAdd_UnknownInnerPromptSkips(t *testing.T) {
	outer := config.WebPrompt{
		Name: "Wrap",
		Parameters: []config.PromptParameter{
			{Name: "Picked", Type: "prompts"},
		},
	}
	// Note: no inner prompt named "Ghost" is registered.
	h, sessionID, writes := buildInnerRememberedHandlers(t,
		[]config.WebPrompt{outer})

	h.rememberFolderArgsForQueueAdd(sessionID, "Wrap", map[string]string{
		"Picked":      "Ghost",
		"Picked_Args": `{"Msg":"orphan"}`,
	})

	if len(writes) != 0 {
		t.Errorf("expected no writes when inner prompt is unknown; got %v", writes)
	}
}

// TestRememberFolderArgsForQueueAdd_InnerResolutionIsCaseInsensitive mirrors
// the outer resolution's strings.EqualFold behavior.
func TestRememberFolderArgsForQueueAdd_InnerResolutionIsCaseInsensitive(t *testing.T) {
	inner := config.WebPrompt{
		Name: "Commit",
		Parameters: []config.PromptParameter{
			{Name: "Msg", Type: "text", Remember: prompts.RememberFolder},
		},
	}
	outer := config.WebPrompt{
		Name: "Wrap",
		Parameters: []config.PromptParameter{
			{Name: "Picked", Type: "prompts"},
		},
	}
	h, sessionID, writes := buildInnerRememberedHandlers(t,
		[]config.WebPrompt{outer, inner})

	// Picked value differs in case from registered inner prompt name "Commit".
	h.rememberFolderArgsForQueueAdd(sessionID, "Wrap", map[string]string{
		"Picked":      "commit",
		"Picked_Args": `{"Msg":"case-insensitive"}`,
	})

	if got := writes["Commit"]["Msg"]; got != "case-insensitive" {
		t.Errorf("inner write via case-insensitive match failed: %v", writes)
	}
}
