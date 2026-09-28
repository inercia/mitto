package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// buildMoveAgentHandlers wires up a Handlers with a real Store and a
// SessionManager holding two ACP servers ("agent-a", "agent-b") each with a
// workspace for the same workingDir, mirroring
// newMoveAgentTestManager in internal/conversation/session_manager_move_agent_test.go.
func buildMoveAgentHandlers(t *testing.T) (h *Handlers, store *session.Store, workingDir string) {
	t.Helper()
	workingDir = t.TempDir()

	var err error
	store, err = session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	sm := conversation.NewSessionManagerWithOptions(conversation.SessionManagerOptions{
		Workspaces: []config.WorkspaceSettings{
			{UUID: "ws-a", ACPServer: "agent-a", WorkingDir: workingDir},
			{UUID: "ws-b", ACPServer: "agent-b", WorkingDir: workingDir},
		},
	})
	sm.SetStore(store)
	mittoConfig := &config.Config{
		ACPServers: []config.ACPServer{
			{Name: "agent-a", Command: "echo test"},
			{Name: "agent-b", Command: "echo test"},
		},
	}
	sm.SetMittoConfig(mittoConfig)

	h = New(Deps{
		Store:          store,
		SessionManager: sm,
		MittoConfig:    mittoConfig,
	})
	return h, store, workingDir
}

func doMoveAgentPreflight(h *Handlers, sessionID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID+"/move-agent/preflight", nil)
	w := httptest.NewRecorder()
	h.HandleSessionMoveAgentPreflight(w, req, sessionID)
	return w
}

func doMoveAgentExecute(h *Handlers, sessionID string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/move-agent", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.HandleSessionMoveAgentExecute(w, req, sessionID)
	return w
}

// --- Preflight ---

func TestHandleSessionMoveAgentPreflight_MethodNotAllowed(t *testing.T) {
	h, _, _ := buildMoveAgentHandlers(t)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/any/move-agent/preflight", nil)
	w := httptest.NewRecorder()

	h.HandleSessionMoveAgentPreflight(w, req, "any")

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusMethodNotAllowed, w.Body.String())
	}
}

