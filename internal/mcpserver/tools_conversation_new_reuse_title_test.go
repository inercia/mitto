// tools_conversation_new_reuse_title_test.go: tests for the reuseTitle
// find-or-route path in handleConversationStart (mitto-kybw). Mirrors the
// reuseIssue parity tests in tools_conversation_new_reuse_test.go — same
// setup helper, same shape.
package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/prompts"
	"github.com/inercia/mitto/internal/session"
)

// TestConversationStart_ReuseTitle_RoutesToExisting verifies that when the
// prompt declares target.reuseTitle (with a non-empty target.title), a second
// mitto_conversation_new for the same prompt in the same working dir routes
// to the existing conversation (reused=true) instead of creating a duplicate
// — MCP-path parity with the web find-or-route (mitto-kybw).
func TestConversationStart_ReuseTitle_RoutesToExisting(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "Weekly triage",
			Prompt: "triage {{ .Args.When }}",
			Target: &prompts.PromptTarget{Title: "Weekly triage", Reuse: &prompts.PromptTargetReuse{Title: true}},
		},
	})

	ctx := context.Background()

	_, first, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Weekly triage",
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
		PromptName: "weekly TRIAGE", // case-insensitive prompt name resolution
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

// TestConversationStart_ReuseTitle_LookupKeyIsTargetTitleNotCallerInput
// verifies the mitto-kybw design decision that when target.reuseTitle is set,
// the lookup key is the author-canonical target.title, not any caller-supplied
// input.Title. Two calls whose input.Title differs (or is absent) must still
// funnel to the same existing conversation.
func TestConversationStart_ReuseTitle_LookupKeyIsTargetTitleNotCallerInput(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "Weekly triage",
			Prompt: "triage",
			Target: &prompts.PromptTarget{Title: "Weekly triage", Reuse: &prompts.PromptTargetReuse{Title: true}},
		},
	})

	ctx := context.Background()

	// First caller passes a completely different input.Title.
	_, first, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Weekly triage",
		Title:      "some caller-picked name",
	})
	if err != nil {
		t.Fatalf("First call: unexpected error: %v", err)
	}

	// The created conversation's Name must be target.title, so a subsequent
	// scan matches it — this is the invariant the reuseTitle miss path relies on.
	meta, err := store.GetMetadata(first.SessionID)
	if err != nil {
		t.Fatalf("GetMetadata(%q) error: %v", first.SessionID, err)
	}
	if meta.Name != "Weekly triage" {
		t.Errorf("Created conversation Name = %q, want %q (must be overridden to target.title)", meta.Name, "Weekly triage")
	}

	// Second caller omits Title entirely; must still land on the same session.
	_, second, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Weekly triage",
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
}

// TestConversationStart_ReuseTitle_BypassesDuplicateTitleRejection verifies
// that the pre-existing "a conversation with the title 'X' already exists"
// rejection in mitto_conversation_new is bypassed when target.reuseTitle is
// active: otherwise reuse and reject would fight, and the second call would
// fail with a 4xx instead of funneling into the existing conversation.
func TestConversationStart_ReuseTitle_BypassesDuplicateTitleRejection(t *testing.T) {
	_, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "Weekly triage",
			Prompt: "triage",
			Target: &prompts.PromptTarget{Title: "Weekly triage", Reuse: &prompts.PromptTargetReuse{Title: true}},
		},
	})

	ctx := context.Background()

	_, first, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Weekly triage",
	})
	if err != nil {
		t.Fatalf("First call: unexpected error: %v", err)
	}

	// Second call would previously have been rejected as a duplicate title
	// (input.Title == existing Name); with reuseTitle it must funnel to reuse.
	_, second, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Weekly triage",
		Title:      "Weekly triage", // explicitly matches existing
	})
	if err != nil {
		t.Fatalf("Second call: unexpected error (duplicate-title bypass failed): %v", err)
	}
	if !second.Reused {
		t.Error("Second call: expected reused=true when reuseTitle bypasses duplicate-title rejection")
	}
	if second.SessionID != first.SessionID {
		t.Errorf("Second call: expected existing session ID %q, got %q", first.SessionID, second.SessionID)
	}
}

