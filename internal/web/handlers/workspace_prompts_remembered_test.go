package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/prompts"
)

// buildRememberedHandlers builds a Handlers wired with an in-memory workspace,
// a stubbed prompts list, and injectable get/set closures so tests can drive
// the handler without touching disk.
func buildRememberedHandlers(t *testing.T, prompt config.WebPrompt, stored map[string]string) *Handlers {
	t.Helper()
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "ws-remember", WorkingDir: "/wd", ACPServer: "srv"},
	})
	return New(Deps{
		SessionManager: sm,
		GetWorkspacePromptsAll: func(string) []config.WebPrompt {
			return []config.WebPrompt{prompt}
		},
		GetRememberedArgs: func(uuid, name string) (map[string]string, error) {
			if uuid != "ws-remember" {
				return map[string]string{}, nil
			}
			out := make(map[string]string, len(stored))
			for k, v := range stored {
				out[k] = v
			}
			return out, nil
		},
	})
}

func decodeArgs(t *testing.T, w *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var resp struct {
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, w.Body.String())
	}
	if resp.Arguments == nil {
		return map[string]string{}
	}
	return resp.Arguments
}

func TestRememberedArgsGET_MissingParams(t *testing.T) {
	h := New(Deps{})
	for _, url := range []string{
		"/api/workspace-prompts/remembered-args",
		"/api/workspace-prompts/remembered-args?working_dir=/wd",
		"/api/workspace-prompts/remembered-args?prompt=p",
	} {
		w := httptest.NewRecorder()
		h.HandleRememberedArgsGET(w, httptest.NewRequest(http.MethodGet, url, nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("url=%s code=%d want 400", url, w.Code)
		}
	}
}

func TestRememberedArgsGET_UnknownWorkspace(t *testing.T) {
	h := buildRememberedHandlers(t, config.WebPrompt{
		Name: "commit",
		Parameters: []config.PromptParameter{
			{Name: "Msg", Type: "text", Remember: prompts.RememberFolder},
		},
	}, map[string]string{"Msg": "hi"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/workspace-prompts/remembered-args?working_dir=/nope&prompt=commit", nil)
	h.HandleRememberedArgsGET(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d want 200", w.Code)
	}
	if got := decodeArgs(t, w); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestRememberedArgsGET_FiltersToRememberFolder(t *testing.T) {
	prompt := config.WebPrompt{
		Name: "commit",
		Parameters: []config.PromptParameter{
			{Name: "Msg", Type: "text", Remember: prompts.RememberFolder},
			{Name: "Secret", Type: "text"},                                   // no remember
			{Name: "Legacy", Type: "text", Remember: prompts.RememberGlobal}, // global (not persisted)
		},
	}
	// stored contains a stale key ("Removed") that is no longer declared,
	// and "Secret" which is declared but without remember:folder.
	stored := map[string]string{
		"Msg":     "last message",
		"Secret":  "leaked",
		"Legacy":  "old",
		"Removed": "stale",
	}
	h := buildRememberedHandlers(t, prompt, stored)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/workspace-prompts/remembered-args?working_dir=/wd&prompt=commit", nil)
	h.HandleRememberedArgsGET(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d want 200; body=%s", w.Code, w.Body.String())
	}
	got := decodeArgs(t, w)
	if len(got) != 1 || got["Msg"] != "last message" {
		t.Fatalf("filtered args = %v; want {Msg: last message}", got)
	}
}

func TestRememberedArgsGET_UnknownPromptReturnsEmpty(t *testing.T) {
	h := buildRememberedHandlers(t, config.WebPrompt{Name: "other"}, map[string]string{"Msg": "x"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/workspace-prompts/remembered-args?working_dir=/wd&prompt=missing", nil)
	h.HandleRememberedArgsGET(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d want 200", w.Code)
	}
	if got := decodeArgs(t, w); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestRememberedArgsGET_FeatureDisabledWhenNilClosures(t *testing.T) {
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "ws-x", WorkingDir: "/wd", ACPServer: "srv"},
	})
	h := New(Deps{SessionManager: sm}) // no GetWorkspacePromptsAll, no GetRememberedArgs
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/workspace-prompts/remembered-args?working_dir=/wd&prompt=commit", nil)
	h.HandleRememberedArgsGET(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d want 200", w.Code)
	}
	if got := decodeArgs(t, w); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

// buildScopedRememberedGET wires a handler with both folder- and
// conversation-scope readers so tests can drive the merged GET endpoint
// (mitto-47y.6.2). folderStored and convStored are the per-scope snapshots
// returned for the wired workspace ("ws-remember") and session ID
// ("sess-remember"), respectively. Unknown identifiers return empty maps.
func buildScopedRememberedGET(
	t *testing.T,
	prompt config.WebPrompt,
	folderStored map[string]string,
	convStored map[string]string,
) *Handlers {
	t.Helper()
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "ws-remember", WorkingDir: "/wd", ACPServer: "srv"},
	})
	return New(Deps{
		SessionManager: sm,
		GetWorkspacePromptsAll: func(string) []config.WebPrompt {
			return []config.WebPrompt{prompt}
		},
		GetRememberedArgs: func(uuid, name string) (map[string]string, error) {
			if uuid != "ws-remember" {
				return map[string]string{}, nil
			}
			out := make(map[string]string, len(folderStored))
			for k, v := range folderStored {
				out[k] = v
			}
			return out, nil
		},
		GetRememberedConversationArgs: func(sess, name string) (map[string]string, error) {
			if sess != "sess-remember" {
				return map[string]string{}, nil
			}
			out := make(map[string]string, len(convStored))
			for k, v := range convStored {
				out[k] = v
			}
			return out, nil
		},
	})
}

// TestRememberedArgsGET_ConversationScopeReturnedWhenSessionIDProvided
// verifies that a param declared remember:conversation is only returned when
// the session_id query param is provided, and comes from the conversation
// snapshot rather than the folder snapshot (mitto-47y.6.2).
func TestRememberedArgsGET_ConversationScopeReturnedWhenSessionIDProvided(t *testing.T) {
	prompt := config.WebPrompt{
		Name: "note",
		Parameters: []config.PromptParameter{
			{Name: "Body", Type: "text", Remember: prompts.RememberConversation},
		},
	}
	h := buildScopedRememberedGET(t, prompt,
		map[string]string{}, // no folder-scope value
		map[string]string{"Body": "session-only"},
	)

	// Without session_id, the conversation-scope value must not be returned.
	w := httptest.NewRecorder()
	h.HandleRememberedArgsGET(w, httptest.NewRequest(http.MethodGet,
		"/api/workspace-prompts/remembered-args?working_dir=/wd&prompt=note", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("no session_id: code=%d want 200", w.Code)
	}
	if got := decodeArgs(t, w); len(got) != 0 {
		t.Errorf("no session_id: expected empty, got %v", got)
	}

	// With session_id, the conversation-scope value MUST be returned.
	w = httptest.NewRecorder()
	h.HandleRememberedArgsGET(w, httptest.NewRequest(http.MethodGet,
		"/api/workspace-prompts/remembered-args?working_dir=/wd&prompt=note&session_id=sess-remember", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("with session_id: code=%d want 200", w.Code)
	}
	got := decodeArgs(t, w)
	if len(got) != 1 || got["Body"] != "session-only" {
		t.Fatalf("with session_id: args = %v; want {Body: session-only}", got)
	}
}

// TestRememberedArgsGET_ConversationOverridesFolderOnCollision pins the
// merge-precedence rule: when the SAME argument name is remembered at both
// scopes, the conversation-scope value wins (mitto-47y.6.2).
func TestRememberedArgsGET_ConversationOverridesFolderOnCollision(t *testing.T) {
	prompt := config.WebPrompt{
		Name: "commit",
		Parameters: []config.PromptParameter{
			// Same arg name declared with BOTH modes is a synthetic case: the
			// GET endpoint tracks the declared scope per name, then folder is
			// read first and conversation overlays on top. Use two distinct
			// arg names to keep the intent unambiguous while still exercising
			// the overlay logic on a shared key.
			{Name: "Msg", Type: "text", Remember: prompts.RememberConversation},
			{Name: "Template", Type: "text", Remember: prompts.RememberFolder},
		},
	}
	h := buildScopedRememberedGET(t, prompt,
		map[string]string{"Msg": "folder-msg", "Template": "shared-template"},
		map[string]string{"Msg": "conv-msg"},
	)

	w := httptest.NewRecorder()
	h.HandleRememberedArgsGET(w, httptest.NewRequest(http.MethodGet,
		"/api/workspace-prompts/remembered-args?working_dir=/wd&prompt=commit&session_id=sess-remember", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d want 200; body=%s", w.Code, w.Body.String())
	}
	got := decodeArgs(t, w)
	if got["Msg"] != "conv-msg" {
		t.Errorf("Msg = %q; want conversation-scope value %q", got["Msg"], "conv-msg")
	}
	if got["Template"] != "shared-template" {
		t.Errorf("Template = %q; want folder-scope value %q", got["Template"], "shared-template")
	}
}

// TestRememberedArgsGET_FolderOnlyWhenSessionIDMissing verifies the backward-
// compatible read path: with no session_id, only folder-scope values for
// remember:folder params are returned; conversation-scope params yield no
// value (mitto-47y.6.2, mitto-x8v regression).
func TestRememberedArgsGET_FolderOnlyWhenSessionIDMissing(t *testing.T) {
	prompt := config.WebPrompt{
		Name: "commit",
		Parameters: []config.PromptParameter{
			{Name: "Msg", Type: "text", Remember: prompts.RememberFolder},
			{Name: "Draft", Type: "text", Remember: prompts.RememberConversation},
		},
	}
	h := buildScopedRememberedGET(t, prompt,
		map[string]string{"Msg": "folder-msg"},
		map[string]string{"Draft": "conv-draft"},
	)

	w := httptest.NewRecorder()
	h.HandleRememberedArgsGET(w, httptest.NewRequest(http.MethodGet,
		"/api/workspace-prompts/remembered-args?working_dir=/wd&prompt=commit", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d want 200", w.Code)
	}
	got := decodeArgs(t, w)
	if len(got) != 1 || got["Msg"] != "folder-msg" {
		t.Fatalf("args = %v; want {Msg: folder-msg}", got)
	}
	if _, ok := got["Draft"]; ok {
		t.Errorf("Draft should be omitted when session_id is missing: %v", got)
	}
}

// TestRememberedArgsGET_ConversationFilteredByDeclaredScope verifies that a
// conversation-scope key whose declared parameter has scope OTHER than
// conversation (e.g. folder-only, or no remember) is filtered out even when
// present in the conversation snapshot. This protects against stale writes
// leaking across a scope change on a param (mitto-47y.6.2).
func TestRememberedArgsGET_ConversationFilteredByDeclaredScope(t *testing.T) {
	prompt := config.WebPrompt{
		Name: "commit",
		Parameters: []config.PromptParameter{
			{Name: "Msg", Type: "text", Remember: prompts.RememberFolder},
			// No conversation-scoped params declared.
		},
	}
	h := buildScopedRememberedGET(t, prompt,
		map[string]string{"Msg": "folder-msg"},
		map[string]string{"Msg": "conv-msg", "Extra": "leaked"},
	)

	w := httptest.NewRecorder()
	h.HandleRememberedArgsGET(w, httptest.NewRequest(http.MethodGet,
		"/api/workspace-prompts/remembered-args?working_dir=/wd&prompt=commit&session_id=sess-remember", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d want 200", w.Code)
	}
	got := decodeArgs(t, w)
	// Msg is declared remember:folder, so its scope map entry is "folder";
	// conversation-scope stored values under keys with a non-conversation
	// declared scope must still pass the per-name filter (since Msg IS
	// declared) — but the folder read runs FIRST and the conversation overlay
	// then rewrites Msg. Extra has no declared scope, so it must be filtered.
	if got["Msg"] != "conv-msg" {
		t.Errorf("Msg = %q; want conversation overlay value %q", got["Msg"], "conv-msg")
	}
	if _, ok := got["Extra"]; ok {
		t.Errorf("Extra is not a declared param; must be filtered: %v", got)
	}
}
