package conversation

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
)

func TestPromptWithMeta_FreshContextFailureHasNoPhantomPrompt(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	recorder := session.NewRecorder(store)
	if err := recorder.Start("test", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	flushErr := errors.New("context deadline exceeded")
	shared := newFakeSharedProcess()
	shared.promptErr = flushErr
	bs := &BackgroundSession{
		ctx: ctx, cancel: cancel, observers: make(map[SessionObserver]struct{}),
		store: store, recorder: recorder, persistedID: recorder.SessionID(), nextSeq: 2,
		sharedProcess: shared, acpID: "acp-sess-1", contextFlushCommand: "/clear",
		pendingConfig: make(map[string]string),
	}
	bs.markACPContextUnknown()
	bs.promptCond = sync.NewCond(&bs.promptMu)
	observer := &mockSessionObserver{}
	bs.AddObserver(observer)

	var accepted atomic.Bool
	completed := make(chan error, 1)
	if err := bs.PromptWithMeta("must not appear", PromptMeta{
		FreshContext:       true,
		OnDispatchAccepted: func() { accepted.Store(true) },
		OnComplete:         func(err error) { completed <- err },
	}); err != nil {
		t.Fatalf("PromptWithMeta() error = %v", err)
	}
	select {
	case err := <-completed:
		if !errors.Is(err, flushErr) {
			t.Fatalf("OnComplete error = %v, want %v", err, flushErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for preparation failure")
	}

	if accepted.Load() {
		t.Fatal("OnDispatchAccepted called despite failed fresh-context preparation")
	}
	if got := observer.getUserPromptMessages(); len(got) != 0 {
		t.Fatalf("OnUserPrompt messages = %v, want none", got)
	}
	events, err := store.ReadEvents(recorder.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	var prompts, failures int
	for _, event := range events {
		switch event.Type {
		case session.EventTypeUserPrompt:
			prompts++
		case session.EventTypeError:
			failures++
		}
	}
	if prompts != 0 || failures != 1 {
		t.Fatalf("persisted prompts=%d errors=%d, want 0 and 1", prompts, failures)
	}
}

func TestPromptWithMeta_DispatchAcceptedExactlyOnceBeforePrompt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	shared := newFakeSharedProcess()
	var accepted atomic.Int32
	var acceptedAtPrompt atomic.Int32
	shared.promptHook = func() { acceptedAtPrompt.Store(accepted.Load()) }
	bs := &BackgroundSession{
		ctx: ctx, cancel: cancel, observers: make(map[SessionObserver]struct{}),
		sharedProcess: shared, acpID: "acp-sess-1", pendingConfig: make(map[string]string),
	}
	bs.markACPContextFresh()
	bs.promptCond = sync.NewCond(&bs.promptMu)
	completed := make(chan error, 1)
	if err := bs.PromptWithMeta("dispatch me", PromptMeta{
		OnDispatchAccepted: func() { accepted.Add(1) },
		OnComplete:         func(err error) { completed <- err },
	}); err != nil {
		t.Fatalf("PromptWithMeta() error = %v", err)
	}
	select {
	case err := <-completed:
		if err != nil {
			t.Fatalf("OnComplete error = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for prompt completion")
	}
	if got := accepted.Load(); got != 1 {
		t.Fatalf("OnDispatchAccepted calls = %d, want 1", got)
	}
	if got := acceptedAtPrompt.Load(); got != 1 {
		t.Fatalf("accepted count observed by Prompt = %d, want 1", got)
	}
}

func TestLoopRunner_OnTasksFreshContextFailurePreservesDeltaAndRearms(t *testing.T) {
	const sessionID = "ontasks-fresh-failure"
	rawBefore := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	rawNow := mustMarshalRows(t,
		beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"),
		beadsRow("mitto-2", "open", "2026-01-02T00:00:00Z"))
	runner, loopStore := newTasksRefireTestRunner(t, sessionID, rawNow)
	runner.SetPromptResolver(func(string, string) (string, error) { return "iterate", nil })
	runner.SetMinLoopTasksCooldownSeconds(0)
	if err := NewTasksBaselineStore(runner.store.SessionDir(sessionID)).Set(rawBefore); err != nil {
		t.Fatal(err)
	}
	loop, err := loopStore.Get()
	if err != nil {
		t.Fatal(err)
	}
	loop.FreshContext = true
	if err := loopStore.Set(loop); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	shared := newFakeSharedProcess()
	shared.promptErr = errors.New("context deadline exceeded")
	bs := &BackgroundSession{
		ctx: ctx, cancel: cancel, observers: make(map[SessionObserver]struct{}),
		store: runner.store, persistedID: sessionID, workingDir: "/proj",
		sharedProcess: shared, acpID: "acp-sess-1", contextFlushCommand: "/clear",
		pendingConfig:  make(map[string]string),
		promptResolver: func(string, string) (string, error) { return "iterate", nil },
	}
	bs.markACPContextUnknown()
	bs.promptCond = sync.NewCond(&bs.promptMu)
	runner.sessionManager.AddSessionForTest(bs)
	runner.SetTasksQuiescenceWindow(time.Hour)
	t.Cleanup(func() { runner.cancelTasksRebaseTimerForTest(sessionID) })

	meta, _ := runner.store.GetMetadata(sessionID)
	loop, _ = loopStore.Get()
	runner.processTasksChange(meta, loop, loopStore, rawNow)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && (bs.IsPrompting() || !runner.tasksRefirePendingForTest(sessionID)) {
		time.Sleep(5 * time.Millisecond)
	}
	baseline, err := NewTasksBaselineStore(runner.store.SessionDir(sessionID)).Get()
	if err != nil {
		t.Fatal(err)
	}
	if !jsonBytesEqual(t, baseline.RawSnapshot, rawBefore) {
		t.Fatal("onTasks baseline advanced despite pre-dispatch failure")
	}
	if !runner.tasksRefirePendingForTest(sessionID) || countTasksRebaseTimers(runner) != 1 {
		t.Fatal("failed onTasks preparation did not preserve and re-arm the delta")
	}
}

func TestLoopRunner_OnTasksAcceptedLongRunningPromptAdvancesBaseline(t *testing.T) {
	const sessionID = "ontasks-accepted-long-running"
	rawBefore := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	rawNow := mustMarshalRows(t,
		beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"),
		beadsRow("mitto-2", "open", "2026-01-02T00:00:00Z"))
	runner, loopStore := newTasksRefireTestRunner(t, sessionID, rawNow)
	runner.SetPromptResolver(func(string, string) (string, error) { return "iterate", nil })
	runner.SetMinLoopTasksCooldownSeconds(0)
	baselineStore := NewTasksBaselineStore(runner.store.SessionDir(sessionID))
	if err := baselineStore.Set(rawBefore); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	shared := newFakeSharedProcess()
	shared.promptBlock = make(chan struct{})
	promptEntered := make(chan struct{})
	shared.promptHook = func() { close(promptEntered) }
	t.Cleanup(func() {
		select {
		case <-shared.promptBlock:
		default:
			close(shared.promptBlock)
		}
	})
	bs := &BackgroundSession{
		ctx: ctx, cancel: cancel, observers: make(map[SessionObserver]struct{}),
		store: runner.store, persistedID: sessionID, workingDir: "/proj",
		sharedProcess: shared, acpID: "acp-sess-1", pendingConfig: make(map[string]string),
		promptResolver: func(string, string) (string, error) { return "iterate", nil },
	}
	bs.markACPContextFresh()
	bs.promptCond = sync.NewCond(&bs.promptMu)
	runner.sessionManager.AddSessionForTest(bs)

	meta, _ := runner.store.GetMetadata(sessionID)
	loop, _ := loopStore.Get()
	runner.processTasksChange(meta, loop, loopStore, rawNow)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		baseline, err := baselineStore.Get()
		if err == nil && jsonBytesEqual(t, baseline.RawSnapshot, rawNow) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	baseline, err := baselineStore.Get()
	if err != nil {
		t.Fatal(err)
	}
	if !jsonBytesEqual(t, baseline.RawSnapshot, rawNow) {
		t.Fatal("onTasks baseline did not advance at dispatch acceptance")
	}
	if !bs.IsPrompting() {
		t.Fatal("prompt completed before long-running acceptance assertion")
	}
	select {
	case <-promptEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("accepted prompt did not enter the ACP transport")
	}
	close(shared.promptBlock)
	deadline = time.Now().Add(2 * time.Second)
	claimHeld := true
	for time.Now().Before(deadline) {
		runner.dispatchInFlightMu.Lock()
		_, claimHeld = runner.dispatchInFlight[sessionID]
		runner.dispatchInFlightMu.Unlock()
		if !claimHeld {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if claimHeld {
		t.Fatal("loop dispatch did not complete after transport was unblocked")
	}
}
