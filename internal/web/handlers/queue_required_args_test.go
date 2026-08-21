package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// buildRequiredArgsQueueHandlers wires a POST /queue handler with:
//   - a real session Store,
//   - a SessionManager with a minimal BackgroundSession pre-registered so
//     GetWorkingDir returns the test fixture,
//   - a GetWorkspacePromptsAll closure returning the caller-supplied prompts.
//
// Returns the handler and the session ID; tests fire real HTTP requests
// through HandleSessionQueue to exercise the required-args guard end to end.
func buildRequiredArgsQueueHandlers(t *testing.T, ws []config.WebPrompt) (*Handlers, *session.Store, string) {
	t.Helper()
	const (
		sessionID     = "20260729-160000-reqargs"
		workspaceUUID = "ws-reqargs"
	)
	workingDir := t.TempDir()

	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test",
		WorkingDir: workingDir,
		Status:     "active",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: workspaceUUID, WorkingDir: workingDir, ACPServer: "test"},
	})
	bs := conversation.NewMinimalBackgroundSession(sessionID, workingDir, workspaceUUID)
	sm.AddSessionForTest(bs)

	h := New(Deps{
		Store:          store,
		SessionManager: sm,
		GetWorkspacePromptsAll: func(string) []config.WebPrompt {
			return ws
		},
	})
	return h, store, sessionID
}

// boolPtr returns a pointer to the given bool; used for the *bool Required
// field of PromptParameter.
func boolPtr(b bool) *bool { return &b }

