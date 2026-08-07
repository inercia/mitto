package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestBootstrapBeadsFragmentRenders is a smoke test for the
// beads-issues/shared/bootstrap fragment: it loads the builtin fragment registry,
// renders each consuming prompt, and asserts that hallmark substrings from
// the fragment body appear in the rendered output. Presence of a hallmark
// in the rendered output means the {{ template "beads-issues/shared/bootstrap"
// . }} call actually resolved and inlined its body.
//
// The fragment is conditional on the workspace lacking a .beads/ directory, so
// each case pins Workspace.Folder to a temp dir: an empty one exercises the
// bootstrap branch, one containing .beads/ exercises the no-op branch.
func TestBootstrapBeadsFragmentRenders(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}

	// Hallmarks unique to the beads/bootstrap fragment body.
	const (
		hallmarkHeading     = "## Step 0 — Bootstrap beads"
		hallmarkInitCommand = "bd init --non-interactive"
		hallmarkQuestion    = `"Yes — initialise beads"`
		hallmarkUnattended  = "cannot answer a question"
	)

	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	consumers := []string{
		"New issue",
		"Identify follow-up issues",
		"Analyze logs",
		"Architectural Analysis",
		"GitHub: post-merge cleanup",
		"On-Call: I have been paged",
		"Support: watch channel",
		"Loop fixing",
		"Loop implementing",
	}

	// Uninitialised workspace: the bootstrap step must render.
	noBeads := t.TempDir()
	interactiveCtx := &cel.PromptEnabledContext{
		Session:   cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
		Workspace: cel.WorkspaceContext{Folder: noBeads},
	}
	interactiveFuncs := cel.BuildTemplateFuncMap(interactiveCtx)
	for _, name := range consumers {
		body, ok := byName[name]
		if !ok {
			t.Errorf("prompt %q not found in builtin corpus", name)
			continue
		}
		out, err := RenderPromptTemplate(name, body, interactiveCtx, interactiveFuncs)
		if err != nil {
			t.Errorf("render %q: %v", name, err)
			continue
		}
		for _, hallmark := range []string{hallmarkHeading, hallmarkQuestion, hallmarkInitCommand} {
			if !strings.Contains(out, hallmark) {
				t.Errorf("prompt %q: rendered output missing hallmark %q — fragment did not inline", name, hallmark)
			}
		}
		if strings.Contains(out, hallmarkUnattended) {
			t.Errorf("prompt %q: interactive render took the loop (unattended) branch", name)
		}
	}

	// Loop conversation: initialise unattended, never ask.
	loopCtx := &cel.PromptEnabledContext{
		Session:   cel.SessionContext{ID: "s", Name: "N", HasMessages: true, IsLoopConversation: true},
		Workspace: cel.WorkspaceContext{Folder: noBeads},
	}
	loopFuncs := cel.BuildTemplateFuncMap(loopCtx)
	for _, name := range consumers {
		out, err := RenderPromptTemplate(name, byName[name], loopCtx, loopFuncs)
		if err != nil {
			t.Errorf("render %q (loop): %v", name, err)
			continue
		}
		for _, hallmark := range []string{hallmarkHeading, hallmarkInitCommand, hallmarkUnattended} {
			if !strings.Contains(out, hallmark) {
				t.Errorf("prompt %q (loop): missing hallmark %q", name, hallmark)
			}
		}
		if strings.Contains(out, hallmarkQuestion) {
			t.Errorf("prompt %q (loop): unattended branch must not ask the user", name)
		}
	}

	// Already-initialised workspace: the fragment must render nothing.
	withBeads := t.TempDir()
	if err := os.MkdirAll(filepath.Join(withBeads, ".beads"), 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	initialisedCtx := &cel.PromptEnabledContext{
		Session:   cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
		Workspace: cel.WorkspaceContext{Folder: withBeads},
	}
	initialisedFuncs := cel.BuildTemplateFuncMap(initialisedCtx)
	for _, name := range consumers {
		out, err := RenderPromptTemplate(name, byName[name], initialisedCtx, initialisedFuncs)
		if err != nil {
			t.Errorf("render %q (initialised): %v", name, err)
			continue
		}
		if strings.Contains(out, hallmarkHeading) {
			t.Errorf("prompt %q: bootstrap step rendered even though .beads/ exists", name)
		}
	}
}

