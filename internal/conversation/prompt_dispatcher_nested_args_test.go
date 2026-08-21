package conversation

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// nestedArgsBody is the exact idiomatic call shape (bead mitto-47y.4) that a
// picker-consuming outer prompt uses to sub-render a picked prompt's body.
const nestedArgsBody = `run: {{ PromptTextWithArgs .Args.Prompt (ArgsMap "Prompt_Args") }}`

// captureWarns installs a buffered slog handler on the fake deps and returns
// the buffer plus a helper to search for the mitto-47y.4 WARN line.
func captureWarns(t *testing.T) (*fakePromptDeps, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	d := newFakePromptDeps()
	d.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	// A prompt resolver: "outer" → the nested-call body; any other name → a
	// plain leaf body so PromptTextWithArgs sub-renders terminate cleanly.
	d.resolver = func(name, _ string) (string, error) {
		if name == "outer" {
			return nestedArgsBody, nil
		}
		return "leaf body for " + name, nil
	}
	return d, &buf
}

const nestedArgsWarnNeedle = "nested inner args missing"

// mcpAgentMeta returns a PromptMeta shaped like an MCP/agent-originated
// dispatch (either mitto_conversation_send_prompt or a mitto_conversation_new
// initial prompt) so the validator branch is taken.
func mcpAgentMeta(args map[string]string) PromptMeta {
	return PromptMeta{
		PromptName:  "outer",
		SenderID:    senderIDQueue,
		QueueOrigin: session.QueueOriginAgent,
		Arguments:   args,
	}
}

// (a) Companion "Prompt" key present, "Prompt_Args" sibling missing → WARN
// emitted; render still succeeds (fail-open on the validator itself).
func TestResolveAndSubstitute_NestedArgs_WarnsWhenCompanionArgsMissing(t *testing.T) {
	p := promptDispatcher{}
	d, buf := captureWarns(t)

	msg, _, _, err := p.resolveAndSubstitute(d, "",
		mcpAgentMeta(map[string]string{"Prompt": "inner-name"}))
	if err != nil {
		t.Fatalf("unexpected error (validator must be fail-open): %v", err)
	}
	if !strings.Contains(buf.String(), nestedArgsWarnNeedle) {
		t.Fatalf("expected WARN about missing nested _Args, got log:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "Prompt_Args") {
		t.Fatalf("WARN must name the missing key %q, got:\n%s", "Prompt_Args", buf.String())
	}
	if msg == "" {
		t.Fatal("expected render to proceed (fail-open); got empty message")
	}
}

// (b) Both keys present → no WARN (the caller mirrored correctly).
func TestResolveAndSubstitute_NestedArgs_QuietWhenCompanionAndArgsPresent(t *testing.T) {
	p := promptDispatcher{}
	d, buf := captureWarns(t)

	_, _, _, err := p.resolveAndSubstitute(d, "",
		mcpAgentMeta(map[string]string{
			"Prompt":      "inner-name",
			"Prompt_Args": `{"X":"y"}`,
		}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(buf.String(), nestedArgsWarnNeedle) {
		t.Fatalf("unexpected WARN when both keys are present:\n%s", buf.String())
	}
}

// (c) Both keys absent → no validator WARN (deliberately silent: caller
// almost certainly intended "no nested picker for this dispatch"; matches
// the companion-key heuristic). The subsequent named-prompt render may
// fail-closed independently (empty picker name is a genuine template
// contract violation) — that behaviour is out of scope for this test; we
// only assert the validator did not emit the mitto-47y.4 WARN.
func TestResolveAndSubstitute_NestedArgs_QuietWhenNoCompanion(t *testing.T) {
	p := promptDispatcher{}
	d, buf := captureWarns(t)

	_, _, _, _ = p.resolveAndSubstitute(d, "", mcpAgentMeta(nil))
	if strings.Contains(buf.String(), nestedArgsWarnNeedle) {
		t.Fatalf("unexpected WARN when neither companion nor _Args are present:\n%s", buf.String())
	}
}

// (d) Body has no PromptTextWithArgs call → no WARN (fast-path skip).
func TestResolveAndSubstitute_NestedArgs_QuietWhenNoNestedCall(t *testing.T) {
	p := promptDispatcher{}
	d, buf := captureWarns(t)
	d.resolver = func(name, _ string) (string, error) { return "plain body without nested call", nil }

	_, _, _, err := p.resolveAndSubstitute(d, "",
		mcpAgentMeta(map[string]string{"Prompt": "inner-name"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(buf.String(), nestedArgsWarnNeedle) {
		t.Fatalf("unexpected WARN for a body without PromptTextWithArgs:\n%s", buf.String())
	}
}

// (e) Human queued dispatch (QueueOriginUser) → no WARN even with the bug
// shape: human free text has no template contract, so silent is correct.
func TestResolveAndSubstitute_NestedArgs_QuietForHumanOriginDispatch(t *testing.T) {
	p := promptDispatcher{}
	d, buf := captureWarns(t)

	meta := PromptMeta{
		PromptName:  "outer",
		SenderID:    senderIDQueue,
		QueueOrigin: session.QueueOriginUser,
		Arguments:   map[string]string{"Prompt": "inner-name"},
	}
	_, _, _, err := p.resolveAndSubstitute(d, "", meta)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(buf.String(), nestedArgsWarnNeedle) {
		t.Fatalf("unexpected WARN for a human-origin queue dispatch:\n%s", buf.String())
	}
}
