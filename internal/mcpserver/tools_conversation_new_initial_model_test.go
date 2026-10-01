package mcpserver

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/prompts"
	"github.com/inercia/mitto/internal/session"
)

// setupInitialModelServer builds a server wired to a mockSessionManager so tests
// can observe which prompt name the genuine-new-child resume step received.
func setupInitialModelServer(t *testing.T) (*Server, *mockSessionManager, string) {
	t.Helper()
	return setupInitialModelServerWithPrompts(t, []config.WebPrompt{
		{Name: "Start work", Prompt: "do the work"},
		{Name: "Loop body", Prompt: "iterate"},
	})
}

func setupInitialModelServerWithPrompts(t *testing.T, webPrompts []config.WebPrompt) (*Server, *mockSessionManager, string) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	parent := session.Metadata{
		SessionID:  session.GenerateSessionID(),
		Name:       "Parent",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		AdvancedSettings: map[string]bool{
			session.FlagCanStartConversation: true,
		},
	}
	if err := store.Create(parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}

	mockSM := &mockSessionManager{
		workspacesForFolder: []config.WorkspaceSettings{
			{ACPServer: "test-server", WorkingDir: "/test/dir"},
		},
	}
	srv, err := NewServer(Config{Port: 0}, Dependencies{
		Store:          store,
		SessionManager: mockSM,
		Config:         &config.Config{Prompts: webPrompts},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(parent.SessionID, nil, logger); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}
	return srv, mockSM, parent.SessionID
}

// A genuinely new child created from a named initial prompt must hand that
// prompt name to the resume step so its preferredModels seed the baseline.
func TestConversationNew_NamedPrompt_PassesPromptNameForInitialModel(t *testing.T) {
	srv, mockSM, parentID := setupInitialModelServer(t)

	_, out, err := srv.handleConversationStart(context.Background(), nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Start work",
	})
	if err != nil {
		t.Fatalf("handleConversationStart: %v", err)
	}
	if out.SessionID == "" {
		t.Fatalf("expected session id, got error %q", out.Error)
	}
	if len(mockSM.resumePromptNames) != 1 || mockSM.resumePromptNames[0] != "Start work" {
		t.Fatalf("resumePromptNames = %v, want [Start work]", mockSM.resumePromptNames)
	}
}

// With only a named loop prompt (plus a free-text seed), the loop prompt's
// preferences are used.
func TestConversationNew_NamedLoopPrompt_PassesPromptNameForInitialModel(t *testing.T) {
	srv, mockSM, parentID := setupInitialModelServer(t)

	_, out, err := srv.handleConversationStart(context.Background(), nil, ConversationStartInput{
		SelfID:             parentID,
		InitialPrompt:      "seed",
		LoopPromptName:     "Loop body",
		LoopFrequencyValue: 5,
		LoopFrequencyUnit:  "minutes",
	})
	if err != nil {
		t.Fatalf("handleConversationStart: %v", err)
	}
	if out.SessionID == "" {
		t.Fatalf("expected session id, got error %q", out.Error)
	}
	if len(mockSM.resumePromptNames) != 1 || mockSM.resumePromptNames[0] != "Loop body" {
		t.Fatalf("resumePromptNames = %v, want [Loop body]", mockSM.resumePromptNames)
	}
}

// Free-text prompts keep the workspace/ACP initial-model precedence untouched.
func TestConversationNew_FreeTextPrompt_PassesNoPromptNameForInitialModel(t *testing.T) {
	srv, mockSM, parentID := setupInitialModelServer(t)

	_, out, err := srv.handleConversationStart(context.Background(), nil, ConversationStartInput{
		SelfID:        parentID,
		InitialPrompt: "just text",
	})
	if err != nil {
		t.Fatalf("handleConversationStart: %v", err)
	}
	if out.SessionID == "" {
		t.Fatalf("expected session id, got error %q", out.Error)
	}
	if len(mockSM.resumePromptNames) != 1 || mockSM.resumePromptNames[0] != "" {
		t.Fatalf("resumePromptNames = %v, want [\"\"]", mockSM.resumePromptNames)
	}
}

// A reuse hit routes to an existing conversation: it must not run the
// genuine-new resume step, so no prompt preference is resolved for it.
func TestConversationNew_ReuseHit_DoesNotResolveInitialModelPreference(t *testing.T) {
	srv, mockSM, parentID := setupInitialModelServerWithPrompts(t, []config.WebPrompt{{
		Name:   "Work on issue",
		Prompt: "do work",
		Target: &prompts.PromptTarget{Reuse: &prompts.PromptTargetReuse{Issue: true}},
	}})
	ctx := context.Background()
	in := ConversationStartInput{SelfID: parentID, PromptName: "Work on issue", BeadsIssue: "mitto-abc"}

	_, first, err := srv.handleConversationStart(ctx, nil, in)
	if err != nil || first.SessionID == "" {
		t.Fatalf("first call: err=%v out=%+v", err, first)
	}
	if len(mockSM.resumePromptNames) != 1 {
		t.Fatalf("after create resumePromptNames = %v, want 1 entry", mockSM.resumePromptNames)
	}

	_, second, err := srv.handleConversationStart(ctx, nil, in)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !second.Reused || second.SessionID != first.SessionID {
		t.Fatalf("expected reuse of %q, got %+v", first.SessionID, second)
	}
	if len(mockSM.resumePromptNames) != 1 {
		t.Errorf("reuse hit resolved a prompt model preference: resumePromptNames = %v", mockSM.resumePromptNames)
	}
}
