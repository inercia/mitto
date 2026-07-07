package acpproc

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestPrewarmPin_BasicPinUnpin verifies that PinWorkspace/UnpinWorkspace
// set and clear the pin, and IsPinned/PinnedCount reflect the state.
func TestPrewarmPin_BasicPinUnpin(t *testing.T) {
	m := NewACPProcessManager(context.Background(), nil)
	defer m.Close()

	if m.IsPinned("ws-1") {
		t.Fatal("workspace unexpectedly reported pinned before PinWorkspace")
	}
	if got := m.PinnedCount(); got != 0 {
		t.Fatalf("PinnedCount before pin: got %d, want 0", got)
	}

	if ok := m.PinWorkspace("ws-1", "slow_session_new", 0, 0); !ok {
		t.Fatal("PinWorkspace returned false (expected true)")
	}
	if !m.IsPinned("ws-1") {
		t.Fatal("IsPinned=false after PinWorkspace")
	}
	if got := m.PinnedCount(); got != 1 {
		t.Fatalf("PinnedCount after pin: got %d, want 1", got)
	}

	m.UnpinWorkspace("ws-1")
	if m.IsPinned("ws-1") {
		t.Fatal("IsPinned=true after UnpinWorkspace")
	}
	if got := m.PinnedCount(); got != 0 {
		t.Fatalf("PinnedCount after unpin: got %d, want 0", got)
	}
}

// TestPrewarmPin_MaxPinnedCap verifies that the blast-radius cap refuses
// new pins once the limit is reached.
func TestPrewarmPin_MaxPinnedCap(t *testing.T) {
	m := NewACPProcessManager(context.Background(), nil)
	defer m.Close()

	if ok := m.PinWorkspace("ws-a", "r", 0, 2); !ok {
		t.Fatal("first pin refused (expected accepted)")
	}
	if ok := m.PinWorkspace("ws-b", "r", 0, 2); !ok {
		t.Fatal("second pin refused (expected accepted)")
	}
	if ok := m.PinWorkspace("ws-c", "r", 0, 2); ok {
		t.Fatal("third pin accepted (expected refused by cap)")
	}
	if got := m.PinnedCount(); got != 2 {
		t.Fatalf("PinnedCount: got %d, want 2", got)
	}
	// Re-pinning an already-pinned workspace is not gated by the cap.
	if ok := m.PinWorkspace("ws-a", "r2", 0, 2); !ok {
		t.Fatal("re-pin of already-pinned workspace refused (expected accepted)")
	}
}

// TestPrewarmPin_Hysteresis verifies that RecordPrewarmProbeResult requires
// N consecutive healthy probes to unpin, and that any unhealthy probe
// resets the counter.
func TestPrewarmPin_Hysteresis(t *testing.T) {
	m := NewACPProcessManager(context.Background(), nil)
	defer m.Close()

	if !m.PinWorkspace("ws-1", "slow", 0, 0) {
		t.Fatal("initial pin refused")
	}

	// Two healthy probes → still pinned (N=3).
	if unpinned := m.RecordPrewarmProbeResult("ws-1", true, 3); unpinned {
		t.Fatal("unpinned after 1 healthy probe (expected still pinned)")
	}
	if unpinned := m.RecordPrewarmProbeResult("ws-1", true, 3); unpinned {
		t.Fatal("unpinned after 2 healthy probes (expected still pinned)")
	}
	if !m.IsPinned("ws-1") {
		t.Fatal("not pinned after 2 healthy probes")
	}

	// One unhealthy probe → counter resets.
	if unpinned := m.RecordPrewarmProbeResult("ws-1", false, 3); unpinned {
		t.Fatal("unpinned by unhealthy probe (expected still pinned)")
	}
	if !m.IsPinned("ws-1") {
		t.Fatal("not pinned after unhealthy probe (expected still pinned)")
	}

	// Two more healthy probes → still pinned (counter was reset).
	if unpinned := m.RecordPrewarmProbeResult("ws-1", true, 3); unpinned {
		t.Fatal("unpinned after only 1 fresh healthy probe")
	}
	if unpinned := m.RecordPrewarmProbeResult("ws-1", true, 3); unpinned {
		t.Fatal("unpinned after only 2 fresh healthy probes")
	}

	// Third healthy probe → unpin.
	if unpinned := m.RecordPrewarmProbeResult("ws-1", true, 3); !unpinned {
		t.Fatal("still pinned after 3 consecutive healthy probes")
	}
	if m.IsPinned("ws-1") {
		t.Fatal("IsPinned=true after hysteresis unpin")
	}
}

