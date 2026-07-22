// tools_conversation_model_tag_test.go: unit tests for the model_tag path on
// mitto_conversation_update and mitto_conversation_new (mitto-41o1).
//
// The tests exercise the handler wiring around the BackgroundSession.ApplyModelTag
// boundary method — the tag-resolution semantics themselves live inside
// (*BackgroundSession).ApplyModelTag in the conversation package and are covered
// by SelectPreferredModel's own tests. Here we only need to verify that the MCP
// tool handlers correctly forward the requested tag, surface errors loudly, and
// distinguish the "not running" and "self alias" branches.
package mcpserver

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// mockBackgroundSessionForModelTag is a stub BackgroundSession that records
// ApplyModelTag invocations and returns caller-configurable results. Only the
// methods the model_tag path calls are non-trivial; everything else on the
// BackgroundSession interface is a no-op stub.
type mockBackgroundSessionForModelTag struct {
	mu            sync.Mutex
	applyCalls    []string     // tags received, in order
	applyResolved string       // resolved id to return from ApplyModelTag
	applyErr      error        // error to return from ApplyModelTag
	titleFromLoop atomic.Int32 // TriggerTitleGenerationFromLoop calls (needed by conversation_new tests with no title)
	queueConfig   *config.QueueConfig
}

func (m *mockBackgroundSessionForModelTag) ApplyModelTag(_ context.Context, tag string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.applyCalls = append(m.applyCalls, tag)
	return m.applyResolved, m.applyErr
}

func (m *mockBackgroundSessionForModelTag) callsSnapshot() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.applyCalls))
	copy(out, m.applyCalls)
	return out
}

// Remaining BackgroundSession methods — no-op stubs.
func (m *mockBackgroundSessionForModelTag) IsPrompting() bool                   { return false }
func (m *mockBackgroundSessionForModelTag) HasQueuedDeliveryInProgress() bool   { return false }
func (m *mockBackgroundSessionForModelTag) GetQueueConfig() *config.QueueConfig { return m.queueConfig }
func (m *mockBackgroundSessionForModelTag) GetEventCount() int                  { return 0 }
func (m *mockBackgroundSessionForModelTag) GetMaxAssignedSeq() int64            { return 0 }
func (m *mockBackgroundSessionForModelTag) TryProcessQueuedMessage() bool       { return false }
func (m *mockBackgroundSessionForModelTag) TriggerTitleGeneration(string)       {}
func (m *mockBackgroundSessionForModelTag) TriggerTitleGenerationFromLoop(string, string) {
	m.titleFromLoop.Add(1)
}
func (m *mockBackgroundSessionForModelTag) RequestSelfDestruct() {}
func (m *mockBackgroundSessionForModelTag) LastQueuedSendError() (string, time.Time) {
	return "", time.Time{}
}
func (m *mockBackgroundSessionForModelTag) RecordChildWait(time.Duration) {}
func (m *mockBackgroundSessionForModelTag) WaitForResponseComplete(time.Duration) bool {
	return true
}
func (m *mockBackgroundSessionForModelTag) ActivePromptDispatch() (string, map[string]string, bool) {
	return "", nil, false
}

// mockSessionManagerForModelTag is a SessionManager stub whose GetSession and
// ResumeSession return a caller-provided *mockBackgroundSessionForModelTag. Any
// session id not in the map returns nil (mirrors production's "not running").
type mockSessionManagerForModelTag struct {
	mu                  sync.Mutex
	sessions            map[string]*mockBackgroundSessionForModelTag
	resumeMock          *mockBackgroundSessionForModelTag // returned from ResumeSession and inserted at that key
	workspacesForFolder []config.WorkspaceSettings
	broadcastCreated    []broadcastCall
}

func newMockSessionManagerForModelTag() *mockSessionManagerForModelTag {
	return &mockSessionManagerForModelTag{
		sessions: make(map[string]*mockBackgroundSessionForModelTag),
	}
}

func (m *mockSessionManagerForModelTag) GetSession(sessionID string) BackgroundSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	bs, ok := m.sessions[sessionID]
	if !ok {
		return nil
	}
	return bs
}