// TestLoadBeadContextFragmentRenders is a smoke test for the
// beads-issues/shared/load-context fragment: it renders each of the consuming
// prompts and asserts that (a) each bd command hallmark appears and (b) the
// bead ID substitutes correctly — both on the `$target` runtime path and on
// the literal `<bead-id>` placeholder path used by the multi-bead caller
// ("Investigate ALL more").
func TestLoadBeadContextFragmentRenders(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}

	// ctx supplies a target bead via Session.BeadsIssue — the callers pick
	// $target from Session.BeadsIssue OR .Args.IssueID, so populating both
	// is safe.
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true, BeadsIssue: "mitto-abc", HasBeadsIssue: true},
		Args:    map[string]string{"IssueID": "mitto-abc", "Topic": "test topic"},
	}

	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}
	funcs := cel.BuildTemplateFuncMap(ctx)

	consumers := []string{"Assess issue readiness", "Discuss a topic", "Investigate more"}
	// Hallmark substrings that appear (with the target substituted) in the
	// rendered output whenever the fragment inlines successfully.
	hallmarks := []string{
		"bd show mitto-abc --long --json",
		"bd dep tree mitto-abc",
		"bd show mitto-abc --children --json",
		"bd comments mitto-abc",
	}
	for _, name := range consumers {
		body, ok := byName[name]
		if !ok {
			t.Errorf("prompt %q not found in builtin corpus", name)
			continue
		}
		out, err := RenderPromptTemplate(name, body, ctx, funcs)
		if err != nil {
			t.Errorf("render %q: %v", name, err)
			continue
		}
		for _, hallmark := range hallmarks {
			if !strings.Contains(out, hallmark) {
				t.Errorf("prompt %q: rendered output missing hallmark %q — fragment did not inline or target did not substitute", name, hallmark)
			}
		}
	}

	// The multi-bead caller iterates a candidate list, so it passes the
	// literal `<bead-id>` placeholder rather than a resolved target.
	litName := "Investigate ALL more"
	litBody, ok := byName[litName]
	if !ok {
		t.Fatalf("prompt %q not found in builtin corpus", litName)
	}
	litOut, err := RenderPromptTemplate(litName, litBody, ctx, funcs)
	if err != nil {
		t.Fatalf("render %q: %v", litName, err)
	}
	for _, hallmark := range []string{
		"bd show <bead-id> --long --json",
		"bd dep tree <bead-id>",
		"bd show <bead-id> --children --json",
		"bd comments <bead-id>",
	} {
		if !strings.Contains(litOut, hallmark) {
			t.Errorf("prompt %q: rendered output missing hallmark %q — load-context did not inline", litName, hallmark)
		}
	}
}

// TestLoadBeadContextMinFragmentRenders is a smoke test for the
// beads-issues/shared/load-context-min fragment: renders each of the callers
// that adopted the 2-line loader and asserts (a) both bd hallmarks appear
// and (b) the target-id substitution wins (both the `<bead-id>` literal
// placeholder path and the `$target` runtime path). A regression that
// silently drops the fragment or breaks the argument passthrough is caught.
func TestLoadBeadContextMinFragmentRenders(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}
	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	// Callers that inline `<bead-id>` literally (no target resolution).
	literalConsumers := []string{
		"Cleanup stale issues",
		"Reevaluate all issues",
		"Status ALL in-progress",
		"Status ONE in-progress",
	}
	litCtx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
	}
	litFuncs := cel.BuildTemplateFuncMap(litCtx)
	for _, name := range literalConsumers {
		body, ok := byName[name]
		if !ok {
			t.Errorf("prompt %q not found in builtin corpus", name)
			continue
		}
		out, err := RenderPromptTemplate(name, body, litCtx, litFuncs)
		if err != nil {
			t.Errorf("render %q: %v", name, err)
			continue
		}
		for _, hallmark := range []string{
			"bd show <bead-id> --long --json",
			"bd dep tree <bead-id>",
		} {
			if !strings.Contains(out, hallmark) {
				t.Errorf("prompt %q: missing hallmark %q — load-context-min did not inline", name, hallmark)
			}
		}
	}

	// Caller that substitutes `$target` from Session.BeadsIssue.
	targetCtx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true, BeadsIssue: "mitto-abc", HasBeadsIssue: true},
		Args:    map[string]string{"IssueID": "mitto-abc"},
	}
	targetFuncs := cel.BuildTemplateFuncMap(targetCtx)
	body, ok := byName["Show status"]
	if !ok {
		t.Fatalf("prompt \"Show status\" not found in builtin corpus")
	}
	out, err := RenderPromptTemplate("Show status", body, targetCtx, targetFuncs)
	if err != nil {
		t.Fatalf("render \"Show status\": %v", err)
	}
	for _, hallmark := range []string{
		"bd show mitto-abc --long --json",
		"bd dep tree mitto-abc",
	} {
		if !strings.Contains(out, hallmark) {
			t.Errorf("prompt \"Show status\": missing hallmark %q — load-context-min did not substitute $target", hallmark)
		}
	}
}

