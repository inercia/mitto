package conversation

import (
	"errors"
	"fmt"
	"testing"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/acpproc/acperrors"
	"github.com/inercia/mitto/internal/agentbackend"
)

// TestClassifyAcquireError pins the mitto-lrt.7 acceptance criterion that
// known acquisition failure signals map to actionable neutral states rather
// than a one-size-fits-all "failed" — and that unrecognized errors fail
// closed to Disconnected rather than guessing.
func TestClassifyAcquireError(t *testing.T) {
	permanent := &mittoAcp.ACPClassifiedError{
		Class:         mittoAcp.ACPErrorPermanent,
		OriginalError: errors.New("missing module"),
	}
	transient := &mittoAcp.ACPClassifiedError{
		Class:         mittoAcp.ACPErrorTransient,
		OriginalError: errors.New("timeout"),
	}

	tests := []struct {
		name string
		err  error
		want agentbackend.LifecycleState
	}{
		{"nil", nil, agentbackend.LifecycleConnected},
		{"saturated", fmt.Errorf("wrap: %w", acperrors.ErrSharedProcessSaturated), agentbackend.LifecycleReconnecting},
		{"process busy", acperrors.ErrProcessBusy, agentbackend.LifecycleReconnecting},
		{"process closed concurrently", acperrors.ErrProcessClosedConcurrently, agentbackend.LifecycleReconnecting},
		{"session not found", agentbackend.ErrSessionNotFound, agentbackend.LifecycleDisconnected},
		{"not connected", agentbackend.ErrNotConnected, agentbackend.LifecycleDisconnected},
		{"classified permanent", permanent, agentbackend.LifecycleStopped},
		{"classified transient", transient, agentbackend.LifecycleReconnecting},
		{"unrecognized", errors.New("something else"), agentbackend.LifecycleDisconnected},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotState, gotErr := ClassifyAcquireError(tt.err)
			if gotState != tt.want {
				t.Errorf("ClassifyAcquireError(%v) state = %v, want %v", tt.err, gotState, tt.want)
			}
			if !errors.Is(gotErr, tt.err) && gotErr != tt.err {
				t.Errorf("ClassifyAcquireError(%v) err = %v, want the original error returned unchanged", tt.err, gotErr)
			}
		})
	}
}
