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

func TestSupportPhaseModelPreferences(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	cases := []struct {
		file string
		tags []string
	}{
		{"support/watch-channel.prompt.yaml", []string{"Cheap", "Coding"}},
		{"support/investigate.prompt.yaml", []string{"Reasoning"}},
		{"support/compose-draft.prompt.yaml", []string{"Coding"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("../../config/prompts/builtin", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			prompt, err := ParsePromptFile(tc.file, data, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(prompt.PreferredModels) != len(tc.tags) {
				t.Fatalf("preferred models = %+v, want tags %v", prompt.PreferredModels, tc.tags)
			}
			for i, tag := range tc.tags {
				if prompt.PreferredModels[i].ModelTag != tag {
					t.Errorf("preferred model %d = %+v, want %s", i, prompt.PreferredModels[i], tag)
				}
			}
		})
	}
}
