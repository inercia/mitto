package prompts

import (
	"strings"
	"testing"
)

// TestBuiltinPrompts_ManyParametersUseGroups keeps large parameter dialogs
// scannable. Smaller dialogs may remain flat when tabs would add more friction
// than clarity, but four or more fields must resolve into at least two tabs.
func TestBuiltinPrompts_ManyParametersUseGroups(t *testing.T) {
	const builtinDir = "../../config/prompts/builtin"

	reg, fragmentErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(%s): %v", builtinDir, err)
	}
	if len(fragmentErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(%s): %d fragment error(s)", builtinDir, len(fragmentErrs))
	}

	builtins, loadErrs, err := LoadPromptsFromDirWithErrorsAndFragments(builtinDir, reg)
	if err != nil {
		t.Fatalf("LoadPromptsFromDirWithErrorsAndFragments(%s): %v", builtinDir, err)
	}
	if len(loadErrs) != 0 {
		for _, loadErr := range loadErrs {
			t.Errorf("prompt failed to load: %s: %v", loadErr.Path, loadErr.Err)
		}
		t.FailNow()
	}

	for _, prompt := range builtins {
		if len(prompt.Parameters) < 4 {
			continue
		}

		effectiveGroups := make(map[string]struct{})
		for _, param := range prompt.Parameters {
			group := strings.TrimSpace(param.Group)
			if group == "" {
				group = "General"
			}
			effectiveGroups[group] = struct{}{}
		}
		if len(effectiveGroups) < 2 {
			t.Errorf("%q has %d parameters but resolves to %d tab; split it into at least two parameter groups",
				prompt.Name, len(prompt.Parameters), len(effectiveGroups))
		}
	}
}
