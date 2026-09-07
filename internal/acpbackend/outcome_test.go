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
