package conversation

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// compile-time check that fakeQueueDeps satisfies queueDeps.
var _ queueDeps = (*fakeQueueDeps)(nil)

type fakeQueueDeps struct {
	enabled          bool
	delaySeconds     int
	queue            *session.Queue
	prompting        bool
	closed           bool
	lastResponse     time.Time
	promptWithMetaFn func(message string, meta PromptMeta) error

	// recorders
	deliveryInProgress   []bool
	notifiedObservers    []string // captures observer event names via sentinel observer
	restoreBaselineCalls int
	promptWithMetaCalls  []PromptMeta
	promptWithMetaMsgs   []string
	recordedErrors       []string
	lastSendErr          string
}

func (f *fakeQueueDeps) queueProcessingEnabled() bool        { return f.enabled }
func (f *fakeQueueDeps) queueDelaySeconds() int              { return f.delaySeconds }
func (f *fakeQueueDeps) queueForSession() *session.Queue     { return f.queue }
func (f *fakeQueueDeps) queueIsPrompting() bool              { return f.prompting }
func (f *fakeQueueDeps) queueIsClosed() bool                 { return f.closed }
func (f *fakeQueueDeps) lastResponseCompleteTime() time.Time { return f.lastResponse }
func (f *fakeQueueDeps) queueLogger() *slog.Logger           { return nil }
func (f *fakeQueueDeps) queueSessionID() string              { return "test-session" }
func (f *fakeQueueDeps) queueRecordErrorEvent(msg string) {
	f.recordedErrors = append(f.recordedErrors, msg)
}
func (f *fakeQueueDeps) setLastQueueSendError(msg string) { f.lastSendErr = msg }

func (f *fakeQueueDeps) setQueuedDeliveryInProgress(v bool) {
	f.deliveryInProgress = append(f.deliveryInProgress, v)
}

func (f *fakeQueueDeps) restoreBaselineIfOverride() {
	f.restoreBaselineCalls++
}

func (f *fakeQueueDeps) notifyObservers(fn func(SessionObserver)) {
	fn(&recorderObserver{deps: f})
}

func (f *fakeQueueDeps) promptWithMeta(message string, meta PromptMeta) error {
	f.promptWithMetaMsgs = append(f.promptWithMetaMsgs, message)
	f.promptWithMetaCalls = append(f.promptWithMetaCalls, meta)
	if f.promptWithMetaFn != nil {
		return f.promptWithMetaFn(message, meta)
	}
	return nil
}

// recorderObserver records which SessionObserver methods were called.
type recorderObserver struct {
	deps *fakeQueueDeps
}

func (r *recorderObserver) OnQueueMessageSending(id string) {
	r.deps.notifiedObservers = append(r.deps.notifiedObservers, "sending:"+id)
}
func (r *recorderObserver) OnQueueMessageSent(id string) {
	r.deps.notifiedObservers = append(r.deps.notifiedObservers, "sent:"+id)
}
func (r *recorderObserver) OnQueueUpdated(n int, a, id string) {
	r.deps.notifiedObservers = append(r.deps.notifiedObservers, "updated:"+a)
}
func (r *recorderObserver) OnQueueReordered([]session.QueuedMessage) {
	r.deps.notifiedObservers = append(r.deps.notifiedObservers, "reordered")
}
func (r *recorderObserver) OnError(msg string) {
	r.deps.notifiedObservers = append(r.deps.notifiedObservers, "error:"+msg)
}
func (r *recorderObserver) OnAgentMessage(int64, string)             {}
func (r *recorderObserver) OnAgentThought(int64, string)             {}
func (r *recorderObserver) OnToolCall(int64, string, string, string) {}
func (r *recorderObserver) OnToolUpdate(int64, string, *string)      {}
func (r *recorderObserver) OnPlan(int64, []PlanEntry)                {}
func (r *recorderObserver) OnFileWrite(int64, string, int)           {}
func (r *recorderObserver) OnFileRead(int64, string, int)            {}
func (r *recorderObserver) OnPromptComplete(int)                     {}
func (r *recorderObserver) OnActionButtons([]ActionButton)           {}
func (r *recorderObserver) OnUserPrompt(int64, string, string, string, []string, []string, string, int, map[string]string) {
}
func (r *recorderObserver) OnAvailableCommandsUpdated([]AvailableCommand) {}
func (r *recorderObserver) OnACPStopped(string)                           {}
func (r *recorderObserver) OnACPStarted()                                 {}
func (r *recorderObserver) OnUIPrompt(UIPromptRequest)                    {}
func (r *recorderObserver) OnUIPromptDismiss(string, string)              {}
func (r *recorderObserver) OnNotification(UINotifyRequest)                {}
func (r *recorderObserver) OnContextUsageUpdate(int, int)                 {}