// TestTargetBeadHeaderFragmentRenders is a smoke test for the flexible
// beads-issues/shared/target-bead-header fragment: renders each Pattern A consumer
// in BOTH branches (linked bead + no linked bead) and asserts the
// branch-specific hallmark substrings appear (with the target substituted
// where relevant), so a regression that breaks the dict parameterization,
// the with-block-based Noun default, or the fragment attachment is caught.
func TestTargetBeadHeaderFragmentRenders(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}
	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	// Pattern A consumers of beads-issues/shared/target-bead-header: they render
	// "The **target bead** is `<id>`. <Role>" when a bead is linked, and
	// "There is **no linked bead** for this conversation. <NoTarget>" when
	// none is resolvable.
	consumers := []string{
		"Assess issue readiness",
		"Discuss a topic",
		"Investigate more",
		"Check if issue resolved",
		"Show status",
		"Start work",
	}

	// linked branch
	linkedCtx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true, BeadsIssue: "mitto-abc", HasBeadsIssue: true},
		Args:    map[string]string{"Topic": "test topic"},
	}
	linkedFuncs := cel.BuildTemplateFuncMap(linkedCtx)
	for _, name := range consumers {
		body, ok := byName[name]
		if !ok {
			t.Errorf("prompt %q not found", name)
			continue
		}
		out, err := RenderPromptTemplate(name, body, linkedCtx, linkedFuncs)
		if err != nil {
			t.Errorf("render %q (linked): %v", name, err)
			continue
		}
		want := "The **target bead** is `mitto-abc`."
		if !strings.Contains(out, want) {
			t.Errorf("prompt %q (linked): missing %q — target-bead-header did not inline or noun default failed", name, want)
		}
		// The failure phrase must NOT appear in the linked branch.
		if strings.Contains(out, "There is **no linked bead** for this conversation.") {
			t.Errorf("prompt %q (linked): fallback branch rendered — dict.Target not honoured", name)
		}
	}

	// no-link branch
	noLinkCtx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
		Args:    map[string]string{"Topic": "test topic"},
	}
	noLinkFuncs := cel.BuildTemplateFuncMap(noLinkCtx)
	for _, name := range consumers {
		body := byName[name]
		out, err := RenderPromptTemplate(name, body, noLinkCtx, noLinkFuncs)
		if err != nil {
			t.Errorf("render %q (nolink): %v", name, err)
			continue
		}
		want := "There is **no linked bead** for this conversation."
		if !strings.Contains(out, want) {
			t.Errorf("prompt %q (nolink): missing %q — target-bead-header fallback did not render", name, want)
		}
	}
}

