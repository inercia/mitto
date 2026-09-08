package auxiliary

import (
	"errors"
	"fmt"
	"testing"

	"github.com/inercia/mitto/internal/acpproc/acperrors"
	"github.com/inercia/mitto/internal/agentbackend"
)

func TestClassifyOutcome(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want OutcomeKind
	}{
		{"nil is success", nil, OutcomeSuccess},
		{"ErrUnsupported sentinel", agentbackend.ErrUnsupported, OutcomeUnsupported},
		{"wrapped ErrUnsupported", fmt.Errorf("dispatch failed: %w", agentbackend.ErrUnsupported), OutcomeUnsupported},
		{"UnsupportedError struct", &agentbackend.UnsupportedError{Feature: "title-gen"}, OutcomeUnsupported},
		{"wrapped UnsupportedError struct", fmt.Errorf("dispatch failed: %w", &agentbackend.UnsupportedError{Feature: "title-gen"}), OutcomeUnsupported},
		{"saturated umbrella sentinel", acperrors.ErrSharedProcessSaturated, OutcomeRetryable},
		{"reactive saturation", acperrors.ErrProcessSaturated, OutcomeRetryable},
		{"proactive busy load-shedding", acperrors.ErrProcessBusy, OutcomeRetryable},
		{"mcp-init gated", acperrors.ErrMCPInitGated, OutcomeRetryable},
		{"wrapped proactive busy", fmt.Errorf("dispatch failed: %w", acperrors.ErrProcessBusy), OutcomeRetryable},
		{"unrecognized error", errors.New("boom"), OutcomeError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyOutcome(tt.err); got != tt.want {
				t.Errorf("ClassifyOutcome(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestOutcomeKind_String(t *testing.T) {
	tests := []struct {
		kind OutcomeKind
		want string
	}{
		{OutcomeSuccess, "success"},
		{OutcomeRetryable, "retryable"},
		{OutcomeUnsupported, "unsupported"},
		{OutcomeError, "error"},
		{OutcomeKind(99), "error"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("OutcomeKind(%d).String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}
