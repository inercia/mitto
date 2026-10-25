package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/cel"
)

const proposeSomethingBuiltin = "misc/propose-something.prompt.yaml"

func TestBuiltinProposeSomethingConversationOnly(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	data, err := os.ReadFile(filepath.Join("../../config/prompts/builtin", proposeSomethingBuiltin))
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := ParsePromptFile(proposeSomethingBuiltin, data, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if prompt.Name != "Propose something" || prompt.Menus != "conversation" {
		t.Fatalf("name/menus = %q/%q, want Propose something/conversation only", prompt.Name, prompt.Menus)
	}
	evaluator, err := cel.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := evaluator.Compile(prompt.EnabledWhen)
	if err != nil {
		t.Fatal(err)
	}
	for _, hasMessages := range []bool{false, true} {
		for _, hasBead := range []bool{false, true} {
			ctx := &cel.PromptEnabledContext{
				Workspace: cel.WorkspaceContext{Folder: t.TempDir()},
				Session:   cel.SessionContext{HasMessages: hasMessages, HasBeadsIssue: hasBead},
			}
			got, err := evaluator.Evaluate(compiled, ctx)
			if err != nil || got != hasMessages {
				t.Errorf("messages=%v linked=%v: enabled=%v err=%v, want %v", hasMessages, hasBead, got, err, hasMessages)
			}
		}
	}
}

func TestBuiltinProposeSomethingRendersDiagnosisAndAdviceOnly(t *testing.T) {
	for _, ctx := range []*cel.PromptEnabledContext{{}, linkedWorkRegressionContext()} {
		_, body := renderBuiltinPromptForWorkRegression(t, proposeSomethingBuiltin, ctx)
		body = strings.Join(strings.Fields(body), " ")
		for _, want := range []string{
			"latest user clarifications", "confirmed blockers from uncertainty",
			"An earlier proposal is not an approved requirement", "already tried or rejected",
			"one concrete, proportionate next move", "smallest reversible step",
			"Check recent agent questions and decision requests",
			"Do not reopen questions already answered, rejected, or superseded",
			"do not merely repeat it or say that you are waiting",
			"Recommend a specific answer, decision, or path",
			"give a short reason for the recommendation",
			"at most two alternatives alongside the recommended path",
			"give each a short reason and the key tradeoff",
			"Do not manufacture options", "Present these choices through the Mitto UI options",
			"do not select it or infer approval from the unanswered question",
			"offer conditional paths or a bounded evidence-gathering step",
			"do not invent the user's preferences",
			"propose a concise plan", "goal and scope (with non-goals)",
			"ordered steps, dependencies, material risks and mitigations",
			"verification with observable completion criteria",
			"not verified facts", "Do not impose ticket creation",
			"single most useful clarifying question", "no genuine blocker",
			"Do not assume this is a coding task or require a Beads workspace",
			"**Goal:**", "**What is holding us back:**", "**My proposal:**",
			"**First step and success signal:**", "This is advice only",
			"Do not execute the proposal, edit files, run commands, change tickets",
			"Do not treat invoking this prompt as approval to act",
			"wait for the user's direction",
			"mitto_ui_options(self_id:", "mitto_ui_form(self_id:", "mitto_ui_textbox",
			"Mark the recommended choice", "revise/decline choice",
			"they do not authorize implementation or ticket creation",
			"UI tool is unavailable", "Cancellation, timeout, or no response is not consent",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("rendered proposal missing %q", want)
			}
		}
		for _, forbidden := range []string{"bd show", "bd create", "bd update", "bd close", "mitto-baseline", "<no value>", "{{"} {
			if strings.Contains(body, forbidden) {
				t.Errorf("conversation-level proposal leaked %q", forbidden)
			}
		}
	}
}