func TestHandleSessionMoveAgentPreflight_SessionNotFound(t *testing.T) {
	h, _, _ := buildMoveAgentHandlers(t)

	w := doMoveAgentPreflight(h, "does-not-exist")

	if w.Code != http.StatusNotFound {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestHandleSessionMoveAgentPreflight_Success(t *testing.T) {
	h, store, workingDir := buildMoveAgentHandlers(t)

	if err := store.Create(session.Metadata{
		SessionID: "s1", ACPServer: "agent-a", WorkingDir: workingDir, BaselineModel: "gpt-x",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// A non-archived child bound to the current agent, in the same folder —
	// should be counted.
	if err := store.Create(session.Metadata{
		SessionID: "s1-child", ACPServer: "agent-a", WorkingDir: workingDir, ParentSessionID: "s1",
	}); err != nil {
		t.Fatalf("Create child: %v", err)
	}

	w := doMoveAgentPreflight(h, "s1")

	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp conversation.MoveAgentPreflight
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.CurrentAgent != "agent-a" {
		t.Errorf("current_agent = %q, want %q", resp.CurrentAgent, "agent-a")
	}
	if resp.Busy {
		t.Errorf("busy = true, want false")
	}
	if resp.Archived {
		t.Errorf("archived = true, want false")
	}
	if resp.BaselineModel != "gpt-x" {
		t.Errorf("baseline_model = %q, want %q", resp.BaselineModel, "gpt-x")
	}
	if resp.ChildrenCount != 1 {
		t.Errorf("children_count = %d, want 1", resp.ChildrenCount)
	}
	if len(resp.Candidates) != 1 || resp.Candidates[0].Name != "agent-b" {
		t.Fatalf("candidates = %+v, want exactly [agent-b]", resp.Candidates)
	}
	if !resp.Candidates[0].Available {
		t.Errorf("candidates[0].Available = false, want true")
	}
}

func TestHandleSessionMoveAgentPreflight_ReportsLoopInfo(t *testing.T) {
	h, store, workingDir := buildMoveAgentHandlers(t)

	if err := store.Create(session.Metadata{
		SessionID: "s1", ACPServer: "agent-a", WorkingDir: workingDir,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Loop("s1").Set(&session.LoopPrompt{
		PromptName: "my-loop-prompt",
		Frequency:  session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Loop Set: %v", err)
	}

	w := doMoveAgentPreflight(h, "s1")
	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp conversation.MoveAgentPreflight
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.IsLoop {
		t.Errorf("is_loop = false, want true")
	}
	if resp.LoopPromptName != "my-loop-prompt" {
		t.Errorf("loop_prompt_name = %q, want %q", resp.LoopPromptName, "my-loop-prompt")
	}
	// LoopPromptAvailable is name-only resolution (documented limitation):
	// "my-loop-prompt" is not configured anywhere, so it must resolve false
	// for every candidate rather than being left nil (nil would mean "not
	// computed", but the handler always computes it when IsLoop is true).
	if len(resp.Candidates) != 1 {
		t.Fatalf("candidates = %+v, want exactly one", resp.Candidates)
	}
	if resp.Candidates[0].LoopPromptAvailable == nil || *resp.Candidates[0].LoopPromptAvailable {
		t.Errorf("candidates[0].LoopPromptAvailable = %v, want non-nil false", resp.Candidates[0].LoopPromptAvailable)
	}
}

// --- Execute ---

func TestHandleSessionMoveAgentExecute_MethodNotAllowed(t *testing.T) {
	h, _, _ := buildMoveAgentHandlers(t)
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/any/move-agent", nil)
	w := httptest.NewRecorder()

	h.HandleSessionMoveAgentExecute(w, req, "any")

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusMethodNotAllowed, w.Body.String())
	}
}

func TestHandleSessionMoveAgentExecute_BadBody(t *testing.T) {
	h, store, workingDir := buildMoveAgentHandlers(t)
	if err := store.Create(session.Metadata{SessionID: "s1", ACPServer: "agent-a", WorkingDir: workingDir}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	w := doMoveAgentExecute(h, "s1", "{not json")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestHandleSessionMoveAgentExecute_MissingTargetAgent(t *testing.T) {
	h, store, workingDir := buildMoveAgentHandlers(t)
	if err := store.Create(session.Metadata{SessionID: "s1", ACPServer: "agent-a", WorkingDir: workingDir}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	w := doMoveAgentExecute(h, "s1", `{}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestHandleSessionMoveAgentExecute_SessionNotFound(t *testing.T) {
	h, _, _ := buildMoveAgentHandlers(t)

	w := doMoveAgentExecute(h, "does-not-exist", `{"target_agent":"agent-b"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestHandleSessionMoveAgentExecute_Archived(t *testing.T) {
	h, store, workingDir := buildMoveAgentHandlers(t)
	if err := store.Create(session.Metadata{
		SessionID: "s1", ACPServer: "agent-a", WorkingDir: workingDir, Archived: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	w := doMoveAgentExecute(h, "s1", `{"target_agent":"agent-b"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusConflict, w.Body.String())
	}
}

func TestHandleSessionMoveAgentExecute_Busy(t *testing.T) {
	h, store, workingDir := buildMoveAgentHandlers(t)
	if err := store.Create(session.Metadata{SessionID: "s1", ACPServer: "agent-a", WorkingDir: workingDir}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bs := conversation.NewTestBackgroundSessionPromptingWithCtx("s1", true, ctx, cancel)
	h.deps.SessionManager.AddSessionForTest(bs)

	w := doMoveAgentExecute(h, "s1", `{"target_agent":"agent-b"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusConflict, w.Body.String())
	}
}

func TestHandleSessionMoveAgentExecute_SameAgent(t *testing.T) {
	h, store, workingDir := buildMoveAgentHandlers(t)
	if err := store.Create(session.Metadata{SessionID: "s1", ACPServer: "agent-a", WorkingDir: workingDir}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	w := doMoveAgentExecute(h, "s1", `{"target_agent":"agent-a"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestHandleSessionMoveAgentExecute_UnknownTargetAgent(t *testing.T) {
	h, store, workingDir := buildMoveAgentHandlers(t)
	if err := store.Create(session.Metadata{SessionID: "s1", ACPServer: "agent-a", WorkingDir: workingDir}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	w := doMoveAgentExecute(h, "s1", `{"target_agent":"agent-does-not-exist"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestHandleSessionMoveAgentExecute_NoWorkspaceForTarget(t *testing.T) {
	h, store, workingDir := buildMoveAgentHandlers(t)
	// A third agent is configured but has no workspace for this workingDir.
	mittoConfig := &config.Config{
		ACPServers: []config.ACPServer{
			{Name: "agent-a", Command: "echo test"},
			{Name: "agent-b", Command: "echo test"},
			{Name: "agent-c", Command: "echo test"},
		},
	}
	h.deps.SessionManager.SetMittoConfig(mittoConfig)
	h.deps.MittoConfig = mittoConfig

	if err := store.Create(session.Metadata{SessionID: "s1", ACPServer: "agent-a", WorkingDir: workingDir}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	w := doMoveAgentExecute(h, "s1", `{"target_agent":"agent-c"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestHandleSessionMoveAgentExecute_Success(t *testing.T) {
	h, store, workingDir := buildMoveAgentHandlers(t)
	if err := store.Create(session.Metadata{
		SessionID: "s1", ACPServer: "agent-a", WorkingDir: workingDir, BaselineModel: "gpt-x",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var broadcasts []string
	h.deps.BroadcastSessionAgentMoved = func(sessionID, newAgent, previousAgent string) {
		broadcasts = append(broadcasts, sessionID+":"+newAgent+":"+previousAgent)
	}

	w := doMoveAgentExecute(h, "s1", `{"target_agent":"agent-b"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["previous_agent"] != "agent-a" {
		t.Errorf("previous_agent = %v, want %q", resp["previous_agent"], "agent-a")
	}
	if resp["previous_baseline_model"] != "gpt-x" {
		t.Errorf("previous_baseline_model = %v, want %q", resp["previous_baseline_model"], "gpt-x")
	}
	moved, _ := resp["moved"].([]interface{})
	if len(moved) != 1 || moved[0] != "s1" {
		t.Fatalf("moved = %v, want [s1]", resp["moved"])
	}

	meta, err := store.GetMetadata("s1")
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if meta.ACPServer != "agent-b" {
		t.Errorf("ACPServer = %q, want %q", meta.ACPServer, "agent-b")
	}

	if len(broadcasts) != 1 || broadcasts[0] != "s1:agent-b:agent-a" {
		t.Fatalf("broadcasts = %v, want exactly [\"s1:agent-b:agent-a\"]", broadcasts)
	}
}
