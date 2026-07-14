package acpproc

// Tests for WaitForMCPInit (mitto-54k.4). The helper blocks background resume
// callers until the shared process's MCP-init window closes so foreground
// session/new wins the agent's event loop first.

import (
	"context"
	"testing"
	"time"
)

func TestWaitForMCPInit_ReturnsTrueWhenAlreadyWarm(t *testing.T) {
	p := &SharedACPProcess{mcpInitDoneCh: make(chan struct{})}
	p.markMCPInitDone()

	if !p.WaitForMCPInit(context.Background()) {
		t.Fatal("expected WaitForMCPInit=true when mcpInitDone already latched")
	}
}

func TestWaitForMCPInit_UnblocksOnLatch(t *testing.T) {
	p := &SharedACPProcess{mcpInitDoneCh: make(chan struct{})}

	go func() {
		time.Sleep(20 * time.Millisecond)
		p.markMCPInitDone()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	if !p.WaitForMCPInit(ctx) {
		t.Fatal("expected WaitForMCPInit=true after markMCPInitDone latches")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("WaitForMCPInit unblocked too slowly: %v", elapsed)
	}
}

func TestWaitForMCPInit_ReturnsFalseOnCtxCancel(t *testing.T) {
	p := &SharedACPProcess{mcpInitDoneCh: make(chan struct{})}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if p.WaitForMCPInit(ctx) {
		t.Fatal("expected WaitForMCPInit=false when ctx cancels before latch")
	}
}

func TestWaitForMCPInit_ReturnsFalseOnProcessDone(t *testing.T) {
	done := make(chan struct{})
	close(done)
	p := &SharedACPProcess{
		mcpInitDoneCh: make(chan struct{}),
		processDone:   done,
	}

	if p.WaitForMCPInit(context.Background()) {
		t.Fatal("expected WaitForMCPInit=false when process is already done")
	}
}
