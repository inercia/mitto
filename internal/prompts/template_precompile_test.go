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