// TestTargetBeadHeaderStrictFragmentRenders is the same smoke check for the
// beads-issues/shared/target-bead-header-strict fragment used by every phase /
// mention-driver / mention-phase-* prompt.
func TestTargetBeadHeaderStrictFragmentRenders(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}
	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	// Each entry is (prompt name, noun, scope). Every phase uses "for this
	// phase"; mention-driver uses "for this run".
	type consumer struct{ name, noun, scope string }
	consumers := []consumer{
		{"Feature — plan phase", "target feature", "for this phase"},
		{"Feature — implement phase", "target feature", "for this phase"},
		{"Feature — test phase", "target feature", "for this phase"},
		{"Feature — review phase", "target feature", "for this phase"},
		{"Bug fix — reproduce phase", "target bug", "for this phase"},
		{"Bug fix — investigate phase", "target bug", "for this phase"},
		{"Bug fix — fix phase", "target bug", "for this phase"},
		{"Mention — driver", "target bead", "for this run"},
		{"Mention — answer phase", "target bead", "for this phase"},
		{"Mention — implement phase", "target bead", "for this phase"},
		{"Mention — investigate phase", "target bead", "for this phase"},
		{"Mention — plan phase", "target bead", "for this phase"},
	}

	// linked branch — target bead present.
	linkedCtx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true, BeadsIssue: "mitto-abc", HasBeadsIssue: true},
		Args:    map[string]string{"Commit": "true", "MentionTS": "2026-07-25T00:00:00Z"},
	}
	linkedFuncs := cel.BuildTemplateFuncMap(linkedCtx)
	for _, c := range consumers {
		body, ok := byName[c.name]
		if !ok {
			t.Errorf("prompt %q not found", c.name)
			continue
		}
		out, err := RenderPromptTemplate(c.name, body, linkedCtx, linkedFuncs)
		if err != nil {
			t.Errorf("render %q (linked): %v", c.name, err)
			continue
		}
		want := "The **" + c.noun + "** " + c.scope + " is `mitto-abc`"
		if !strings.Contains(out, want) {
			t.Errorf("prompt %q (linked): missing %q — strict fragment did not inline or noun/scope override failed", c.name, want)
		}
		// FromLink=true must yield the "linked beads issue" note.
		if !strings.Contains(out, "(from this conversation's linked beads issue") {
			t.Errorf("prompt %q (linked): missing FromLink=true note", c.name)
		}
	}

	// mention-driver additionally overrides LinkNote and FailStop; check
	// those inline correctly.
	if body, ok := byName["Mention — driver"]; ok {
		out, err := RenderPromptTemplate("Mention — driver", body, linkedCtx, linkedFuncs)
		if err != nil {
			t.Fatalf("render Mention — driver (linked): %v", err)
		}
		if !strings.Contains(out, "durable across loop runs") {
			t.Errorf("Mention — driver: LinkNote override not rendered")
		}
	}

	// no-link branch — the failure notification appears everywhere.
	noLinkCtx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
		Args:    map[string]string{},
	}
	noLinkFuncs := cel.BuildTemplateFuncMap(noLinkCtx)
	for _, c := range consumers {
		body := byName[c.name]
		out, err := RenderPromptTemplate(c.name, body, noLinkCtx, noLinkFuncs)
		if err != nil {
			t.Errorf("render %q (nolink): %v", c.name, err)
			continue
		}
		want := "No " + c.noun + " is resolvable"
		if !strings.Contains(out, want) {
			t.Errorf("prompt %q (nolink): missing %q — strict fragment fallback did not render", c.name, want)
		}
		if !strings.Contains(out, "Post a `mitto_ui_notify`") {
			t.Errorf("prompt %q (nolink): missing mitto_ui_notify directive", c.name)
		}
	}

	// mention-driver's custom FailStop must inline the loop-report clause.
	if body, ok := byName["Mention — driver"]; ok {
		out, err := RenderPromptTemplate("Mention — driver", body, noLinkCtx, noLinkFuncs)
		if err != nil {
			t.Fatalf("render Mention — driver (nolink): %v", err)
		}
		if !strings.Contains(out, "report failure to the parent via `mitto_children_tasks_report`") {
			t.Errorf("Mention — driver (nolink): FailStop override missing")
		}
	}
}

