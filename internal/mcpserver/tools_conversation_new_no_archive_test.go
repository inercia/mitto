// tools_conversation_new_no_archive_test.go: tests for target.noArchive
// (mitto-yvel.2) on the MCP create path in handleConversationStart. Mirrors
// the setup/shape of tools_conversation_new_background_color_test.go.
package mcpserver

import (
	"context"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/prompts"
	"github.com/inercia/mitto/internal/session"
)

// TestConversationStart_NoArchive_AppliedOnCreate verifies that a prompt
// declaring target.noArchive: true causes the newly created conversation's
// metadata to carry NoArchive=true.
func TestConversationStart_NoArchive_AppliedOnCreate(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "Protected prompt",
			Prompt: "do work",
			Target: &prompts.PromptTarget{NoArchive: true},
		},
	})

	ctx := context.Background()
	_, output, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Protected prompt",
	})
	if err != nil {
		t.Fatalf("handleConversationStart: unexpected error: %v", err)
	}
	if output.SessionID == "" {
		t.Fatal("expected non-empty session ID")
	}

	meta, err := store.GetMetadata(output.SessionID)
	if err != nil {
		t.Fatalf("GetMetadata(%q) error: %v", output.SessionID, err)
	}
	if !meta.NoArchive {
		t.Errorf("NoArchive = false, want true for a conversation created from a target.noArchive:true prompt")
	}
}

// TestConversationStart_NoArchive_AbsentWhenPromptHasNone verifies that a
// prompt with no target.noArchive leaves the new conversation's flag unset —
// existing prompts must behave exactly as before mitto-yvel.2.
func TestConversationStart_NoArchive_AbsentWhenPromptHasNone(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{Name: "Plain prompt", Prompt: "do work"},
	})

	ctx := context.Background()
	_, output, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Plain prompt",
	})
	if err != nil {
		t.Fatalf("handleConversationStart: unexpected error: %v", err)
	}

	meta, err := store.GetMetadata(output.SessionID)
	if err != nil {
		t.Fatalf("GetMetadata(%q) error: %v", output.SessionID, err)
	}
	if meta.NoArchive {
		t.Errorf("NoArchive = true, want false for a prompt with no target.noArchive")
	}
}

// TestConversationStart_ReuseIssue_DoesNotOverwriteNoArchive_Unprotected
// verifies that funneling a dispatch into an EXISTING, unprotected
// conversation via target.reuse.issue never re-applies the prompt's
// target.noArchive: true — a creation-time-only flag must not be retroactively
// applied on later dispatches (epic decision 4).
func TestConversationStart_ReuseIssue_DoesNotOverwriteNoArchive_Unprotected(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "Work on issue",
			Prompt: "do work on {{ .Args.IssueID }}",
			Target: &prompts.PromptTarget{
				NoArchive: true, // would-be default, must NOT apply on reuse
				Reuse:     &prompts.PromptTargetReuse{Issue: true},
			},
		},
	})

	existingID := session.GenerateSessionID()
	if err := store.Create(session.Metadata{
		SessionID:  existingID,
		Name:       "Existing",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		BeadsIssue: "mitto-noarchive-a",
		NoArchive:  false, // unprotected: must stay unprotected
		UpdatedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("store.Create(existing) error: %v", err)
	}

	ctx := context.Background()
	_, output, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Work on issue",
		BeadsIssue: "mitto-noarchive-a",
	})
	if err != nil {
		t.Fatalf("handleConversationStart: unexpected error: %v", err)
	}
	if !output.Reused || output.SessionID != existingID {
		t.Fatalf("expected reuse of %q, got SessionID=%q Reused=%v", existingID, output.SessionID, output.Reused)
	}

	meta, err := store.GetMetadata(existingID)
	if err != nil {
		t.Fatalf("GetMetadata(%q) error: %v", existingID, err)
	}
	if meta.NoArchive {
		t.Errorf("NoArchive = true after reuseIssue hit, want unchanged false (an unprotected conversation must stay unprotected)")
	}
}

// TestConversationStart_ReuseIssue_DoesNotOverwriteNoArchive_Protected is the
// mirror direction: a reuse dispatch via a prompt WITHOUT target.noArchive
// must not clear an existing PROTECTED conversation's flag — this is the
// direction that would catch an accidental unconditional write.
func TestConversationStart_ReuseIssue_DoesNotOverwriteNoArchive_Protected(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "Work on issue",
			Prompt: "do work on {{ .Args.IssueID }}",
			Target: &prompts.PromptTarget{
				Reuse: &prompts.PromptTargetReuse{Issue: true}, // no NoArchive here
			},
		},
	})

	existingID := session.GenerateSessionID()
	if err := store.Create(session.Metadata{
		SessionID:  existingID,
		Name:       "Existing protected",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
		BeadsIssue: "mitto-noarchive-b",
		NoArchive:  true, // protected: must stay protected
		UpdatedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("store.Create(existing) error: %v", err)
	}

	ctx := context.Background()
	_, output, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Work on issue",
		BeadsIssue: "mitto-noarchive-b",
	})
	if err != nil {
		t.Fatalf("handleConversationStart: unexpected error: %v", err)
	}
	if !output.Reused || output.SessionID != existingID {
		t.Fatalf("expected reuse of %q, got SessionID=%q Reused=%v", existingID, output.SessionID, output.Reused)
	}

	meta, err := store.GetMetadata(existingID)
	if err != nil {
		t.Fatalf("GetMetadata(%q) error: %v", existingID, err)
	}
	if !meta.NoArchive {
		t.Errorf("NoArchive = false after reuseIssue hit, want unchanged true (a protected conversation must stay protected)")
	}
}