// TestHandleSessionQueue_Add_MissingRequiredArgReturns400 pins the mitto-gtf
// backend guard: POST /queue with a prompt_name whose declared prompt has a
// `required: true` parameter that is absent from Arguments must be rejected
// with 400 missing_required_arguments, listing the missing parameter names.
func TestHandleSessionQueue_Add_MissingRequiredArgReturns400(t *testing.T) {
	h, store, sid := buildRequiredArgsQueueHandlers(t, []config.WebPrompt{{
		Name: "Skill: ingest from Confluence page",
		Parameters: []config.PromptParameter{
			{Name: "ConfluencePageURL", Type: "text", Required: boolPtr(true)},
		},
	}})

	body := `{"prompt_name": "Skill: ingest from Confluence page"}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sid+"/queue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sid, "")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errObj, _ := resp["error"].(map[string]interface{})
	if code, _ := errObj["code"].(string); code != "missing_required_arguments" {
		t.Errorf("error.code = %v, want missing_required_arguments; body: %s", errObj["code"], w.Body.String())
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "ConfluencePageURL") {
		t.Errorf("error.message = %q should name the missing param ConfluencePageURL", msg)
	}

	// The queue must remain empty — the guard fired before Add().
	msgs, err := store.Queue(sid).List()
	if err != nil {
		t.Fatalf("Queue.List: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("queue should be empty on rejected enqueue; got %d messages", len(msgs))
	}
}

// TestHandleSessionQueue_Add_EmptyStringRequiredArgReturns400 verifies that
// supplying an empty / whitespace-only value for a required parameter is
// treated the same as a missing value (mitto-gtf: the whole point of the
// guard is to catch the case where the wire carried the key with a blank
// value because the dialog was skipped).
func TestHandleSessionQueue_Add_EmptyStringRequiredArgReturns400(t *testing.T) {
	h, _, sid := buildRequiredArgsQueueHandlers(t, []config.WebPrompt{{
		Name: "TestPrompt",
		Parameters: []config.PromptParameter{
			{Name: "URL", Type: "text", Required: boolPtr(true)},
		},
	}})

	body := `{"prompt_name": "TestPrompt", "arguments": {"URL": "   "}}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sid+"/queue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sid, "")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

// TestHandleSessionQueue_Add_AllRequiredArgsPresentSucceeds verifies the
// happy path: when all required parameters are supplied with non-empty
// values, the enqueue succeeds with 201 and the arguments land in the
// queued message.
func TestHandleSessionQueue_Add_AllRequiredArgsPresentSucceeds(t *testing.T) {
	h, store, sid := buildRequiredArgsQueueHandlers(t, []config.WebPrompt{{
		Name: "Skill: ingest from Confluence page",
		Parameters: []config.PromptParameter{
			{Name: "ConfluencePageURL", Type: "text", Required: boolPtr(true)},
			{Name: "Optional", Type: "text"}, // no Required
		},
	}})

	body := `{"prompt_name": "Skill: ingest from Confluence page", "arguments": {"ConfluencePageURL": "https://example.atlassian.net/wiki/spaces/X/pages/1"}}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sid+"/queue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sid, "")

	if w.Code != http.StatusCreated {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}
	msgs, err := store.Queue(sid).List()
	if err != nil {
		t.Fatalf("Queue.List: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(msgs))
	}
	if got := msgs[0].Arguments["ConfluencePageURL"]; got == "" {
		t.Errorf("queued message did not carry the required arg through; args=%v", msgs[0].Arguments)
	}
}

// TestHandleSessionQueue_Add_UnknownPromptNamePasses verifies the guard is
// silent when the named prompt is not found in the workspace prompts list.
// The queue.Add path itself handles unknown prompts downstream; the guard's
// job is only to catch a KNOWN prompt missing a KNOWN required arg (the
// classic mitto-gtf scenario), not to gate arbitrary prompt names.
func TestHandleSessionQueue_Add_UnknownPromptNamePasses(t *testing.T) {
	h, _, sid := buildRequiredArgsQueueHandlers(t, []config.WebPrompt{{
		Name: "SomethingElse",
		Parameters: []config.PromptParameter{
			{Name: "X", Type: "text", Required: boolPtr(true)},
		},
	}})

	body := `{"prompt_name": "NotRegistered"}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sid+"/queue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sid, "")

	if w.Code != http.StatusCreated {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}
}

// TestHandleSessionQueue_Add_PromptWithoutRequiredParamsSucceeds verifies
// the guard is a no-op for prompts that declare no `required: true`
// parameters, preserving the prior behavior of the queue-add endpoint.
func TestHandleSessionQueue_Add_PromptWithoutRequiredParamsSucceeds(t *testing.T) {
	h, _, sid := buildRequiredArgsQueueHandlers(t, []config.WebPrompt{{
		Name: "SimplePrompt",
		Parameters: []config.PromptParameter{
			{Name: "X", Type: "text"}, // no Required
			{Name: "Y", Type: "text", Required: boolPtr(false)},
		},
	}})

	body := `{"prompt_name": "SimplePrompt"}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sid+"/queue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sid, "")

	if w.Code != http.StatusCreated {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}
}

// TestHandleSessionQueue_Add_RequiredArgGuard_CaseInsensitivePromptName
// verifies the guard resolves the prompt name via case-insensitive match,
// matching the rememberScopedArgsForQueueAdd resolution semantics.
func TestHandleSessionQueue_Add_RequiredArgGuard_CaseInsensitivePromptName(t *testing.T) {
	h, _, sid := buildRequiredArgsQueueHandlers(t, []config.WebPrompt{{
		Name: "TestPrompt",
		Parameters: []config.PromptParameter{
			{Name: "URL", Type: "text", Required: boolPtr(true)},
		},
	}})

	// Client sends the prompt name in a different case than what is
	// registered — the guard must still find it and reject the missing arg.
	body := `{"prompt_name": "testprompt"}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sid+"/queue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sid, "")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

// TestHandleSessionQueue_Add_RequiredArgGuard_FailsOpenWithoutDeps verifies
// the guard falls through (does not 500 or 400) when SessionManager or
// GetWorkspacePromptsAll is nil — the fail-open contract preserves the
// existing queue-add behavior under test harnesses that don't wire those
// deps (mitto-gtf implementation decision).
func TestHandleSessionQueue_Add_RequiredArgGuard_FailsOpenWithoutDeps(t *testing.T) {
	// No SessionManager, no GetWorkspacePromptsAll wired.
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	const sid = "20260729-160500-failopen"
	if err := store.Create(session.Metadata{SessionID: sid, Status: "active"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	h := New(Deps{Store: store})

	// A prompt_name-only request with no arguments. The guard cannot resolve
	// the prompt without deps and must fall through to the normal path.
	body := `{"prompt_name": "AnyPrompt"}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sid+"/queue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sid, "")

	if w.Code != http.StatusCreated {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}
}
