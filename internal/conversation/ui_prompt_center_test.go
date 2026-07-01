package conversation

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// compile-time check that fakeUIPromptDeps satisfies uiPromptDeps.
var _ uiPromptDeps = (*fakeUIPromptDeps)(nil)

type fakeUIPromptDeps struct {
	mu sync.Mutex

	sessionID  string
	logger     *slog.Logger
	closed     bool
	sessionCtx context.Context
	cancelCtx  context.CancelFunc

	// active prompt state
	promptMu     sync.Mutex
	activePrompt *activeUIPrompt

	// recorders
	notifiedEvents      []string
	recordedAnswers     [][]string
	triggeredTimeouts   []UIPromptRequest
	stateChanges        []bool
	flushMarkdownCalled int

	// hook config
	hasStateChangedHook bool
	hasTimeoutHook      bool
}

func newFakeUIPromptDeps() *fakeUIPromptDeps {
	ctx, cancel := context.WithCancel(context.Background())
	return &fakeUIPromptDeps{sessionID: "test-session", sessionCtx: ctx, cancelCtx: cancel}
}

func (f *fakeUIPromptDeps) upSessionID() string           { return f.sessionID }
func (f *fakeUIPromptDeps) upLogger() *slog.Logger        { return f.logger }
func (f *fakeUIPromptDeps) upIsClosed() bool              { return f.closed }
func (f *fakeUIPromptDeps) upSessionCtx() context.Context { return f.sessionCtx }

func (f *fakeUIPromptDeps) upLockPromptMu()   { f.promptMu.Lock() }
func (f *fakeUIPromptDeps) upUnlockPromptMu() { f.promptMu.Unlock() }

func (f *fakeUIPromptDeps) upGetActivePrompt() *activeUIPrompt  { return f.activePrompt }
func (f *fakeUIPromptDeps) upSetActivePrompt(p *activeUIPrompt) { f.activePrompt = p }
func (f *fakeUIPromptDeps) upDismissActivePromptLocked(reason string) {
	if f.activePrompt == nil {
		return
	}
	requestID := f.activePrompt.request.RequestID
	f.activePrompt.cancelFn()
	select {
	case f.activePrompt.responseCh <- UIPromptResponse{RequestID: requestID, TimedOut: true}:
	default:
	}
	f.activePrompt = nil
	go f.upNotifyObservers(func(o SessionObserver) { o.OnUIPromptDismiss(requestID, reason) })
}

func (f *fakeUIPromptDeps) upNotifyObservers(fn func(SessionObserver)) {
	fn(&promptRecorderObserver{deps: f})
}
func (f *fakeUIPromptDeps) upHasObservers() bool { return true }

func (f *fakeUIPromptDeps) upFlushMarkdown() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flushMarkdownCalled++
}

func (f *fakeUIPromptDeps) upHasUIPromptStateChangedHook() bool { return f.hasStateChangedHook }
func (f *fakeUIPromptDeps) upNotifyUIPromptStateChanged(active bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stateChanges = append(f.stateChanges, active)
}
func (f *fakeUIPromptDeps) upHasUIPromptTimeoutHook() bool { return f.hasTimeoutHook }
func (f *fakeUIPromptDeps) upTriggerUIPromptTimeout(req UIPromptRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.triggeredTimeouts = append(f.triggeredTimeouts, req)
}
func (f *fakeUIPromptDeps) upRecordUIPromptAnswer(requestID, optionID, label string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordedAnswers = append(f.recordedAnswers, []string{requestID, optionID, label})
}

type promptRecorderObserver struct{ deps *fakeUIPromptDeps }