// TestBlockedDeferLoopDriverFragmentRenders is a smoke test for the
// beads-issues/shared/blocked-defer-loop-driver fragment: renders the 3
// loop-driver-shaped consumers and asserts each variant's hallmarks
// (per-caller Placeholder, per-caller IntroExtra, and the fixed
// loop-driver-only "state label" / "End the iteration" / "user resumes
// the loop" phrasing) appear correctly.
func TestBlockedDeferLoopDriverFragmentRenders(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}
	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	type consumer struct {
		name, placeholder, introExtra string
	}
	consumers := []consumer{
		{"Loop fixing bug", "<target-bug>", ""},
		{"Loop implementing feature", "<target-feature>", ""},
	}

	// Linked branch: $target resolves so the bd commands substitute the id.
	linkedCtx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "sess-1", Name: "N", HasMessages: true, BeadsIssue: "mitto-abc", HasBeadsIssue: true},
		Args:    map[string]string{"IssueID": "mitto-abc"},
	}
	linkedFuncs := cel.BuildTemplateFuncMap(linkedCtx)
	for _, c := range consumers {
		body, ok := byName[c.name]
		if !ok {
			t.Errorf("prompt %q not found", c.name)
			continue
		}
		out, err := RenderPromptTemplate(c.name, body, linkedCtx, linkedFuncs)
		if err != nil {
			t.Errorf("render %q (linked): %v", c.name, err)
			continue
		}
		// Shared loop-driver-only hallmarks.
		for _, hallmark := range []string{
			"Use this whenever a run cannot make progress autonomously",
			"advance the state label in this case. Instead:",
			"bd update mitto-abc --add-label needs-human --defer <when>   # e.g. tomorrow / +1d",
			`Write a **structured handoff** comment on the bead:`,
			`bd comment mitto-abc "Blocked at <stage>.`,
			"End the iteration with a concise handoff",
			"(interactive runs also `mitto_ui_notify`)",
			`mitto_conversation_update(self_id: "sess-1"`,
			"The user resumes the loop",
			"`needs-human` label and addressing the handoff",
		} {
			if !strings.Contains(out, hallmark) {
				t.Errorf("%q (linked): missing hallmark %q", c.name, hallmark)
			}
		}
		// IntroExtra: publish-post extends the intro; the other 2 do not.
		if c.introExtra != "" {
			if !strings.Contains(out, "a product decision"+c.introExtra+"),") {
				t.Errorf("%q (linked): IntroExtra %q not spliced into intro", c.name, c.introExtra)
			}
		}
	}

	// No-link branch: bd commands fall back to the caller-supplied placeholder.
	noLinkCtx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "sess-1", Name: "N", HasMessages: true},
		Args:    map[string]string{},
	}
	noLinkFuncs := cel.BuildTemplateFuncMap(noLinkCtx)
	for _, c := range consumers {
		body := byName[c.name]
		out, err := RenderPromptTemplate(c.name, body, noLinkCtx, noLinkFuncs)
		if err != nil {
			t.Errorf("render %q (nolink): %v", c.name, err)
			continue
		}
		want := "bd update " + c.placeholder + " --add-label needs-human"
		if !strings.Contains(out, want) {
			t.Errorf("%q (nolink): missing fallback %q", c.name, want)
		}
	}
}

