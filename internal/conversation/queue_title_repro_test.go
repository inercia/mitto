package conversation

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/acpproc/acperrors"
	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/session"
)

type queueTitleRetryProvider struct {
	calls        atomic.Int32
	firstAttempt chan struct{}
	releaseFirst chan struct{}
}

func (p *queueTitleRetryProvider) PromptAuxiliary(context.Context, string, string, string) (string, error) {
	if p.calls.Add(1) == 1 {
		if p.firstAttempt != nil {
			close(p.firstAttempt)
		}
		if p.releaseFirst != nil {
			<-p.releaseFirst
		}
		return "", acperrors.ErrProcessBusy
	}
	return "Recovered Queue Title", nil
}

func (*queueTitleRetryProvider) PromptAuxiliaryAsync(context.Context, string, string, string) error {
	return nil
}

func (*queueTitleRetryProvider) CloseWorkspaceAuxiliary(string) error { return nil }

type queueTitleSuccessProvider struct {
	calls        atomic.Int32
	firstAttempt chan struct{}
	releaseFirst chan struct{}
}

func (p *queueTitleSuccessProvider) PromptAuxiliary(context.Context, string, string, string) (string, error) {
	if p.calls.Add(1) == 1 {
		close(p.firstAttempt)
		<-p.releaseFirst
	}
	return "Generated Queue Title", nil
}

func (*queueTitleSuccessProvider) PromptAuxiliaryAsync(context.Context, string, string, string) error {
	return nil
}

func (*queueTitleSuccessProvider) CloseWorkspaceAuxiliary(string) error { return nil }

func newQueueTitleReproWorker(t *testing.T, provider auxiliary.ProcessProvider) (*QueueTitleWorker, *session.Queue, QueueTitleRequest) {
	t.Helper()

	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	const (
		sessionID     = "queue-title-repro"
		workspaceUUID = "queue-title-workspace"
	)
	if err := store.Create(session.Metadata{SessionID: sessionID, WorkingDir: t.TempDir()}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	queue := store.Queue(sessionID)
	message := "Retry this queue title after shared process saturation"
	queued, err := queue.Add(message, nil, nil, "", nil, 10, nil, "")
	if err != nil {
		t.Fatalf("Queue.Add: %v", err)
	}

	sm := NewSessionManager("", "test", false, nil)
	sm.AddSessionForTest(NewMinimalBackgroundSession(sessionID, t.TempDir(), workspaceUUID))
	worker := &QueueTitleWorker{
		store:            store,
		sessionManager:   sm,
		auxiliaryManager: auxiliary.NewWorkspaceAuxiliaryManager(provider, nil),
		ctx:              context.Background(),
	}
	return worker, queue, QueueTitleRequest{SessionID: sessionID, MessageID: queued.ID, Message: message}
}

// TestQueueTitleWorker_RetriesProcessBusyUntilMessageTitled reproduces mitto-0qeg:
// transient shared-process load shedding must retain one job until it can title the queued message.
func TestQueueTitleWorker_RetriesProcessBusyUntilMessageTitled(t *testing.T) {
	origMax, origDelay := titleMaxRetries, titleRetryBaseDelay
	titleMaxRetries, titleRetryBaseDelay = 3, time.Millisecond
	t.Cleanup(func() { titleMaxRetries, titleRetryBaseDelay = origMax, origDelay })

	provider := &queueTitleRetryProvider{}
	worker, queue, req := newQueueTitleReproWorker(t, provider)
	worker.processRequest(req)

	if got := provider.calls.Load(); got != 2 {
		t.Fatalf("auxiliary calls = %d, want 2 (busy attempt plus recovery retry)", got)
	}
	queued, err := queue.Get(req.MessageID)
	if err != nil {
		t.Fatalf("Queue.Get: %v", err)
	}
	if queued.Title != "Recovered Queue Title" {
		t.Fatalf("queue title = %q, want recovered title", queued.Title)
	}
}

// TestQueueTitleWorker_CancelsBusyRetryAfterDispatch pins the paired cancellation contract:
// once dispatch pops the message, a pending process-busy retry must stop without a callback.
func TestQueueTitleWorker_CancelsBusyRetryAfterDispatch(t *testing.T) {
	origMax, origDelay := titleMaxRetries, titleRetryBaseDelay
	titleMaxRetries, titleRetryBaseDelay = 3, time.Millisecond
	t.Cleanup(func() { titleMaxRetries, titleRetryBaseDelay = origMax, origDelay })

	provider := &queueTitleRetryProvider{
		firstAttempt: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
	worker, queue, req := newQueueTitleReproWorker(t, provider)
	var finalCallbacks atomic.Int32
	worker.OnTitleGenerated = func(_, _, title string) {
		if title == "Recovered Queue Title" {
			finalCallbacks.Add(1)
		}
	}
	done := make(chan struct{})
	go func() {
		worker.processRequest(req)
		close(done)
	}()

	<-provider.firstAttempt
	dispatched, err := queue.Pop()
	if err != nil {
		t.Fatalf("Queue.Pop: %v", err)
	}
	if dispatched.Title == "" {
		t.Fatal("dispatched message has no content-derived fallback title")
	}
	close(provider.releaseFirst)
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("queue-title worker did not stop after message dispatch")
	}
	time.Sleep(10 * time.Millisecond)
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("auxiliary calls after dispatch = %d, want 1", got)
	}
	if got := finalCallbacks.Load(); got != 0 {
		t.Fatalf("final-title callbacks after dispatch = %d, want 0", got)
	}
}

func TestQueueTitleWorker_CoalescesDuplicateMessageJobs(t *testing.T) {
	provider := &queueTitleSuccessProvider{
		firstAttempt: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
	worker, _, req := newQueueTitleReproWorker(t, provider)
	worker.ctx, worker.cancel = context.WithCancel(context.Background())
	worker.requests = make(chan QueueTitleRequest, 2)
	worker.wg.Add(1)
	go worker.run()
	t.Cleanup(worker.Close)

	generated := make(chan struct{}, 1)
	worker.OnTitleGenerated = func(_, _, title string) {
		if title == "Generated Queue Title" {
			generated <- struct{}{}
		}
	}
	worker.Enqueue(req)
	<-provider.firstAttempt
	worker.Enqueue(req)
	close(provider.releaseFirst)
	select {
	case <-generated:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("timed out waiting for generated queue title")
	}
	time.Sleep(10 * time.Millisecond)
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("auxiliary calls for duplicate message jobs = %d, want 1", got)
	}
}