func (m *mockSessionManagerForModelTag) ListRunningSessions() []string { return nil }
func (m *mockSessionManagerForModelTag) CloseSessionGracefully(string, string, time.Duration) bool {
	return true
}
func (m *mockSessionManagerForModelTag) CloseSession(string, string) {}
func (m *mockSessionManagerForModelTag) ResumeSession(sessionID, _, _ string) (BackgroundSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.resumeMock != nil {
		m.sessions[sessionID] = m.resumeMock
		return m.resumeMock, nil
	}
	return nil, nil
}
func (m *mockSessionManagerForModelTag) GetWorkspacesForFolder(string) []config.WorkspaceSettings {
	return m.workspacesForFolder
}
func (m *mockSessionManagerForModelTag) BroadcastSessionCreated(sessionID, name, acpServer, workingDir, parentSessionID, _ string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.broadcastCreated = append(m.broadcastCreated, broadcastCall{
		sessionID:       sessionID,
		name:            name,
		acpServer:       acpServer,
		workingDir:      workingDir,
		parentSessionID: parentSessionID,
	})
}
func (m *mockSessionManagerForModelTag) BroadcastSessionArchived(string, bool, ...session.ArchiveReason) {
}
func (m *mockSessionManagerForModelTag) BroadcastSessionDeleted(string)            {}
func (m *mockSessionManagerForModelTag) BroadcastWaitingForChildren(string, bool)  {}
func (m *mockSessionManagerForModelTag) DeleteChildSessions(string)                {}
func (m *mockSessionManagerForModelTag) GetWorkspaces() []config.WorkspaceSettings { return nil }
func (m *mockSessionManagerForModelTag) GetWorkspaceByUUID(string) *config.WorkspaceSettings {
	return nil
}
func (m *mockSessionManagerForModelTag) BroadcastSessionRenamed(string, string)           {}
func (m *mockSessionManagerForModelTag) BroadcastSessionBeadsIssueUpdated(string, string) {}
func (m *mockSessionManagerForModelTag) BroadcastLoopUpdated(string, *session.LoopPrompt) {}
func (m *mockSessionManagerForModelTag) BroadcastWorkspaceUINotify(string, string, string, UINotifyRequest) {
}
func (m *mockSessionManagerForModelTag) GetUserDataSchema(string) *config.UserDataSchema { return nil }
func (m *mockSessionManagerForModelTag) GetWorkspacePrompts(string) []config.WebPrompt   { return nil }
func (m *mockSessionManagerForModelTag) GetWorkspacePromptsDirs(string) []string         { return nil }
func (m *mockSessionManagerForModelTag) GetWorkspaceRCLastModified(string) time.Time {
	return time.Time{}
}
func (m *mockSessionManagerForModelTag) GetWorkspace(string) *config.WorkspaceSettings { return nil }
func (m *mockSessionManagerForModelTag) InvalidateWorkspaceRC(string)                  {}
func (m *mockSessionManagerForModelTag) IsMCPInitTimeout(error) bool                   { return false }

// setupModelTagServer wires a Server with a session store + mockSessionManagerForModelTag,
// registers a caller session with can_start_conversation enabled, and returns
// the store, server, SM mock, and caller session id.
func setupModelTagServer(t *testing.T) (*session.Store, *Server, *mockSessionManagerForModelTag, string) {
	t.Helper()

	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	callerMeta := session.Metadata{
		SessionID:  session.GenerateSessionID(),
		Name:       "Caller",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		AdvancedSettings: map[string]bool{
			session.FlagCanStartConversation: true,
		},
	}
	if err := store.Create(callerMeta); err != nil {
		t.Fatalf("Create caller: %v", err)
	}

	sm := newMockSessionManagerForModelTag()
	sm.workspacesForFolder = []config.WorkspaceSettings{
		{ACPServer: "test-server", WorkingDir: "/test/dir"},
	}
	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store, SessionManager: sm})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(callerMeta.SessionID, nil, logger); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}

	return store, srv, sm, callerMeta.SessionID
}

