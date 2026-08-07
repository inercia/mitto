package prompts

import (
	"strings"
	"testing"
)

// =============================================================================
// PrecompileTemplateConds tests
//
// Moved from templatefuncs_test.go when the CEL evaluator/context/templatefuncs
// were extracted into internal/cel (mitto-b8k.3). PrecompileTemplateConds lives
// in internal/config/prompt_template.go so its tests stay in this package.
// =============================================================================

// TestPrecompileTemplateConds_Valid returns nil for valid literal Cond args.
func TestPrecompileTemplateConds_Valid(t *testing.T) {
	body := `{{ if Cond "Session.IsChild" }}child{{ end }}`
	if err := PrecompileTemplateConds("my-prompt", body); err != nil {
		t.Errorf("expected nil for valid cond, got: %v", err)
	}
}

// TestPrecompileTemplateConds_Invalid returns non-nil error for invalid CEL.
func TestPrecompileTemplateConds_Invalid(t *testing.T) {
	body := `{{ if Cond "this is ::: not valid CEL" }}x{{ end }}`
	err := PrecompileTemplateConds("my-prompt", body)
	if err == nil {
		t.Fatal("expected non-nil error for invalid CEL literal, got nil")
	}
	// Error message must include prompt name and "cond precompile".
	if !strings.Contains(err.Error(), "my-prompt") {
		t.Errorf("error missing prompt name: %v", err)
	}
	if !strings.Contains(err.Error(), "cond precompile") {
		t.Errorf("error missing 'cond precompile': %v", err)
	}
}

// TestPrecompileTemplateConds_NoTemplate returns nil for bodies without {{}}.
func TestPrecompileTemplateConds_NoTemplate(t *testing.T) {
	if err := PrecompileTemplateConds("p", "plain text ${VAR} @mitto:x"); err != nil {
		t.Errorf("expected nil for no-template body, got: %v", err)
	}
}

// TestPrecompileTemplateConds_ValidWhen returns nil when using the When alias.
func TestPrecompileTemplateConds_ValidWhen(t *testing.T) {
	body := `{{ if When "!Session.IsChild" }}root{{ end }}`
	if err := PrecompileTemplateConds("p", body); err != nil {
		t.Errorf("expected nil for valid when alias, got: %v", err)
	}
}

