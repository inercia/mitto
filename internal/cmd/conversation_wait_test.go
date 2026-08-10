package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/pkg/api"
)

// --- queueGate ---------------------------------------------------------

func TestQueueGate_SendingBeforeWant(t *testing.T) {
	g := newQueueGate()
	g.onQueueMessageSending("id-1")
	select {
	case <-g.matched:
		t.Fatal("matched fired before setWant was ever called")
	default:
	}
	g.setWant("id-1")
	select {
	case <-g.matched:
	default:
		t.Fatal("matched did not fire once setWant matched a previously-seen id")
	}
}

func TestQueueGate_WantBeforeSending(t *testing.T) {
	g := newQueueGate()
	g.setWant("id-1")
	select {
	case <-g.matched:
		t.Fatal("matched fired before any sending notification arrived")
	default:
	}
	g.onQueueMessageSending("id-1")
	select {
	case <-g.matched:
	default:
		t.Fatal("matched did not fire once the matching sending notification arrived")
	}
}

func TestQueueGate_NonMatchingIDsNeverFire(t *testing.T) {
	g := newQueueGate()
	g.onQueueMessageSending("other-1")
	g.setWant("id-1")
	g.onQueueMessageSending("other-2")
	select {
	case <-g.matched:
		t.Fatal("matched fired for a non-matching id")
	default:
	}
	g.onQueueMessageSending("id-1")
	select {
	case <-g.matched:
	default:
		t.Fatal("matched did not fire once the real match arrived")
	}
}

func TestQueueGate_FireIsIdempotent(t *testing.T) {
	g := newQueueGate()
	g.setWant("id-1")
	g.onQueueMessageSending("id-1")
	// A second, redundant notification for the same id must not panic
	// (sync.Once) and matched must remain readable any number of times.
	g.onQueueMessageSending("id-1")
	for i := 0; i < 2; i++ {
		select {
		case <-g.matched:
		default:
			t.Fatal("matched should stay closed/readable")
		}
	}
}

// --- waitForQueuedMessage -----------------------------------------------

// runWait starts waitForQueuedMessage in a goroutine and returns channels for
// its result/error plus the stdout/stderr buffers it writes to.
func runWait(gate *queueGate, streamText bool) (evCh chan api.Event, errCh chan error, resultCh chan *waitResult, errOutCh chan error, stdout, stderr *bytes.Buffer) {
	evCh = make(chan api.Event, 16)
	errCh = make(chan error, 1)
	resultCh = make(chan *waitResult, 1)
	errOutCh = make(chan error, 1)
	stdout = &bytes.Buffer{}
	stderr = &bytes.Buffer{}
	go func() {
		res, err := waitForQueuedMessage(context.Background(), evCh, errCh, gate, streamText, stdout, stderr)
		resultCh <- res
		errOutCh <- err
	}()
	return
}

func TestWaitForQueuedMessage_IgnoresEventsBeforeSending(t *testing.T) {
	gate := newQueueGate()
	evCh, _, resultCh, errOutCh, stdout, _ := runWait(gate, true)

	// A foreign turn's completion arrives before our gate ever fires: must be
	// ignored (loop keeps waiting), not returned as our result.
	evCh <- api.Event{Kind: api.EventPromptComplete, EventCount: 1}
	evCh <- api.Event{Kind: api.EventAgentMessage, Seq: 1, Text: "someone else's reply"}

	time.Sleep(20 * time.Millisecond)
	select {
	case <-resultCh:
		t.Fatal("waitForQueuedMessage returned before the gate ever fired")
	default:
	}

	gate.setWant("mine")
	gate.onQueueMessageSending("mine")

	evCh <- api.Event{Kind: api.EventAgentMessage, Seq: 2, Text: "hello"}
	evCh <- api.Event{Kind: api.EventAgentMessage, Seq: 2, Text: "hello world"}
	evCh <- api.Event{Kind: api.EventPromptComplete, EventCount: 5}

	res := <-resultCh
	err := <-errOutCh
	if err != nil {
		t.Fatalf("waitForQueuedMessage: %v", err)
	}
	if res.Message != "hello world" {
		t.Errorf("Message = %q, want %q", res.Message, "hello world")
	}
	if res.EventCount != 5 {
		t.Errorf("EventCount = %d, want 5", res.EventCount)
	}
	if !strings.Contains(stdout.String(), "hello world") {
		t.Errorf("stdout = %q, want the streamed reply", stdout.String())
	}
	// The foreign message must never have reached stdout.
	if strings.Contains(stdout.String(), "someone else's reply") {
		t.Errorf("stdout leaked a foreign turn's content: %q", stdout.String())
	}
}

