package agentbackend

import (
	"context"
	"errors"
	"testing"
)

func TestFakeHost_MultipleProvidersAndSessionsIsolated(t *testing.T) {
	h := newConnectedFakeHost(t, "p1", "p2")

	s1, err := h.NewSession(context.Background(), "p1")
	if err != nil {
		t.Fatalf("NewSession p1: %v", err)
	}
	s2, err := h.NewSession(context.Background(), "p2")
	if err != nil {
		t.Fatalf("NewSession p2: %v", err)
	}
	if s1.Ref().Provider == s2.Ref().Provider {
		t.Fatalf("expected distinct providers, got %v twice", s1.Ref().Provider)
	}
	if s1.Ref().ConversationID == s2.Ref().ConversationID {
		t.Fatalf("expected distinct conversation ids")
	}

	var got1, got2 int
	sub1, _ := h.Subscribe(context.Background(), s1.Ref(), func(ev Event) { got1++ })
	sub2, _ := h.Subscribe(context.Background(), s2.Ref(), func(ev Event) { got2++ })
	defer sub1.Close()
	defer sub2.Close()

	if _, err := h.Prompt(context.Background(), s1.Ref(), nil); err != nil {
		t.Fatalf("Prompt s1: %v", err)
	}
	if got1 != 1 || got2 != 0 {
		t.Fatalf("cross-talk between sessions: got1=%d got2=%d", got1, got2)
	}
}

func TestFakeHost_SubscriptionTeardownHaltsDelivery(t *testing.T) {
	h := newConnectedFakeHost(t, "p1")
	sess, _ := h.NewSession(context.Background(), "p1")

	var count int
	sub, err := h.Subscribe(context.Background(), sess.Ref(), func(ev Event) { count++ })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if _, err := h.Prompt(context.Background(), sess.Ref(), nil); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	if err := sub.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := h.Prompt(context.Background(), sess.Ref(), nil); err != nil {
		t.Fatalf("Prompt (2nd): %v", err)
	}
	if count != 1 {
		t.Fatalf("count after unsubscribe = %d, want 1 (no further delivery)", count)
	}
}

func TestFakeHost_UnsupportedCapabilityReturnsTypedError(t *testing.T) {
	h := newConnectedFakeHost(t, "p1")
	sess, _ := h.NewSession(context.Background(), "p1")

	if state := sess.Capabilities().Query(FeatureModeSelection); state != CapabilityUnsupported {
		t.Fatalf("FeatureModeSelection = %v, want Unsupported", state)
	}

	err := h.SetMode(context.Background(), sess.Ref(), "mode-x")
	var uerr *UnsupportedError
	if !errors.As(err, &uerr) {
		t.Fatalf("err = %v, want *UnsupportedError", err)
	}
	if uerr.Feature != FeatureModeSelection {
		t.Fatalf("uerr.Feature = %v, want %v", uerr.Feature, FeatureModeSelection)
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("errors.Is(err, ErrUnsupported) = false, want true")
	}
}

func TestFakeHost_UnknownCapabilityNeverCollapsed(t *testing.T) {
	h := newConnectedFakeHost(t, "p1")
	sess, _ := h.NewSession(context.Background(), "p1")
	state := sess.Capabilities().Query(FeatureTerminals)
	if state != CapabilityUnknown {
		t.Fatalf("FeatureTerminals = %v, want Unknown", state)
	}
	if state == CapabilitySupported || state == CapabilityUnsupported {
		t.Fatalf("Unknown must not equal Supported/Unsupported")
	}
}

func TestFakeHost_ExternallyOriginatedUpdateIsDistinct(t *testing.T) {
	h := newConnectedFakeHost(t, "p1")
	sess, _ := h.NewSession(context.Background(), "p1")

	var origins []Origin
	sub, err := h.Subscribe(context.Background(), sess.Ref(), func(ev Event) { origins = append(origins, ev.Origin) })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()

	if _, err := h.Prompt(context.Background(), sess.Ref(), nil); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	h.InjectRemoteUpdate(sess.Ref(), []ContentBlock{{Text: &TextBlock{Text: "external"}}})

	if len(origins) != 2 {
		t.Fatalf("origins = %v, want 2 events", origins)
	}
	if origins[0] != OriginLocal {
		t.Fatalf("origins[0] = %v, want OriginLocal", origins[0])
	}
	if origins[1] != OriginRemote {
		t.Fatalf("origins[1] = %v, want OriginRemote", origins[1])
	}
}

func TestFakeHost_ReconnectEmitsSequenceGapSignal(t *testing.T) {
	h := newConnectedFakeHost(t, "p1")
	sess, _ := h.NewSession(context.Background(), "p1")

	var events []Event
	sub, err := h.Subscribe(context.Background(), sess.Ref(), func(ev Event) { events = append(events, ev) })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()

	if _, err := h.ResumeSession(context.Background(), sess.Ref()); err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Kind != EventLifecycle || ev.Lifecycle != LifecycleReconnected {
		t.Fatalf("unexpected reconnect event: %+v", ev)
	}
	if ev.UpstreamCursor == "" {
		t.Fatalf("expected non-empty UpstreamCursor as sequence-gap signal")
	}
}

func TestFakeHost_RuntimeCapabilityChangePublishesEvent(t *testing.T) {
	h := newConnectedFakeHost(t, "p1")
	sess, _ := h.NewSession(context.Background(), "p1")

	var got []CapabilityState
	sub, err := h.Subscribe(context.Background(), sess.Ref(), func(ev Event) {
		if ev.Kind == EventCapabilityChange {
			got = append(got, ev.Capabilities.Query(FeatureModeSelection))
		}
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()

	h.SetCapability(sess.Ref(), FeatureModeSelection, CapabilitySupported)

	if len(got) != 1 || got[0] != CapabilitySupported {
		t.Fatalf("got = %v, want [Supported]", got)
	}
	if err := h.SetMode(context.Background(), sess.Ref(), "mode-x"); err != nil {
		t.Fatalf("SetMode after capability upgrade: %v", err)
	}
}
