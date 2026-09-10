package acpproc

import (
	"context"
	"testing"
)

// TestWouldShedProactiveAux pins ACPProcessManager.WouldShedProactiveAux
// (mitto-z4w) to each of the four pre-RPC bail conditions mirrored from
// getOrCreateAuxiliarySession — process==nil, IsSaturated(), the ActiveRPCs
// threshold, MCP-init gating (timed-out or in-progress-not-done), and a
// recent agent-internal-deadline hit — plus the nil-manager and healthy-
// process negative cases. Keeping this table in lockstep with the live bail's
// own regression tests (TestGetOrCreateAuxiliarySession_*Bails above) is what
// guarantees the predicate cannot silently drift from the real bail it
// mirrors, since both consult the same processBusyByActiveRPCs /
// processMCPInitGated helpers.
func TestWouldShedProactiveAux(t *testing.T) {
	const wsUUID = "ws-would-shed"

	t.Run("nil manager reports true (fail toward deferral)", func(t *testing.T) {
		var m *ACPProcessManager
		if !m.WouldShedProactiveAux(wsUUID) {
			t.Fatal("nil *ACPProcessManager must report true")
		}
	})

	t.Run("no shared process for workspace reports true", func(t *testing.T) {
		m := NewACPProcessManager(context.Background(), nil)
		defer m.Close()
		if !m.WouldShedProactiveAux("ws-no-process") {
			t.Fatal("a workspace with no registered shared process must report true")
		}
	})

	t.Run("saturated process reports true", func(t *testing.T) {
		m := NewACPProcessManager(context.Background(), nil)
		defer m.Close()
		proc := newTestSharedProcess()
		for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
			proc.recordRPCTimeout()
		}
		if !proc.IsSaturated() {
			t.Fatal("test setup: expected process to be saturated")
		}
		m.mu.Lock()
		m.processes[wsUUID] = proc
		m.mu.Unlock()
		if !m.WouldShedProactiveAux(wsUUID) {
			t.Fatal("a saturated process must report true")
		}
	})

	t.Run("ActiveRPCs at threshold reports true", func(t *testing.T) {
		m := NewACPProcessManager(context.Background(), nil)
		defer m.Close()
		proc := newTestSharedProcess()
		proc.activeRPCs.Store(auxSessionCreateBusyRPCThreshold)
		m.mu.Lock()
		m.processes[wsUUID] = proc
		m.mu.Unlock()
		if !m.WouldShedProactiveAux(wsUUID) {
			t.Fatal("ActiveRPCs() >= threshold must report true")
		}
	})

	t.Run("MCP init timed out reports true", func(t *testing.T) {
		m := NewACPProcessManager(context.Background(), nil)
		defer m.Close()
		proc := newTestSharedProcess()
		proc.mcpInitTimedOut.Store(true)
		m.mu.Lock()
		m.processes[wsUUID] = proc
		m.mu.Unlock()
		if !m.WouldShedProactiveAux(wsUUID) {
			t.Fatal("MCPInitTimedOut()=true must report true")
		}
	})

	t.Run("MCP init in progress and not done reports true", func(t *testing.T) {
		m := NewACPProcessManager(context.Background(), nil)
		defer m.Close()
		proc := newTestSharedProcess()
		proc.mcpInitInProgress.Store(true)
		m.mu.Lock()
		m.processes[wsUUID] = proc
		m.mu.Unlock()
		if !m.WouldShedProactiveAux(wsUUID) {
			t.Fatal("MCPInitInProgress()=true && MCPInitDone()=false must report true")
		}
	})

	t.Run("recent agent-internal-deadline hit reports true", func(t *testing.T) {
		m := NewACPProcessManager(context.Background(), nil)
		defer m.Close()
		proc := newTestSharedProcess()
		proc.recordAgentInternalDeadline()
		m.mu.Lock()
		m.processes[wsUUID] = proc
		m.mu.Unlock()
		if !m.WouldShedProactiveAux(wsUUID) {
			t.Fatal("RecentlyHitAgentInternalDeadline()=true must report true")
		}
	})

	t.Run("healthy quiescent process reports false", func(t *testing.T) {
		m := NewACPProcessManager(context.Background(), nil)
		defer m.Close()
		proc := newTestSharedProcess()
		m.mu.Lock()
		m.processes[wsUUID] = proc
		m.mu.Unlock()
		if m.WouldShedProactiveAux(wsUUID) {
			t.Fatal("a healthy, quiescent process must report false")
		}
	})
}