// registerRunningTarget creates a second session in the store and registers a
// mock BackgroundSession for it in the SM so handleConversationUpdate treats it
// as a "running" target that can receive a model_tag.
func registerRunningTarget(t *testing.T, store *session.Store, srv *Server, sm *mockSessionManagerForModelTag) (string, *mockBackgroundSessionForModelTag) {
	t.Helper()

	targetMeta := session.Metadata{
		SessionID:  session.GenerateSessionID(),
		Name:       "Target",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(targetMeta); err != nil {
		t.Fatalf("Create target: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(targetMeta.SessionID, nil, logger); err != nil {
		t.Fatalf("RegisterSession target: %v", err)
	}
	mockBS := &mockBackgroundSessionForModelTag{applyResolved: "resolved-model-id"}
	sm.sessions[targetMeta.SessionID] = mockBS
	return targetMeta.SessionID, mockBS
}

// -----------------------------------------------------------------------------
// mitto_conversation_update model_tag tests
// -----------------------------------------------------------------------------

func TestConversationUpdate_ModelTag_Applies(t *testing.T) {
	store, srv, sm, callerID := setupModelTagServer(t)
	targetID, mockBS := registerRunningTarget(t, store, srv, sm)

	tag := "Coding"
	_, out, err := srv.handleConversationUpdate(context.Background(), nil, ConversationUpdateInput{
		SelfID:         callerID,
		ConversationID: targetID,
		ModelTag:       &tag,
	})
	if err != nil {
		t.Fatalf("handleConversationUpdate error: %v", err)
	}
	if !out.Success {
		t.Fatalf("expected Success, got Error=%q", out.Error)
	}

	calls := mockBS.callsSnapshot()
	if len(calls) != 1 || calls[0] != tag {
		t.Fatalf("ApplyModelTag calls = %v, want [%q]", calls, tag)
	}
	found := false
	for _, k := range out.Updated {
		if k == "model_tag" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Updated = %v, want to include 'model_tag'", out.Updated)
	}
}

func TestConversationUpdate_ModelTag_Empty_ClearsOverride(t *testing.T) {
	store, srv, sm, callerID := setupModelTagServer(t)
	targetID, mockBS := registerRunningTarget(t, store, srv, sm)

	empty := ""
	_, out, err := srv.handleConversationUpdate(context.Background(), nil, ConversationUpdateInput{
		SelfID:         callerID,
		ConversationID: targetID,
		ModelTag:       &empty,
	})
	if err != nil {
		t.Fatalf("handleConversationUpdate error: %v", err)
	}
	if !out.Success {
		t.Fatalf("expected Success on empty-tag clear, got Error=%q", out.Error)
	}

	// Empty tag must still forward to ApplyModelTag (which restores baseline);
	// the handler does NOT short-circuit — that would prevent callers from
	// clearing a transient override.
	calls := mockBS.callsSnapshot()
	if len(calls) != 1 || calls[0] != "" {
		t.Fatalf("ApplyModelTag calls = %v, want [\"\"]", calls)
	}
	found := false
	for _, k := range out.Updated {
		if k == "model_tag" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Updated = %v, want to include 'model_tag'", out.Updated)
	}
}

func TestConversationUpdate_ModelTag_TargetNotRunning(t *testing.T) {
	store, srv, sm, callerID := setupModelTagServer(t)

	// Create a target session in the STORE but do NOT register a running BS
	// for it in the SM: GetSession will return nil.
	targetMeta := session.Metadata{
		SessionID:  session.GenerateSessionID(),
		Name:       "Not-Running",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(targetMeta); err != nil {
		t.Fatalf("Create target: %v", err)
	}
	_ = sm // GetSession returns nil for unregistered ids by construction

	tag := "Coding"
	_, out, err := srv.handleConversationUpdate(context.Background(), nil, ConversationUpdateInput{
		SelfID:         callerID,
		ConversationID: targetMeta.SessionID,
		ModelTag:       &tag,
	})
	if err != nil {
		t.Fatalf("handleConversationUpdate error: %v", err)
	}
	if out.Success {
		t.Fatal("expected Success=false when target is not running")
	}
	if !strings.Contains(out.Error, "requires a running target conversation") {
		t.Errorf("Error = %q, want to contain 'requires a running target conversation'", out.Error)
	}
}

func TestConversationUpdate_ModelTag_ApplyError_Surfaced(t *testing.T) {
	store, srv, sm, callerID := setupModelTagServer(t)
	targetID, mockBS := registerRunningTarget(t, store, srv, sm)
	mockBS.applyErr = errors.New("tag %q did not resolve")

	tag := "NonexistentTier"
	_, out, err := srv.handleConversationUpdate(context.Background(), nil, ConversationUpdateInput{
		SelfID:         callerID,
		ConversationID: targetID,
		ModelTag:       &tag,
	})
	if err != nil {
		t.Fatalf("handleConversationUpdate error: %v", err)
	}
	if out.Success {
		t.Fatal("expected Success=false when ApplyModelTag returns error")
	}
	if !strings.Contains(out.Error, tag) {
		t.Errorf("Error = %q, want to contain the tag %q", out.Error, tag)
	}
	if !strings.Contains(out.Error, "did not resolve") {
		t.Errorf("Error = %q, want to contain the underlying error text", out.Error)
	}
}

func TestConversationUpdate_ModelTag_SelfAlias(t *testing.T) {
	_, srv, sm, callerID := setupModelTagServer(t)

	// Register a mock BS for the caller itself, then invoke with ConversationID="self".
	callerBS := &mockBackgroundSessionForModelTag{applyResolved: "resolved-self"}
	sm.sessions[callerID] = callerBS

	tag := "Reasoning"
	_, out, err := srv.handleConversationUpdate(context.Background(), nil, ConversationUpdateInput{
		SelfID:         callerID,
		ConversationID: "self",
		ModelTag:       &tag,
	})
	if err != nil {
		t.Fatalf("handleConversationUpdate error: %v", err)
	}
	if !out.Success {
		t.Fatalf("expected Success, got Error=%q", out.Error)
	}
	if out.ConversationID != callerID {
		t.Errorf("output ConversationID = %q, want caller id %q", out.ConversationID, callerID)
	}
	calls := callerBS.callsSnapshot()
	if len(calls) != 1 || calls[0] != tag {
		t.Fatalf("ApplyModelTag calls = %v, want [%q]", calls, tag)
	}
}

// -----------------------------------------------------------------------------
// mitto_conversation_new model_tag tests
// -----------------------------------------------------------------------------

func TestConversationStart_ModelTag_Applies(t *testing.T) {
	_, srv, sm, callerID := setupModelTagServer(t)

	// Arrange: ResumeSession will insert this mock BS under the newly-created
	// session id, so the handler's `bs != nil` guard passes and ApplyModelTag runs.
	sm.resumeMock = &mockBackgroundSessionForModelTag{applyResolved: "resolved-new"}

	tag := "Coding"
	_, out, err := srv.handleConversationStart(context.Background(), nil, ConversationStartInput{
		SelfID:   callerID,
		Title:    "Pinned Conversation",
		ModelTag: tag,
	})
	if err != nil {
		t.Fatalf("handleConversationStart error: %v", err)
	}
	if out.SessionID == "" {
		t.Fatal("expected non-empty SessionID")
	}
	calls := sm.resumeMock.callsSnapshot()
	if len(calls) != 1 || calls[0] != tag {
		t.Fatalf("ApplyModelTag calls = %v, want [%q]", calls, tag)
	}
}

func TestConversationStart_ModelTag_ApplyError_ReturnsError(t *testing.T) {
	_, srv, sm, callerID := setupModelTagServer(t)

	sm.resumeMock = &mockBackgroundSessionForModelTag{
		applyErr: errors.New("no available model matches tag"),
	}

	tag := "MissingTier"
	_, _, err := srv.handleConversationStart(context.Background(), nil, ConversationStartInput{
		SelfID:   callerID,
		Title:    "Failing Pin",
		ModelTag: tag,
	})
	if err == nil {
		t.Fatal("expected error from handleConversationStart when pinning fails, got nil")
	}
	if !strings.Contains(err.Error(), tag) {
		t.Errorf("error = %v, want to contain tag %q", err, tag)
	}
	if !strings.Contains(err.Error(), "no available model matches tag") {
		t.Errorf("error = %v, want to contain the wrapped ApplyModelTag error", err)
	}
	// The pin must have been ATTEMPTED — sanity-check the mock saw the call.
	calls := sm.resumeMock.callsSnapshot()
	if len(calls) != 1 || calls[0] != tag {
		t.Errorf("ApplyModelTag calls = %v, want [%q]", calls, tag)
	}
}

func TestConversationStart_ModelTag_Empty_Skipped(t *testing.T) {
	_, srv, sm, callerID := setupModelTagServer(t)

	sm.resumeMock = &mockBackgroundSessionForModelTag{applyResolved: "should-not-be-used"}

	_, out, err := srv.handleConversationStart(context.Background(), nil, ConversationStartInput{
		SelfID:   callerID,
		Title:    "Unpinned Conversation",
		ModelTag: "", // empty = do not pin at spawn (differs from update, which forwards "")
	})
	if err != nil {
		t.Fatalf("handleConversationStart error: %v", err)
	}
	if out.SessionID == "" {
		t.Fatal("expected non-empty SessionID")
	}
	calls := sm.resumeMock.callsSnapshot()
	if len(calls) != 0 {
		t.Errorf("ApplyModelTag calls = %v, want no calls when ModelTag is empty at spawn", calls)
	}
}
