// tools_conversation_new_reuse_test.go: tests for the reuseIssue find-or-route
// path in handleConversationStart (mitto-bx40). Mirrors the singleton parity
// tests in server_test.go — same setup helper, same shape.
package mcpserver

import (
	"context"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/prompts"
	"github.com/inercia/mitto/internal/session"
)

// TestConversationStart_ReuseIssue_RoutesToExisting verifies that when the
// prompt declares target.reuseIssue AND the call carries beads_issue, a second
// mitto_conversation_new for the same beads_issue in the same working dir
// routes to the existing conversation (reused=true) instead of creating a
// duplicate — MCP-path parity with the web find-or-route (mitto-bx40).
func TestConversationStart_ReuseIssue_RoutesToExisting(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "Work on issue",
			Prompt: "do work on {{ .Args.IssueID }}",
			Target: &prompts.PromptTarget{ReuseIssue: true},
		},
	})

	ctx := context.Background()

	_, first, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Work on issue",
		BeadsIssue: "mitto-abc",
	})
	if err != nil {
		t.Fatalf("First call: unexpected error: %v", err)
	}
	if first.SessionID == "" {
		t.Fatal("First call: expected a non-empty session ID")
	}
	if first.Reused {
		t.Error("First call: expected reused=false for the initial create")
	}

	afterFirst, err := store.List()
	if err != nil {
		t.Fatalf("store.List() error: %v", err)
	}

	_, second, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "work on ISSUE", // case-insensitive resolution
		BeadsIssue: "mitto-abc",
	})
	if err != nil {
		t.Fatalf("Second call: unexpected error: %v", err)
	}
	if !second.Reused {
		t.Error("Second call: expected reused=true")
	}
	if second.SessionID != first.SessionID {
		t.Errorf("Second call: expected existing session ID %q, got %q", first.SessionID, second.SessionID)
	}

	afterSecond, err := store.List()
	if err != nil {
		t.Fatalf("store.List() error: %v", err)
	}
	if len(afterSecond) != len(afterFirst) {
		t.Errorf("Second call created a duplicate: session count went from %d to %d",
			len(afterFirst), len(afterSecond))
	}
}

// TestConversationStart_ReuseIssue_NoBeadsIssue_CreatesNew verifies that a
// prompt with target.reuseIssue but a call WITHOUT beads_issue does NOT take
// the reuseIssue branch — the reuseIssue lookup keys off beads_issue, so
// omitting it must fall through to normal creation (parity with HTTP where
// the same guard is `req.BeadsIssue != ""`).
func TestConversationStart_ReuseIssue_NoBeadsIssue_CreatesNew(t *testing.T) {
	_, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "Work on issue",
			Prompt: "do work",
			Target: &prompts.PromptTarget{ReuseIssue: true},
		},
	})

	ctx := context.Background()

	_, first, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Work on issue",
	})
	if err != nil {
		t.Fatalf("First call: unexpected error: %v", err)
	}

	_, second, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Work on issue",
	})
	if err != nil {
		t.Fatalf("Second call: unexpected error: %v", err)
	}
	if second.Reused {
		t.Error("Second call: expected reused=false when beads_issue is absent")
	}
	if second.SessionID == first.SessionID {
		t.Error("Second call: expected a distinct session ID when beads_issue is absent")
	}
}

// TestConversationStart_ReuseIssue_DifferentIssue_CreatesNew verifies that a
// second call for the same reuseIssue prompt but with a DIFFERENT beads_issue
// still creates a new conversation — two distinct issues driven by the same
// prompt must not collapse into one conversation (the guard that also causes
// the singleton fallback to be skipped when reuseIssue is evaluated).
func TestConversationStart_ReuseIssue_DifferentIssue_CreatesNew(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "Work on issue",
			Prompt: "do work",
			// Also declare Singleton to prove reuseIssue takes precedence:
			// if the singleton branch had run, the second call (different
			// beads_issue but same prompt) would have collapsed.
			Singleton: true,
			Target:    &prompts.PromptTarget{ReuseIssue: true},
		},
	})

	ctx := context.Background()

	_, first, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Work on issue",
		BeadsIssue: "mitto-abc",
	})
	if err != nil {
		t.Fatalf("First call: unexpected error: %v", err)
	}

	_, second, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Work on issue",
		BeadsIssue: "mitto-def",
	})
	if err != nil {
		t.Fatalf("Second call: unexpected error: %v", err)
	}
	if second.Reused {
		t.Error("Second call: expected reused=false for a different beads_issue")
	}
	if second.SessionID == first.SessionID {
		t.Error("Second call: expected a distinct session ID for a different beads_issue")
	}

	metas, err := store.List()
	if err != nil {
		t.Fatalf("store.List() error: %v", err)
	}
	// parent + 2 distinct children
	if len(metas) != 3 {
		t.Errorf("Expected 3 sessions (parent + 2 children), got %d", len(metas))
	}
}

