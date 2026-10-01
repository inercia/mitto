package prompts

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuiltinCostAwareModelPreferences(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	files := []string{
		"beads-issues/loop-fixing-bug.prompt.yaml",
		"beads-issues/loop-implementing-feature.prompt.yaml",
		"beads-issues/loop-until-complete.prompt.yaml",
		"beads-issues/mention-driver.prompt.yaml",
		"github/babysit-contributions.prompt.yaml",
		"github/babysit-my-prs.prompt.yaml",
		"github/babysit-this-pr.prompt.yaml",
		"github/check-pr-comments.prompt.yaml",
		"support/watch-channel.prompt.yaml",
		"skills/add-doc.prompt.yaml",
		"skills/update-references.prompt.yaml",
	}
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("../../config/prompts/builtin", file))
			if err != nil {
				t.Fatal(err)
			}
			prompt, err := ParsePromptFile(file, data, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(prompt.PreferredModels) != 1 || prompt.PreferredModels[0].ModelTag != "Coding" {
				t.Errorf("preferred models = %+v, want only Coding", prompt.PreferredModels)
			}
		})
	}
}
