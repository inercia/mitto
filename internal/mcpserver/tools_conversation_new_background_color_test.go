// tools_conversation_new_background_color_test.go: tests for target.backgroundColor
// (mitto-8sk) on the MCP create path in handleConversationStart. Mirrors the
// setup/shape of tools_conversation_new_reuse_test.go and the BeadsIssue
// coverage in server_test.go.
package mcpserver

import (
	"context"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/prompts"
	"github.com/inercia/mitto/internal/session"
)

// TestConversationStart_BackgroundColor_AppliedOnCreate verifies that a
// prompt declaring target.backgroundColor causes the newly created
// conversation's metadata to carry that color.
func TestConversationStart_BackgroundColor_AppliedOnCreate(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "Colored prompt",
			Prompt: "do work",
			Target: &prompts.PromptTarget{BackgroundColor: "#E1BEE7"},
		},
	})

	ctx := context.Background()
	_, output, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Colored prompt",
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
	if meta.BackgroundColor != "#E1BEE7" {
		t.Errorf("BackgroundColor = %q, want %q", meta.BackgroundColor, "#E1BEE7")
	}
}

// TestConversationStart_BackgroundColor_AbsentWhenPromptHasNone verifies that
// a prompt with no target.backgroundColor leaves the new conversation's color
// unset — existing prompts must behave exactly as before mitto-8sk.
func TestConversationStart_BackgroundColor_AbsentWhenPromptHasNone(t *testing.T) {
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
	if meta.BackgroundColor != "" {
		t.Errorf("BackgroundColor = %q, want empty for a prompt with no target.backgroundColor", meta.BackgroundColor)
	}
}

// TestConversationStart_BackgroundColor_TopLevelFallback pins mitto-8s89:
// the MCP create path shares the same fallback as resolvePromptTargetByPromptName
// (internal/web/server.go) — a prompt's top-level backgroundColor (the
// "prompt button" color) is applied as the new conversation's default color
// when target.backgroundColor is unset, whether or not a target: block is
// present at all. target.backgroundColor still wins when both are set.
func TestConversationStart_BackgroundColor_TopLevelFallback(t *testing.T) {
	t.Run("falls back when target block has no color", func(t *testing.T) {
		store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
			{
				Name:            "Loopish",
				Prompt:          "do work",
				BackgroundColor: "#E1BEE7",
				Target:          &prompts.PromptTarget{Title: "Loop"},
			},
		})

		ctx := context.Background()
		_, output, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
			SelfID:     parentID,
			PromptName: "Loopish",
		})
		if err != nil {
			t.Fatalf("handleConversationStart: unexpected error: %v", err)
		}

		meta, err := store.GetMetadata(output.SessionID)
		if err != nil {
			t.Fatalf("GetMetadata(%q) error: %v", output.SessionID, err)
		}
		if meta.BackgroundColor != "#E1BEE7" {
			t.Errorf("BackgroundColor = %q, want %q (top-level backgroundColor fallback)", meta.BackgroundColor, "#E1BEE7")
		}
	})

	t.Run("falls back even with no target block at all", func(t *testing.T) {
		store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
			{
				Name:            "No target",
				Prompt:          "do work",
				BackgroundColor: "#BBDEFB",
			},
		})

		ctx := context.Background()
		_, output, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
			SelfID:     parentID,
			PromptName: "No target",
		})
		if err != nil {
			t.Fatalf("handleConversationStart: unexpected error: %v", err)
		}

		meta, err := store.GetMetadata(output.SessionID)
		if err != nil {
			t.Fatalf("GetMetadata(%q) error: %v", output.SessionID, err)
		}
		if meta.BackgroundColor != "#BBDEFB" {
			t.Errorf("BackgroundColor = %q, want %q (top-level backgroundColor fallback with no target block)", meta.BackgroundColor, "#BBDEFB")
		}
	})

	t.Run("target.backgroundColor still wins when both are set", func(t *testing.T) {
		store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
			{
				Name:            "Both",
				Prompt:          "do work",
				BackgroundColor: "#000000",
				Target:          &prompts.PromptTarget{BackgroundColor: "#FFFFFF"},
			},
		})

		ctx := context.Background()
		_, output, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
			SelfID:     parentID,
			PromptName: "Both",
		})
		if err != nil {
			t.Fatalf("handleConversationStart: unexpected error: %v", err)
		}

		meta, err := store.GetMetadata(output.SessionID)
		if err != nil {
			t.Fatalf("GetMetadata(%q) error: %v", output.SessionID, err)
		}
		if meta.BackgroundColor != "#FFFFFF" {
			t.Errorf("BackgroundColor = %q, want %q (target.backgroundColor must take precedence)", meta.BackgroundColor, "#FFFFFF")
		}
	})
}

// TestConversationStart_ReuseIssue_DoesNotOverwriteBackgroundColor verifies
// that funneling a dispatch into an existing conversation via target.reuse.issue
// never re-applies the prompt's target.backgroundColor — a creation-time
// default must not clobber a manual recolor on later dispatches.
func TestConversationStart_ReuseIssue_DoesNotOverwriteBackgroundColor(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "Work on issue",
			Prompt: "do work on {{ .Args.IssueID }}",
			Target: &prompts.PromptTarget{
				BackgroundColor: "#E1BEE7", // would-be default, must NOT apply on reuse
				Reuse:           &prompts.PromptTargetReuse{Issue: true},
			},
		},
	})

	existingID := session.GenerateSessionID()
	if err := store.Create(session.Metadata{
		SessionID:       existingID,
		Name:            "Existing",
		ACPServer:       "test-server",
		WorkingDir:      "/test/dir",
		BeadsIssue:      "mitto-abc",
		BackgroundColor: "#111111", // pre-existing / user-recolored value
		UpdatedAt:       time.Now(),
	}); err != nil {
		t.Fatalf("store.Create(existing) error: %v", err)
	}

	ctx := context.Background()
	_, output, err := srv.handleConversationStart(ctx, nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "Work on issue",
		BeadsIssue: "mitto-abc",
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
	if meta.BackgroundColor != "#111111" {
		t.Errorf("BackgroundColor = %q after reuseIssue hit, want unchanged %q", meta.BackgroundColor, "#111111")
	}
}