// TestBlockedDeferHandoffFragmentRenders is a smoke test for the
// beads-issues/shared/blocked-defer-handoff fragment: renders each of the 11
// phase prompts and asserts that the fragment's hallmark substrings
// (per-phase `Blocked at <X>` handoff line, per-phase state label, the
// `mitto_conversation_update` closer) appear correctly, and that the
// short/long style + driver/router loop selectors flip the right way.
func TestBlockedDeferHandoffFragmentRenders(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}
	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	// (name, label, blockedAt, style, loopWord, wantNotifyNote)
	type consumer struct {
		name, label, blockedAt, style, loopWord string
		notifyNote                              bool
	}
	consumers := []consumer{
		{"Feature — plan phase", "planned", "plan", "short", "driver", true},
		{"Feature — implement phase", "implemented", "implement", "short", "driver", false},
		{"Feature — test phase", "tested", "test", "short", "driver", false},
		{"Feature — review phase", "verified", "review", "short", "driver", false},
		{"Bug fix — reproduce phase", "reproduced", "reproduce", "short", "driver", false},
		{"Bug fix — investigate phase", "researched", "investigate", "short", "driver", true},
		{"Bug fix — fix phase", "fixed", "fix", "short", "driver", false},
		{"Mention — answer phase", "mention-answered", "mention-answer", "long", "router", false},
		{"Mention — investigate phase", "mention-investigated", "mention-investigate", "long", "router", false},
		{"Mention — plan phase", "mention-planned", "mention-plan", "long", "router", false},
		{"Mention — implement phase", "mention-implemented", "mention-implement", "long", "router", false},
	}

	// Linked branch: $target resolves so the bd commands substitute the id.
	linkedCtx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "sess-1", Name: "N", HasMessages: true, BeadsIssue: "mitto-abc", HasBeadsIssue: true},
		Args:    map[string]string{"Commit": "true", "MentionTS": "2026-07-25T00:00:00Z"},
	}
	linkedFuncs := cel.BuildTemplateFuncMap(linkedCtx)
	for _, c := range consumers {
		body, ok := byName[c.name]
		if !ok {
			t.Errorf("prompt %q not found", c.name)
			continue
		}
		out, err := RenderPromptTemplate(c.name, body, linkedCtx, linkedFuncs)
		if err != nil {
			t.Errorf("render %q (linked): %v", c.name, err)
			continue
		}
		// Every phase must render the label + a bd-update deferring the target.
		if !strings.Contains(out, "**Do not** advance") {
			t.Errorf("%q: missing 'Do not advance' clause", c.name)
		}
		if !strings.Contains(out, "`"+c.label+"`") {
			t.Errorf("%q: missing state label %q", c.name, c.label)
		}
		if !strings.Contains(out, "bd update mitto-abc --add-label needs-human --defer <when>") {
			t.Errorf("%q: missing bd update target substitution", c.name)
		}
		// Style-specific hallmarks.
		if c.style == "short" {
			want := "bd comment mitto-abc \"Blocked at " + c.blockedAt + "."
			if !strings.Contains(out, want) {
				t.Errorf("%q (short): missing short-form handoff %q", c.name, want)
			}
		} else {
			want := "Why: Blocked at " + c.blockedAt + " — <root cause>."
			if !strings.Contains(out, want) {
				t.Errorf("%q (long): missing [deferred:] handoff line %q", c.name, want)
			}
		}
		// Loop word: driver vs router. Normalise all whitespace runs to a
		// single space so a wrapped line ("so the driver\n   loop") still
		// matches the canonical phrase.
		normalised := strings.Join(strings.Fields(out), " ")
		if !strings.Contains(normalised, "so the "+c.loopWord+" loop does not spin") {
			t.Errorf("%q: missing loop-word clause with %q", c.name, c.loopWord)
		}
		// Session id is threaded through the closer.
		if !strings.Contains(out, `mitto_conversation_update(self_id: "sess-1"`) {
			t.Errorf("%q: session id not threaded into mitto_conversation_update", c.name)
		}
		// NotifyNote toggle (short style only).
		if c.style == "short" {
			hasNote := strings.Contains(out, "interactive runs also `mitto_ui_notify`")
			if hasNote != c.notifyNote {
				t.Errorf("%q: NotifyNote mismatch — want %v, got %v", c.name, c.notifyNote, hasNote)
			}
		}
	}

	// mention-phase-investigate is the one long-style caller that opts in
	// to "the FIRST line must be" (TheFirst=true).
	if body, ok := byName["Mention — investigate phase"]; ok {
		out, err := RenderPromptTemplate("Mention — investigate phase", body, linkedCtx, linkedFuncs)
		if err != nil {
			t.Fatalf("render Mention — investigate phase: %v", err)
		}
		if !strings.Contains(out, "(the FIRST line must be the `[deferred: <ts>]`") {
			t.Errorf("Mention — investigate phase: TheFirst=true wording missing")
		}
	}

	// No-link branch: bd commands must fall back to the caller-supplied
	// placeholder (per Style/Family: <target-feature> / <target-bug> / <target-bead>).
	noLinkCtx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "sess-1", Name: "N", HasMessages: true},
		Args:    map[string]string{},
	}
	noLinkFuncs := cel.BuildTemplateFuncMap(noLinkCtx)
	fallback := map[string]string{
		"Feature — plan phase":        "<target-feature>",
		"Feature — implement phase":   "<target-feature>",
		"Feature — test phase":        "<target-feature>",
		"Feature — review phase":      "<target-feature>",
		"Bug fix — reproduce phase":   "<target-bug>",
		"Bug fix — investigate phase": "<target-bug>",
		"Bug fix — fix phase":         "<target-bug>",
		"Mention — answer phase":      "<target-bead>",
		"Mention — investigate phase": "<target-bead>",
		"Mention — plan phase":        "<target-bead>",
		"Mention — implement phase":   "<target-bead>",
	}
	for _, c := range consumers {
		body := byName[c.name]
		out, err := RenderPromptTemplate(c.name, body, noLinkCtx, noLinkFuncs)
		if err != nil {
			t.Errorf("render %q (nolink): %v", c.name, err)
			continue
		}
		want := "bd update " + fallback[c.name] + " --add-label needs-human"
		if !strings.Contains(out, want) {
			t.Errorf("%q (nolink): missing fallback %q", c.name, want)
		}
	}
}

