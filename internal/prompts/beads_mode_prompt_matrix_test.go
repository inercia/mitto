package prompts

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

func TestBeadsModeAndTaskUpstreamPromptMatrix(t *testing.T) {
	const builtinDir = "../../config/prompts/builtin"
	fragments, fragmentErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil || len(fragmentErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir: err=%v fileErrors=%v", err, fragmentErrs)
	}
	list, loadErrs, err := LoadPromptsFromDirWithErrorsAndFragments(builtinDir, fragments)
	if err != nil || len(loadErrs) != 0 {
		t.Fatalf("LoadPromptsFromDirWithErrorsAndFragments: err=%v fileErrors=%v", err, loadErrs)
	}
	expressions := make(map[string]string, len(list))
	for _, prompt := range list {
		expressions[prompt.Name] = prompt.EnabledWhen
	}

	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, ".beads"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(workspace, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".git", "config"), []byte("[core]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	installPromptMatrixCommands(t, "bd", "jira", "gh")

	evaluator, err := cel.NewCELEvaluator()
	if err != nil {
		t.Fatalf("NewCELEvaluator: %v", err)
	}
	specs := []struct {
		name         string
		labels       []string
		wantUpstream string
	}{
		{name: "JIRA: sync tasks", wantUpstream: "jira"},
		{name: "JIRA: pull issue", labels: []string{"jira-sync"}, wantUpstream: "jira"},
		{name: "JIRA: push issue", labels: []string{"jira-sync"}, wantUpstream: "jira"},
		{name: "JIRA: push to new", labels: []string{"support-question"}, wantUpstream: "jira"},
		{name: "GitHub: sync tasks", wantUpstream: "github"},
		{name: "Show status"},
		{name: "GitHub: review a Pull Request"},
	}

	for _, mode := range []string{"local", "shared"} {
		for _, upstream := range []string{"", "jira", "github"} {
			for _, spec := range specs {
				t.Run(mode+"/"+upstream+"/"+spec.name, func(t *testing.T) {
					expr, ok := expressions[spec.name]
					if !ok || expr == "" {
						t.Fatalf("prompt %q missing enabledWhen", spec.name)
					}
					compiled, err := evaluator.Compile(expr)
					if err != nil {
						t.Fatalf("Compile(%s): %v", expr, err)
					}
					ctx := &cel.PromptEnabledContext{
						Workspace: cel.WorkspaceContext{
							Folder: workspace, TasksUpstream: upstream, BeadsDatabaseMode: mode,
						},
						Tools: cel.NewReachableToolsContext([]string{"jira_get_issue", "github_list_issues"}),
						Item:  cel.ItemContext{Id: "mitto-1", Status: "open", Labels: spec.labels},
					}
					got, err := evaluator.Evaluate(compiled, ctx)
					if err != nil {
						t.Fatalf("Evaluate(%s): %v", expr, err)
					}
					want := spec.wantUpstream == "" || upstream == spec.wantUpstream
					if got != want {
						t.Errorf("enabled = %v, want %v (mode=%s upstream=%q)", got, want, mode, upstream)
					}
				})
			}
		}
	}
}

func installPromptMatrixCommands(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