// TestConversationStart_TargetTitle_WithoutReuseTitle_StillRejectsDuplicate
// verifies the complementary invariant: target.title alone (no reuseTitle)
// merely acts as a default Name — it does NOT bypass the duplicate-title
// check. A second call whose effective Title collides must still surface as
// an error, because the prompt has not opted in to reuse.
func TestConversationStart_TargetTitle_WithoutReuseTitle_StillRejectsDuplicate(t *testing.T) {
	_, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "Weekly triage",
			Prompt: "triage",
			// title-only: no ReuseTitle.
			Target: &prompts.PromptTarget{Title: "Weekly triage"},
		},
	})

	ctx := context.Background()

	_, _, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Weekly triage",
		Title:      "Weekly triage",
	})
	if err != nil {
		t.Fatalf("First call: unexpected error: %v", err)
	}

	_, _, err = srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Weekly triage",
		Title:      "Weekly triage",
	})
	if err == nil {
		t.Fatal("Second call: expected duplicate-title error when reuseTitle is not set, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("Second call: expected duplicate-title error, got: %v", err)
	}
}

// TestConversationStart_ReuseTitle_NotDeclared_CreatesDuplicate verifies that
// a plain prompt (no target.reuseTitle) is NOT subject to per-title
// find-or-route even when the caller reuses the same title: reuseTitle
// routing is authoritative only when the prompt opts in.
func TestConversationStart_ReuseTitle_NotDeclared_CreatesDuplicate(t *testing.T) {
	// Plain prompt with no Target — duplicate-title rejection is what would
	// normally fire here, so use distinct input.Title values to prove reuseTitle
	// is truly gated on the prompt's opt-in and not silently active by default.
	_, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{Name: "Plain work", Prompt: "do work"},
	})

	ctx := context.Background()

	_, first, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Plain work",
		Title:      "instance A",
	})
	if err != nil {
		t.Fatalf("First call: unexpected error: %v", err)
	}

	_, second, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Plain work",
		Title:      "instance B",
	})
	if err != nil {
		t.Fatalf("Second call: unexpected error: %v", err)
	}
	if second.Reused {
		t.Error("Second call: expected reused=false when prompt does not declare target.reuseTitle")
	}
	if second.SessionID == first.SessionID {
		t.Error("Second call: expected a distinct session ID when prompt does not declare target.reuseTitle")
	}
}

// TestConversationStart_ReuseTitle_CrossWorkspaceIgnored verifies that a
// conversation with the matching title but in a DIFFERENT working_dir is NOT
// reused — the reuseTitle scan is scoped by target working_dir so two
// workspaces with the same target.title stay isolated. Mirrors the HTTP
// behavior via session.FindConversationByTitle, which filters on WorkingDir.
func TestConversationStart_ReuseTitle_CrossWorkspaceIgnored(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "Weekly triage",
			Prompt: "triage",
			Target: &prompts.PromptTarget{Title: "Weekly triage", Reuse: &prompts.PromptTargetReuse{Title: true}},
		},
	})

	// Pre-create a matching session in a DIFFERENT working_dir. The parent
	// session (from the fixture) has WorkingDir="/test/dir".
	otherMeta := session.Metadata{
		SessionID:  session.GenerateSessionID(),
		Name:       "Weekly triage",
		ACPServer:  "test-server",
		WorkingDir: "/other/dir",
	}
	if err := store.Create(otherMeta); err != nil {
		t.Fatalf("store.Create(otherMeta) error: %v", err)
	}

	ctx := context.Background()
	_, out, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Weekly triage",
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