// newTestQueue creates a real *session.Queue backed by a temp dir for tests.
func newTestQueue(t *testing.T) *session.Queue {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store.Queue("test-session")
}

// --- hasImmediateQueued ---

func TestQueueDispatcher_HasImmediateQueued(t *testing.T) {
	qd := queueDispatcher{}

	t.Run("disabled → false", func(t *testing.T) {
		d := &fakeQueueDeps{enabled: false}
		if qd.hasImmediateQueued(d) {
			t.Fatal("expected false when disabled")
		}
	})

	t.Run("nil queue → false", func(t *testing.T) {
		d := &fakeQueueDeps{enabled: true, queue: nil}
		if qd.hasImmediateQueued(d) {
			t.Fatal("expected false when queue is nil")
		}
	})

	t.Run("empty queue → false", func(t *testing.T) {
		d := &fakeQueueDeps{enabled: true, queue: newTestQueue(t)}
		if qd.hasImmediateQueued(d) {
			t.Fatal("expected false for empty queue")
		}
	})

	t.Run("len>0 with delay=0 → true", func(t *testing.T) {
		q := newTestQueue(t)
		if _, err := q.Add("hello", nil, nil, "", nil, 0, nil, ""); err != nil {
			t.Fatalf("Add: %v", err)
		}
		d := &fakeQueueDeps{enabled: true, queue: q, delaySeconds: 0}
		if !qd.hasImmediateQueued(d) {
			t.Fatal("expected true for non-empty queue with no delay")
		}
	})

	t.Run("len>0 with delay>0 → false", func(t *testing.T) {
		q := newTestQueue(t)
		if _, err := q.Add("hello", nil, nil, "", nil, 0, nil, ""); err != nil {
			t.Fatalf("Add: %v", err)
		}
		d := &fakeQueueDeps{enabled: true, queue: q, delaySeconds: 5}
		if qd.hasImmediateQueued(d) {
			t.Fatal("expected false when delay is configured")
		}
	})
}

// --- tryProcess ---

func TestQueueDispatcher_TryProcess(t *testing.T) {
	qd := queueDispatcher{}

	t.Run("prompting → false", func(t *testing.T) {
		d := &fakeQueueDeps{enabled: true, prompting: true}
		if qd.tryProcess(d) {
			t.Fatal("expected false when prompting")
		}
	})

	t.Run("closed → false", func(t *testing.T) {
		d := &fakeQueueDeps{enabled: true, closed: true}
		if qd.tryProcess(d) {
			t.Fatal("expected false when closed")
		}
	})

	t.Run("nil queue → false", func(t *testing.T) {
		d := &fakeQueueDeps{enabled: true, queue: nil}
		if qd.tryProcess(d) {
			t.Fatal("expected false when queue is nil")
		}
	})

	t.Run("empty queue → false", func(t *testing.T) {
		d := &fakeQueueDeps{enabled: true, queue: newTestQueue(t)}
		if qd.tryProcess(d) {
			t.Fatal("expected false for empty queue")
		}
	})

	t.Run("delay not elapsed → false", func(t *testing.T) {
		q := newTestQueue(t)
		if _, err := q.Add("msg", nil, nil, "", nil, 0, nil, ""); err != nil {
			t.Fatalf("Add: %v", err)
		}
		d := &fakeQueueDeps{
			enabled:      true,
			queue:        q,
			delaySeconds: 60,
			lastResponse: time.Now(), // just now, delay not elapsed
		}
		if qd.tryProcess(d) {
			t.Fatal("expected false when delay has not elapsed")
		}
	})

	t.Run("happy path with delay=0 → sends message", func(t *testing.T) {
		q := newTestQueue(t)
		if _, err := q.Add("the message", nil, nil, "", nil, 0, nil, ""); err != nil {
			t.Fatalf("Add: %v", err)
		}
		d := &fakeQueueDeps{enabled: true, queue: q, delaySeconds: 0}
		if !qd.tryProcess(d) {
			t.Fatal("expected true on happy path")
		}
		// Should have fired OnQueueMessageSending before send
		if len(d.notifiedObservers) == 0 {
			t.Fatal("expected observer notifications")
		}
		sendingFired := false
		for _, ev := range d.notifiedObservers {
			if len(ev) >= 8 && ev[:8] == "sending:" {
				sendingFired = true
			}
		}
		if !sendingFired {
			t.Fatalf("expected OnQueueMessageSending, got %v", d.notifiedObservers)
		}
		// promptWithMeta must have been called
		if len(d.promptWithMetaMsgs) == 0 {
			t.Fatal("expected promptWithMeta to be called")
		}
		if d.promptWithMetaMsgs[0] != "the message" {
			t.Fatalf("expected message 'the message', got %q", d.promptWithMetaMsgs[0])
		}
		// OnQueueMessageSent must have fired
		sentFired := false
		for _, ev := range d.notifiedObservers {
			if len(ev) >= 5 && ev[:5] == "sent:" {
				sentFired = true
			}
		}
		if !sentFired {
			t.Fatalf("expected OnQueueMessageSent, got %v", d.notifiedObservers)
		}
	})
}

