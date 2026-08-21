package acpproc

import (
	"context"
	"testing"
	"time"
)

func TestWaitForQuiescence_UnblocksOnlyAtZeroRPCs(t *testing.T) {
	p := &SharedACPProcess{}
	p.beginRPC()
	p.beginRPC()

	result := make(chan bool, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		result <- p.WaitForQuiescence(ctx)
	}()

	p.endRPC()
	select {
	case <-result:
		t.Fatal("quiescence fired while one RPC remained active")
	case <-time.After(20 * time.Millisecond):
	}
	p.endRPC()
	select {
	case observed := <-result:
		if !observed {
			t.Fatal("expected zero-RPC transition to signal quiescence")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for zero-RPC transition")
	}
}

func TestWaitForProcessQuiescence_CoalescesAndPreservesForegroundPriority(t *testing.T) {
	managerCtx, cancelManager := context.WithCancel(context.Background())
	defer cancelManager()
	m := NewACPProcessManager(managerCtx, nil)
	const workspaceUUID = "quiescence-coalescing"
	p := &SharedACPProcess{}
	p.beginRPC()
	m.processes[workspaceUUID] = p

	results := make(chan bool, 2)
	for range 2 {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			results <- m.WaitForProcessQuiescence(ctx, workspaceUUID)
		}()
	}

	deadline := time.Now().Add(time.Second)
	for {
		m.auxQuiescenceMu.Lock()
		waitCount := len(m.auxQuiescenceWaits)
		m.auxQuiescenceMu.Unlock()
		if waitCount == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected one coalesced workspace wait, got %d", waitCount)
		}
		time.Sleep(time.Millisecond)
	}

	p.endRPC()
	time.Sleep(deferredAuxQuiescenceWindow / 4)
	p.beginRPC() // foreground work wins during the settle window
	select {
	case <-results:
		t.Fatal("deferred admission beat foreground work")
	case <-time.After(deferredAuxQuiescenceWindow):
	}
	p.endRPC()

	for i := 0; i < 2; i++ {
		select {
		case observed := <-results:
			if !observed {
				t.Fatal("expected coalesced waiters to observe stable quiescence")
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for stable process quiescence")
		}
	}
}
