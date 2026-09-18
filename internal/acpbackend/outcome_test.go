package acpbackend

import (
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
)

func TestToNeutralStopReason(t *testing.T) {
	cases := []struct {
		in   acp.StopReason
		want agentbackend.StopReason
	}{
		{acp.StopReasonEndTurn, agentbackend.StopReasonEndTurn},
		{acp.StopReasonCancelled, agentbackend.StopReasonCancelled},
		{acp.StopReasonMaxTokens, agentbackend.StopReasonMaxTokens},
		{acp.StopReasonMaxTurnRequests, agentbackend.StopReasonMaxTokens}, // folded per plan
		{acp.StopReasonRefusal, agentbackend.StopReasonRefusal},
		{acp.StopReason("something-unrecognized"), agentbackend.StopReasonError},
		{acp.StopReason(""), agentbackend.StopReasonError},
	}
	for _, c := range cases {
		if got := ToNeutralStopReason(c.in); got != c.want {
			t.Errorf("ToNeutralStopReason(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestToNeutralPromptOutcome(t *testing.T) {
	content := []agentbackend.ContentBlock{{Text: &agentbackend.TextBlock{Text: "hi"}}}
	out := ToNeutralPromptOutcome(acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, content)
	if out.StopReason != agentbackend.StopReasonEndTurn {
		t.Errorf("StopReason = %q, want end_turn", out.StopReason)
	}
	if len(out.Content) != 1 || out.Content[0].Text.Text != "hi" {
		t.Errorf("Content = %+v, want passthrough of caller-supplied content", out.Content)
	}
}

func TestToNeutralPromptOutcome_NilContent(t *testing.T) {
	out := ToNeutralPromptOutcome(acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil)
	if out.StopReason != agentbackend.StopReasonCancelled {
		t.Errorf("StopReason = %q, want cancelled", out.StopReason)
	}
	if out.Content != nil {
		t.Errorf("Content = %+v, want nil", out.Content)
	}
}

// TestToNeutralPromptOutcome_UsagePassthrough is the mitto-mx9.1.1
// acceptance-criteria test for the ACP->neutral direction: "agentbackend.
// PromptOutcome carries token usage; production token-accounting parity
// verified against the ACP baseline." A populated acp.Usage must survive
// into the neutral PromptOutcome's Usage field with exact field values.
func TestToNeutralPromptOutcome_UsagePassthrough(t *testing.T) {
	resp := acp.PromptResponse{
		StopReason: acp.StopReasonEndTurn,
		Usage: &acp.Usage{
			InputTokens:  1000,
			OutputTokens: 250,
			TotalTokens:  1250,
		},
	}
	out := ToNeutralPromptOutcome(resp, nil)
	if out.Usage == nil {
		t.Fatal("Usage = nil, want non-nil (response reported usage)")
	}
	if out.Usage.InputTokens != 1000 || out.Usage.OutputTokens != 250 || out.Usage.TotalTokens != 1250 {
		t.Errorf("Usage = %+v, want {1000, 250, 1250}", out.Usage)
	}
}

// TestToNeutralUsage_NilAndPopulated covers both branches of the per-turn
// usage translator directly: nil input (no usage reported by the agent for
// this turn) must return nil rather than a zero-valued *PromptUsage, so
// callers can distinguish "unknown" from "zero" (mitto-mx9.1.1).
func TestToNeutralUsage_NilAndPopulated(t *testing.T) {
	if got := ToNeutralUsage(nil); got != nil {
		t.Fatalf("ToNeutralUsage(nil) = %+v, want nil", got)
	}

	got := ToNeutralUsage(&acp.Usage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10})
	if got == nil {
		t.Fatal("ToNeutralUsage(populated) = nil, want non-nil")
	}
	if got.InputTokens != 7 || got.OutputTokens != 3 || got.TotalTokens != 10 {
		t.Errorf("ToNeutralUsage(populated) = %+v, want {7, 3, 10}", got)
	}
}
