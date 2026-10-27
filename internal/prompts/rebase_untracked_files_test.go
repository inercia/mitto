package prompts

import (
	"strings"
	"testing"
)

// TestRebaseChangesAllowsUntrackedFiles guards the standalone Rebase prompt
// against treating untracked files as a reason to stop. Tracked changes still
// block, and an untracked file that Git would overwrite must be preserved.
func TestRebaseChangesAllowsUntrackedFiles(t *testing.T) {
	const builtinDir = "../../config/prompts/builtin"

	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}

	var body string
	for _, p := range list {
		if p.Name == "Rebase changes" {
			body = p.Content
			break
		}
	}
	if body == "" {
		t.Fatal(`prompt "Rebase changes" not found in builtin corpus`)
	}

	required := []string{
		"git status --porcelain --untracked-files=no",
		"Only tracked changes block the rebase",
		"Untracked files do not: leave them",
		"Do not stash, commit, or discard untracked work",
		"Do not proceed until the tracked working tree is clean",
		"would be overwritten",
		"Never delete untracked",
	}
	for _, needle := range required {
		if !strings.Contains(body, needle) {
			t.Errorf("Rebase changes prompt missing %q", needle)
		}
	}

	const stale = "If uncommitted changes exist, ask user whether to stash, commit, or discard. Do not proceed until clean."
	if strings.Contains(body, stale) {
		t.Errorf("Rebase changes prompt still treats all uncommitted/untracked files as blockers: %q", stale)
	}
}
