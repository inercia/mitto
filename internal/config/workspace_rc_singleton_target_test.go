package config

import "testing"

// TestParseWorkspaceRC_PromptSingletonAndTarget is the workspace-.mittorc mirror
// of TestParse_PromptSingletonAndTags / TestParse_PerServerPromptSingletonAndTags:
// it verifies that a prompt declared inline under `prompts:` in a workspace RC
// file carries `singleton: true` and `target: { reuse: { issue: true } }`
// through parseWorkspaceRC into the resulting WebPrompt (bug mitto-nims;
// nested reuse block mitto-6b3). A plain sibling prompt that omits both keys
// must default to Singleton=false and Target=nil, guarding against
// accidental cross-entry coupling.
func TestParseWorkspaceRC_PromptSingletonAndTarget(t *testing.T) {
	yaml := `
prompts:
  - name: "Singleton Prompt"
    prompt: "Singleton prompt text"
    singleton: true
    target:
      reuse:
        issue: true
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
		t.Errorf("singleton prompt Target = nil, want non-nil {Reuse.Issue: true}")
	} else if singleton.Target.Reuse == nil || !singleton.Target.Reuse.Issue {
		t.Errorf("singleton prompt Target.Reuse.Issue = %+v, want true", singleton.Target.Reuse)
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
// fields, now nested under target.reuse (mitto-6b3): `target: { title,
// reuse: { title: true } }`. It verifies that a prompt declared inline under
// `prompts:` in a workspace RC file carries both fields through
// parseWorkspaceRC into the resulting WebPrompt. A sibling prompt that
// declares `target.title` alone (no reuse block) must land with
// Reuse.Title=false / Reuse=nil. A third prompt that omits `target:`
// entirely stays Target=nil, guarding against accidental cross-entry
// coupling.
func TestParseWorkspaceRC_PromptTargetTitleAndReuseTitle(t *testing.T) {
	yaml := `
prompts:
  - name: "Weekly Triage"
    prompt: "Weekly triage body"
    target:
      title: "Weekly triage"
      reuse:
        title: true
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
		t.Fatal("reuseTitle prompt Target = nil, want non-nil {Title, Reuse.Title}")
	}
	if reuse.Target.Title != "Weekly triage" {
		t.Errorf("reuseTitle prompt Target.Title = %q, want %q", reuse.Target.Title, "Weekly triage")
	}
	if reuse.Target.Reuse == nil || !reuse.Target.Reuse.Title {
		t.Errorf("reuseTitle prompt Target.Reuse.Title = %+v, want true", reuse.Target.Reuse)
	}
	// Reuse.Issue must NOT be silently coupled to Reuse.Title.
	if reuse.Target.Reuse != nil && reuse.Target.Reuse.Issue {
		t.Errorf("reuseTitle prompt Target.Reuse.Issue = %v, want false (must not couple to reuse.title)", reuse.Target.Reuse.Issue)
	}

	defaultName := rc.Prompts[1]
	if defaultName.Target == nil {
		t.Fatal("default-name prompt Target = nil, want non-nil {Title}")
	}
	if defaultName.Target.Title != "Fixed name" {
		t.Errorf("default-name prompt Target.Title = %q, want %q", defaultName.Target.Title, "Fixed name")
	}
	if defaultName.Target.Reuse != nil && defaultName.Target.Reuse.Title {
		t.Errorf("default-name prompt Target.Reuse.Title = %v, want false (title alone must not imply reuse.title)", defaultName.Target.Reuse.Title)
	}

	plain := rc.Prompts[2]
	if plain.Target != nil {
		t.Errorf("plain prompt Target = %+v, want nil", plain.Target)
	}
}
