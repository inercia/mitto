package acpproc

import "time"

// processDegradedState reports why the shared ACP process p is currently
// "degraded" (stuck / unable to reliably serve new-session or auxiliary
// work), or "" if it is healthy (mitto-13n.3).
//
// This is the SAME predicate GC Tier 5 (acp_process_gc.go) already computes
// to decide whether an idle process is eligible for a health recycle, now
// extracted so the recycle decision and the pre-recycle degraded-state
// notification below can never diverge:
//
//   - "process_saturated": p.IsSaturated() — repeated RPC timeouts tripped
//     the reactive saturation cooldown (mitto-13ck.2).
//   - "mcp_init_gated": p.MCPInitTimedOut() — the agent explicitly reported
//     its internal MCP-init wait budget elapsed (mitto-13n).
//   - "mcp_init_wedged": the agent's MCP-init handshake started
//     (MCPInitInProgress()) but never completed (!MCPInitDone()) for more
//     than 2x the configured MCPInitTimeout, bounded via
//     MCPInitInProgressSince() so a merely slow-but-progressing handshake is
//     not flagged prematurely (mitto-13n.1). Skipped when MCPInitTimeout is
//     disabled (<=0).
//
// Priority matches Tier 5: IsSaturated() is checked first, then
// MCPInitTimedOut(), then the wedged window — a process can satisfy more
// than one signal at once (e.g. saturation tripped by RPCs that were
// themselves timing out on a wedged MCP handshake).
//
// Deliberately excludes ActiveRPCs()-based proactive load shedding
// ("process_busy" in getOrCreateAuxiliarySession): a busy-but-otherwise-
// healthy process is not degraded, just momentarily loaded, and must not
// produce a user-facing alarm (mitto-13n.3 acceptance criterion 2).
func processDegradedState(p *SharedACPProcess, now time.Time) string {
	if p == nil {
		return ""
	}
	if p.IsSaturated() {
		return "process_saturated"
	}
	if p.MCPInitTimedOut() {
		return "mcp_init_gated"
	}
	if p.config.MCPInitTimeout > 0 && p.MCPInitInProgress() && !p.MCPInitDone() {
		since := p.MCPInitInProgressSince()
		if !since.IsZero() && now.Sub(since) > 2*p.config.MCPInitTimeout {
			return "mcp_init_wedged"
		}
	}
	return ""
}

// updateDegradedState records the current degraded-state reason for
// workspaceUUID and fires onDegraded exactly once per healthy<->degraded
// transition edge (mitto-13n.3). Called every GC tick for every live
// process from Tier 5, BEFORE the idle safety gates that can hold off an
// actual recycle indefinitely — this is what closes the invisible
// pre-recycle window the bead reports. A no-op on every steady-state tick
// where reason has not changed since the last call.
//
// Must only be called from the single-threaded GC loop; degradedMu guards
// the map against any future concurrent reader, not concurrent GC ticks.
func (m *ACPProcessManager) updateDegradedState(workspaceUUID, reason string) {
	m.degradedMu.Lock()
	if m.degradedState == nil {
		m.degradedState = make(map[string]string)
	}
	prev, hadEntry := m.degradedState[workspaceUUID]
	changed := false
	if reason == "" {
		if hadEntry {
			delete(m.degradedState, workspaceUUID)
			changed = true
		}
	} else if !hadEntry || prev != reason {
		m.degradedState[workspaceUUID] = reason
		changed = true
	}
	m.degradedMu.Unlock()

	if !changed || m.onDegraded == nil {
		return
	}
	m.onDegraded(workspaceUUID, reason, reason != "")
}

// dropDegradedStateSilently removes any tracked degraded-state entry for
// workspaceUUID WITHOUT firing onDegraded's recovery edge. Called from
// StopProcess, i.e. on every path that removes a process (GC Tiers 1/4/5/6
// and manual stops): the entry would otherwise leak and a later process for
// the same workspace UUID would miss its degraded edge. Silent because the
// stop paths the user should hear about already broadcast their own
// dedicated toast (onMemoryRecycled / onHealthRecycled), so also firing the
// degraded-recovery toast would double-notify for one event (mitto-13n.3).
func (m *ACPProcessManager) dropDegradedStateSilently(workspaceUUID string) {
	m.degradedMu.Lock()
	delete(m.degradedState, workspaceUUID)
	m.degradedMu.Unlock()
}
