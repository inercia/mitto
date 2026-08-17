package conversation

import (
	"sync"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

type recordingSlackReconciler struct {
	mu         sync.Mutex
	reconciled []string
	removed    []string
}

func (r *recordingSlackReconciler) ReconcileSession(sessionID string) error {
	r.mu.Lock()
	r.reconciled = append(r.reconciled, sessionID)
	r.mu.Unlock()
	return nil
}

func (r *recordingSlackReconciler) RemoveSession(sessionID string) {
	r.mu.Lock()
	r.removed = append(r.removed, sessionID)
	r.mu.Unlock()
}

func TestSessionManagerSlackReconcilerReceivesLifecycleBroadcastsWithoutClients(t *testing.T) {
	manager := NewSessionManager("", "", false, nil)
	reconciler := &recordingSlackReconciler{}
	manager.SetSlackReconciler(reconciler)
	manager.BroadcastLoopUpdated("loop", &session.LoopPrompt{})
	manager.BroadcastSessionArchived("archive", true)
	manager.BroadcastSessionDeleted("delete")

	reconciler.mu.Lock()
	defer reconciler.mu.Unlock()
	if len(reconciler.reconciled) != 2 || reconciler.reconciled[0] != "loop" || reconciler.reconciled[1] != "archive" {
		t.Fatalf("reconciled = %v", reconciler.reconciled)
	}
	if len(reconciler.removed) != 1 || reconciler.removed[0] != "delete" {
		t.Fatalf("removed = %v", reconciler.removed)
	}
}
