package slackbridge

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestFakeSource_PlaysBackScriptedRuns verifies FakeSource emits each run's
// events in order and returns the scripted error, driving Bridge.Run's
// reconnect loop without any credentials or network I/O.
func TestFakeSource_PlaysBackScriptedRuns(t *testing.T) {
	var identified []string
	src := &FakeSource{
		OnSelfIdentified: func(id string) { identified = append(identified, id) },
		Runs: []FakeRun{
			{SelfUserID: "U-SELF", Events: []Event{{EventID: "Ev1"}, {EventID: "Ev2"}}, Err: errors.New("disconnect")},
		},
	}

	var got []Event
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := src.Run(ctx, func(e Event) { got = append(got, e) })
	if err == nil || err.Error() != "disconnect" {
		t.Errorf("Run() error = %v, want the scripted disconnect error", err)
	}
	if len(got) != 2 || got[0].EventID != "Ev1" || got[1].EventID != "Ev2" {
		t.Errorf("emitted events = %#v, want [Ev1, Ev2] in order", got)
	}
	if len(identified) != 1 || identified[0] != "U-SELF" {
		t.Errorf("OnSelfIdentified calls = %#v, want [U-SELF]", identified)
	}
}

// TestFakeSource_ExhaustedScriptBlocksUntilCancel verifies that once every
// scripted run has been consumed, Run blocks (as a real long-lived Source
// would between events) until ctx is cancelled, rather than returning
// immediately and spinning Bridge.Run's reconnect loop hot.
func TestFakeSource_ExhaustedScriptBlocksUntilCancel(t *testing.T) {
	src := &FakeSource{Runs: []FakeRun{{Events: nil}}}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// First call consumes the single (empty) scripted run and returns immediately.
	if err := src.Run(ctx, func(Event) {}); err != nil {
		t.Errorf("first Run() error = %v, want nil (empty run, no Err)", err)
	}

	// Second call has no more scripted runs — must block until ctx is done.
	start := time.Now()
	err := src.Run(ctx, func(Event) {})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("second Run() error = %v, want context.DeadlineExceeded", err)
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Error("second Run() returned too quickly; expected it to block until ctx deadline")
	}
}

func TestFakeSource_WaitGatesRunAndHonorsCancellation(t *testing.T) {
	t.Run("release", func(t *testing.T) {
		gate := make(chan struct{})
		src := &FakeSource{Runs: []FakeRun{{Wait: gate, Events: []Event{{EventID: "released"}}}}}
		events := make(chan Event, 1)
		done := make(chan error, 1)
		go func() { done <- src.Run(context.Background(), func(event Event) { events <- event }) }()

		select {
		case event := <-events:
			t.Fatalf("event emitted before gate release: %+v", event)
		case <-time.After(20 * time.Millisecond):
		}
		close(gate)
		if event := <-events; event.EventID != "released" {
			t.Fatalf("event after gate release = %+v", event)
		}
		if err := <-done; err != nil {
			t.Fatalf("Run() after gate release: %v", err)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		src := &FakeSource{Runs: []FakeRun{{Wait: make(chan struct{})}}}
		if err := src.Run(ctx, func(Event) {}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	})
}
