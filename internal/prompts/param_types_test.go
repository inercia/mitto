package prompts

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// knownParamTypesEntryRe matches a quoted string-literal entry line inside
// the KNOWN_PARAM_TYPES array literal, e.g. `  "workspaceFolder",`.
var knownParamTypesEntryRe = regexp.MustCompile(`^\s*"([^"]+)",?\s*$`)

// TestParamTypes_KnownPromptParameterTypesMatchesFrontendRegistry pins the
// mirroring contract documented on KnownPromptParameterTypes (mitto-uqq.1/.3):
// KnownPromptParameterTypes (the Go source of truth) must match
// KNOWN_PARAM_TYPES in web/static/utils/prompts.js EXACTLY, including
// ordinal position — the doc comment on both sides promises "keep byte-for-
// byte in sync". Same docs/code-sync convention as
// TestMenus_KnownMenusMatchesFrontendRegistry (menus_test.go).
func TestParamTypes_KnownPromptParameterTypesMatchesFrontendRegistry(t *testing.T) {
	root := repoRootForTest(t)
	jsPath := filepath.Join(root, "web", "static", "utils", "prompts.js")
	data, err := os.ReadFile(jsPath)
	if err != nil {
		t.Fatalf("read %s: %v", jsPath, err)
	}

	const marker = "export const KNOWN_PARAM_TYPES = ["
	start := strings.Index(string(data), marker)
	if start == -1 {
		t.Fatalf("%s: marker %q not found", jsPath, marker)
	}
	body := string(data)[start+len(marker):]
	end := strings.Index(body, "];")
	if end == -1 {
		t.Fatalf("%s: closing '];' for KNOWN_PARAM_TYPES not found", jsPath)
	}
	body = body[:end]

	var jsTypes []string
	for _, line := range strings.Split(body, "\n") {
		if m := knownParamTypesEntryRe.FindStringSubmatch(line); m != nil {
			jsTypes = append(jsTypes, m[1])
		}
	}
	if len(jsTypes) == 0 {
		t.Fatalf("%s: no entries extracted from KNOWN_PARAM_TYPES body: %q", jsPath, body)
	}

	if !reflect.DeepEqual(jsTypes, KnownPromptParameterTypes) {
		t.Errorf("KNOWN_PARAM_TYPES %v != KnownPromptParameterTypes %v (keep both registries in sync, same ordinal position)", jsTypes, KnownPromptParameterTypes)
	}
}
