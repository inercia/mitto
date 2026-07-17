package config

import (
	"testing"
)

// TestWorkspaceRC_SkipsInvalidChildSessionIdPrompt lives here (rather than in
// internal/prompts) because it exercises the config-private parseWorkspaceRC
// helper. It verifies that a .mittorc prompt declaring a childSessionId
// parameter in a menu that cannot supply one (e.g. beadsList) is silently
// dropped rather than making the whole workspace RC fail to parse.
func TestWorkspaceRC_SkipsInvalidChildSessionIdPrompt(t *testing.T) {
	yaml := `
prompts:
  - name: "Valid Prompt"
    prompt: "do something"
    menus: conversation
    parameters:
      - name: child
        type: childSessionId
  - name: "Invalid Prompt"
    prompt: "do something else"
    menus: beadsList
    parameters:
      - name: child
        type: childSessionId
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}
	if len(rc.Prompts) != 1 {
		t.Errorf("Prompts count = %d, want 1 (invalid prompt should be skipped)", len(rc.Prompts))
	}
	if len(rc.Prompts) > 0 && rc.Prompts[0].Name != "Valid Prompt" {
		t.Errorf("Prompts[0].Name = %q, want %q", rc.Prompts[0].Name, "Valid Prompt")
	}
}
