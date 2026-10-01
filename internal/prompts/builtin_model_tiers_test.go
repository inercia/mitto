package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuiltinCostAwareModelPreferences(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	files := []string{
		"beads-issues/loop-until-complete.prompt.yaml",
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

func TestBuiltinCheapOuterLoopModelPreferences(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	files := []string{
		"beads-issues/loop-fixing-bug.prompt.yaml",
		"beads-issues/loop-implementing-feature.prompt.yaml",
		"beads-issues/mention-driver.prompt.yaml",
		"github/babysit-contributions.prompt.yaml",
		"github/babysit-my-prs.prompt.yaml",
		"github/babysit-this-pr.prompt.yaml",
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
			if len(prompt.PreferredModels) != 1 || prompt.PreferredModels[0].ModelTag != "Cheap" {
				t.Errorf("preferred models = %+v, want only Cheap", prompt.PreferredModels)
			}
		})
	}
	for _, frag := range []string{"pr-comments", "pr-fix-ci", "pr-rebase-if-behind"} {
		data, err := os.ReadFile(filepath.Join("../../config/prompts/builtin/github/shared", frag+".tmpl"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `model_tag: "Coding"`) {
			t.Errorf("%s spawn must pin model_tag Coding", frag)
		}
	}
}

func TestBuiltinBeadsDriversUseFreshDurableTaskTriggers(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	cases := []struct {
		file   string
		states []string
	}{
		{
			file: "beads-issues/loop-fixing-bug.prompt.yaml",
			states: []string{
				"researched", "reproduced", "fixed",
				"Investigation:", "Reproduction:", "Fix:",
			},
		},
		{
			file: "beads-issues/loop-implementing-feature.prompt.yaml",
			states: []string{
				"planned", "implemented", "tested", "verified",
				"Plan:", "Implementation:", "Testing:", "Review:",
			},
		},
		{
			file: "beads-issues/mention-driver.prompt.yaml",
			states: []string{
				"mention-investigated", "mention-planned", "mention-implemented",
				"mention-answered", "[addressed-comment: {{ $ts }}]",
			},
		},
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
			if prompt.Loop == nil {
				t.Fatal("loop is nil")
			}
			if prompt.Loop.FreshContext == nil || !*prompt.Loop.FreshContext {
				t.Error("freshContext must be true")
			}
			if len(prompt.Loop.Trigger) != 2 {
				t.Errorf("triggers = %v, want onTasks and onCompletion", prompt.Loop.Trigger)
			}
			hasTrigger := func(want string) bool {
				for _, got := range prompt.Loop.Trigger {
					if got == want {
						return true
					}
				}
				return false
			}
			for _, want := range []string{"onTasks", "onCompletion"} {
				if !hasTrigger(want) {
					t.Errorf("triggers = %v, missing %q", prompt.Loop.Trigger, want)
				}
			}
			if prompt.Loop.OnTasks == nil {
				t.Fatal("onTasks settings are missing")
			}
			if prompt.Loop.OnTasks.SettleWindow != 30 || prompt.Loop.OnTasks.Cooldown != 600 {
				t.Errorf("onTasks settings = %+v, want settleWindow 30s and cooldown 600s", prompt.Loop.OnTasks)
			}
			if prompt.Loop.OnCompletion == nil || prompt.Loop.OnCompletion.Delay != 30 {
				t.Errorf("onCompletion = %+v, want delay 30s", prompt.Loop.OnCompletion)
			}

			for _, marker := range []string{
				"bd show {{ $target }} --json --include-comments",
				`eq .Kind "onTasks"`,
				`eq .Kind "onCompletion"`,
			} {
				if !strings.Contains(prompt.Content, marker) {
					t.Errorf("prompt content is missing %q", marker)
				}
			}
			for _, state := range tc.states {
				if !strings.Contains(prompt.Content, state) {
					t.Errorf("prompt content is missing durable state marker %q", state)
				}
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
