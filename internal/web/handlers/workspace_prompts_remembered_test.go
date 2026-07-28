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