// TestLoopDriverGuidelinesFragmentsRender is a smoke test for the two
// beads-issues/shared/loop-driver-guidelines-{common,parallel} fragments:
// renders the 3 loop-driver-shaped consumers and asserts each variant's
// hallmarks (per-caller MilestoneList for the common fragment; per-caller
// Chain/LabelList for the parallel fragment; and the constraint that the
// parallel fragment is present in the two dispatch drivers but absent
// from publish-post, which is a label-only driver).
func TestLoopDriverGuidelinesFragmentsRender(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}
	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "sess-1", Name: "N", HasMessages: true, BeadsIssue: "mitto-abc", HasBeadsIssue: true},
		Args:    map[string]string{"IssueID": "mitto-abc"},
	}
	funcs := cel.BuildTemplateFuncMap(ctx)

	// Common-fragment hallmarks that MUST appear verbatim in every consumer.
	commonHallmarks := []string{
		"**Live state only.** Always re-read labels via `bd show --json`",
		"**Decide autonomously; never guess.**",
		"**Silent unless it matters.** On scheduled runs, `mitto_ui_notify`",
		"**Always log to the tracker** with `bd comment`",
	}

	// (name, milestoneList, chain, labelList, splitAntipattern, wantsParallel)
	type consumer struct {
		name             string
		milestoneList    string
		chain            string
		labelList        string
		splitAntipattern string
		wantsParallel    bool
	}
	consumers := []consumer{
		{
			name:             "Loop fixing bug",
			milestoneList:    "phase dispatched, fixed, blocked/deferred, or final stop",
			chain:            "`researched` → `reproduced` → `fixed`",
			labelList:        "`researched`/`reproduced`/`fixed`",
			splitAntipattern: "a single-root-cause fix or work that shares files",
			wantsParallel:    true,
		},
		{
			name:             "Loop implementing feature",
			milestoneList:    "phase dispatched, verified, blocked/deferred, or final stop",
			chain:            "`planned` → `implemented` → `tested` → `verified`",
			labelList:        "`planned`/`implemented`/`tested`/`verified`",
			splitAntipattern: "a tightly-coupled increment or work that shares files",
			wantsParallel:    true,
		},
	}

	// Parallel-fragment hallmarks (leader phrases from each of the 3 bullets).
	parallelHallmarks := []string{
		"**One phase per run.** Dispatch a single phase prompt",
		"**Parallelize only when genuinely independent.**",
		"**Never do the phase work inline.**",
	}

	for _, c := range consumers {
		body, ok := byName[c.name]
		if !ok {
			t.Errorf("prompt %q not found", c.name)
			continue
		}
		out, err := RenderPromptTemplate(c.name, body, ctx, funcs)
		if err != nil {
			t.Errorf("render %q: %v", c.name, err)
			continue
		}

		// Common fragment: always present, all 4 hallmarks + per-caller
		// MilestoneList substring.
		for _, h := range commonHallmarks {
			if !strings.Contains(out, h) {
				t.Errorf("%q: missing common hallmark %q", c.name, h)
			}
		}
		if !strings.Contains(out, "meaningful milestones ("+c.milestoneList+").") {
			t.Errorf("%q: missing MilestoneList %q", c.name, c.milestoneList)
		}

		// Parallel fragment: present iff wantsParallel. Also check per-caller
		// Chain / LabelList / SplitAntipattern substitutions.
		if c.wantsParallel {
			for _, h := range parallelHallmarks {
				if !strings.Contains(out, h) {
					t.Errorf("%q: missing parallel hallmark %q", c.name, h)
				}
			}
			if !strings.Contains(out, "single phase prompt ("+c.chain+")") {
				t.Errorf("%q: missing Chain %q", c.name, c.chain)
			}
			if !strings.Contains(out, "`bd update ... --add-label` for\n  "+c.labelList+" inside") {
				t.Errorf("%q: missing LabelList %q", c.name, c.labelList)
			}
			if !strings.Contains(out, "never\n  split "+c.splitAntipattern+".") {
				t.Errorf("%q: missing SplitAntipattern %q", c.name, c.splitAntipattern)
			}
		} else {
			for _, h := range parallelHallmarks {
				if strings.Contains(out, h) {
					t.Errorf("%q: unexpected parallel hallmark %q (should be absent for label-only drivers)", c.name, h)
				}
			}
			// Publish-post keeps its 2 caller-unique bullets inline.
			for _, want := range []string{
				"**One stage per run.** Add a single next label",
				"**Publish step is a placeholder.**",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("%q: missing caller-unique bullet %q", c.name, want)
				}
			}
		}
	}
}
