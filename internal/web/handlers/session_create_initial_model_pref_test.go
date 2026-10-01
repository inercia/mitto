package handlers

import (
	"sync/atomic"
	"testing"
	"time"

	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// Creating a genuinely new conversation for a named prompt resolves that
// prompt's preferredModels as the creation-time initial model preference.
func TestCreateSession_NamedPrompt_ResolvesInitialModelPreference(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  func(wd string) SessionCreateRequest
		want string
	}{
		{"origin_prompt_name", func(wd string) SessionCreateRequest {
			return SessionCreateRequest{WorkingDir: wd, OriginPromptName: "smart-prompt"}
		}, "smart-prompt"},
		{"initial_prompt_name", func(wd string) SessionCreateRequest {
			return SessionCreateRequest{WorkingDir: wd, InitialPromptName: "init-prompt"}
		}, "init-prompt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workingDir := t.TempDir()
			_, h := newReuseE2EHandlers(t, workingDir)

			var calls int32
			var gotName, gotWD atomic.Value
			h.deps.SessionManager.SetPreferredModelsResolver(func(name, wd string) []configPkg.PromptPreferredModel {
				atomic.AddInt32(&calls, 1)
				gotName.Store(name)
				gotWD.Store(wd)
				return []configPkg.PromptPreferredModel{{ModelTag: "Reasoning"}}
			})

			postSession(t, h, tc.req(workingDir))

			if atomic.LoadInt32(&calls) == 0 {
				t.Fatalf("preferredModels resolver was not consulted for a new named-prompt conversation")
			}
			if gotName.Load() != tc.want || gotWD.Load() != workingDir {
				t.Errorf("resolver args = (%v, %v), want (%q, %q)", gotName.Load(), gotWD.Load(), tc.want, workingDir)
			}
		})
	}
}

// A plain create (no named prompt) must keep the workspace/ACP precedence and
// never consult the prompt resolver.
func TestCreateSession_NoPromptName_DoesNotResolveInitialModelPreference(t *testing.T) {
	workingDir := t.TempDir()
	_, h := newReuseE2EHandlers(t, workingDir)

	var calls int32
	h.deps.SessionManager.SetPreferredModelsResolver(func(string, string) []configPkg.PromptPreferredModel {
		atomic.AddInt32(&calls, 1)
		return []configPkg.PromptPreferredModel{{ModelTag: "Reasoning"}}
	})

	postSession(t, h, SessionCreateRequest{WorkingDir: workingDir})

	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Errorf("resolver calls = %d for a create without a prompt name, want 0", got)
	}
}

// A reuse hit returns the existing conversation and must never rebaseline it,
// so the initial-model resolver is not consulted.
func TestCreateSession_ReuseHit_DoesNotResolveInitialModelPreference(t *testing.T) {
	workingDir := t.TempDir()
	store, h := newReuseE2EHandlers(t, workingDir)

	sessionID := "20260201-150000-reuse"
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		Status:     "active",
		ACPServer:  "test-server",
		WorkingDir: workingDir,
		BeadsIssue: "mitto-200",
		UpdatedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	h.deps.ResolvePromptReuseIssue = func(string, string) bool { return true }
	h.deps.ResolvePromptSingleton = func(string, string) bool { return false }

	var calls int32
	h.deps.SessionManager.SetPreferredModelsResolver(func(string, string) []configPkg.PromptPreferredModel {
		atomic.AddInt32(&calls, 1)
		return []configPkg.PromptPreferredModel{{ModelTag: "Reasoning"}}
	})

	w := postSession(t, h, SessionCreateRequest{
		WorkingDir:       workingDir,
		BeadsIssue:       "mitto-200",
		OriginPromptName: "cleanup-issue",
	})
	resp := decodeSessionResponse(t, w)
	if resp == nil || resp["reused"] != true || resp["session_id"] != sessionID {
		t.Fatalf("expected reuse of %s, got code=%d resp=%v", sessionID, w.Code, resp)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Errorf("resolver calls = %d on a reuse hit, want 0 (existing conversations must not be rebaselined)", got)
	}
}