// --- processNext ---

func TestQueueDispatcher_ProcessNext(t *testing.T) {
	qd := queueDispatcher{}

	t.Run("disabled → restoreBaseline + false", func(t *testing.T) {
		d := &fakeQueueDeps{enabled: false}
		if qd.processNext(d) {
			t.Fatal("expected false")
		}
		if d.restoreBaselineCalls != 1 {
			t.Fatalf("expected restoreBaselineIfOverride called once, got %d", d.restoreBaselineCalls)
		}
	})

	t.Run("nil queue → restoreBaseline + false", func(t *testing.T) {
		d := &fakeQueueDeps{enabled: true, queue: nil}
		if qd.processNext(d) {
			t.Fatal("expected false")
		}
		if d.restoreBaselineCalls != 1 {
			t.Fatalf("expected restoreBaselineIfOverride called once, got %d", d.restoreBaselineCalls)
		}
	})

	t.Run("empty queue → restoreBaseline + false", func(t *testing.T) {
		d := &fakeQueueDeps{enabled: true, queue: newTestQueue(t)}
		if qd.processNext(d) {
			t.Fatal("expected false")
		}
		if d.restoreBaselineCalls != 1 {
			t.Fatalf("expected restoreBaselineIfOverride called once, got %d", d.restoreBaselineCalls)
		}
	})

	// TestQueueDispatcher_ProcessNext_ClosedSession_ShouldNotDispatch reproduces
	// mitto-xlwh (defect 1): unlike tryProcess (which checks queueIsClosed()),
	// processNext — the path the prompt-completion tail actually calls via
	// pdProcessNextQueuedMessage → processNextQueuedMessage — has NO
	// closed-session guard at all. A message popped here while the session is
	// closed/deleted is dispatched via promptWithMeta (which will itself fail
	// with "session is closed"), and Pop() has already destroyed the message
	// from the durable queue by the time that failure is observed — the
	// message is silently lost. This test currently FAILS because processNext
	// dispatches even when closed=true; after the fix it must return false and
	// leave promptWithMeta uncalled.
	t.Run("closed session → must not pop/dispatch (mitto-xlwh)", func(t *testing.T) {
		q := newTestQueue(t)
		if _, err := q.Add("queued msg", nil, nil, "", nil, 0, nil, ""); err != nil {
			t.Fatalf("Add: %v", err)
		}
		d := &fakeQueueDeps{enabled: true, queue: q, delaySeconds: 0, closed: true}
		if qd.processNext(d) {
			t.Fatal("expected processNext to return false when session is closed")
		}
		if len(d.promptWithMetaMsgs) != 0 {
			t.Fatalf("expected promptWithMeta NOT to be called on a closed session, got %v", d.promptWithMetaMsgs)
		}
		queueLen, err := q.Len()
		if err != nil {
			t.Fatalf("Len: %v", err)
		}
		if queueLen != 1 {
			t.Fatalf("expected the queued message to remain (not popped) on a closed session, queue len = %d", queueLen)
		}
	})

	t.Run("happy path with delay=0 → sets inProgress, sends, returns true", func(t *testing.T) {
		q := newTestQueue(t)
		if _, err := q.Add("queued msg", nil, nil, "", nil, 0, nil, ""); err != nil {
			t.Fatalf("Add: %v", err)
		}
		d := &fakeQueueDeps{enabled: true, queue: q, delaySeconds: 0}
		if !qd.processNext(d) {
			t.Fatal("expected true on happy path")
		}
		// setQueuedDeliveryInProgress must have been called with true then false
		if len(d.deliveryInProgress) < 2 {
			t.Fatalf("expected at least 2 deliveryInProgress calls, got %d: %v", len(d.deliveryInProgress), d.deliveryInProgress)
		}
		if !d.deliveryInProgress[0] {
			t.Fatal("first call should be true")
		}
		if d.deliveryInProgress[len(d.deliveryInProgress)-1] {
			t.Fatal("last call should be false (deferred)")
		}
		// promptWithMeta must have been called
		if len(d.promptWithMetaMsgs) == 0 {
			t.Fatal("expected promptWithMeta to be called")
		}
		// OnQueueMessageSending must be first notification
		if len(d.notifiedObservers) == 0 {
			t.Fatal("expected observer notifications")
		}
		if len(d.notifiedObservers[0]) < 8 || d.notifiedObservers[0][:8] != "sending:" {
			t.Fatalf("expected first notification to be OnQueueMessageSending, got %q", d.notifiedObservers[0])
		}
		// OnQueueMessageSent must also have fired
		sentFired := false
		for _, ev := range d.notifiedObservers {
			if len(ev) >= 5 && ev[:5] == "sent:" {
				sentFired = true
			}
		}
		if !sentFired {
			t.Fatalf("expected OnQueueMessageSent, got %v", d.notifiedObservers)
		}
	})
}