// TestConversationStart_ReuseTitle_ArchivedIgnored verifies that an archived
// conversation with the matching title is NOT reused — a new conversation
// must be created. Mirrors the HTTP behavior via session.FindConversationByTitle,
// which filters archived sessions.
func TestConversationStart_ReuseTitle_ArchivedIgnored(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "Weekly triage",
			Prompt: "triage",
			Target: &prompts.PromptTarget{Title: "Weekly triage", Reuse: &prompts.PromptTargetReuse{Title: true}},
		},
	})

	ctx := context.Background()

	_, first, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Weekly triage",
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
		PromptName: "Weekly triage",
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

// TestConversationStart_ReuseTitle_TakesPrecedenceOverSingleton verifies the
// mitto-kybw design decision (mirroring reuseIssue) that the reuseTitle
// ladder step short-circuits the singleton fallback: when a prompt declares
// both singleton:true and target.{title, reuseTitle}, per-title reuse is
// authoritative and the singleton fallback is skipped on both hit and miss.
func TestConversationStart_ReuseTitle_TakesPrecedenceOverSingleton(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:      "Weekly triage",
			Prompt:    "triage",
			Singleton: true, // singleton would collapse everything into one
			// but reuseTitle is authoritative per-title
			Target: &prompts.PromptTarget{Title: "Weekly triage", Reuse: &prompts.PromptTargetReuse{Title: true}},
		},
	})

	ctx := context.Background()

	_, first, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Weekly triage",
	})
	if err != nil {
		t.Fatalf("First call: unexpected error: %v", err)
	}

	// Second call must reuse (title matches) rather than being collapsed via
	// the singleton branch — a distinction that only matters when both are set.
	_, second, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Weekly triage",
	})
	if err != nil {
		t.Fatalf("Second call: unexpected error: %v", err)
	}
	if !second.Reused {
		t.Error("Second call: expected reused=true (reuseTitle path)")
	}
	if second.SessionID != first.SessionID {
		t.Errorf("Second call: expected existing session ID %q, got %q", first.SessionID, second.SessionID)
	}

	metas, err := store.List()
	if err != nil {
		t.Fatalf("store.List() error: %v", err)
	}
	// parent + 1 child (reused, not duplicated)
	if len(metas) != 2 {
		t.Errorf("Expected 2 sessions (parent + 1 child), got %d", len(metas))
	}
}

// TestConversationStart_TargetTitle_WithoutReuseTitle_AdoptedAsDefaultName
// pins the bead's acceptance criterion for the plain (non-reuse) target.title
// path: when the prompt declares target.title but NOT target.reuseTitle, the
// created conversation's Name must default to target.title when the caller
// did not supply an input.Title. Caller-supplied Title wins.
func TestConversationStart_TargetTitle_WithoutReuseTitle_AdoptedAsDefaultName(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "Weekly triage",
			Prompt: "triage",
			// title-only: no ReuseTitle.
			Target: &prompts.PromptTarget{Title: "Weekly triage"},
		},
	})

	ctx := context.Background()

	// Caller omits Title entirely → plain target.title is adopted as Name.
	_, out, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Weekly triage",
	})
	if err != nil {
		t.Fatalf("handleConversationStart: unexpected error: %v", err)
	}
	if out.Reused {
		t.Error("expected reused=false (plain target.title does NOT trigger find-or-route)")
	}
	meta, err := store.GetMetadata(out.SessionID)
	if err != nil {
		t.Fatalf("GetMetadata(%q) error: %v", out.SessionID, err)
	}
	if meta.Name != "Weekly triage" {
		t.Errorf("Created conversation Name = %q, want %q (target.title must be adopted as default Name)", meta.Name, "Weekly triage")
	}
}

// TestConversationStart_TargetTitle_WithoutReuseTitle_CallerTitleWins pins the
// complementary invariant: caller-supplied input.Title always wins over the
// plain (non-reuse) target.title default. This distinguishes the plain-title
// path (default-only, caller override wins) from the reuseTitle path
// (canonical, overrides caller).
func TestConversationStart_TargetTitle_WithoutReuseTitle_CallerTitleWins(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "Weekly triage",
			Prompt: "triage",
			Target: &prompts.PromptTarget{Title: "Weekly triage"},
		},
	})

	ctx := context.Background()
	_, out, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Weekly triage",
		Title:      "caller wins",
	})
	if err != nil {
		t.Fatalf("handleConversationStart: unexpected error: %v", err)
	}
	meta, err := store.GetMetadata(out.SessionID)
	if err != nil {
		t.Fatalf("GetMetadata(%q) error: %v", out.SessionID, err)
	}
	if meta.Name != "caller wins" {
		t.Errorf("Created conversation Name = %q, want %q (caller Title must win over plain target.title default)", meta.Name, "caller wins")
	}
}

