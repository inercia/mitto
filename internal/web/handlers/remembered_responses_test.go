package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/prompts"
	"github.com/inercia/mitto/internal/rememberedargs"
)

// memSidecars is an in-memory rememberedargs.SessionSidecars for handler tests.
type memSidecars struct {
	files map[string][]byte
}

func (m *memSidecars) UpdateSessionSidecarJSON(sessionID, name string, update func([]byte) ([]byte, error)) error {
	key := sessionID + "/" + name
	out, err := update(m.files[key])
	if err != nil {
		return err
	}
	if out != nil {
		m.files[key] = out
	}
	return nil
}

const (
	respSessionID     = "20260201-140000-resp"
	respWorkspaceUUID = "ws-resp"
	respWorkingDir    = "/wd-resp"
)

func buildRememberResponsesHandlers(t *testing.T, ws []config.WebPrompt) *Handlers {
	t.Helper()
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: respWorkspaceUUID, WorkingDir: respWorkingDir, ACPServer: "srv"},
	})
	sm.AddSessionForTest(conversation.NewMinimalBackgroundSession(respSessionID, respWorkingDir, respWorkspaceUUID))
	return New(Deps{
		SessionManager: sm,
		GetWorkspacePromptsAll: func(string) []config.WebPrompt {
			return ws
		},
		ConversationResponses: rememberedargs.NewConversationResponses(&memSidecars{files: map[string][]byte{}}, ""),
	})
}

func getRememberedResponses(t *testing.T, h *Handlers, prompt string) map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/workspace-prompts/remembered-args?working_dir="+respWorkingDir+
		"&prompt="+prompt+"&session_id="+respSessionID, nil)
	rec := httptest.NewRecorder()
	h.HandleRememberedArgsGET(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode GET body: %v", err)
	}
	return out
}

func TestRememberResponses_EnabledSnapshotRoundTrip(t *testing.T) {
	greet := config.WebPrompt{
		Name: "Greet",
		Parameters: []config.PromptParameter{
			{Name: "Name", Type: "text", Remember: prompts.RememberConversation},
			{Name: "Secret", Type: "text", Remember: prompts.RememberNever},
			{Name: "Empty", Type: "text"},
		},
	}
	h := buildRememberResponsesHandlers(t, []config.WebPrompt{greet})
	bs := h.deps.SessionManager.GetSession(respSessionID)

	if err := h.applyRememberResponses(respSessionID, bs, "greet", map[string]string{
		"Name":   "Ann",
		"Secret": "hidden",
		"Empty":  "",
		"Stray":  "dropped",
	}, true); err != nil {
		t.Fatalf("apply enabled: %v", err)
	}

	out := getRememberedResponses(t, h, "Greet")
	if out["remember_responses"] != true {
		t.Fatalf("remember_responses = %v; want true", out["remember_responses"])
	}
	want := map[string]interface{}{"Name": "Ann", "Empty": ""}
	if got := out["arguments"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %v; want %v (remember:never and undeclared keys must not persist)", got, want)
	}
}

func TestRememberResponses_DisabledTombstoneHidesValues(t *testing.T) {
	greet := config.WebPrompt{
		Name: "Greet",
		Parameters: []config.PromptParameter{
			{Name: "Name", Type: "text", Remember: prompts.RememberConversation},
		},
	}
	h := buildRememberResponsesHandlers(t, []config.WebPrompt{greet})
	bs := h.deps.SessionManager.GetSession(respSessionID)

	if err := h.applyRememberResponses(respSessionID, bs, "Greet", map[string]string{"Name": "Ann"}, true); err != nil {
		t.Fatalf("apply enabled: %v", err)
	}
	if err := h.applyRememberResponses(respSessionID, bs, "Greet", map[string]string{"Name": "Bob"}, false); err != nil {
		t.Fatalf("apply disabled: %v", err)
	}

	out := getRememberedResponses(t, h, "Greet")
	if out["remember_responses"] != false {
		t.Fatalf("remember_responses = %v; want false", out["remember_responses"])
	}
	if got := out["arguments"]; !reflect.DeepEqual(got, map[string]interface{}{}) {
		t.Fatalf("arguments = %v; want empty after opt-out", got)
	}
}

func TestRememberResponses_UnsetLeavesLegacyBehavior(t *testing.T) {
	greet := config.WebPrompt{
		Name: "Greet",
		Parameters: []config.PromptParameter{
			{Name: "Name", Type: "text", Remember: prompts.RememberConversation},
		},
	}
	h := buildRememberResponsesHandlers(t, []config.WebPrompt{greet})

	out := getRememberedResponses(t, h, "Greet")
	if _, ok := out["remember_responses"]; ok {
		t.Fatalf("remember_responses present for unset state: %v", out)
	}
}
