package prompts

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestResolveBlockedSchemaAndAuthority(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	data, err := os.ReadFile("../../config/prompts/builtin/beads-issues/resolve-blocked.prompt.yaml")
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParsePromptFile("resolve-blocked.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Resolve blocked phase" || len(p.PreferredModels) != 1 || p.PreferredModels[0].ModelTag != "Smartest" {
		t.Fatalf("unexpected phase schema: %s %+v", p.Name, p.PreferredModels)
	}
	for _, allow := range []string{"", "false", "true"} {
		t.Run("corrections="+allow, func(t *testing.T) {
			ctx := linkedWorkRegressionContext()
			ctx.Args["AllowTrackerCorrections"] = allow
			_, out := renderBuiltinPromptForWorkRegression(t, "beads-issues/resolve-blocked.prompt.yaml", ctx)
			for _, want := range []string{"mitto-baseline", "Verify before declaring it blocked", "ready | prerequisite | blocked | unknown", "Do not ask work/design questions", "Caller owns durable execution-state persistence", "never closes tickets"} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q", want)
				}
			}
			if allow == "true" {
				for _, want := range []string{"Limited correction authority", "Unknown provenance means", "Never remove dependency edges", "never claim/reassign work"} {
					if !strings.Contains(out, want) {
						t.Errorf("missing %q", want)
					}
				}
			} else if !strings.Contains(out, "**Read-only:**") || strings.Contains(out, "**Limited correction authority:**") {
				t.Error("corrections must default to read-only")
			}
			for _, banned := range []string{"mitto_ui_options(", "mitto_ui_textbox(", "mitto_ui_form("} {
				if strings.Contains(out, banned) {
					t.Errorf("interactive UI leaked: %s", banned)
				}
			}
		})
	}
}

func TestEpicLoopResolvesBlockedBeforeStop(t *testing.T) {
	ctx := linkedWorkRegressionContext()
	_, out := renderBuiltinPromptForWorkRegression(t, "beads-issues/loop-until-complete.prompt.yaml", ctx)
	gate := strings.Index(out, "### Resolve blocked before stopping an epic")
	stop := strings.Index(out, `loop_enabled: false`)
	if gate < 0 || stop < gate {
		t.Fatal("diagnosis gate must precede loop disable")
	}
	for _, want := range []string{
		"Standalone tickets retain", "completed-but-open subtree", "Reconcile first",
		"idle-without-report", "not automatically respawned", "post-correction input",
		"Exclude `updated_at`", "current wall time", "Same completed input",
		"own lifecycle", "defer this gate to the next run", "never extend the configured cap",
		`model_tag: "Smartest"`, `prompt_name: "Resolve blocked phase"`,
		"before waiting", "Never fall through to implementation", "next run ranks freshly ready",
		"blocked/unknown", "ClosePolicy change", "last-skipped `<target>`",
		"within the remaining run budget", "without deleting the child or extending the cap",
		"skip spawning and continue Step 5's stop path", "fully specified, and live-routable",
		"persist its target/next phase and end this run", "never overrides Step 2 eligibility",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing gate invariant %q", want)
		}
	}
}

func TestInteractiveUnblockRetainsUIAndSharedDiagnosis(t *testing.T) {
	ctx := linkedWorkRegressionContext()
	_, out := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", ctx)
	for _, want := range []string{"Verify before declaring it blocked", "Propose the smallest concrete next step", "one direct, specific question", "mitto_ui_options"} {
		if !strings.Contains(out, want) {
			t.Errorf("interactive unblock lost %q", want)
		}
	}
}

