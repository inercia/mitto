package config

import "testing"

// TestParseWorkspaceRC_PromptSingletonAndTarget is the workspace-.mittorc mirror
// of TestParse_PromptSingletonAndTags / TestParse_PerServerPromptSingletonAndTags:
// it verifies that a prompt declared inline under `prompts:` in a workspace RC
// file carries `singleton: true` and `target: { reuseIssue: true }` through
// parseWorkspaceRC into the resulting WebPrompt (bug mitto-nims). A plain
// sibling prompt that omits both keys must default to Singleton=false and
// Target=nil, guarding against accidental cross-entry coupling.
func TestParseWorkspaceRC_PromptSingletonAndTarget(t *testing.T) {
	yaml := `
prompts:
  - name: "Singleton Prompt"
    prompt: "Singleton prompt text"
    singleton: true
    target:
      reuseIssue: true
  - name: "Plain Prompt"
    prompt: "Plain prompt text"
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}
	if rc == nil {
		t.Fatal("parseWorkspaceRC returned nil")
	}
	if len(rc.Prompts) != 2 {
		t.Fatalf("Prompts count = %d, want 2", len(rc.Prompts))
	}

	singleton := rc.Prompts[0]
	if !singleton.Singleton {
		t.Errorf("singleton prompt Singleton = %v, want true", singleton.Singleton)
	}
	if singleton.Target == nil {
		t.Errorf("singleton prompt Target = nil, want non-nil {ReuseIssue: true}")
	} else if !singleton.Target.ReuseIssue {
		t.Errorf("singleton prompt Target.ReuseIssue = %v, want true", singleton.Target.ReuseIssue)
	}

	plain := rc.Prompts[1]
	if plain.Singleton {
		t.Errorf("plain prompt Singleton = %v, want false", plain.Singleton)
	}
	if plain.Target != nil {
		t.Errorf("plain prompt Target = %+v, want nil", plain.Target)
	}
}

// TestParseWorkspaceRC_PromptTargetTitleAndReuseTitle is the workspace-.mittorc
// mirror of TestParseWorkspaceRC_PromptSingletonAndTarget for the mitto-kybw
// fields: `target: { title, reuseTitle }`. It verifies that a prompt declared
// inline under `prompts:` in a workspace RC file carries both fields through
// parseWorkspaceRC into the resulting WebPrompt. A sibling prompt that
// declares `target.title` alone (no reuseTitle) must land with ReuseTitle=false.
// A third prompt that omits `target:` entirely stays Target=nil, guarding
// against accidental cross-entry coupling.
func TestParseWorkspaceRC_PromptTargetTitleAndReuseTitle(t *testing.T) {
	yaml := `
prompts:
  - name: "Weekly Triage"
    prompt: "Weekly triage body"
    target:
      title: "Weekly triage"
      reuseTitle: true
  - name: "Default-Name Prompt"
    prompt: "Body"
    target:
      title: "Fixed name"
  - name: "Plain Prompt"
    prompt: "Plain prompt text"
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}
	if rc == nil {
		t.Fatal("parseWorkspaceRC returned nil")
	}
	if len(rc.Prompts) != 3 {
		t.Fatalf("Prompts count = %d, want 3", len(rc.Prompts))
	}

	reuse := rc.Prompts[0]
	if reuse.Target == nil {
		t.Fatal("reuseTitle prompt Target = nil, want non-nil {Title, ReuseTitle}")
	}
	if reuse.Target.Title != "Weekly triage" {
		t.Errorf("reuseTitle prompt Target.Title = %q, want %q", reuse.Target.Title, "Weekly triage")
	}
	if !reuse.Target.ReuseTitle {
		t.Errorf("reuseTitle prompt Target.ReuseTitle = %v, want true", reuse.Target.ReuseTitle)
	}
	// ReuseIssue must NOT be silently coupled to ReuseTitle.
	if reuse.Target.ReuseIssue {
		t.Errorf("reuseTitle prompt Target.ReuseIssue = %v, want false (must not couple to reuseTitle)", reuse.Target.ReuseIssue)
	}

	defaultName := rc.Prompts[1]
	if defaultName.Target == nil {
		t.Fatal("default-name prompt Target = nil, want non-nil {Title}")
	}
	if defaultName.Target.Title != "Fixed name" {
		t.Errorf("default-name prompt Target.Title = %q, want %q", defaultName.Target.Title, "Fixed name")
	}
	if defaultName.Target.ReuseTitle {
		t.Errorf("default-name prompt Target.ReuseTitle = %v, want false (title alone must not imply reuseTitle)", defaultName.Target.ReuseTitle)
	}

	plain := rc.Prompts[2]
	if plain.Target != nil {
		t.Errorf("plain prompt Target = %+v, want nil", plain.Target)
	}
}
