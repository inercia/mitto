package conversation

import (
	"errors"
	"log/slog"
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
}

func (f *fakeQueueDeps) queueProcessingEnabled() bool        { return f.enabled }
func (f *fakeQueueDeps) queueDelaySeconds() int              { return f.delaySeconds }
func (f *fakeQueueDeps) queueForSession() *session.Queue     { return f.queue }
func (f *fakeQueueDeps) queueIsPrompting() bool              { return f.prompting }
func (f *fakeQueueDeps) queueIsClosed() bool                 { return f.closed }
func (f *fakeQueueDeps) lastResponseCompleteTime() time.Time { return f.lastResponse }
func (f *fakeQueueDeps) queueLogger() *slog.Logger           { return nil }
func (f *fakeQueueDeps) queueSessionID() string              { return "test-session" }

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
func (r *recorderObserver) OnUserPrompt(int64, string, string, string, []string, []string, string, int) {
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
