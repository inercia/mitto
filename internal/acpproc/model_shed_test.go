package acpproc

import (
	"context"
	"errors"
	"testing"

	"github.com/inercia/mitto/internal/acpproc/acperrors"
)

func TestSetSessionModel_AlreadySaturatedNeverConfirmsUnappliedSwitch(t *testing.T) {
	p := &SharedACPProcess{conn: newUnresponsiveACPPeerConn(), setModelSem: make(chan struct{}, 1)}
	for range sessionSaturationTimeoutThreshold {
		p.recordRPCTimeout()
	}
	for range 3 {
		if err := p.SetSessionModel(context.Background(), "conversation", "selected-model"); !errors.Is(err, acperrors.ErrProcessSaturated) {
			t.Fatalf("shed must leave model switch pending/retryable, got %v", err)
		}
	}
}