func (r *promptRecorderObserver) record(s string) {
	r.deps.mu.Lock()
	r.deps.notifiedEvents = append(r.deps.notifiedEvents, s)
	r.deps.mu.Unlock()
}
func (r *promptRecorderObserver) OnUIPrompt(req UIPromptRequest) {
	r.record("ui_prompt:" + req.RequestID)
}
func (r *promptRecorderObserver) OnUIPromptDismiss(id, reason string) {
	r.record("dismiss:" + id + ":" + reason)
}
func (r *promptRecorderObserver) OnNotification(req UINotifyRequest)            { r.record("notify") }
func (r *promptRecorderObserver) OnAgentMessage(int64, string)                  {}
func (r *promptRecorderObserver) OnAgentThought(int64, string)                  {}
func (r *promptRecorderObserver) OnToolCall(int64, string, string, string)      {}
func (r *promptRecorderObserver) OnToolUpdate(int64, string, *string)           {}
func (r *promptRecorderObserver) OnPlan(int64, []PlanEntry)                     {}
func (r *promptRecorderObserver) OnFileWrite(int64, string, int)                {}
func (r *promptRecorderObserver) OnFileRead(int64, string, int)                 {}
func (r *promptRecorderObserver) OnContextUsageUpdate(int, int)                 {}
func (r *promptRecorderObserver) OnAvailableCommandsUpdated([]AvailableCommand) {}
func (r *promptRecorderObserver) OnQueueMessageSending(string)                  {}
func (r *promptRecorderObserver) OnQueueMessageSent(string)                     {}
func (r *promptRecorderObserver) OnQueueUpdated(int, string, string)            {}
func (r *promptRecorderObserver) OnQueueReordered([]session.QueuedMessage)      {}
func (r *promptRecorderObserver) OnError(string)                                {}
func (r *promptRecorderObserver) OnPromptComplete(int)                          {}
func (r *promptRecorderObserver) OnActionButtons([]ActionButton)                {}
func (r *promptRecorderObserver) OnUserPrompt(int64, string, string, string, []string, []string, string, int) {
}
func (r *promptRecorderObserver) OnACPStopped(string) {}
func (r *promptRecorderObserver) OnACPStarted()       {}

// --- Tests ---

func TestUIPromptCenter_HandleAnswer_MatchingID(t *testing.T) {
	c := uiPromptCenter{}
	d := newFakeUIPromptDeps()

	responseCh := make(chan UIPromptResponse, 1)
	_, cancel := context.WithCancel(context.Background())
	d.activePrompt = &activeUIPrompt{
		request:    UIPromptRequest{RequestID: "req-1"},
		responseCh: responseCh,
		cancelFn:   cancel,
	}

	c.handleUIPromptAnswer(d, "req-1", "allow", "Allow", "")

	if d.activePrompt != nil {
		t.Fatal("expected active prompt cleared after answer")
	}
	select {
	case resp := <-responseCh:
		if resp.OptionID != "allow" {
			t.Fatalf("expected option 'allow', got %q", resp.OptionID)
		}
	default:
		t.Fatal("expected response on channel")
	}
	if len(d.recordedAnswers) != 1 || d.recordedAnswers[0][0] != "req-1" {
		t.Fatalf("expected recorded answer, got %v", d.recordedAnswers)
	}
}

func TestUIPromptCenter_HandleAnswer_NonMatchingID_Ignored(t *testing.T) {
	c := uiPromptCenter{}
	d := newFakeUIPromptDeps()

	responseCh := make(chan UIPromptResponse, 1)
	_, cancel := context.WithCancel(context.Background())
	d.activePrompt = &activeUIPrompt{
		request:    UIPromptRequest{RequestID: "req-1"},
		responseCh: responseCh,
		cancelFn:   cancel,
	}
	defer cancel()

	c.handleUIPromptAnswer(d, "req-999", "allow", "Allow", "")

	if d.activePrompt == nil {
		t.Fatal("expected active prompt to remain when ID doesn't match")
	}
	if len(d.recordedAnswers) != 0 {
		t.Fatal("expected no recorded answer for non-matching ID")
	}
}

func TestUIPromptCenter_DismissPrompt_MatchingID(t *testing.T) {
	c := uiPromptCenter{}
	d := newFakeUIPromptDeps()

	responseCh := make(chan UIPromptResponse, 1)
	_, cancel := context.WithCancel(context.Background())
	d.activePrompt = &activeUIPrompt{
		request:    UIPromptRequest{RequestID: "req-x"},
		responseCh: responseCh,
		cancelFn:   cancel,
	}

	c.dismissPrompt(d, "req-x")

	if d.activePrompt != nil {
		t.Fatal("expected active prompt cleared after dismiss")
	}
}

func TestUIPromptCenter_DismissPrompt_NonMatchingID_NoOp(t *testing.T) {
	c := uiPromptCenter{}
	d := newFakeUIPromptDeps()

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.activePrompt = &activeUIPrompt{
		request:    UIPromptRequest{RequestID: "req-x"},
		responseCh: make(chan UIPromptResponse, 1),
		cancelFn:   cancel,
	}

	c.dismissPrompt(d, "req-other")

	if d.activePrompt == nil {
		t.Fatal("expected active prompt to remain for non-matching ID")
	}
}

func TestUIPromptCenter_DismissActiveUIPrompt_NilSafe(t *testing.T) {
	c := uiPromptCenter{}
	d := newFakeUIPromptDeps()
	// No active prompt — should not panic.
	c.dismissActiveUIPrompt(d)
}

