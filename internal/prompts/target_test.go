package prompts

import (
	"strings"
	"testing"
)

// TestRenderPromptTargetTitle_LiteralFastPath verifies that a title without
// "{{" is returned byte-for-byte with no template parse. This preserves the
// behavior of every existing literal target.title.
func TestRenderPromptTargetTitle_LiteralFastPath(t *testing.T) {
	ctx := PromptTargetContext{Args: map[string]string{"IssueID": "abc"}}
	got, err := RenderPromptTargetTitle("weekly-triage", "Weekly triage", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Weekly triage" {
		t.Errorf("got %q, want %q (fast-path passthrough)", got, "Weekly triage")
	}
}

// TestRenderPromptTargetTitle_ArgsSubstitution verifies the primary user-facing
// case: {{ .Args.IssueID }} renders to the argument value.
func TestRenderPromptTargetTitle_ArgsSubstitution(t *testing.T) {
	ctx := PromptTargetContext{Args: map[string]string{"IssueID": "mitto-abc"}}
	got, err := RenderPromptTargetTitle("bead-work", "{{ .Args.IssueID }}: work", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "mitto-abc: work" {
		t.Errorf("got %q, want %q", got, "mitto-abc: work")
	}
}

// TestRenderPromptTargetTitle_SessionBeadsIssue verifies the .Session.BeadsIssue
// escape hatch for prompts that want to name by the top-level linked bead.
func TestRenderPromptTargetTitle_SessionBeadsIssue(t *testing.T) {
	var ctx PromptTargetContext
	ctx.Session.BeadsIssue = "mitto-xyz"
	got, err := RenderPromptTargetTitle("bead-work", "{{ .Session.BeadsIssue }}: work", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "mitto-xyz: work" {
		t.Errorf("got %q, want %q", got, "mitto-xyz: work")
	}
}

// TestRenderPromptTargetTitle_MissingKeyRejected verifies fail-closed behavior
// when the rendered output is empty (all-blank after substitution) — otherwise
// two dispatches with different missing keys would silently collide on the
// empty-string reuseTitle bucket.
func TestRenderPromptTargetTitle_MissingKeyRejected(t *testing.T) {
	ctx := PromptTargetContext{Args: map[string]string{}}
	_, err := RenderPromptTargetTitle("bead-work", "{{ .Args.MISSING }}", ctx)
	if err == nil {
		t.Fatal("expected error for empty render, got nil")
	}
	if !strings.Contains(err.Error(), "bead-work") {
		t.Errorf("error should reference prompt name; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "target.title") {
		t.Errorf("error should mention target.title; got %q", err.Error())
	}
}

// TestRenderPromptTargetTitle_WhitespaceOnlyRejected verifies that a render
// consisting only of whitespace is treated as empty (same guard as literal
// whitespace-only titles in ValidatePromptTarget).
func TestRenderPromptTargetTitle_WhitespaceOnlyRejected(t *testing.T) {
	ctx := PromptTargetContext{Args: map[string]string{"X": ""}}
	_, err := RenderPromptTargetTitle("p", "  {{ .Args.X }}  ", ctx)
	if err == nil {
		t.Fatal("expected error for whitespace-only render, got nil")
	}
}

// TestRenderPromptTargetTitle_InvalidTemplateRejected verifies that a broken
// template surfaces as an error rather than being silently passed through.
func TestRenderPromptTargetTitle_InvalidTemplateRejected(t *testing.T) {
	ctx := PromptTargetContext{}
	_, err := RenderPromptTargetTitle("p", "{{ .Args.X ", ctx)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

// TestRenderPromptTargetTitle_ArgHelper verifies the Arg helper with a default.
func TestRenderPromptTargetTitle_ArgHelper(t *testing.T) {
	ctx := PromptTargetContext{Args: map[string]string{}}
	got, err := RenderPromptTargetTitle("p", `{{ Arg "MISSING" "fallback" }}`, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "fallback" {
		t.Errorf("got %q, want %q", got, "fallback")
	}
}

// TestRenderPromptTargetTitle_WorkspaceFolder verifies the .Workspace.Folder
// field is populated and reachable from the template.
func TestRenderPromptTargetTitle_WorkspaceFolder(t *testing.T) {
	var ctx PromptTargetContext
	ctx.Workspace.Folder = "/work/x"
	got, err := RenderPromptTargetTitle("p", "{{ .Workspace.Folder }}: work", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/work/x: work" {
		t.Errorf("got %q, want %q", got, "/work/x: work")
	}
}