// --- send ---

func TestQueueDispatcher_Send(t *testing.T) {
	qd := queueDispatcher{}

	t.Run("promptWithMeta error → OnError fired, no OnQueueMessageSent", func(t *testing.T) {
		q := newTestQueue(t)
		msg := session.QueuedMessage{ID: "m1", Message: "fail"}
		d := &fakeQueueDeps{
			enabled: true,
			promptWithMetaFn: func(string, PromptMeta) error {
				return errors.New("send failed")
			},
		}
		qd.send(d, q, msg)
		errorFired := false
		sentFired := false
		for _, ev := range d.notifiedObservers {
			if len(ev) >= 6 && ev[:6] == "error:" {
				errorFired = true
			}
			if len(ev) >= 5 && ev[:5] == "sent:" {
				sentFired = true
			}
		}
		if !errorFired {
			t.Fatalf("expected OnError, got %v", d.notifiedObservers)
		}
		if sentFired {
			t.Fatalf("expected NO OnQueueMessageSent on error, got %v", d.notifiedObservers)
		}
	})

	t.Run("happy path → OnQueueUpdated(removed) then OnQueueMessageSent", func(t *testing.T) {
		q := newTestQueue(t)
		msg := session.QueuedMessage{ID: "m2", Message: "hello"}
		d := &fakeQueueDeps{enabled: true}
		qd.send(d, q, msg)
		updatedIdx := -1
		sentIdx := -1
		for i, ev := range d.notifiedObservers {
			if ev == "updated:removed" {
				updatedIdx = i
			}
			if len(ev) >= 5 && ev[:5] == "sent:" {
				sentIdx = i
			}
		}
		if updatedIdx == -1 {
			t.Fatalf("expected OnQueueUpdated(removed), got %v", d.notifiedObservers)
		}
		if sentIdx == -1 {
			t.Fatalf("expected OnQueueMessageSent, got %v", d.notifiedObservers)
		}
		if updatedIdx > sentIdx {
			t.Fatal("OnQueueUpdated must fire before OnQueueMessageSent")
		}
	})
}

// --- notifyUpdated / notifyReordered ---

func TestQueueDispatcher_NotifyUpdated(t *testing.T) {
	qd := queueDispatcher{}
	d := &fakeQueueDeps{enabled: true}
	qd.notifyUpdated(d, 3, "added", "m1")
	if len(d.notifiedObservers) != 1 || d.notifiedObservers[0] != "updated:added" {
		t.Fatalf("expected updated:added, got %v", d.notifiedObservers)
	}
}

func TestQueueDispatcher_NotifyReordered(t *testing.T) {
	qd := queueDispatcher{}
	d := &fakeQueueDeps{enabled: true}
	qd.notifyReordered(d, []session.QueuedMessage{{ID: "m1"}})
	if len(d.notifiedObservers) != 1 || d.notifiedObservers[0] != "reordered" {
		t.Fatalf("expected reordered, got %v", d.notifiedObservers)
	}
}