// TestConversationStart_ReuseIssue_NotDeclared_CreatesDuplicate verifies that
// a plain prompt (no target.reuseIssue) is NOT subject to per-issue
// find-or-route even when the call carries beads_issue: reuseIssue routing is
// authoritative only when the prompt opts in.
func TestConversationStart_ReuseIssue_NotDeclared_CreatesDuplicate(t *testing.T) {
	_, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{Name: "Plain work", Prompt: "do work"}, // no Target, no Singleton
	})

	ctx := context.Background()

	_, first, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Plain work",
		BeadsIssue: "mitto-abc",
	})
	if err != nil {
		t.Fatalf("First call: unexpected error: %v", err)
	}

	_, second, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Plain work",
		BeadsIssue: "mitto-abc",
	})
	if err != nil {
		t.Fatalf("Second call: unexpected error: %v", err)
	}
	if second.Reused {
		t.Error("Second call: expected reused=false when prompt does not declare target.reuseIssue")
	}
	if second.SessionID == first.SessionID {
		t.Error("Second call: expected a distinct session ID when prompt does not declare target.reuseIssue")
	}
}

// TestConversationStart_ReuseIssue_CrossWorkspaceIgnored verifies that a
// conversation with the matching beads_issue but in a DIFFERENT working_dir
// is NOT reused — the reuseIssue scan is scoped by target working_dir so two
// workspaces with the same bead ID stay isolated. Mirrors the HTTP behavior
// via session.FindConversationByBeadsIssue, which filters on WorkingDir.
func TestConversationStart_ReuseIssue_CrossWorkspaceIgnored(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "Work on issue",
			Prompt: "do work",
			Target: &prompts.PromptTarget{ReuseIssue: true},
		},
	})

	// Pre-create a matching session in a DIFFERENT working_dir. The parent
	// session (from the fixture) has WorkingDir="/test/dir", so the target
	// working_dir the handler will scope its scan by is "/test/dir".
	otherMeta := session.Metadata{
		SessionID:  session.GenerateSessionID(),
		Name:       "In another workspace",
		ACPServer:  "test-server",
		WorkingDir: "/other/dir",
		BeadsIssue: "mitto-abc",
	}
	if err := store.Create(otherMeta); err != nil {
		t.Fatalf("store.Create(otherMeta) error: %v", err)
	}

	ctx := context.Background()
	_, out, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Work on issue",
		BeadsIssue: "mitto-abc",
	})
	if err != nil {
		t.Fatalf("handleConversationStart: unexpected error: %v", err)
	}
	if out.Reused {
		t.Error("expected reused=false when the matching conversation lives in a different working_dir")
	}
	if out.SessionID == otherMeta.SessionID {
		t.Errorf("expected a distinct session ID; got the cross-workspace candidate %q", otherMeta.SessionID)
	}
}

// TestConversationStart_ReuseIssue_ArchivedIgnored verifies that an archived
// conversation with the matching beads_issue is NOT reused — a new
// conversation must be created. Mirrors the HTTP behavior via
// session.FindConversationByBeadsIssue, which filters archived sessions.
func TestConversationStart_ReuseIssue_ArchivedIgnored(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "Work on issue",
			Prompt: "do work",
			Target: &prompts.PromptTarget{ReuseIssue: true},
		},
	})

	ctx := context.Background()

	_, first, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Work on issue",
		BeadsIssue: "mitto-abc",
	})
	if err != nil {
		t.Fatalf("First call: unexpected error: %v", err)
	}

	// Archive the first conversation so it is no longer a reuse candidate.
	if err := store.UpdateMetadata(first.SessionID, func(m *session.Metadata) {
		m.Archived = true
		m.ArchiveReason = session.ArchiveReasonManual
	}); err != nil {
		t.Fatalf("UpdateMetadata error: %v", err)
	}

	_, second, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Work on issue",
		BeadsIssue: "mitto-abc",
	})
	if err != nil {
		t.Fatalf("Second call: unexpected error: %v", err)
	}
	if second.Reused {
		t.Error("Second call: expected reused=false when the prior matching conversation is archived")
	}
	if second.SessionID == first.SessionID {
		t.Error("Second call: expected a distinct session ID when the prior matching conversation is archived")
	}
}
