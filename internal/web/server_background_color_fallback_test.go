package web

import (
	"testing"
)

// TestResolvePromptTargetByPromptName_TopLevelBackgroundColorFallback pins
// mitto-8s89: a prompt's top-level backgroundColor (the "prompt button"
// color, mitto-8sk's sibling field) is not applied to conversations created
// from that prompt when target.backgroundColor is absent — only
// target.backgroundColor is consumed by resolvePromptTargetByPromptName.
//
// Reproduces both scenarios named in the bead: (1) a prompt with a target:
// block that omits backgroundColor (e.g.
// config/prompts/builtin/beads-issues/loop-processing.prompt.yaml), and (2)
// a prompt with a top-level backgroundColor and no target: block at all
// (today's early-return at server.go:3555-3557 skips the fallback
// entirely). In both cases the expected fix behavior is that the top-level
// color is used as the creation-time default when the target-level field is
// empty; target.backgroundColor still wins when both are set.
//
// EXPECTED (post-fix): all three assertions below pass.
// ACTUAL (pre-fix): the first two currently fail, since
// resolvePromptTargetByPromptName never reads p.BackgroundColor.
func TestResolvePromptTargetByPromptName_TopLevelBackgroundColorFallback(t *testing.T) {
	t.Run("top-level backgroundColor falls back when target block has no color", func(t *testing.T) {
		files := map[string]string{
			"loopish.prompt.yaml": `name: "loopish"
backgroundColor: '#E1BEE7'
target:
  title: "Loop processing tasks"
  reuse:
    title: true
prompt: hi
`,
		}
		s := newSuppressResolverServer(t, files, nil, "")
		got, err := s.resolvePromptTargetByPromptName("loopish", "/work", nil, "")
		if err != nil {
			t.Fatalf("resolvePromptTargetByPromptName error: %v", err)
		}
		if got.BackgroundColor != "#E1BEE7" {
			t.Errorf("BackgroundColor = %q, want %q (top-level backgroundColor should be used as fallback when target.backgroundColor is unset, mitto-8s89)", got.BackgroundColor, "#E1BEE7")
		}
	})

	t.Run("top-level backgroundColor falls back even when there is no target block at all", func(t *testing.T) {
		files := map[string]string{
			"notarget.prompt.yaml": `name: "notarget"
backgroundColor: '#BBDEFB'
prompt: hi
`,
		}
		s := newSuppressResolverServer(t, files, nil, "")
		got, err := s.resolvePromptTargetByPromptName("notarget", "/work", nil, "")
		if err != nil {
			t.Fatalf("resolvePromptTargetByPromptName error: %v", err)
		}
		if got.BackgroundColor != "#BBDEFB" {
			t.Errorf("BackgroundColor = %q, want %q (top-level backgroundColor should apply even with no target: block, mitto-8s89)", got.BackgroundColor, "#BBDEFB")
		}
	})

	t.Run("target.backgroundColor still wins when both are set", func(t *testing.T) {
		files := map[string]string{
			"both.prompt.yaml": `name: "both"
backgroundColor: '#000000'
target:
  backgroundColor: '#FFFFFF'
prompt: hi
`,
		}
		s := newSuppressResolverServer(t, files, nil, "")
		got, err := s.resolvePromptTargetByPromptName("both", "/work", nil, "")
		if err != nil {
			t.Fatalf("resolvePromptTargetByPromptName error: %v", err)
		}
		if got.BackgroundColor != "#FFFFFF" {
			t.Errorf("BackgroundColor = %q, want %q (target.backgroundColor must take precedence over the top-level fallback)", got.BackgroundColor, "#FFFFFF")
		}
	})
}
