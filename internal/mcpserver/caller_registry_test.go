package mcpserver

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func rootGeneration(t *testing.T, srv *Server, owner, root string) uint64 {
	t.Helper()
	generation, ok := srv.BindACPRootGeneration(owner, root)
	if !ok || generation == 0 {
		t.Fatalf("BindACPRootGeneration(%q, %q) = (%d, %v)", owner, root, generation, ok)
	}
	return generation
}

func childStatus(root, child, parent string, depth int, typ, status string) ACPChildStatus {
	return ACPChildStatus{RootSessionID: root, ChildSessionID: child, ParentSessionID: parent, Depth: depth, Type: typ, Status: status}
}

func TestACPChildRegistry_DirectNestedTerminalAndDuplicate(t *testing.T) {
	srv := newSharedIdentityServer(t, "conv-a")
	generation := rootGeneration(t, srv, "conv-a", "root-a")
	created := childStatus("root-a", "child-a", "root-a", 1, "status", "created")
	if !srv.ApplyACPChildStatus("conv-a", generation, created) {
		t.Fatal("direct child creation rejected")
	}
	if got := srv.resolveACPCaller("child-a"); got != "conv-a" {
		t.Fatalf("direct child owner = %q, want conv-a", got)
	}
	nested := childStatus("root-a", "child-b", "child-a", 2, "status", "created")
	if !srv.ApplyACPChildStatus("conv-a", generation, nested) {
		t.Fatal("nested child creation rejected")
	}
	terminal := childStatus("root-a", "child-a", "root-a", 1, "status", "completed")
	if !srv.ApplyACPChildStatus("conv-a", generation, terminal) || srv.resolveACPCaller("child-a") != "" {
		t.Fatal("terminal child must remain as ancestry but no longer authorize")
	}
	if !srv.ApplyACPChildStatus("conv-a", generation, terminal) {
		t.Fatal("duplicate terminal event should be harmless")
	}
	if !srv.ApplyACPChildStatus("conv-a", generation, nested) {
		t.Fatal("terminal parent ancestry must still admit surviving nested child")
	}
}

func TestACPChildRegistry_RejectsInvalidAndCyclicChains(t *testing.T) {
	srv := newSharedIdentityServer(t, "conv-a")
	generation := rootGeneration(t, srv, "conv-a", "root-a")
	for name, update := range map[string]ACPChildStatus{
		"wrong root parent": childStatus("root-a", "child-a", "other", 1, "status", "created"),
		"unknown parent":    childStatus("root-a", "child-a", "missing", 2, "status", "created"),
		"self cycle":        childStatus("root-a", "child-a", "child-a", 2, "status", "created"),
		"depth mismatch":    childStatus("root-a", "child-a", "root-a", 2, "status", "created"),
		"oversized depth":   childStatus("root-a", "child-a", "root-a", maxACPChildDepth+1, "status", "created"),
		"uncreated event":   childStatus("root-a", "child-a", "root-a", 1, "status", "running"),
	} {
		t.Run(name, func(t *testing.T) {
			if srv.ApplyACPChildStatus("conv-a", generation, update) {
				t.Fatal("invalid update admitted")
			}
		})
	}
	if got := srv.resolveACPCaller("child-a"); got != "" {
		t.Fatalf("invalid child acquired owner %q", got)
	}
}

func TestACPChildRegistry_CollisionsQuarantineBothDirections(t *testing.T) {
	t.Run("root-child", func(t *testing.T) {
		srv := newSharedIdentityServer(t, "conv-a", "conv-b")
		genA := rootGeneration(t, srv, "conv-a", "root-a")
		genB := rootGeneration(t, srv, "conv-b", "root-b")
		if !srv.ApplyACPChildStatus("conv-a", genA, childStatus("root-a", "sibling-child", "root-a", 1, "status", "created")) {
			t.Fatal("sibling child admission failed")
		}
		if !srv.ApplyACPChildStatus("conv-a", genA, childStatus("root-a", "shared-id", "root-a", 1, "status", "created")) {
			t.Fatal("child admission failed")
		}
		if generation, ok := srv.BindACPRootGeneration("conv-b", "shared-id"); ok || generation != 0 {
			t.Fatalf("root binding collision = (%d, %v), want (0, false)", generation, ok)
		}
		if srv.resolveACPCaller("shared-id") != "" || srv.resolveACPCaller("sibling-child") != "conv-a" || srv.resolveACPCaller("root-b") != "conv-b" {
			t.Fatal("collision was not quarantined without damaging unrelated root")
		}
		srv.UnbindACPRootGeneration("conv-a", genA)
		srv.UnbindACPRootGeneration("conv-b", genB)
		genB = rootGeneration(t, srv, "conv-b", "root-b")
		if !srv.ApplyACPChildStatus("conv-b", genB, childStatus("root-b", "shared-id", "root-b", 1, "status", "created")) {
			t.Fatal("quarantine did not clear after old claimant left")
		}
	})

	t.Run("child-root", func(t *testing.T) {
		srv := newSharedIdentityServer(t, "conv-a", "conv-b")
		genA := rootGeneration(t, srv, "conv-a", "root-a")
		genB := rootGeneration(t, srv, "conv-b", "shared-id")
		if !srv.ApplyACPChildStatus("conv-b", genB, childStatus("shared-id", "sibling-child", "shared-id", 1, "status", "created")) {
			t.Fatal("sibling child admission failed")
		}
		if srv.ApplyACPChildStatus("conv-a", genA, childStatus("root-a", "shared-id", "root-a", 1, "status", "created")) {
			t.Fatal("child/root collision admitted")
		}
		if srv.resolveACPCaller("shared-id") != "" || srv.resolveACPCaller("sibling-child") != "" {
			t.Fatal("root collision did not quarantine root and its child identities")
		}
	})

	t.Run("child-child", func(t *testing.T) {
		srv := newSharedIdentityServer(t, "conv-a", "conv-b")
		genA := rootGeneration(t, srv, "conv-a", "root-a")
		genB := rootGeneration(t, srv, "conv-b", "root-b")
		if !srv.ApplyACPChildStatus("conv-a", genA, childStatus("root-a", "shared-id", "root-a", 1, "status", "created")) {
			t.Fatal("first child admission failed")
		}
		if srv.ApplyACPChildStatus("conv-b", genB, childStatus("root-b", "shared-id", "root-b", 1, "status", "created")) {
			t.Fatal("second owner collision admitted")
		}
		if srv.resolveACPCaller("shared-id") != "" {
			t.Fatal("colliding child still authorizes a caller")
		}
	})
}