// TestPrewarmPin_ExpiryCap verifies that pins with MaxPinDuration set are
// auto-expired by ExpirePinsAndAlert once the cap elapses, and that the
// alert callback fires with expired=true.
func TestPrewarmPin_ExpiryCap(t *testing.T) {
	m := NewACPProcessManager(context.Background(), nil)
	defer m.Close()

	var mu sync.Mutex
	type alert struct {
		uuid, reason string
		expired      bool
	}
	var alerts []alert
	m.SetOnPrewarmPinAlert(func(uuid, reason string, expired bool) {
		mu.Lock()
		defer mu.Unlock()
		alerts = append(alerts, alert{uuid, reason, expired})
	})

	// Pin with a very short cap so we can wait it out synchronously.
	if !m.PinWorkspace("ws-1", "mcp_timeout", 20*time.Millisecond, 0) {
		t.Fatal("pin refused")
	}
	// Before expiry: IsPinned=true, ExpirePinsAndAlert returns nothing.
	if !m.IsPinned("ws-1") {
		t.Fatal("not pinned immediately after PinWorkspace")
	}
	if got := m.ExpirePinsAndAlert(); len(got) != 0 {
		t.Fatalf("ExpirePinsAndAlert before cap: got %v, want []", got)
	}

	time.Sleep(40 * time.Millisecond)

	expired := m.ExpirePinsAndAlert()
	if len(expired) != 1 || expired[0] != "ws-1" {
		t.Fatalf("ExpirePinsAndAlert after cap: got %v, want [ws-1]", expired)
	}
	if m.IsPinned("ws-1") {
		t.Fatal("IsPinned=true after expiry")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(alerts) != 1 {
		t.Fatalf("alert count: got %d, want 1", len(alerts))
	}
	if alerts[0].uuid != "ws-1" || alerts[0].reason != "mcp_timeout" || !alerts[0].expired {
		t.Fatalf("alert payload: got %+v, want {ws-1, mcp_timeout, expired=true}", alerts[0])
	}
}

// TestPrewarmPin_FireAlertIsIdempotent verifies that FirePrewarmPinAlert
// only fires once per pin.
func TestPrewarmPin_FireAlertIsIdempotent(t *testing.T) {
	m := NewACPProcessManager(context.Background(), nil)
	defer m.Close()

	var mu sync.Mutex
	fired := 0
	m.SetOnPrewarmPinAlert(func(uuid, reason string, expired bool) {
		mu.Lock()
		defer mu.Unlock()
		if expired {
			t.Errorf("expected expired=false, got true")
		}
		fired++
	})

	if !m.PinWorkspace("ws-1", "mcp_timeout", 0, 0) {
		t.Fatal("pin refused")
	}
	m.FirePrewarmPinAlert("ws-1")
	m.FirePrewarmPinAlert("ws-1")
	m.FirePrewarmPinAlert("ws-1")

	mu.Lock()
	defer mu.Unlock()
	if fired != 1 {
		t.Fatalf("alert fired %d times, want 1 (at-most-once per pin)", fired)
	}
}

// TestPrewarmPin_FireAlertNoOpWhenNotPinned verifies FirePrewarmPinAlert
// is a safe no-op for unpinned workspaces.
func TestPrewarmPin_FireAlertNoOpWhenNotPinned(t *testing.T) {
	m := NewACPProcessManager(context.Background(), nil)
	defer m.Close()

	fired := false
	m.SetOnPrewarmPinAlert(func(uuid, reason string, expired bool) {
		fired = true
	})

	m.FirePrewarmPinAlert("nonexistent")
	if fired {
		t.Fatal("alert fired for unpinned workspace")
	}
}

// TestPrewarmPin_ExpiredPinIsNotIsPinned verifies that a pin whose Expiry
// has passed reports IsPinned=false even before ExpirePinsAndAlert runs.
func TestPrewarmPin_ExpiredPinIsNotIsPinned(t *testing.T) {
	m := NewACPProcessManager(context.Background(), nil)
	defer m.Close()

	if !m.PinWorkspace("ws-1", "r", 10*time.Millisecond, 0) {
		t.Fatal("pin refused")
	}
	if !m.IsPinned("ws-1") {
		t.Fatal("IsPinned=false before expiry")
	}
	time.Sleep(25 * time.Millisecond)
	if m.IsPinned("ws-1") {
		t.Fatal("IsPinned=true after expiry (before ExpirePinsAndAlert)")
	}
}

// TestPrewarmPin_CleanupStaleAuxSkipsPinnedKeepalive verifies that
// CleanupStaleAuxiliarySessions leaves a pinned workspace's keepalive
// session in place, while still reaping non-keepalive stale sessions.
func TestPrewarmPin_CleanupStaleAuxSkipsPinnedKeepalive(t *testing.T) {
	m := NewACPProcessManager(context.Background(), nil)
	defer m.Close()

	// Seed the aux session map directly (bypassing NewSession, which needs a
	// real process) — this test exercises only the cleanup skip logic.
	past := time.Now().Add(-1 * time.Hour)
	m.auxMu.Lock()
	m.auxSessions[auxSessionKey{"ws-pinned", "keepalive"}] = &auxiliarySessionState{lastUsed: past}
	m.auxSessions[auxSessionKey{"ws-pinned", "title-gen"}] = &auxiliarySessionState{lastUsed: past}
	m.auxSessions[auxSessionKey{"ws-unpinned", "keepalive"}] = &auxiliarySessionState{lastUsed: past}
	m.auxMu.Unlock()

	if !m.PinWorkspace("ws-pinned", "r", 0, 0) {
		t.Fatal("pin refused")
	}

	m.CleanupStaleAuxiliarySessions(1 * time.Minute)

	m.auxMu.Lock()
	defer m.auxMu.Unlock()
	if _, ok := m.auxSessions[auxSessionKey{"ws-pinned", "keepalive"}]; !ok {
		t.Fatal("pinned workspace's keepalive was reaped (expected exempt)")
	}
	if _, ok := m.auxSessions[auxSessionKey{"ws-pinned", "title-gen"}]; ok {
		t.Fatal("pinned workspace's non-keepalive session was NOT reaped (expected reaped)")
	}
	if _, ok := m.auxSessions[auxSessionKey{"ws-unpinned", "keepalive"}]; ok {
		t.Fatal("unpinned workspace's keepalive was NOT reaped (expected reaped)")
	}
}
