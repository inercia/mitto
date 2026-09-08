package conversation

import (
	"context"
	"testing"

	"github.com/inercia/mitto/internal/agentbackend"
)

// TestShouldTriggerLocalAutomation_OriginGate pins the truth table directly.
func TestShouldTriggerLocalAutomation_OriginGate(t *testing.T) {
	if !ShouldTriggerLocalAutomation(agentbackend.OriginLocal) {
		t.Error("ShouldTriggerLocalAutomation(OriginLocal) = false, want true")
	}
	if ShouldTriggerLocalAutomation(agentbackend.OriginRemote) {
		t.Error("ShouldTriggerLocalAutomation(OriginRemote) = true, want false")
	}
}

// TestShouldTriggerLocalAutomation_HostEchoDoesNotTriggerAutomation exercises
// the predicate against real agentbackend.Event.Origin values produced by
// NewFakeHost: a normal local Prompt call publishes OriginLocal, while
// InjectRemoteUpdate (simulating a host-echoed turn from another client)
// publishes OriginRemote. Only the local turn should be eligible to trigger
// this conversation's automation.
func TestShouldTriggerLocalAutomation_HostEchoDoesNotTriggerAutomation(t *testing.T) {
	host := agentbackend.NewFakeHost()
	ctx := context.Background()

	sess, err := host.NewSession(ctx, "fake-provider")
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	ref := sess.Ref()

	var origins []agentbackend.Origin
	sub, err := host.Subscribe(ctx, ref, func(ev agentbackend.Event) {
		origins = append(origins, ev.Origin)
	})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer sub.Close()

	if _, err := host.Prompt(ctx, ref, nil); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	host.InjectRemoteUpdate(ref, nil)

	if len(origins) != 2 {
		t.Fatalf("got %d events, want 2 (local Prompt + remote echo)", len(origins))
	}

	if !ShouldTriggerLocalAutomation(origins[0]) {
		t.Errorf("local Prompt event (Origin=%v) should trigger automation", origins[0])
	}
	if ShouldTriggerLocalAutomation(origins[1]) {
		t.Errorf("host-echoed event (Origin=%v) must NOT trigger automation (would duplicate)", origins[1])
	}
}