// TestPrecompileTemplateConds_ParseError returns an error for template parse failures.
func TestPrecompileTemplateConds_ParseError(t *testing.T) {
	body := `{{ if Cond "true" }}no end`
	err := PrecompileTemplateConds("p", body)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

// TestPrecompileTemplateConds_UnknownFragmentFailsWithNilRegistry pins the
// mitto-ezw paradox contract at the PrecompileTemplateConds entry point:
// a prompt that references a fragment with NO registry installed (the
// package default before web.NewServer bootstraps one) must fail load-time
// precompile with the characteristic `template "…" not defined` error.
//
// This is the exact failure mode the runtime WARN at server.go:1356 advertises
// (`Fragment registry bootstrapped empty; prompts that reference {{ template
// "_shared/…" . }} will fail to load…`) — proving the WARN's premise is real,
// and pinning it at the smallest surface (no ParsePromptFile envelope, no
// ValidatePromptTemplateSyntax wrapper) so any regression in the attach
// loop's nil-registry branch surfaces immediately.
func TestPrecompileTemplateConds_UnknownFragmentFailsWithNilRegistry(t *testing.T) {
	// Belt-and-braces: some other test may have installed a registry and
	// forgotten to clear it. Explicitly reset to the true default (nil).
	prev := CurrentFragments()
	SetCurrentFragments(nil)
	t.Cleanup(func() { SetCurrentFragments(prev) })

	body := `intro {{ template "_shared/session-context" . }} outro`
	err := PrecompileTemplateConds("paradox-nil", body)
	if err == nil {
		t.Fatal("expected error for fragment reference with nil registry, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "paradox-nil") {
		t.Errorf("error %q should name the prompt (%q)", msg, "paradox-nil")
	}
	if !strings.Contains(msg, "_shared/session-context") {
		t.Errorf("error %q should name the missing fragment (%q)", msg, "_shared/session-context")
	}
	if !strings.Contains(msg, "not defined") {
		t.Errorf("error %q should mention 'not defined' (paradox signature)", msg)
	}
}

// TestPrecompileTemplateConds_UnknownFragmentFailsWithEmptyRegistry pins the
// second half of the mitto-ezw paradox contract: an EMPTY (non-nil, zero-
// entry) registry is functionally equivalent to no registry — a prompt
// referencing a fragment must still fail precompile. This is the exact
// state the runtime WARN targets: bootstrap ran but produced reg.Len() == 0,
// which silently defeats every fragment-using prompt. Distinct from the
// nil-registry test above because the attach loop's guard is
// `if frags := CurrentFragments(); frags != nil` — an empty non-nil
// registry takes the attach branch but attaches nothing, and must not
// somehow rescue the reference.
func TestPrecompileTemplateConds_UnknownFragmentFailsWithEmptyRegistry(t *testing.T) {
	prev := CurrentFragments()
	SetCurrentFragments(NewFragmentRegistry())
	t.Cleanup(func() { SetCurrentFragments(prev) })

	body := `intro {{ template "_shared/session-context" . }} outro`
	err := PrecompileTemplateConds("paradox-empty", body)
	if err == nil {
		t.Fatal("expected error for fragment reference with empty registry, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "paradox-empty") {
		t.Errorf("error %q should name the prompt (%q)", msg, "paradox-empty")
	}
	if !strings.Contains(msg, "_shared/session-context") {
		t.Errorf("error %q should name the missing fragment (%q)", msg, "_shared/session-context")
	}
	if !strings.Contains(msg, "not defined") {
		t.Errorf("error %q should mention 'not defined' (paradox signature)", msg)
	}
}

// TestPrecompileTemplateConds_KnownFragmentSucceedsWithPopulatedRegistry
// completes the paradox-contract matrix: with a registry that contains the
// referenced fragment, PrecompileTemplateConds succeeds — the same body that
// fails in the nil/empty cases above must load cleanly. This is the exact
// state a healthy runtime achieves after server.go bootstraps the fragment
// registry from getFragmentScanDirs() before the first prompts cache reload.
// Together the three tests pin the WARN's contract from both sides:
// empty ⇒ paradox, populated ⇒ recovery.
func TestPrecompileTemplateConds_KnownFragmentSucceedsWithPopulatedRegistry(t *testing.T) {
	prev := CurrentFragments()
	SetCurrentFragments(&FragmentRegistry{entries: map[string]string{
		"_shared/session-context": "hello from the fragment",
	}})
	t.Cleanup(func() { SetCurrentFragments(prev) })

	body := `intro {{ template "_shared/session-context" . }} outro`
	if err := PrecompileTemplateConds("paradox-recovered", body); err != nil {
		t.Fatalf("expected nil for fragment ref with populated registry, got: %v", err)
	}
}

// TestPrecompileTemplateConds_UnknownWorkspaceField is the reproduction for
// mitto-cubg: a prompt template referencing a field that does not exist on
// cel.WorkspaceContext (e.g. the real-world
// .mitto/prompts/profile-ui-with-playwright.prompt.yaml, which used
// "{{ .Workspace.WorkingDir }}" instead of the actual field "Folder") must
// fail PrecompileTemplateConds at load time with a "can't evaluate field"
// error naming the bad field — causing LoadPromptFile to reject the file and
// the prompt to be dropped from mitto_prompt_list, exactly as observed for
// "Profile UI with Playwright" in this workspace.
//
// This pins the failure at the smallest surface (PrecompileTemplateConds
// itself, no LoadPromptFile/LoadPromptsFromDirWithErrors envelope) so the fix
// (rewriting the prompt body to use ".Workspace.Folder") can be verified by
// asserting the corrected body precompiles cleanly.
func TestPrecompileTemplateConds_UnknownWorkspaceField(t *testing.T) {
	badBody := `import from "{{ .Workspace.WorkingDir }}/node_modules/playwright/index.mjs";`
	err := PrecompileTemplateConds("profile-ui-with-playwright", badBody)
	if err == nil {
		t.Fatal("expected non-nil error for unknown .Workspace.WorkingDir field, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "WorkingDir") {
		t.Errorf("error %q should name the missing field %q", msg, "WorkingDir")
	}
	if !strings.Contains(msg, "can't evaluate field") {
		t.Errorf("error %q should be a struct-field-evaluation error (\"can't evaluate field\")", msg)
	}
	if !strings.Contains(msg, "profile-ui-with-playwright") {
		t.Errorf("error %q should name the prompt", msg)
	}

	// The corrected accessor (the real field on cel.WorkspaceContext) must
	// precompile cleanly — proves the fix direction, not just the bug.
	goodBody := `import from "{{ .Workspace.Folder }}/node_modules/playwright/index.mjs";`
	if err := PrecompileTemplateConds("profile-ui-with-playwright", goodBody); err != nil {
		t.Errorf("expected nil for corrected .Workspace.Folder field, got: %v", err)
	}
}
