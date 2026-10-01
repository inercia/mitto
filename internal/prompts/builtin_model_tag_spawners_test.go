package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuiltinModelTagSpawners pins the mitto-conversation-new spawn blocks in
// two builtin loop prompts (support/housekeeping and github/review-slack-prs)
// to use mitto_conversation_new's model_tag parameter instead of the old
// acp_server prose placeholder. See config/prompts/builtin/github/shared/
// spawn-rules.tmpl for the precedent this mirrors.
func TestBuiltinModelTagSpawners(t *testing.T) {
	installBuiltinFragmentsForTest(t)

	cases := []struct {
		file string
		tag  string
	}{
		{"support/housekeeping.prompt.yaml", "Reasoning"},
		{"github/review-slack-prs.prompt.yaml", "Coding"},
		{"beads-issues/loop-fixing-bug.prompt.yaml", "Coding"},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("../../config/prompts/builtin", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			content := string(data)

			wantTag := `model_tag: "` + tc.tag + `"`
			if !strings.Contains(content, wantTag) {
				t.Errorf("%s spawn must pin %s, body:\n%s", tc.file, wantTag, content)
			}

			if strings.Contains(content, "acp_server: <prefer a") {
				t.Errorf("%s still contains the old acp_server prose placeholder", tc.file)
			}

			if !strings.Contains(content, "retry once without") {
				t.Errorf("%s must document a best-effort retry without model_tag when no model matches", tc.file)
			}

			// Canonical-tag membership is validated authoritatively by
			// `make check-model-tags`; this test only pins the prompt
			// body's exact wording (see builtin_model_tiers_test.go for
			// the same convention — importing internal/config here would
			// create an import cycle, since internal/config imports
			// internal/prompts via prompts_shim.go).
		})
	}
}