func TestResolveBlockedSharedCallers(t *testing.T) {
	for _, file := range []string{"beads-issues/work.prompt.yaml", "misc/continue.prompt.yaml"} {
		t.Run(file, func(t *testing.T) {
			for _, scheduled := range []bool{false, true} {
				ctx := linkedWorkRegressionContext()
				ctx.Session.IsLoop = scheduled
				_, out := renderBuiltinPromptForWorkRegression(t, file, ctx)
				for _, want := range []string{`"AllowTrackerCorrections":"false"`, "Same input: no new diagnosis", "Idle/timeout is not evidence", "before waiting", "matching task/target", "post-correction input", "no implementation child", "timeout", "readiness", "restricted"} {
					if !strings.Contains(out, want) {
						t.Errorf("scheduled=%v missing %q", scheduled, want)
					}
				}
				if (!scheduled || file == "beads-issues/work.prompt.yaml") && !strings.Contains(out, "explicit Check blockers") {
					t.Error("interactive diagnosis requires explicit approval")
				}
				if file == "misc/continue.prompt.yaml" {
					container := strings.Index(out, "1. **The bead is an epic/parent")
					leaf := strings.Index(out, "2. **A non-container leaf")
					if container < 0 || leaf <= container {
						t.Error("container must route before leaf implementation")
					}
					if scheduled && !strings.Contains(out, "Do not ask work/design UI questions") {
						t.Error("scheduled diagnostic gate must be noninteractive")
					}
					if scheduled && strings.Contains(out[strings.Index(out, "### B4."):], `Offer "Check blockers"`) {
						t.Error("scheduled B4 must not render interactive diagnosis approval")
					}
				}
			}
		})
	}
	forced := linkedWorkRegressionContext()
	forced.Session.IsLoop = true
	forced.Session.IsLoopForced = true
	_, forcedOut := renderBuiltinPromptForWorkRegression(t, "misc/continue.prompt.yaml", forced)
	if !strings.Contains(forcedOut, `Offer "Check blockers"`) {
		t.Error("forced Continue uses interactive diagnostic approval")
	}
	ctx := linkedWorkRegressionContext()
	ctx.Session.BeadsIssue = ""
	ctx.Session.HasBeadsIssue = false
	ctx.Args = map[string]string{}
	for _, file := range []string{"beads-issues/work.prompt.yaml", "misc/continue.prompt.yaml"} {
		_, out := renderBuiltinPromptForWorkRegression(t, file, ctx)
		if strings.Contains(out, "Diagnostic handoff contract") {
			t.Errorf("%s must not dispatch diagnosis without a ticket", file)
		}
	}
}

func TestUnblockSharedDependencyAndRecoveryPolicy(t *testing.T) {
	ctx := linkedWorkRegressionContext()
	for _, file := range []string{
		"beads-issues/resolve-blocked.prompt.yaml",
		"beads-issues/work.prompt.yaml",
		"beads-issues/explain.prompt.yaml",
		"beads-issues/status.prompt.yaml",
		"beads/reevaluate.prompt.yaml",
	} {
		t.Run(file, func(t *testing.T) {
			_, out := renderBuiltinPromptForWorkRegression(t, file, ctx)
			for _, want := range []string{
				"producer/consumer handoffs", "whether B delivers it",
				"stale assumptions", "knowledge gaps/unsupported impossibility claims",
				"bounded retry with new evidence", "not repeated encouragement",
				"mutation/coordination authority and confirmation",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("missing shared unblock rule %q", want)
				}
			}
		})
	}
}

func TestResolveBlockedEvidenceBackedCoordination(t *testing.T) {
	ctx := linkedWorkRegressionContext()
	_, out := renderBuiltinPromptForWorkRegression(t, "beads-issues/resolve-blocked.prompt.yaml", ctx)
	for _, want := range []string{
		"both blocking and blocked-by directions", "consumer -> producer",
		"closed producer or existing edge does not prove", "never silently expand B",
		"actual task", "attempted commands/errors", "repository precedents",
		"Missing history/access means unknown", "safety refusal, human hold or permission denial",
		"one bounded recovery task with new evidence", "success criterion",
		"authorized parent may relay", "does not message/restart workers",
		"No duplicate workers, claim stealing", "Active other workers remain untouched",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing diagnostic coordination rule %q", want)
		}
	}
	for _, file := range []string{"beads-issues/work.prompt.yaml", "misc/continue.prompt.yaml", "beads-issues/loop-until-complete.prompt.yaml"} {
		_, caller := renderBuiltinPromptForWorkRegression(t, file, ctx)
		if !strings.Contains(caller, "producer/test/worker evidence") || !strings.Contains(caller, "Same input: no new diagnosis") {
			t.Errorf("%s must consider new evidence without retrying unchanged input", file)
		}
	}
}
