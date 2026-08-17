package slackbridge

import (
	"context"
	"time"
)

// FakeSource is a credential-free, injectable Source used by automated tests
// (acceptance criterion #6). It plays back a scripted sequence of "runs",
// where each run emits zero or more events and then either returns nil
// (Run ends cleanly, e.g. ctx cancelled) or a non-nil error simulating a
// forced disconnect — letting Bridge.Run's outer reconnect loop exercise the
// reconnect-then-receive-another-event path (acceptance criterion #5)
// without any real network connection.
type FakeSource struct {
	// Runs is the scripted sequence of Run() invocations. Each call to Run
	// consumes the next FakeRun in order; once exhausted, Run blocks until
	// ctx is cancelled and returns ctx.Err().
	Runs []FakeRun

	// OnSelfIdentified, if set, is invoked once per Run before any events are
	// emitted, mirroring SlackSource's real self-identification step
	// (AuthTest). Lets tests exercise self-event filtering end-to-end.
	OnSelfIdentified func(userID string)

	runIndex int
}

// FakeRun describes a single scripted Source.Run invocation.
type FakeRun struct {
	// SelfUserID, if non-empty, is reported via OnSelfIdentified before Events
	// are emitted.
	SelfUserID string
	// Events are emitted in order, one at a time.
	Events []Event
	// Err is returned once all Events have been emitted, simulating a
	// disconnect. A nil Err with a nil ctx.Err() means "clean end of script".
	Err error
}

// Run implements Source.
func (f *FakeSource) Run(ctx context.Context, emit func(Event)) error {
	if f.runIndex >= len(f.Runs) {
		<-ctx.Done()
		return ctx.Err()
	}
	run := f.Runs[f.runIndex]
	f.runIndex++

	if run.SelfUserID != "" && f.OnSelfIdentified != nil {
		f.OnSelfIdentified(run.SelfUserID)
	}

	for _, evt := range run.Events {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		emit(evt)
		// Small yield so ordering/timing-sensitive tests (e.g. dedupe across
		// two emits) behave deterministically without a real network.
		time.Sleep(time.Millisecond)
	}

	return run.Err
}
