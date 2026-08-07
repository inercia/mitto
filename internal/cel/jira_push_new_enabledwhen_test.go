package cel

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCELEvaluator_JiraPushNewEnabledWhen is a regression test for mitto-w8jp.3
// ("JIRA: push to new"). It pins the exact literal enabledWhen expression from
// config/prompts/builtin/jira/push-new.prompt.yaml and exercises every clause
// of the conjunction, including the de-Morgan inverse-linkage gate the plan
// phase corrected (`!(A || B)` must be written, and evaluate, as `!A && !B`):
//
//   - Workspace.TasksUpstream must be exactly "jira".
//   - Either a reachable JIRA MCP tool or the `jira` CLI must be present.
//   - `bd` must be on PATH and a `.beads` dir must exist (beads is usable).
//   - The bead must NOT already be linked to JIRA: neither the beads-row menu
//     path (`"jira-sync" in Item.Labels`) nor the conversation menu path
//     (`Session.HasBeadsIssue && BeadHasLabels(Session.BeadsIssue, "jira-sync")`)
//     may be true.
//
// A future CEL/prompt-authoring change that silently reintroduces the `(A ||
// B)` mistake, drops a clause, or breaks the TasksUpstream gate will fail this
// test instead of only surfacing as a manually-observed UI regression.
func TestCELEvaluator_JiraPushNewEnabledWhen(t *testing.T) {
	const pushNewExpr = `Workspace.TasksUpstream == "jira" && (Tools.Names.exists(n, n.matches("(?i)jira")) || CommandExists("jira")) && CommandExists("bd") && DirExists(".beads") && !("jira-sync" in Item.Labels) && !(Session.HasBeadsIssue && BeadHasLabels(Session.BeadsIssue, "jira-sync"))`

	e := newTestEvaluator(t)
	ce := compile(t, e, pushNewExpr)

	// Workspace with a .beads dir, and a fake "jira" CLI on PATH (covers the
	// "no MCP tools, CLI fallback" branch of the second clause).
	tmpDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmpDir, ".beads"), 0755); err != nil {
		t.Fatalf("failed to create .beads dir: %v", err)
	}
	installFakeExecutable(t, "jira")
	// installFakeBd both puts a fake `bd` on PATH and resets the beads caches
	// between scenarios so a prior scenario's BeadHasLabels result can't leak.
	linkedBeadJSON := `{"id":"mitto-1","labels":["jira-sync"]}`
	unlinkedBeadJSON := `{"id":"mitto-1","labels":["support-question"]}`

	ws := WorkspaceContext{Folder: tmpDir, TasksUpstream: "jira"}
	noJiraWS := WorkspaceContext{Folder: tmpDir, TasksUpstream: ""}
	githubWS := WorkspaceContext{Folder: tmpDir, TasksUpstream: "github"}

	tests := []struct {
		name       string
		ctx        *PromptEnabledContext
		want       bool
		beadStdout string
	}{
		{
			name: "TasksUpstream not jira (empty) hides the prompt",
			ctx:  &PromptEnabledContext{Workspace: noJiraWS},
			want: false,
		},
		{
			name: "TasksUpstream is a different upstream (github) hides the prompt",
			ctx:  &PromptEnabledContext{Workspace: githubWS},
			want: false,
		},
		{
			name: "jira upstream, unlinked bead (beads-row menu path) shows the prompt",
			ctx: &PromptEnabledContext{
				Workspace: ws,
				Item:      ItemContext{Id: "mitto-1", Labels: []string{"support-question"}},
			},
			want: true,
		},
		{
			name: "jira upstream but Item already carries jira-sync hides the prompt",
			ctx: &PromptEnabledContext{
				Workspace: ws,
				Item:      ItemContext{Id: "mitto-1", Labels: []string{"jira-sync"}},
			},
			want: false,
		},
		{
			name: "jira upstream, conversation menu path, linked bead (BeadHasLabels true) hides the prompt",
			ctx: &PromptEnabledContext{
				Workspace: ws,
				Session:   SessionContext{HasBeadsIssue: true, BeadsIssue: "mitto-1"},
			},
			want:       false,
			beadStdout: linkedBeadJSON,
		},
		{
			name: "jira upstream, conversation menu path, unlinked bead (BeadHasLabels false) shows the prompt",
			ctx: &PromptEnabledContext{
				Workspace: ws,
				Session:   SessionContext{HasBeadsIssue: true, BeadsIssue: "mitto-1"},
			},
			want:       true,
			beadStdout: unlinkedBeadJSON,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.beadStdout != "" {
				installFakeBd(t, tt.beadStdout, 0)
			} else {
				installFakeBd(t, unlinkedBeadJSON, 0)
			}
			got := evaluate(t, e, ce, tt.ctx)
			if got != tt.want {
				t.Errorf("Evaluate(pushNewExpr) = %v, want %v", got, tt.want)
			}
		})
	}
}

// installFakeExecutable writes a no-op executable script named `name` to a
// fresh temp dir and prepends that dir to PATH for the duration of the test,
// so CommandExists(name) resolves true. Mirrors installFakeBd's PATH-splicing
// approach but for a command whose output/exit code the test does not care
// about (only its presence on PATH matters for CommandExists).
func installFakeExecutable(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)
	return dir
}