func TestWaitForQueuedMessage_NoStreamingWhenTableOff(t *testing.T) {
	gate := newQueueGate()
	gate.setWant("mine")
	gate.onQueueMessageSending("mine")
	evCh, _, resultCh, errOutCh, stdout, _ := runWait(gate, false)

	evCh <- api.Event{Kind: api.EventAgentMessage, Seq: 1, Text: "quiet reply"}
	evCh <- api.Event{Kind: api.EventPromptComplete, EventCount: 2}

	res := <-resultCh
	if err := <-errOutCh; err != nil {
		t.Fatalf("waitForQueuedMessage: %v", err)
	}
	if res.Message != "quiet reply" {
		t.Errorf("Message = %q, want %q", res.Message, "quiet reply")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing written when streamText=false", stdout.String())
	}
}

func TestWaitForQueuedMessage_ProgressAlwaysGoesToStderr(t *testing.T) {
	gate := newQueueGate()
	gate.setWant("mine")
	gate.onQueueMessageSending("mine")
	evCh, _, resultCh, errOutCh, _, stderr := runWait(gate, true)

	evCh <- api.Event{Kind: api.EventToolCall, Title: "Read file", Status: "in_progress"}
	evCh <- api.Event{Kind: api.EventToolUpdate, ID: "tool-1", Status: "completed"}
	evCh <- api.Event{Kind: api.EventAgentThought, Text: "thinking..."}
	evCh <- api.Event{Kind: api.EventPermission, Title: "Write", Description: "may I?"}
	evCh <- api.Event{Kind: api.EventFileRead, Path: "/a", Size: 10}
	evCh <- api.Event{Kind: api.EventFileWrite, Path: "/b", Size: 20}
	evCh <- api.Event{Kind: api.EventPromptComplete, EventCount: 1}

	<-resultCh
	if err := <-errOutCh; err != nil {
		t.Fatalf("waitForQueuedMessage: %v", err)
	}
	out := stderr.String()
	for _, want := range []string{"Read file", "tool-1", "thinking...", "may I?", "/a", "/b"} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr = %q, want it to contain %q", out, want)
		}
	}
}

func TestWaitForQueuedMessage_ErrorEvent(t *testing.T) {
	gate := newQueueGate()
	gate.setWant("mine")
	gate.onQueueMessageSending("mine")
	evCh, _, resultCh, errOutCh, _, _ := runWait(gate, true)

	evCh <- api.Event{Kind: api.EventError, Message: "boom"}

	res := <-resultCh
	err := <-errOutCh
	if res != nil {
		t.Errorf("result = %v, want nil on error", res)
	}
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want it to mention 'boom'", err)
	}
}

func TestWaitForQueuedMessage_ACPStopped(t *testing.T) {
	gate := newQueueGate()
	evCh, _, resultCh, errOutCh, _, _ := runWait(gate, true)
	evCh <- api.Event{Kind: api.EventACPStopped, Reason: "archived"}
	if res := <-resultCh; res != nil {
		t.Errorf("result = %v, want nil", res)
	}
	err := <-errOutCh
	if err == nil || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("err = %v, want it to mention the stop reason", err)
	}
}

func TestWaitForQueuedMessage_SessionGone(t *testing.T) {
	gate := newQueueGate()
	evCh, _, resultCh, errOutCh, _, _ := runWait(gate, true)
	evCh <- api.Event{Kind: api.EventSessionGone}
	if res := <-resultCh; res != nil {
		t.Errorf("result = %v, want nil", res)
	}
	err := <-errOutCh
	if err == nil || !strings.Contains(err.Error(), "deleted") {
		t.Fatalf("err = %v, want it to mention the session was deleted", err)
	}
}

func TestWaitForQueuedMessage_TimeoutMapsToExitWaitTimeout(t *testing.T) {
	gate := newQueueGate()
	evCh, errCh, resultCh, errOutCh, _, _ := runWait(gate, true)
	close(evCh)
	errCh <- context.DeadlineExceeded

	if res := <-resultCh; res != nil {
		t.Errorf("result = %v, want nil on timeout", res)
	}
	err := <-errOutCh
	var ec *exitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("err = %v, want *exitCodeError", err)
	}
	if ec.ExitCode() != exitWaitTimeout {
		t.Errorf("ExitCode() = %d, want exitWaitTimeout (%d)", ec.ExitCode(), exitWaitTimeout)
	}
}

func TestWaitForQueuedMessage_NonTimeoutDisconnectIsGenericError(t *testing.T) {
	gate := newQueueGate()
	evCh, errCh, resultCh, errOutCh, _, _ := runWait(gate, true)
	close(evCh)
	errCh <- errors.New("connection reset")

	if res := <-resultCh; res != nil {
		t.Errorf("result = %v, want nil", res)
	}
	err := <-errOutCh
	var ec *exitCodeError
	if errors.As(err, &ec) {
		t.Fatalf("err = %v, want a plain (non-exitCodeError) error for a non-timeout disconnect", err)
	}
	if err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("err = %v, want it to wrap the underlying disconnect error", err)
	}
}
