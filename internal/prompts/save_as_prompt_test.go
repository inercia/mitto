package prompts

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/cel"
)

func TestSaveAsPrompt_Metadata(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	const path = "../../config/prompts/builtin/misc/save-as-prompt.prompt.yaml"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParsePromptFile(path, data, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Warnings) != 0 {
		t.Errorf("parse warnings: %v", p.Warnings)
	}
	if p.Name != "Save as prompt" || p.Group != "Agents & Mitto" {
		t.Errorf("unexpected name/group: %q/%q", p.Name, p.Group)
	}
	if p.Menus != "prompts, conversation, !promptsLoop" || p.Target != nil || p.Loop != nil || p.Singleton {
		t.Error("must run in the existing conversation, not route to a new or loop session")
	}
	if p.EnabledWhen != "Session.HasMessages && !Session.IsLoopConversation && Permissions.CanPromptUser" {
		t.Errorf("unexpected conversation gate: %q", p.EnabledWhen)
	}
	evaluator, err := cel.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	gate, err := evaluator.Compile(p.EnabledWhen)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		hasMessages bool
		isLoop      bool
		canPrompt   bool
		want        bool
	}{
		{"interactive conversation", true, false, true, true},
		{"no history", false, false, true, false},
		{"loop conversation", true, true, true, false},
		{"UI permission disabled", true, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := evaluator.Evaluate(gate, &cel.PromptEnabledContext{
				Session:     cel.SessionContext{HasMessages: tc.hasMessages, IsLoopConversation: tc.isLoop},
				Permissions: cel.PermissionsContext{CanPromptUser: tc.canPrompt},
			})
			if err != nil || got != tc.want {
				t.Errorf("gate = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	if len(p.PreferredModels) != 2 || p.PreferredModels[0].ModelTag != "Reasoning" || p.PreferredModels[1].ModelTag != "Smart" {
		t.Errorf("preferred models = %+v, want Reasoning then Smart", p.PreferredModels)
	}
	if len(p.Parameters) != 2 {
		t.Fatalf("parameters = %d, want 2", len(p.Parameters))
	}
	name, context := p.Parameters[0], p.Parameters[1]
	// required plain text would hide this prompt in both menus. show: always
	// opens the dialog; the prompt itself asks for a name if left blank.
	if name.Name != "PromptName" || name.Type != "text" || name.Show != "always" || name.MultiLine || name.Required == nil || *name.Required {
		t.Errorf("unexpected name parameter: %+v", name)
	}
	if context.Name != "Context" || context.Type != "text" || !context.MultiLine || context.Required == nil || *context.Required {
		t.Errorf("unexpected context parameter: %+v", context)
	}
}

func TestSaveAsPrompt_Render(t *testing.T) {
	cases := []struct {
		name string
		args map[string]string
	}{
		{"no arguments", nil},
		{"blank arguments", map[string]string{"PromptName": "  ", "Context": "\n  "}},
		{"name only", map[string]string{"PromptName": "Debug pod"}},
		{"focused context", map[string]string{
			"PromptName": `Debug pod: "read-only"`,
			"Context":    "Only pod diagnosis.\nExclude deployments; keep {{ .Args.Namespace }} for later.",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := renderBuiltinPromptWithFragments(t, "Save as prompt", &cel.PromptEnabledContext{
				Session:   cel.SessionContext{ID: "save-prompt-session", HasMessages: true},
				Workspace: cel.WorkspaceContext{Folder: "/projects/example app"},
				Args:      tc.args,
			})
			for _, want := range []string{
				"Your session ID is `save-prompt-session`",
				"Project folder: `/projects/example app`",
				"work in **this current conversation**",
				"without this conversation",
				"mitto_conversation_history",
				"for **this session only**",
				"Clearly distinguish verified steps from proposed improvements",
				".mitto/prompts/debug-pod.prompt.yaml",
				"plain `debug-pod.yaml` is not loaded",
				"both filename and display-name collisions",
				"escape the project through symlinks",
				"Never copy secrets",
				"not** standing approval for future runs",
				"mitto_ui_textbox",
				"timeout, do not write anything",
				"re-check for concurrent changes/collisions",
				"mitto prompts verify --dir",
				"mitto prompts render <name> --dir",
				"Leave this source conversation intact",
				// These are examples for the GENERATED prompt, not values to bake in.
				"`{{ .Args.Pod }}`",
				"`{{ .Session.ID }}`",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("render missing %q", want)
				}
			}
			if strings.Contains(out, "<no value>") || strings.Contains(out, `{{ template "`) {
				t.Error("render leaked missing values or unexpanded fragments")
			}
			if name := tc.args["PromptName"]; strings.TrimSpace(name) != "" {
				if !strings.Contains(out, "Requested prompt name: "+name) || strings.Contains(out, "No prompt name was supplied") {
					t.Error("supplied name was lost or missing-name fallback leaked")
				}
			} else if !strings.Contains(out, "No prompt name was supplied") {
				t.Error("missing name must trigger a clarifying question")
			}
			if context := tc.args["Context"]; strings.TrimSpace(context) != "" {
				if !strings.Contains(out, context) || strings.Contains(out, "No context filter was supplied") {
					t.Error("context guidance must survive verbatim, without recursive rendering")
				}
			} else if !strings.Contains(out, "No context filter was supplied") {
				t.Error("missing context must infer scope or ask when multiple topics exist")
			}
		})
	}
}
