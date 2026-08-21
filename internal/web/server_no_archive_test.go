package web

import (
	"testing"

	"github.com/inercia/mitto/internal/config"
)

// TestResolvePromptTargetByPromptName_NoArchive pins mitto-yvel.2: the
// resolver reads target.noArchive from the merged 5-tier prompt list (global
// file → settings inline → ACP-specific → workspace-dir → workspace-inline)
// into ResolvedPromptTarget.NoArchive, alongside BackgroundColor and Title.
// Reuses the newSuppressResolverServer fixture helper (server_suppress_auto_children_test.go)
// rather than inventing new scaffolding, per the plan.
func TestResolvePromptTargetByPromptName_NoArchive(t *testing.T) {
	t.Run("prompt not found returns zero value (NoArchive=false)", func(t *testing.T) {
		s := newSuppressResolverServer(t, nil, nil, "")
		got, err := s.resolvePromptTargetByPromptName("no-such-prompt", "/work", nil, "")
		if err != nil {
			t.Fatalf("resolvePromptTargetByPromptName error: %v", err)
		}
		if got.NoArchive {
			t.Errorf("NoArchive = true for a missing prompt, want false")
		}
	})

	t.Run("global prompt with noArchive: true returns NoArchive=true", func(t *testing.T) {
		files := map[string]string{
			"protected.prompt.yaml": `name: "protected"
target:
  noArchive: true
prompt: hi
`,
		}
		s := newSuppressResolverServer(t, files, nil, "")
		got, err := s.resolvePromptTargetByPromptName("protected", "/work", nil, "")
		if err != nil {
			t.Fatalf("resolvePromptTargetByPromptName error: %v", err)
		}
		if !got.NoArchive {
			t.Errorf("global prompt with noArchive=true: NoArchive = false, want true")
		}
	})

	t.Run("global prompt without target.noArchive returns NoArchive=false", func(t *testing.T) {
		files := map[string]string{
			"plain.prompt.yaml": `name: "plain"
target:
  backgroundColor: "#E1BEE7"
prompt: hi
`,
		}
		s := newSuppressResolverServer(t, files, nil, "")
		got, err := s.resolvePromptTargetByPromptName("plain", "/work", nil, "")
		if err != nil {
			t.Fatalf("resolvePromptTargetByPromptName error: %v", err)
		}
		if got.NoArchive {
			t.Errorf("prompt with target set but no noArchive key: NoArchive = true, want false")
		}
		if got.BackgroundColor != "#E1BEE7" {
			t.Errorf("BackgroundColor = %q, want %q (sanity: other target fields still resolve)", got.BackgroundColor, "#E1BEE7")
		}
	})

	t.Run("settings prompt overrides global (settings=false wins over global=true)", func(t *testing.T) {
		files := map[string]string{
			"shared.prompt.yaml": `name: "shared"
target:
  noArchive: true
prompt: hi
`,
		}
		settings := []config.WebPrompt{
			{Name: "shared", Prompt: "settings body"},
		}
		s := newSuppressResolverServer(t, files, settings, "")
		got, err := s.resolvePromptTargetByPromptName("shared", "/work", nil, "")
		if err != nil {
			t.Fatalf("resolvePromptTargetByPromptName error: %v", err)
		}
		if got.NoArchive {
			t.Errorf("settings override of global noArchive=true: NoArchive = true, want false (higher-priority tier wins)")
		}
	})

	t.Run("prompt name lookup is case-insensitive", func(t *testing.T) {
		files := map[string]string{
			"case.prompt.yaml": `name: "MixedCase"
target:
  noArchive: true
prompt: hi
`,
		}
		s := newSuppressResolverServer(t, files, nil, "")
		got, err := s.resolvePromptTargetByPromptName("mixedcase", "/work", nil, "")
		if err != nil {
			t.Fatalf("resolvePromptTargetByPromptName error: %v", err)
		}
		if !got.NoArchive {
			t.Errorf("case-insensitive lookup of \"mixedcase\" against \"MixedCase\": NoArchive = false, want true")
		}
	})
}