func TestACPChildRegistry_GenerationRebindAndCleanup(t *testing.T) {
	srv := newSharedIdentityServer(t, "conv-a")
	old := rootGeneration(t, srv, "conv-a", "root-a")
	if !srv.ApplyACPChildStatus("conv-a", old, childStatus("root-a", "child-a", "root-a", 1, "status", "created")) {
		t.Fatal("child admission failed")
	}
	current := rootGeneration(t, srv, "conv-a", "root-b")
	if current == old || srv.resolveACPCaller("child-a") != "" {
		t.Fatal("root rebind did not invalidate old child generation")
	}
	if srv.ApplyACPChildStatus("conv-a", old, childStatus("root-a", "stale-child", "root-a", 1, "status", "created")) {
		t.Fatal("stale generation event admitted")
	}
	if !srv.ApplyACPChildStatus("conv-a", current, childStatus("root-b", "child-b", "root-b", 1, "status", "created")) {
		t.Fatal("current generation event rejected")
	}
	srv.UnregisterSession("conv-a")
	if srv.resolveACPCaller("root-b") != "" || srv.resolveACPCaller("child-b") != "" {
		t.Fatal("unregister did not clean root and child identities")
	}
}

func TestACPChildRegistry_Capacity(t *testing.T) {
	srv := newSharedIdentityServer(t, "conv-a")
	generation := rootGeneration(t, srv, "conv-a", "root-a")
	for i := 0; i < maxACPChildRecords; i++ {
		id := fmt.Sprintf("child-%04d", i)
		if !srv.ApplyACPChildStatus("conv-a", generation, childStatus("root-a", id, "root-a", 1, "status", "created")) {
			t.Fatalf("child %d rejected before capacity", i)
		}
	}
	if srv.ApplyACPChildStatus("conv-a", generation, childStatus("root-a", "overflow", "root-a", 1, "status", "created")) {
		t.Fatal("child beyond capacity admitted")
	}
}

func TestResolveSharedCallerContext_AdmissionWaitCancelAndTimeout(t *testing.T) {
	srv := newSharedIdentityServer(t, "conv-a")
	generation := rootGeneration(t, srv, "conv-a", "root-a")
	if !srv.ApplyACPChildStatus("conv-a", generation, childStatus("root-a", "early-child", "root-a", 1, "status", "created")) {
		t.Fatal("early child admission failed")
	}
	if got := srv.resolveSharedCallerContext(context.Background(), sharedReq(srv, "early-child"), ""); got != "conv-a" {
		t.Fatalf("pre-admitted child resolved to %q", got)
	}
	if got := srv.acpChildWaiters.Load(); got != 0 {
		t.Fatalf("pre-admitted child unnecessarily waited: %d", got)
	}
	go func() {
		time.Sleep(30 * time.Millisecond)
		srv.ApplyACPChildStatus("conv-a", generation, childStatus("root-a", "late-child", "root-a", 1, "status", "created"))
	}()
	start := time.Now()
	if got := srv.resolveSharedCallerContext(context.Background(), sharedReq(srv, "late-child"), ""); got != "conv-a" {
		t.Fatalf("late admission resolved to %q", got)
	}
	if elapsed := time.Since(start); elapsed >= maxACPChildWait {
		t.Fatalf("admission wait took %v", elapsed)
	}
	if got := srv.resolveSharedCallerContext(context.Background(), sharedReq(srv, "unknown-child"), ""); got != "" {
		t.Fatal("unknown caller unexpectedly resolved")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start = time.Now()
	if got := srv.resolveSharedCallerContext(ctx, sharedReq(srv, "cancelled-child"), ""); got != "" || time.Since(start) > 100*time.Millisecond {
		t.Fatal("cancelled resolution did not stop promptly")
	}
	if got := srv.acpChildWaiters.Load(); got != 0 {
		t.Fatalf("waiter counter leaked: %d", got)
	}
}

func TestResolveSharedCallerContext_WaiterAdmissionBounded(t *testing.T) {
	srv := newSharedIdentityServer(t, "conv-a")
	start := make(chan struct{})
	done := make(chan struct{}, maxACPChildWaiters+1)
	for i := 0; i < maxACPChildWaiters+1; i++ {
		go func() {
			<-start
			srv.waitForACPChild(context.Background(), "unknown-child")
			done <- struct{}{}
		}()
	}
	close(start)
	deadline := time.Now().Add(time.Second)
	for srv.acpChildWaiters.Load() < maxACPChildWaiters && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := srv.acpChildWaiters.Load(); got > maxACPChildWaiters {
		t.Fatalf("admitted %d waiters, max %d", got, maxACPChildWaiters)
	}
	for i := 0; i < maxACPChildWaiters+1; i++ {
		<-done
	}
	if got := srv.acpChildWaiters.Load(); got != 0 {
		t.Fatalf("waiter counter after completion = %d", got)
	}
}
