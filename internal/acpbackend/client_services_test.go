package acpbackend

import (
	"context"
	"errors"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
)

func TestClientServices_NilHooks_FailClosed(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	ref := agentbackend.SessionRef{ConversationID: "conv-1"}

	var unsupported *agentbackend.UnsupportedError

	if _, err := c.ReadFile(context.Background(), ref, "/tmp/a.txt"); !errors.As(err, &unsupported) || unsupported.Feature != agentbackend.FeatureFiles {
		t.Fatalf("ReadFile: expected *UnsupportedError{files}, got %v", err)
	}
	if err := c.WriteFile(context.Background(), ref, "/tmp/a.txt", []byte("x")); !errors.As(err, &unsupported) || unsupported.Feature != agentbackend.FeatureFiles {
		t.Fatalf("WriteFile: expected *UnsupportedError{files}, got %v", err)
	}
	if _, err := c.RequestPermission(context.Background(), ref, "do it?"); !errors.As(err, &unsupported) || unsupported.Feature != agentbackend.FeaturePermissions {
		t.Fatalf("RequestPermission: expected *UnsupportedError{permissions}, got %v", err)
	}
}

func TestClientServices_HooksInstalled_Delegates(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	ref := agentbackend.SessionRef{ConversationID: "conv-1"}

	var gotPath string
	c.SetClientHooks(&ClientHooks{
		ReadFile: func(_ context.Context, _ agentbackend.SessionRef, path string) ([]byte, error) {
			gotPath = path
			return []byte("line1\nline2\nline3"), nil
		},
		WriteFile: func(context.Context, agentbackend.SessionRef, string, []byte) error { return nil },
		RequestPermission: func(context.Context, agentbackend.SessionRef, string) (bool, error) {
			return true, nil
		},
	})

	data, err := c.ReadFile(context.Background(), ref, "/tmp/a.txt")
	if err != nil || string(data) != "line1\nline2\nline3" {
		t.Fatalf("ReadFile = %q, %v", data, err)
	}
	if gotPath != "/tmp/a.txt" {
		t.Errorf("path = %q, want /tmp/a.txt", gotPath)
	}
	if err := c.WriteFile(context.Background(), ref, "/tmp/a.txt", []byte("x")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	approved, err := c.RequestPermission(context.Background(), ref, "do it?")
	if err != nil || !approved {
		t.Fatalf("RequestPermission = %v, %v", approved, err)
	}
}

func TestOnReadTextFile_AppliesLineLimit(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	c.SetClientHooks(&ClientHooks{
		ReadFile: func(context.Context, agentbackend.SessionRef, string) ([]byte, error) {
			return []byte("l1\nl2\nl3\nl4"), nil
		},
	})
	handler := c.onReadTextFile(agentbackend.SessionRef{ConversationID: "conv-1"})
	line, limit := 2, 2
	resp, err := handler(context.Background(), acp.ReadTextFileRequest{Path: "/tmp/a.txt", Line: &line, Limit: &limit})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if resp.Content != "l2\nl3" {
		t.Errorf("Content = %q, want l2\\nl3", resp.Content)
	}
}

func TestOnRequestPermission_UsesToolCallTitle(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	var gotPrompt string
	c.SetClientHooks(&ClientHooks{
		RequestPermission: func(_ context.Context, _ agentbackend.SessionRef, prompt string) (bool, error) {
			gotPrompt = prompt
			return true, nil
		},
	})
	handler := c.onRequestPermission(agentbackend.SessionRef{ConversationID: "conv-1"})
	title := "Run rm -rf /tmp/x"
	options := []acp.PermissionOption{
		{Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow", OptionId: "allow-1"},
		{Kind: acp.PermissionOptionKindRejectOnce, Name: "Reject", OptionId: "reject-1"},
	}
	resp, err := handler(context.Background(), acp.RequestPermissionRequest{ToolCall: acp.ToolCallUpdate{Title: &title}, Options: options})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if gotPrompt != title {
		t.Errorf("prompt = %q, want %q", gotPrompt, title)
	}
	if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "allow-1" {
		t.Errorf("expected the Allow option selected, got %+v", resp.Outcome)
	}
}

func TestSelectPermissionOutcome(t *testing.T) {
	options := []acp.PermissionOption{
		{Kind: acp.PermissionOptionKindAllowOnce, OptionId: "allow-1"},
		{Kind: acp.PermissionOptionKindRejectOnce, OptionId: "reject-1"},
	}
	if got := selectPermissionOutcome(options, true); got.Outcome.Selected == nil || got.Outcome.Selected.OptionId != "allow-1" {
		t.Errorf("approved: got %+v", got.Outcome)
	}
	if got := selectPermissionOutcome(options, false); got.Outcome.Selected == nil || got.Outcome.Selected.OptionId != "reject-1" {
		t.Errorf("denied: got %+v", got.Outcome)
	}
	// No matching kind: falls back to the first option.
	onlyAllow := []acp.PermissionOption{{Kind: acp.PermissionOptionKindAllowAlways, OptionId: "only"}}
	if got := selectPermissionOutcome(onlyAllow, false); got.Outcome.Selected == nil || got.Outcome.Selected.OptionId != "only" {
		t.Errorf("fallback: got %+v", got.Outcome)
	}
	// No options at all: Cancelled outcome.
	if got := selectPermissionOutcome(nil, true); got.Outcome.Cancelled == nil {
		t.Errorf("expected a Cancelled outcome for zero options, got %+v", got.Outcome)
	}
}

func TestSliceTextLines(t *testing.T) {
	content := "a\nb\nc\nd\ne"
	if got := sliceTextLines(content, nil, nil); got != content {
		t.Errorf("no line/limit: got %q, want unchanged", got)
	}
	line, limit := 2, 2
	if got := sliceTextLines(content, &line, &limit); got != "b\nc" {
		t.Errorf("line=2 limit=2: got %q, want b\\nc", got)
	}
	line = 100
	if got := sliceTextLines(content, &line, nil); got != "" {
		t.Errorf("line beyond EOF: got %q, want empty", got)
	}
}