// TestConversationStart_TargetTitle_TemplateRendered verifies mitto-5qbo: a
// prompt whose target.title is a Go text/template renders against the
// caller-supplied Arguments before it becomes the conversation Name and (with
// reuse.title: true) the FindConversationByTitle lookup key. Two dispatches
// with different .Args.IssueID must open two distinct conversations named
// "<id>: work"; a second dispatch with the same IssueID must funnel back.
func TestConversationStart_TargetTitle_TemplateRendered(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "bead-work",
			Prompt: "work on {{ .Args.IssueID }}",
			Target: &prompts.PromptTarget{Title: "{{ .Args.IssueID }}: work", Reuse: &prompts.PromptTargetReuse{Title: true}},
		},
	})

	ctx := context.Background()

	// First: IssueID=abc → new conversation named "abc: work".
	_, first, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "bead-work",
		Arguments:  map[string]string{"IssueID": "abc"},
	})
	if err != nil {
		t.Fatalf("First call: unexpected error: %v", err)
	}
	if first.Reused {
		t.Error("First call: expected reused=false")
	}
	meta1, err := store.GetMetadata(first.SessionID)
	if err != nil {
		t.Fatalf("GetMetadata(%q) error: %v", first.SessionID, err)
	}
	if meta1.Name != "abc: work" {
		t.Errorf("First conversation Name = %q, want %q", meta1.Name, "abc: work")
	}

	// Second: IssueID=xyz → distinct new conversation named "xyz: work".
	_, second, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "bead-work",
		Arguments:  map[string]string{"IssueID": "xyz"},
	})
	if err != nil {
		t.Fatalf("Second call: unexpected error: %v", err)
	}
	if second.Reused {
		t.Error("Second call: expected reused=false for a distinct IssueID")
	}
	if second.SessionID == first.SessionID {
		t.Error("Second call: expected a distinct session ID for a different IssueID")
	}

	// Third: IssueID=abc again → must funnel back to the first conversation.
	_, third, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "bead-work",
		Arguments:  map[string]string{"IssueID": "abc"},
	})
	if err != nil {
		t.Fatalf("Third call: unexpected error: %v", err)
	}
	if !third.Reused {
		t.Error("Third call: expected reused=true when IssueID matches the first dispatch")
	}
	if third.SessionID != first.SessionID {
		t.Errorf("Third call: expected reuse of %q, got %q", first.SessionID, third.SessionID)
	}
}

// TestConversationStart_TargetTitle_EmptyRenderRejected verifies mitto-5qbo:
// when a templated target.title renders to empty (missing key), the dispatch
// is rejected pre-create — no session is left behind and the caller gets a
// clear error naming the prompt.
func TestConversationStart_TargetTitle_EmptyRenderRejected(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "bead-work",
			Prompt: "work",
			Target: &prompts.PromptTarget{Title: "{{ .Args.MISSING }}", Reuse: &prompts.PromptTargetReuse{Title: true}},
		},
	})

	before, _ := store.List()
	ctx := context.Background()

	_, _, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "bead-work",
		// No Arguments → .Args.MISSING renders to "" → rejection.
	})
	if err == nil {
		t.Fatal("expected error for empty target.title render, got nil")
	}
	if !strings.Contains(err.Error(), "bead-work") {
		t.Errorf("error should reference prompt name; got %q", err.Error())
	}
	after, _ := store.List()
	if len(after) != len(before) {
		t.Errorf("expected no session created on empty-render rejection; before=%d after=%d", len(before), len(after))
	}
}