// --- send failure persistence ---

func TestQueueDispatcher_Send_FailurePersistsErrorEvent(t *testing.T) {
	q := newTestQueue(t)
	msg, err := q.Add("hello", nil, nil, "", nil, 0, nil, "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	sendErr := errors.New("template parse error: unknown prompt")
	d := &fakeQueueDeps{
		enabled: true,
		queue:   q,
		promptWithMetaFn: func(_ string, _ PromptMeta) error {
			return sendErr
		},
	}

	queueDispatcher{}.send(d, q, msg)

	if len(d.recordedErrors) != 1 {
		t.Fatalf("expected 1 recordedError, got %d: %v", len(d.recordedErrors), d.recordedErrors)
	}
	if want := "Failed to send queued message: " + sendErr.Error(); d.recordedErrors[0] != want {
		t.Errorf("recordedErrors[0] = %q, want %q", d.recordedErrors[0], want)
	}
	if d.lastSendErr != sendErr.Error() {
		t.Errorf("lastSendErr = %q, want %q", d.lastSendErr, sendErr.Error())
	}
	// OnError must still be called
	var gotOnError bool
	for _, ev := range d.notifiedObservers {
		if len(ev) > 6 && ev[:6] == "error:" {
			gotOnError = true
		}
	}
	if !gotOnError {
		t.Errorf("expected OnError notification, got %v", d.notifiedObservers)
	}
}

// --- transient prompt-compile-race retry (mitto-omu) ---

// withStubbedRetrySleep swaps queueTransientRetrySleep for a no-op recorder for
// the duration of a test so retries do not actually wait. Returns a pointer to
// the recorded delays slice.
func withStubbedRetrySleep(t *testing.T) *[]time.Duration {
	t.Helper()
	prev := queueTransientRetrySleep
	recorded := []time.Duration{}
	queueTransientRetrySleep = func(d time.Duration) {
		recorded = append(recorded, d)
	}
	t.Cleanup(func() { queueTransientRetrySleep = prev })
	return &recorded
}

// TestQueueDispatcher_Send_TransientCompileRace_RetriesAndSucceeds pins
// mitto-omu happy-path retry: the first promptWithMeta call returns a
// transient-wrapped error, the second returns nil, and the observer sees
// exactly one OnQueueMessageSent with no OnError.
func TestQueueDispatcher_Send_TransientCompileRace_RetriesAndSucceeds(t *testing.T) {
	sleeps := withStubbedRetrySleep(t)

	q := newTestQueue(t)
	msg := session.QueuedMessage{ID: "m-retry-ok", Message: "hello"}

	calls := 0
	d := &fakeQueueDeps{
		enabled: true,
		promptWithMetaFn: func(string, PromptMeta) error {
			calls++
			if calls == 1 {
				return fmt.Errorf("%w: prompt \"foo\" not found (load errors present)",
					ErrPromptTransientCompileRace)
			}
			return nil
		},
	}

	queueDispatcher{}.send(d, q, msg)

	if len(d.promptWithMetaCalls) != 2 {
		t.Fatalf("expected 2 promptWithMeta attempts, got %d", len(d.promptWithMetaCalls))
	}
	if len(*sleeps) != 1 {
		t.Fatalf("expected 1 retry sleep between attempts, got %d: %v", len(*sleeps), *sleeps)
	}
	if (*sleeps)[0] != queueTransientRetryDelays[0] {
		t.Errorf("expected first sleep = queueTransientRetryDelays[0]=%v, got %v",
			queueTransientRetryDelays[0], (*sleeps)[0])
	}
	sentCount, errCount := 0, 0
	for _, ev := range d.notifiedObservers {
		if len(ev) >= 5 && ev[:5] == "sent:" {
			sentCount++
		}
		if len(ev) >= 6 && ev[:6] == "error:" {
			errCount++
		}
	}
	if sentCount != 1 {
		t.Errorf("expected exactly 1 OnQueueMessageSent, got %d: %v", sentCount, d.notifiedObservers)
	}
	if errCount != 0 {
		t.Errorf("expected 0 OnError on eventual success, got %d: %v", errCount, d.notifiedObservers)
	}
	if len(d.recordedErrors) != 0 {
		t.Errorf("expected 0 recordedErrors on eventual success, got %v", d.recordedErrors)
	}
	if d.lastSendErr != "" {
		t.Errorf("expected empty lastSendErr on eventual success, got %q", d.lastSendErr)
	}
	// OnQueueUpdated(removed) still fires exactly once, before the retry loop.
	removedCount := 0
	for _, ev := range d.notifiedObservers {
		if ev == "updated:removed" {
			removedCount++
		}
	}
	if removedCount != 1 {
		t.Errorf("expected exactly 1 OnQueueUpdated(removed), got %d: %v", removedCount, d.notifiedObservers)
	}
}

// TestQueueDispatcher_Send_TransientCompileRace_RetriesExhausted pins
// mitto-omu exhaustion path: every attempt (1 + len(queueTransientRetryDelays))
// returns transient → OnError fires once with the retries_exhausted marker in
// the payload, OnQueueMessageSent never fires, and lastSendErr / recordedErrors
// are populated.
func TestQueueDispatcher_Send_TransientCompileRace_RetriesExhausted(t *testing.T) {
	sleeps := withStubbedRetrySleep(t)

	q := newTestQueue(t)
	msg := session.QueuedMessage{ID: "m-exhausted", Message: "hello"}

	sendErr := fmt.Errorf("%w: prompt \"foo\" not found (load errors present)",
		ErrPromptTransientCompileRace)
	d := &fakeQueueDeps{
		enabled: true,
		promptWithMetaFn: func(string, PromptMeta) error {
			return sendErr
		},
	}

	queueDispatcher{}.send(d, q, msg)

	wantAttempts := 1 + len(queueTransientRetryDelays)
	if len(d.promptWithMetaCalls) != wantAttempts {
		t.Fatalf("expected %d promptWithMeta attempts, got %d", wantAttempts, len(d.promptWithMetaCalls))
	}
	// One sleep between each pair of adjacent attempts.
	if len(*sleeps) != wantAttempts-1 {
		t.Errorf("expected %d retry sleeps, got %d: %v", wantAttempts-1, len(*sleeps), *sleeps)
	}
	sentCount, errCount := 0, 0
	var lastErrEvent string
	for _, ev := range d.notifiedObservers {
		if len(ev) >= 5 && ev[:5] == "sent:" {
			sentCount++
		}
		if len(ev) >= 6 && ev[:6] == "error:" {
			errCount++
			lastErrEvent = ev
		}
	}
	if sentCount != 0 {
		t.Errorf("expected 0 OnQueueMessageSent when retries exhausted, got %d", sentCount)
	}
	if errCount != 1 {
		t.Fatalf("expected exactly 1 OnError on exhaustion, got %d: %v", errCount, d.notifiedObservers)
	}
	// The exhaustion marker must ride on OnError so ops can distinguish it from
	// a plain durable failure.
	if !strings.Contains(lastErrEvent, "retries_exhausted=true") {
		t.Errorf("expected OnError payload to contain retries_exhausted=true marker, got %q", lastErrEvent)
	}
	if !strings.Contains(lastErrEvent, fmt.Sprintf("attempts=%d", wantAttempts)) {
		t.Errorf("expected OnError payload to contain attempts=%d, got %q", wantAttempts, lastErrEvent)
	}
	// Historical "Failed to send queued message" prefix must be preserved
	// (frontend contract): the OnError event is "error:" + payload.
	if !strings.Contains(lastErrEvent, "Failed to send queued message") {
		t.Errorf("expected OnError to keep historical 'Failed to send queued message' prefix, got %q", lastErrEvent)
	}
	if len(d.recordedErrors) != 1 {
		t.Fatalf("expected exactly 1 recordedError on exhaustion, got %d: %v", len(d.recordedErrors), d.recordedErrors)
	}
	if !strings.Contains(d.recordedErrors[0], "retries_exhausted=true") {
		t.Errorf("expected recordedErrors to contain retries_exhausted=true, got %q", d.recordedErrors[0])
	}
	if d.lastSendErr != sendErr.Error() {
		t.Errorf("expected lastSendErr = %q, got %q", sendErr.Error(), d.lastSendErr)
	}
}

// TestQueueDispatcher_Send_DurableError_NoRetry pins the no-regression
// contract: an error that does not look like a transient compile-race MUST
// short-circuit on the first attempt, exactly as before mitto-omu.
func TestQueueDispatcher_Send_DurableError_NoRetry(t *testing.T) {
	sleeps := withStubbedRetrySleep(t)

	q := newTestQueue(t)
	msg := session.QueuedMessage{ID: "m-durable", Message: "hello"}

	sendErr := errors.New("prompt \"x\" not found")
	d := &fakeQueueDeps{
		enabled: true,
		promptWithMetaFn: func(string, PromptMeta) error {
			return sendErr
		},
	}

	queueDispatcher{}.send(d, q, msg)

	if len(d.promptWithMetaCalls) != 1 {
		t.Fatalf("expected exactly 1 promptWithMeta attempt on durable error, got %d", len(d.promptWithMetaCalls))
	}
	if len(*sleeps) != 0 {
		t.Errorf("expected 0 retry sleeps on durable error, got %d: %v", len(*sleeps), *sleeps)
	}
	if len(d.recordedErrors) != 1 {
		t.Fatalf("expected exactly 1 recordedError, got %d: %v", len(d.recordedErrors), d.recordedErrors)
	}
	// Durable path MUST NOT carry the exhaustion marker — that is reserved for
	// the transient-race-then-exhausted class so ops can separate them in logs.
	if strings.Contains(d.recordedErrors[0], "retries_exhausted") {
		t.Errorf("durable error must NOT carry retries_exhausted marker, got %q", d.recordedErrors[0])
	}
	if want := "Failed to send queued message: " + sendErr.Error(); d.recordedErrors[0] != want {
		t.Errorf("recordedErrors[0] = %q, want exact match %q", d.recordedErrors[0], want)
	}
	if d.lastSendErr != sendErr.Error() {
		t.Errorf("lastSendErr = %q, want %q", d.lastSendErr, sendErr.Error())
	}
	errCount, sentCount := 0, 0
	for _, ev := range d.notifiedObservers {
		if len(ev) >= 6 && ev[:6] == "error:" {
			errCount++
		}
		if len(ev) >= 5 && ev[:5] == "sent:" {
			sentCount++
		}
	}
	if errCount != 1 {
		t.Errorf("expected exactly 1 OnError, got %d: %v", errCount, d.notifiedObservers)
	}
	if sentCount != 0 {
		t.Errorf("expected 0 OnQueueMessageSent on durable error, got %d", sentCount)
	}
}

// TestQueueDispatcher_Send_TemplateNotDefined_HeuristicRetries pins the
// defensive-fallback branch of isTransientPromptCompileRace: an unwrapped Go
// template error (`template "foo" not defined`) — no ErrPromptTransientCompileRace
// wrapping — must still be classified transient by the substring heuristic and
// trigger a retry that then succeeds.
func TestQueueDispatcher_Send_TemplateNotDefined_HeuristicRetries(t *testing.T) {
	sleeps := withStubbedRetrySleep(t)

	q := newTestQueue(t)
	msg := session.QueuedMessage{ID: "m-heuristic", Message: "hello"}

	calls := 0
	d := &fakeQueueDeps{
		enabled: true,
		promptWithMetaFn: func(string, PromptMeta) error {
			calls++
			if calls == 1 {
				// Raw, unwrapped Go template error — matches the heuristic
				// (contains BOTH "template " AND "not defined") but does NOT
				// match errors.Is(err, ErrPromptTransientCompileRace).
				return errors.New("template \"_shared/foo\" not defined")
			}
			return nil
		},
	}

	queueDispatcher{}.send(d, q, msg)

	if len(d.promptWithMetaCalls) != 2 {
		t.Fatalf("expected 2 promptWithMeta attempts under heuristic classification, got %d",
			len(d.promptWithMetaCalls))
	}
	if len(*sleeps) != 1 {
		t.Errorf("expected 1 retry sleep, got %d", len(*sleeps))
	}
	sentCount, errCount := 0, 0
	for _, ev := range d.notifiedObservers {
		if len(ev) >= 5 && ev[:5] == "sent:" {
			sentCount++
		}
		if len(ev) >= 6 && ev[:6] == "error:" {
			errCount++
		}
	}
	if sentCount != 1 {
		t.Errorf("expected exactly 1 OnQueueMessageSent after successful retry, got %d: %v",
			sentCount, d.notifiedObservers)
	}
	if errCount != 0 {
		t.Errorf("expected 0 OnError on eventual success, got %d: %v", errCount, d.notifiedObservers)
	}
	if len(d.recordedErrors) != 0 {
		t.Errorf("expected 0 recordedErrors on eventual success, got %v", d.recordedErrors)
	}
}
