// tools_conversation_new_suppress_auto_children_test.go: MCP-path coverage for
// mitto-nlx.4. Pins that the MCP mitto_conversation_new tool NEVER spawns
// workspace-level auto_children, regardless of whether the resolved prompt
// declares target.suppressAutoChildren.
//
// Rationale (documented at tools_conversation_new.go:557-563):
// MCP-created conversations always have a ParentSessionID (they are children
// of the caller), and go through store.Create + ResumeSession — not
// SessionManager.CreateSessionWithWorkspace — so the auto_children spawn
// path (which is gated on top-level creates in SessionManager) never runs
// on this code path. The tests here pin that invariant so a future MCP
// refactor that grows a top-level create path can't silently regress it.

package mcpserver

import (
	"context"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/prompts"
	"github.com/inercia/mitto/internal/session"
)

// TestConversationStart_DoesNotTriggerAutoChildren pins the invariant:
// even when the workspace declares AutoChildren AND the resolved prompt
// declares target.suppressAutoChildren=true (which would toggle the REST
// path's gate), the MCP create path never triggers the auto_children spawn
// because it doesn't call CreateSessionWithWorkspace at all. After the
// call, the store contains exactly the parent + the one MCP child — no
// additional auto-children rows.
func TestConversationStart_DoesNotTriggerAutoChildren(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{
			Name:   "with-suppress",
			Prompt: "hi",
			Target: &prompts.PromptTarget{SuppressAutoChildren: true},
		},
	})

	// Snapshot BEFORE: parent metadata was created by the setup helper. We'll
	// diff against this to identify only newly-persisted rows caused by the
	// MCP call.
	before, err := store.List()
	if err != nil {
		t.Fatalf("store.List(before) error: %v", err)
	}
	beforeIDs := make(map[string]struct{}, len(before))
	for _, m := range before {
		beforeIDs[m.SessionID] = struct{}{}
	}

	// Perform an MCP-driven conversation start. This exercises the same code
	// path that mitto_conversation_new dispatches to internally.
	_, out, err := srv.handleConversationStart(context.Background(), nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "with-suppress",
		Title:      "MCP child",
	})
	if err != nil {
		t.Fatalf("handleConversationStart: %v", err)
	}
	if out.SessionID == "" {
		t.Fatal("expected a non-empty session ID for the MCP-created child")
	}

	// Snapshot AFTER: exactly one new row is allowed — the MCP child. Any
	// auto_children spawn on this path would show up as an extra row with
	// IsAutoChild=true / ChildOrigin=auto, which we forbid.
	after, err := store.List()
	if err != nil {
		t.Fatalf("store.List(after) error: %v", err)
	}
	var newRows []session.Metadata
	for _, m := range after {
		if _, existed := beforeIDs[m.SessionID]; !existed {
			newRows = append(newRows, m)
		}
	}
	if got := len(newRows); got != 1 {
		t.Fatalf("MCP-path created %d new rows, want exactly 1 (the MCP child); rows=%+v", got, newRows)
	}

	mcpChild := newRows[0]
	if mcpChild.SessionID != out.SessionID {
		t.Errorf("new row SessionID=%q, want %q (the MCP child returned in output)", mcpChild.SessionID, out.SessionID)
	}
	// MCP children carry the MCP origin, NOT the auto-children origin.
	if mcpChild.ChildOrigin != session.ChildOriginMCP {
		t.Errorf("MCP child ChildOrigin=%q, want %q", mcpChild.ChildOrigin, session.ChildOriginMCP)
	}
	// The critical invariant: no row on this path is flagged as an auto-child.
	// (session.IsAutoChild is a legacy field; ChildOrigin is the modern
	// equivalent — an MCP path must never emit ChildOriginAuto.)
	for _, m := range newRows {
		if m.ChildOrigin == session.ChildOriginAuto {
			t.Errorf("row %q: ChildOrigin=%q, want %q for MCP path", m.SessionID, m.ChildOrigin, session.ChildOriginMCP)
		}
	}
	// Parent-child linkage: the MCP child must be attached to the parent.
	if mcpChild.ParentSessionID != parentID {
		t.Errorf("MCP child ParentSessionID=%q, want %q", mcpChild.ParentSessionID, parentID)
	}
}

// TestConversationStart_DoesNotTriggerAutoChildren_WithoutSuppressFlag is the
// complementary invariant: even a prompt WITHOUT target.suppressAutoChildren
// must not trigger auto_children via the MCP path. This proves that the
// MCP-path immunity to the flag is intrinsic (it doesn't route through the
// SessionManager top-level create), not just a consequence of the flag
// being true. If a future MCP refactor accidentally begins invoking
// CreateSessionWithWorkspace, this test — combined with the REST E2E
// coverage — will surface the regression.
func TestConversationStart_DoesNotTriggerAutoChildren_WithoutSuppressFlag(t *testing.T) {
	store, srv, parentID := setupConversationStartServerWithPrompts(t, []config.WebPrompt{
		{Name: "plain", Prompt: "hi"},
	})

	before, _ := store.List()

	_, out, err := srv.handleConversationStart(context.Background(), nil, ConversationStartInput{
		SelfID:     parentID,
		PromptName: "plain",
		Title:      "plain MCP child",
	})
	if err != nil {
		t.Fatalf("handleConversationStart: %v", err)
	}

	after, _ := store.List()
	// Exactly one new row: the MCP child. No auto_children spawns.
	if got, want := len(after)-len(before), 1; got != want {
		t.Fatalf("new-row count on MCP plain-prompt path = %d, want %d", got, want)
	}
	// Verify no auto-child rows in the store at all.
	for _, m := range after {
		if m.ChildOrigin == session.ChildOriginAuto {
			t.Errorf("unexpected auto-child row on MCP path: %+v", m)
		}
	}
	if out.SessionID == "" {
		t.Fatal("expected non-empty session ID")
	}
}
