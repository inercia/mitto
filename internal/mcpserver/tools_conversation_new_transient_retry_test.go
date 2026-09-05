// tools_conversation_new_transient_retry_test.go: verifies the mitto-nf6 fix —
// when mitto_conversation_new's initial ResumeSession call for the new child
// hits a transient cold-start MCP-init timeout, handleConversationStart must
// NOT surface a hard error, and must schedule a bounded background retry
// (scheduleBoundedResumeRetry, tools_children.go) that eventually resumes the
// child and delivers its queued initial prompt.
package mcpserver

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

func TestConversationStart_TransientResumeFailure_SchedulesRetryAndDeliversQueuedPrompt(t *testing.T) {
	oldDelays := childResumeRetryDelays
	childResumeRetryDelays = []time.Duration{0}
	t.Cleanup(func() { childResumeRetryDelays = oldDelays })

	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("session.NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	parentID := session.GenerateSessionID()
	parentMeta := session.Metadata{
		SessionID:  parentID,
		Name:       "Parent Session",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		AdvancedSettings: map[string]bool{
			session.FlagCanStartConversation: true,
		},
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("store.Create(parent): %v", err)
	}

	transientErr := fmt.Errorf("MCP initialization timed out after 240s")
	mockBS := &mockBackgroundSessionForAutoResume{}
	sm := &mockSessionManagerForAutoResume{
		sessions: map[string]BackgroundSession{
			parentID: &mockBackgroundSessionForAutoResume{}, // parent is running
		},
		// First ResumeSession call (foreground, inside handleConversationStart)
		// fails transiently; the retry's ResumeSession call then succeeds.
		resumeErrs:                []error{transientErr, nil},
		resumeErrIsMCPInitTimeout: true,
		resumeResult:              mockBS,
		workspacesForFolder: []config.WorkspaceSettings{
			{WorkingDir: "/test/dir", ACPServer: "test-server"},
		},
	}

	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store, SessionManager: sm})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	sm.onResume = func(sessionID string) { _ = srv.RegisterSession(sessionID, nil, logger) }
	if err := srv.RegisterSession(parentID, nil, logger); err != nil {
		t.Fatalf("RegisterSession(parent): %v", err)
	}

	ctx := context.Background()
	_, output, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:        parentID,
		Title:         "New Child",
		InitialPrompt: "Hello from transient test",
	})
	if err != nil {
		t.Fatalf("handleConversationStart: unexpected error for transient resume failure: %v", err)
	}
	if output.SessionID == "" {
		t.Fatal("expected a non-empty session ID even though the foreground resume failed transiently")
	}
	if output.IsRunning {
		t.Error("expected IsRunning=false immediately after a failed foreground resume")
	}

	// The initial prompt must have been queued despite the foreground resume failure.
	queue := store.Queue(output.SessionID)
	msgs, err := queue.List()
	if err != nil {
		t.Fatalf("queue.List(): %v", err)
	}
	if len(msgs) != 1 || msgs[0].Message != "Hello from transient test" {
		t.Fatalf("expected the initial prompt to be queued, got %+v", msgs)
	}

	// Wait for the bounded background retry to succeed and kick the queue.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !mockBS.tryProcessCalled.Load() {
		time.Sleep(10 * time.Millisecond)
	}
	if !mockBS.tryProcessCalled.Load() {
		t.Fatal("expected the bounded retry to eventually resume the child and process its queued prompt")
	}

	sm.mu.Lock()
	resumeCalls := len(sm.resumeCalls)
	sm.mu.Unlock()
	if resumeCalls < 2 {
		t.Fatalf("expected at least 2 ResumeSession calls (foreground + retry), got %d", resumeCalls)
	}
}
