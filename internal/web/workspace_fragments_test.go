package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/prompts"
)

// Regression for mitto-7ul8.2: workspace prompts must precompile against
// fragments co-located in that workspace, not only the process-global registry.
func TestLoadPromptsFromDirs_LoadsWorkspaceLocalFragment(t *testing.T) {
	previous := prompts.CurrentFragments()
	prompts.SetCurrentFragments(prompts.NewFragmentRegistry())
	t.Cleanup(func() { prompts.SetCurrentFragments(previous) })

	workspace := t.TempDir()
	promptsDir := filepath.Join(workspace, ".mitto", "prompts")
	if err := os.MkdirAll(filepath.Join(promptsDir, "shared"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptsDir, "shared", "foo.tmpl"), []byte("workspace fragment"), 0o644); err != nil {
		t.Fatalf("WriteFile fragment: %v", err)
	}
	promptBody := "name: Workspace consumer\nprompt: |\n  {{ template \"shared/foo\" . }}\n"
	if err := os.WriteFile(filepath.Join(promptsDir, "consumer.prompt.yaml"), []byte(promptBody), 0o644); err != nil {
		t.Fatalf("WriteFile prompt: %v", err)
	}

	got := (&Server{}).loadPromptsFromDirs(workspace, []string{promptsDir})
	if len(got) != 1 {
		_, loadErrs, loadErr := config.LoadPromptsFromDirWithErrors(promptsDir)
		t.Fatalf("workspace prompt count = %d, want 1; load error = %v; per-file errors = %v", len(got), loadErr, loadErrs)
	}
	if got[0].Name != "Workspace consumer" {
		t.Fatalf("workspace prompt name = %q, want %q", got[0].Name, "Workspace consumer")
	}
}

func TestWorkspaceFragments_AreIsolatedAcrossWorkspaces(t *testing.T) {
	previous := prompts.CurrentFragments()
	prompts.SetCurrentFragments(prompts.NewFragmentRegistry())
	t.Cleanup(func() { prompts.SetCurrentFragments(previous) })

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "one", body: "workspace one"},
		{name: "two", body: "workspace two"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			promptsDir := filepath.Join(workspace, ".mitto", "prompts")
			if err := os.MkdirAll(filepath.Join(promptsDir, "shared"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(promptsDir, "shared", "foo.tmpl"), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}

			registry, loadErrs, err := config.LoadScopedFragmentsFromDirs([]string{promptsDir})
			if err != nil || len(loadErrs) != 0 {
				t.Fatalf("load fragments: err=%v loadErrs=%v", err, loadErrs)
			}
			ctx := &config.PromptEnabledContext{
				TemplateFragments: registry.All(),
				PromptTextResolver: func(string) (string, error) {
					return `{{ template "shared/foo" . }}`, nil
				},
			}
			funcs := config.BuildTemplateFuncMap(ctx)
			got, err := config.RenderPromptTemplateWithFragments(
				"outer", `{{ PromptTextWithArgs "inner" .Args }}`, ctx, funcs, registry,
			)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if got != tc.body {
				t.Fatalf("rendered body = %q, want %q", got, tc.body)
			}
		})
	}
}
