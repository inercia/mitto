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
