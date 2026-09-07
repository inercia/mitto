package agentbackend

import (
	"context"
	"errors"
	"testing"
)

func newConnectedFakeHost(t *testing.T, providers ...ProviderID) *fakeHost {
	t.Helper()
	h := NewFakeHost(providers...)
	if err := h.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if got := h.State(); got != LifecycleConnected {
		t.Fatalf("State() = %v, want %v", got, LifecycleConnected)
	}
	return h
}

func TestFakeHost_PromptOutcome(t *testing.T) {
	h := newConnectedFakeHost(t, "p1")
	sess, err := h.NewSession(context.Background(), "p1")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	content := []ContentBlock{{Text: &TextBlock{Text: "hello"}}}
	outcome, err := h.Prompt(context.Background(), sess.Ref(), content)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if outcome.StopReason != StopReasonEndTurn {
		t.Fatalf("StopReason = %v, want %v", outcome.StopReason, StopReasonEndTurn)
	}
	if len(outcome.Content) != 1 || outcome.Content[0].Text == nil || outcome.Content[0].Text.Text != "hello" {
		t.Fatalf("unexpected outcome content: %+v", outcome.Content)
	}
}

func TestFakeHost_PromptContextCancellation(t *testing.T) {
	h := newConnectedFakeHost(t, "p1")
	sess, err := h.NewSession(context.Background(), "p1")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outcome, err := h.Prompt(ctx, sess.Ref(), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if outcome.StopReason != StopReasonCancelled {
		t.Fatalf("StopReason = %v, want %v", outcome.StopReason, StopReasonCancelled)
	}
}

func TestFakeHost_CancelUnknownSession(t *testing.T) {
	h := newConnectedFakeHost(t, "p1")
	err := h.Cancel(context.Background(), SessionRef{ConversationID: "nope"})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v, want ErrSessionNotFound", err)
	}
}

func TestFakeHost_NewSessionUnknownProvider(t *testing.T) {
	h := newConnectedFakeHost(t, "p1")
	if _, err := h.NewSession(context.Background(), "does-not-exist"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v, want ErrSessionNotFound", err)
	}
}

func TestFakeHost_LoadSessionUnknownRef(t *testing.T) {
	h := newConnectedFakeHost(t, "p1")
	_, err := h.LoadSession(context.Background(), SessionRef{ConversationID: "ghost"})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v, want ErrSessionNotFound", err)
	}
}

func TestFakeHost_EventsDeliveredInOrder(t *testing.T) {
	h := newConnectedFakeHost(t, "p1")
	sess, _ := h.NewSession(context.Background(), "p1")

	var kinds []EventKind
	sub, err := h.Subscribe(context.Background(), sess.Ref(), func(ev Event) {
		kinds = append(kinds, ev.Kind)
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()

	if _, err := h.Prompt(context.Background(), sess.Ref(), []ContentBlock{{Text: &TextBlock{Text: "a"}}}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if err := h.SetModel(context.Background(), sess.Ref(), "m2"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if err := h.Cancel(context.Background(), sess.Ref()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	want := []EventKind{EventAgentMessage, EventModelChange, EventLifecycle}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	for i, k := range want {
		if kinds[i] != k {
			t.Fatalf("kinds[%d] = %v, want %v", i, kinds[i], k)
		}
	}
}
