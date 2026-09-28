// tools_conversation_new_mitto9vng_test.go: reproduction tests for mitto-9vng
// (mitto_conversation_new silently overrides a caller-supplied title with the
// prompt's target.reuse.title, breaking title-prefix-based concurrency-gate
// matching used by the beads-issues orchestrator's Step 5H post-task hook).
//
// These tests encode the *desired* post-fix behavior (caller-supplied title
// survives as the displayed/stored Name; reuse hits refresh a stale
// BeadsIssue link) and are expected to FAIL against the current
// implementation, which unconditionally clobbers input.Title with
// target.title whenever target.reuse.title is set (tools_conversation_new.go
// ~line 293-304) and never refreshes meta.BeadsIssue on a reuse hit
// (reuseSingletonConversation, ~line 1024).
//
// Fixing this bug requires revisiting the mitto-kybw design decision pinned
// by TestConversationStart_ReuseTitle_LookupKeyIsTargetTitleNotCallerInput
// (in tools_conversation_new_reuse_title_test.go): the recommended direction
// is to decouple the *lookup key* (canonical target.title) from the
// *displayed/stored Name* (which should preserve the caller's explicit
// title), rather than letting the caller's title win outright and breaking
// find-or-route matching for the next dispatch.
package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/prompts"
)

// TestConversationStart_ReuseTitle_CallerTitleShouldSurviveForConcurrencyGate_mitto9vng
// reproduces the concrete failure from mitto-9vng: the orchestrator's Step 5H
// spawns the "Run tests" post-task hook with an explicit
// title: "Post-task: <bead-id>" so workspace-wide title-prefix scans (the
// 2-worker concurrency cap, mitto-9mk/mitto-e1x) can recognize it. Because
// "Run tests" declares target.reuse.title: true, the caller's title is
// currently discarded and the stored Name becomes "Run tests" instead —
// defeating the title-prefix convention.
func TestConversationStart_ReuseTitle_CallerTitleShouldSurviveForConcurrencyGate_mitto9vng(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "Run tests",
			Prompt: "run the tests",
			Target: &prompts.PromptTarget{
				Title: "Run tests",
				Reuse: &prompts.PromptTargetReuse{Title: true, Coalesce: boolPtr(true)},
			},
		},
	})

	ctx := context.Background()

	_, out, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Run tests",
		Title:      "Post-task: mitto-tr8m",
		BeadsIssue: "mitto-tr8m",
	})
	if err != nil {
		t.Fatalf("handleConversationStart: unexpected error: %v", err)
	}

	meta, err := store.GetMetadata(out.SessionID)
	if err != nil {
		t.Fatalf("GetMetadata(%q) error: %v", out.SessionID, err)
	}

	// EXPECTED (post-fix): the caller's explicit title must be preserved so
	// the "Post-task: " prefix convention (loop-processing.prompt.yaml Step
	// 5H concurrency gate) keeps working.
	//
	// ACTUAL (current bug): meta.Name is unconditionally overridden to the
	// prompt's target.title ("Run tests"), losing the "Post-task: " prefix.
	if !strings.HasPrefix(meta.Name, "Post-task: ") {
		t.Errorf("mitto-9vng: Created conversation Name = %q, want a name preserving the caller's %q prefix "+
			"(the workspace-wide title-prefix concurrency gate in loop-processing.prompt.yaml Step 5H "+
			"relies on this prefix surviving target.reuse.title)", meta.Name, "Post-task: ")
	}
}

// TestConversationStart_ReuseTitle_ReuseHitShouldRefreshStaleBeadsIssue_mitto9vng
// reproduces a second, related finding from the mitto-9vng investigation:
// reuseSingletonConversation never updates meta.BeadsIssue on a reuse hit, so
// a second post-task hook spawn for a *different* bead silently keeps
// reporting under the *first* bead's beads_issue link.
func TestConversationStart_ReuseTitle_ReuseHitShouldRefreshStaleBeadsIssue_mitto9vng(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "Run tests",
			Prompt: "run the tests",
			Target: &prompts.PromptTarget{
				Title: "Run tests",
				Reuse: &prompts.PromptTargetReuse{Title: true, Coalesce: boolPtr(true)},
			},
		},
	})

	ctx := context.Background()

	// First spawn: linked to mitto-tr8m.
	_, first, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Run tests",
		Title:      "Post-task: mitto-tr8m",
		BeadsIssue: "mitto-tr8m",
	})
	if err != nil {
		t.Fatalf("First call: unexpected error: %v", err)
	}

	// Second spawn for a DIFFERENT bead: coalesce/reuse routes it into the
	// same "Run tests" conversation (find-or-route by target.title).
	_, second, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Run tests",
		Title:      "Post-task: mitto-other",
		BeadsIssue: "mitto-other",
	})
	if err != nil {
		t.Fatalf("Second call: unexpected error: %v", err)
	}
	if !second.Reused {
		t.Fatal("Second call: expected reused=true (find-or-route by target.title)")
	}
	if second.SessionID != first.SessionID {
		t.Fatalf("Second call: expected existing session ID %q, got %q", first.SessionID, second.SessionID)
	}

	meta, err := store.GetMetadata(second.SessionID)
	if err != nil {
		t.Fatalf("GetMetadata(%q) error: %v", second.SessionID, err)
	}

	// EXPECTED (post-fix): the reused conversation's BeadsIssue should
	// reflect the LATEST spawn's bead, not the stale first one.
	//
	// ACTUAL (current bug): reuseSingletonConversation never updates
	// meta.BeadsIssue on a reuse hit, so it stays "mitto-tr8m".
	if meta.BeadsIssue != "mitto-other" {
		t.Errorf("mitto-9vng: reused conversation BeadsIssue = %q, want %q "+
			"(reuseSingletonConversation must refresh a stale beads_issue link on reuse hits)",
			meta.BeadsIssue, "mitto-other")
	}
}
