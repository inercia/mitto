package conversation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/processors"
	"github.com/inercia/mitto/internal/session"
)

func TestApplyOnCloseProcessors_DeletedSessionHistorySurvivesSpoolReplay(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	const (
		sessionID = "session-deleted-before-close-worker"
		wsUUID    = "workspace-close-history"
		acp       = "test-server"
		userText  = "remember the immutable-close-history marker"
		agentText = "the durable snapshot marker was observed"
	)
	workingDir := t.TempDir()
	if err := store.Create(session.Metadata{SessionID: sessionID, WorkingDir: workingDir, ACPServer: acp}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, event := range []session.Event{
		{Type: session.EventTypeUserPrompt, Data: session.UserPromptData{Message: userText}},
		{Type: session.EventTypeAgentMessage, Data: session.AgentMessageData{Text: "<p>" + agentText + "</p>"}},
	} {
		if err := store.AppendEvent(sessionID, event); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	processorDir := appdir.WorkspaceProcessorsDir(workingDir)
	if err := os.MkdirAll(processorDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	processorYAML := "name: close-history-probe\nwhen:\n  on: conversationClosed\n  match: all\nprompt: 'Analyze the captured source conversation.'\noutput: discard\n"
	if err := os.WriteFile(filepath.Join(processorDir, "close-history.yaml"), []byte(processorYAML), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	provider := &reapedCloseProvider{}
	spoolDir := t.TempDir()
	spool := &processors.FilePendingDispatchStore{BaseDir: spoolDir}
	sm := NewSessionManager("echo test", acp, true, nil)
	sm.SetStore(store)
	sm.SetProcessorManager(processors.NewManager("", nil))
	sm.SetPendingDispatchStore(spool)
	sm.SetAuxiliaryManager(auxiliary.NewWorkspaceAuxiliaryManager(provider, nil))
	sm.AddWorkspace(config.WorkspaceSettings{UUID: wsUUID, WorkingDir: workingDir, ACPServer: acp})
	t.Cleanup(sm.WaitForCloseProcessors)

	sm.ApplyOnCloseProcessors(sessionID, "deleted")
	if err := store.Delete(sessionID); err != nil {
		t.Fatalf("Delete immediately after ApplyOnCloseProcessors: %v", err)
	}
	sm.WaitForCloseProcessors()
	if _, err := store.ReadEvents(sessionID); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("ReadEvents after Delete error = %v, want ErrSessionNotFound", err)
	}

	var entries []processors.PendingDispatchEntry
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries, err = spool.Load(wsUUID)
		if err != nil {
			t.Fatalf("Load spooled close dispatch: %v", err)
		}
		if len(entries) == 1 && entries[0].ClaimedBy == "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(entries) != 1 {
		t.Fatalf("spooled entries = %d, want 1", len(entries))
	}
	if entries[0].ClaimedBy != "" {
		t.Fatalf("spooled entry remained claimed after dispatch failure: %#v", entries[0])
	}
	if !strings.Contains(entries[0].Prompt, userText) || !strings.Contains(entries[0].Prompt, agentText) {
		t.Fatalf("spooled prompt lost deleted source history: %q", entries[0].Prompt)
	}
	spoolPath := filepath.Join(spoolDir, wsUUID+".json")
	info, err := os.Stat(spoolPath)
	if err != nil {
		t.Fatalf("Stat spool: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("spool mode = %o, want 600", got)
	}

	var replayedPrompt string
	replay := processors.NewManager("", nil)
	replay.SetPendingDispatchStore(&processors.FilePendingDispatchStore{BaseDir: spoolDir})
	replay.SetPromptCompletionFunc(func(_ context.Context, _, _, _ string, prompt string) (processors.PromptCompletion, error) {
		replayedPrompt = prompt
		return processors.PromptCompletion{SaveCount: 1, SaveCountKnown: true}, nil
	})
	replay.FlushPendingDispatches(context.Background(), wsUUID)
	if !strings.Contains(replayedPrompt, userText) || !strings.Contains(replayedPrompt, agentText) {
		t.Fatalf("restart replay lost captured source history: %q", replayedPrompt)
	}
	remaining, err := spool.Load(wsUUID)
	if err != nil {
		t.Fatalf("Load after acknowledged replay: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("spool retained %d entries after acknowledged replay", len(remaining))
	}
}
