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
