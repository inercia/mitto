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
	cases := []struct {
		file string
		tags []string
	}{
		{"beads-issues/loop-until-complete.prompt.yaml", []string{"Cheap"}},
		{"github/check-pr-comments.prompt.yaml", []string{"Smart", "Coding"}},
		{"skills/add-doc.prompt.yaml", []string{"Smart", "Coding"}},
		{"skills/update-references.prompt.yaml", []string{"Coding"}},
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

func TestBuiltinInvestigatePromptUsesSmartestAndImplementationReadyReport(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	file := "beads-issues/investigate.prompt.yaml"
	data, err := os.ReadFile(filepath.Join("../../config/prompts/builtin", file))
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := ParsePromptFile(file, data, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(prompt.PreferredModels) != 3 ||
		prompt.PreferredModels[0].ModelTag != "Smartest" ||
		prompt.PreferredModels[1].ModelTag != "Reasoning" ||
		prompt.PreferredModels[2].ModelTag != "Smart" {
		t.Errorf("preferred models = %+v, want [Smartest Reasoning Smart]", prompt.PreferredModels)
	}
	report, err := os.ReadFile("../../config/prompts/builtin/beads-issues/shared/investigate-report.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	for _, instruction := range []string{
		"Problem decomposition",
		"Implementation instructions",
		"Code not to touch",
	} {
		if !strings.Contains(string(report), instruction) {
			t.Errorf("investigate-report fragment is missing %q", instruction)
		}
	}
	// Every investigation producer shares the report and the label contract.
	for _, f := range []string{
		"beads-issues/investigate.prompt.yaml",
		"beads-issues/fix-phase-investigate.prompt.yaml",
		"beads-issues/feature-phase-plan.prompt.yaml",
		"beads/investigate-all-more.prompt.yaml",
	} {
		b, err := os.ReadFile(filepath.Join("../../config/prompts/builtin", f))
		if err != nil {
			t.Fatal(err)
		}
		for _, frag := range []string{"investigate-report", "investigation-handoff", "implementation-refined"} {
			if !strings.Contains(string(b), frag) {
				t.Errorf("%s does not use %q", f, frag)
			}
		}
	}
	for _, f := range []string{"beads-issues/fix-phase-fix.prompt.yaml", "beads-issues/feature-phase-implement.prompt.yaml", "beads-issues/loop-until-complete.prompt.yaml"} {
		b, err := os.ReadFile(filepath.Join("../../config/prompts/builtin", f))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "investigation-handoff") {
			t.Errorf("%s does not consume the investigation handoff", f)
		}
	}
	// Every implementing flow shares the work + closure fragments.
	for _, f := range []string{"beads-issues/fix-phase-fix.prompt.yaml", "beads-issues/feature-phase-implement.prompt.yaml"} {
		b, err := os.ReadFile(filepath.Join("../../config/prompts/builtin", f))
		if err != nil {
			t.Fatal(err)
		}
		for _, frag := range []string{"shared/implement-work", "shared/implement-closure", "investigation-handoff"} {
			if !strings.Contains(string(b), frag) {
				t.Errorf("%s does not use %q", f, frag)
			}
		}
	}
	// Entry points that accept an epic share the epic/standalone resolution.
	for _, f := range []string{"beads-issues/investigate.prompt.yaml", "beads-issues/loop-until-complete.prompt.yaml"} {
		b, err := os.ReadFile(filepath.Join("../../config/prompts/builtin", f))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "shared/scope-resolution") {
			t.Errorf("%s does not use scope-resolution", f)
		}
	}
}

func TestBuiltinCheapOuterLoopModelPreferences(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	cases := []struct {
		file string
		tags []string
	}{
		// Cheap-only outer loops (heavy work delegated to children pinned to Coding).
		{"beads-issues/loop-fixing-bug.prompt.yaml", []string{"Cheap"}},
		{"beads-issues/loop-implementing-feature.prompt.yaml", []string{"Cheap"}},
		{"beads-issues/mention-driver.prompt.yaml", []string{"Cheap"}},
		// Babysit loops escalate through the tier ladder when a cheap model is unavailable.
		{"github/babysit-contributions.prompt.yaml", []string{"Cheap", "Smart", "Coding"}},
		{"github/babysit-my-prs.prompt.yaml", []string{"Cheap", "Smart"}},
		{"github/babysit-this-pr.prompt.yaml", []string{"Cheap", "Smart"}},
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
		show   string
		states []string
	}{
		{
			file: "beads-issues/loop-fixing-bug.prompt.yaml",
			show: "bd show {{ $target }} --long --json",
			states: []string{
				"researched", "reproduced", "fixed",
				"Investigation:", "Reproduction:", "Fix:",
			},
		},
		{
			file: "beads-issues/loop-implementing-feature.prompt.yaml",
			show: "bd show {{ $target }} --long --json",
			states: []string{
				"planned", "implemented", "tested", "verified",
				"Plan:", "Implementation:", "Testing:", "Review:",
			},
		},
		{
			file: "beads-issues/mention-driver.prompt.yaml",
			show: "bd show {{ $target }} --json --include-comments",
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
				tc.show,
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
		{"support/investigate.prompt.yaml", []string{"Reasoning", "Smartest", "Smart"}},
		{"support/compose-draft.prompt.yaml", []string{"Smart", "Coding"}},
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

func TestBuiltinStartWorkDispatchesLabelGuidedPhases(t *testing.T) {
	data, err := os.ReadFile("../../config/prompts/builtin/beads-issues/work.prompt.yaml")
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{
		"Bug fix — investigate phase", "Bug fix — reproduce phase", "Bug fix — fix phase",
		"Feature — plan phase", "Feature — implement phase", "Feature — test phase", "Feature — review phase",
		"implementation-refined", "first Fix/Implement",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Start work missing %q", want)
		}
	}
	if !strings.Contains(body, "Only an explicit **Yes, close it**") {
		t.Errorf("Start work must require explicit approval when it owns closure")
	}
}
