package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestBootstrapBeadsFragmentRenders is a smoke test for the
// _shared/bootstrap-beads fragment: it loads the builtin fragment registry,
// renders each consuming prompt, and asserts that hallmark substrings from
// the fragment body appear in the rendered output. Presence of a hallmark
// in the rendered output means the {{ template "_shared/bootstrap-beads"
// . }} call actually resolved and inlined its body.
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

	// Hallmarks unique to the bootstrap-beads fragment body.
	const (
		hallmarkProbe       = "ls -d .beads 2>/dev/null"
		hallmarkQuestion    = "Beads is not initialised in this project yet."
		hallmarkInitCommand = "bd init --non-interactive"
	)

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
	}

	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}
	funcs := cel.BuildTemplateFuncMap(ctx)

	consumers := []string{"New issue"}
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
		for _, hallmark := range []string{hallmarkProbe, hallmarkQuestion, hallmarkInitCommand} {
			if !strings.Contains(out, hallmark) {
				t.Errorf("prompt %q: rendered output missing hallmark %q — fragment did not inline", name, hallmark)
			}
		}
	}
}

// TestLoadBeadContextFragmentRenders is a smoke test for the
// _shared/load-bead-context fragment: it renders each of the 3 consuming
// prompts with a valid target bead ID and asserts that (a) each bd command
// hallmark appears and (b) the target ID substitutes correctly.
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
}

// TestTargetBeadHeaderFragmentRenders is a smoke test for the flexible
// _shared/beads/target-bead-header fragment: renders each Pattern A consumer
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

	// Pattern A consumers of _shared/beads/target-bead-header: they render
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
// _shared/beads/target-bead-header-strict fragment used by every phase /
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

// TestBlockedDeferHandoffFragmentRenders is a smoke test for the
// _shared/beads/blocked-defer-handoff fragment: renders each of the 11
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
