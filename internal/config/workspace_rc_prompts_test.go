package config

import (
	"bytes"
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

// TestWorkspaceRC_InlineLoop_FlatSchemaMigratedInMemory pins mitto-opoh: a
// .mittorc inline prompt's pre-r6j flat loop: block is migrated in memory
// (via DecodeInlineLoop) onto the grouped schema instead of being silently
// dropped (the pre-fix behavior — rawWorkspaceRC.Prompts had no Loop field
// at all) or hard-failing the load.
func TestWorkspaceRC_InlineLoop_FlatSchemaMigratedInMemory(t *testing.T) {
	src := `
prompts:
  - name: "Legacy Loop"
    prompt: "do something"
    loop:
      trigger: onCompletion
      delay: 30
      maxIterations: 10
`
	rc, err := parseWorkspaceRC([]byte(src))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}
	if len(rc.Prompts) != 1 {
		t.Fatalf("Prompts count = %d, want 1", len(rc.Prompts))
	}
	loop := rc.Prompts[0].Loop
	if loop == nil {
		t.Fatal("Loop = nil, want migrated PromptLoop")
	}
	if len(loop.Trigger) != 1 || loop.Trigger[0] != "onCompletion" {
		t.Errorf("Trigger = %v, want [onCompletion]", loop.Trigger)
	}
	if loop.OnCompletion == nil || loop.OnCompletion.Delay != 30 {
		t.Errorf("OnCompletion = %+v, want Delay=30", loop.OnCompletion)
	}
	if loop.MaxIterations != 10 {
		t.Errorf("MaxIterations = %d, want 10", loop.MaxIterations)
	}
}

// TestWorkspaceRC_InlineLoop_GroupedSchemaUnchanged verifies a loop: block
// already on the grouped schema decodes as-is (no migration fires).
func TestWorkspaceRC_InlineLoop_GroupedSchemaUnchanged(t *testing.T) {
	src := `
prompts:
  - name: "Grouped Loop"
    prompt: "do something"
    loop:
      trigger: [onCompletion]
      onCompletion:
        delay: 45
      maxIterations: 5
`
	rc, err := parseWorkspaceRC([]byte(src))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}
	if len(rc.Prompts) != 1 {
		t.Fatalf("Prompts count = %d, want 1", len(rc.Prompts))
	}
	loop := rc.Prompts[0].Loop
	if loop == nil {
		t.Fatal("Loop = nil, want PromptLoop")
	}
	if loop.OnCompletion == nil || loop.OnCompletion.Delay != 45 {
		t.Errorf("OnCompletion = %+v, want Delay=45", loop.OnCompletion)
	}
}

// TestWorkspaceRC_InlineLoop_InvalidDropsLoopKeepsPrompt verifies an invalid
// loop: block (e.g. an unknown mode) only drops the loop config, not the
// whole prompt, and does not fail the .mittorc load.
func TestWorkspaceRC_InlineLoop_InvalidDropsLoopKeepsPrompt(t *testing.T) {
	src := `
prompts:
  - name: "Bad Loop"
    prompt: "do something"
    loop:
      mode: bogus
`
	rc, err := parseWorkspaceRC([]byte(src))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}
	if len(rc.Prompts) != 1 {
		t.Fatalf("Prompts count = %d, want 1 (prompt kept despite bad loop)", len(rc.Prompts))
	}
	if rc.Prompts[0].Loop != nil {
		t.Errorf("Loop = %+v, want nil for invalid mode", rc.Prompts[0].Loop)
	}
}

// TestWorkspaceRC_InlineLoop_NoWriteBack verifies loading a .mittorc with a
// legacy flat inline loop: block never rewrites the source bytes — the
// migration registry's line-splice write-back only targets top-level
// .prompt.yaml documents, not a loop: nested inside .mittorc's prompts:
// sequence (mitto-opoh: in-memory only, by design).
func TestWorkspaceRC_InlineLoop_NoWriteBack(t *testing.T) {
	src := []byte(`
prompts:
  - name: "Legacy Loop"
    prompt: "do something"
    loop:
      trigger: onCompletion
      delay: 30
`)
	original := bytes.Clone(src)
	if _, err := parseWorkspaceRC(src); err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}
	if !bytes.Equal(src, original) {
		t.Errorf("parseWorkspaceRC mutated its input bytes; got %q, want %q", src, original)
	}
}