func TestUIPromptCenter_DismissActiveUIPrompt_WithPrompt(t *testing.T) {
	c := uiPromptCenter{}
	d := newFakeUIPromptDeps()

	_, cancel := context.WithCancel(context.Background())
	d.activePrompt = &activeUIPrompt{
		request:    UIPromptRequest{RequestID: "req-y"},
		responseCh: make(chan UIPromptResponse, 1),
		cancelFn:   cancel,
	}

	c.dismissActiveUIPrompt(d)

	if d.activePrompt != nil {
		t.Fatal("expected active prompt cleared after dismissActiveUIPrompt")
	}
}

func TestUIPromptCenter_GetActiveUIPrompt_NilWhenNone(t *testing.T) {
	c := uiPromptCenter{}
	d := newFakeUIPromptDeps()
	if c.getActiveUIPrompt(d) != nil {
		t.Fatal("expected nil when no active prompt")
	}
}

func TestUIPromptCenter_GetActiveUIPrompt_ReturnsCopy(t *testing.T) {
	c := uiPromptCenter{}
	d := newFakeUIPromptDeps()

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.activePrompt = &activeUIPrompt{
		request:    UIPromptRequest{RequestID: "req-z", Question: "Hello?"},
		responseCh: make(chan UIPromptResponse, 1),
		cancelFn:   cancel,
	}

	got := c.getActiveUIPrompt(d)
	if got == nil || got.RequestID != "req-z" || got.Question != "Hello?" {
		t.Fatalf("unexpected GetActiveUIPrompt result: %+v", got)
	}
}

func TestUIPromptCenter_UINotify_WhenClosed_ReturnsError(t *testing.T) {
	c := uiPromptCenter{}
	d := newFakeUIPromptDeps()
	d.closed = true

	err := c.uiNotify(d, UINotifyRequest{Title: "hello"})
	if err == nil {
		t.Fatal("expected error when session closed")
	}
}

func TestUIPromptCenter_UINotify_Broadcasts(t *testing.T) {
	c := uiPromptCenter{}
	d := newFakeUIPromptDeps()

	err := c.uiNotify(d, UINotifyRequest{Title: "ping"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.notifiedEvents) != 1 || d.notifiedEvents[0] != "notify" {
		t.Fatalf("expected notify event, got %v", d.notifiedEvents)
	}
}

func TestUIPromptCenter_UIPrompt_AnsweredBeforeTimeout(t *testing.T) {
	c := uiPromptCenter{}
	d := newFakeUIPromptDeps()

	req := UIPromptRequest{RequestID: "req-ans", TimeoutSeconds: 5}

	// Answer the prompt from a goroutine shortly after it's set.
	var resp UIPromptResponse
	var promptErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, promptErr = c.uiPrompt(d, context.Background(), req)
	}()

	// Wait for prompt to become active, then answer it.
	for i := 0; i < 200; i++ {
		d.promptMu.Lock()
		ap := d.activePrompt
		d.promptMu.Unlock()
		if ap != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	c.handleUIPromptAnswer(d, "req-ans", "ok", "OK", "")
	<-done

	if promptErr != nil {
		t.Fatalf("unexpected error: %v", promptErr)
	}
	if resp.OptionID != "ok" {
		t.Fatalf("expected OptionID 'ok', got %q", resp.OptionID)
	}
}

func TestUIPromptCenter_UIPrompt_SessionCancelReturnsError(t *testing.T) {
	c := uiPromptCenter{}
	d := newFakeUIPromptDeps()

	req := UIPromptRequest{RequestID: "req-cancel", TimeoutSeconds: 60}

	done := make(chan struct{})
	var promptErr error
	go func() {
		defer close(done)
		_, promptErr = c.uiPrompt(d, context.Background(), req)
	}()

	// Wait for the prompt to become active, then cancel the session context.
	for i := 0; i < 200; i++ {
		d.promptMu.Lock()
		ap := d.activePrompt
		d.promptMu.Unlock()
		if ap != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	d.cancelCtx()
	<-done

	if promptErr == nil {
		t.Fatal("expected error on session ctx cancellation")
	}
}

func TestUIPromptCenter_UIPrompt_FlushesMarkdown(t *testing.T) {
	c := uiPromptCenter{}
	d := newFakeUIPromptDeps()

	req := UIPromptRequest{RequestID: "req-flush", TimeoutSeconds: 5}

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.uiPrompt(d, context.Background(), req) //nolint:errcheck
	}()

	for i := 0; i < 200; i++ {
		d.promptMu.Lock()
		ap := d.activePrompt
		d.promptMu.Unlock()
		if ap != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	c.handleUIPromptAnswer(d, "req-flush", "x", "X", "")
	<-done

	d.mu.Lock()
	flushes := d.flushMarkdownCalled
	d.mu.Unlock()
	if flushes != 1 {
		t.Fatalf("expected 1 markdown flush, got %d", flushes)
	}
}
