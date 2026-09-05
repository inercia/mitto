package conversation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// Gate the transport, not the dispatcher: every assertion below exercises the
// real PromptWithMeta preparation, cancellation and completion paths. RPCs can
// acknowledge after context cancellation, as an already-sent request may do.
type modelTurnRPC struct {
	ctx       context.Context
	sessionID acp.SessionId
	model     string
	blocks    []acp.ContentBlock
	reply     chan error
}

type modelTurnProcess struct {
	*fakeSharedProcess
	models  chan modelTurnRPC
	prompts chan modelTurnRPC
	cancels chan acp.SessionId
	stop    chan struct{}
}

type modelTurnCompletionObserver struct {
	trackingObserver
	completed chan struct{}
}

func (o *modelTurnCompletionObserver) OnPromptComplete(int) { o.completed <- struct{}{} }

func (p *modelTurnProcess) SetSessionModel(ctx context.Context, id acp.SessionId, model string) error {
	call := modelTurnRPC{ctx: ctx, sessionID: id, model: model, reply: make(chan error, 1)}
	p.models <- call
	select {
	case err := <-call.reply:
		return err
	case <-p.stop:
		return context.Canceled
	}
}

func (p *modelTurnProcess) Prompt(ctx context.Context, id acp.SessionId, blocks []acp.ContentBlock) (acp.PromptResponse, error) {
	call := modelTurnRPC{ctx: ctx, sessionID: id, blocks: blocks, reply: make(chan error, 1)}
	p.prompts <- call
	select {
	case err := <-call.reply:
		return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, err
	case <-p.stop:
		return acp.PromptResponse{}, context.Canceled
	}
}

func (p *modelTurnProcess) Cancel(_ context.Context, id acp.SessionId) error {
	p.cancels <- id
	return nil
}

func newModelTurnSession(t *testing.T) (*BackgroundSession, *modelTurnProcess) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	p := &modelTurnProcess{
		fakeSharedProcess: newFakeSharedProcess(),
		models:            make(chan modelTurnRPC, 16), prompts: make(chan modelTurnRPC, 16),
		cancels: make(chan acp.SessionId, 16), stop: make(chan struct{}),
	}
	bs := &BackgroundSession{
		ctx: ctx, cancel: cancel, persistedID: "model-turn", acpID: "acp-model-turn",
		workingDir: t.TempDir(), sharedProcess: p, observers: make(map[SessionObserver]struct{}),
		pendingConfig: make(map[string]string), isFirstPrompt: true,
		mittoConfig: &config.Config{Models: []config.ModelProfile{
			{Name: "Initial", Criteria: &config.ACPServerConstraint{Pattern: "Initial", MatchMode: "exact"}},
			{Name: "Manual", Criteria: &config.ACPServerConstraint{Pattern: "Manual", MatchMode: "exact"}},
		}},
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)
	t.Cleanup(func() {
		cancel()
		close(p.stop)
		bs.waitForStartupConfigConstraints()
	})
	bs.setAgentModels(lifecycleModels())
	bs.waitForStartupConfigConstraints()
	return bs, p
}

func modelTurnReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for model-turn lifecycle boundary")
		var zero T
		return zero
	}
}

func modelTurnExpectModel(t *testing.T, p *modelTurnProcess, model string) modelTurnRPC {
	t.Helper()
	call := modelTurnReceive(t, p.models)
	if call.sessionID != "acp-model-turn" || call.model != model {
		t.Fatalf("set_model = (%q, %q), want (acp-model-turn, %q)", call.sessionID, call.model, model)
	}
	return call
}

func modelTurnNoRPC(t *testing.T, calls <-chan modelTurnRPC) {
	t.Helper()
	select {
	case call := <-calls:
		t.Fatalf("unexpected RPC: session=%q model=%q", call.sessionID, call.model)
	default:
	}
}

func modelTurnDispatch(bs *BackgroundSession, message, profile string) (<-chan error, <-chan error) {
	accepted, complete := make(chan error, 1), make(chan error, 1)
	meta := PromptMeta{OnComplete: func(err error) { complete <- err }}
	if profile != "" {
		meta.PreferredModels = []config.PromptPreferredModel{{ModelName: profile}}
	}
	go func() { accepted <- bs.PromptWithMeta(message, meta) }()
	return accepted, complete
}

func modelTurnAssertIdle(t *testing.T, bs *BackgroundSession, want string) {
	t.Helper()
	if bs.IsPrompting() {
		t.Fatal("turn still owns the prompt slot after completion")
	}
	if got := bs.cmGetCurrentModelID(); got != want {
		t.Fatalf("agent model = %q, want %q", got, want)
	}
	if got := bs.GetConfigValue(ConfigOptionCategoryModel); got != want {
		t.Fatalf("displayed model = %q, want %q", got, want)
	}
	if got := bs.GetBaselineModel(); got != want {
		t.Fatalf("conversation baseline = %q, want %q", got, want)
	}
	bs.modelMu.Lock()
	override := bs.overrideActive
	bs.modelMu.Unlock()
	if override {
		t.Fatal("override survives completed turn")
	}
}

func TestPromptTurn_ModelPreflightReservesBeforeRender(t *testing.T) {
	for _, tc := range []struct {
		name, rendered string
		switchErr      error
	}{
		{name: "confirmed", rendered: "Initial"},
		{name: "rejected", rendered: "Default", switchErr: errors.New("model unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bs, proc := newModelTurnSession(t)
			accepted, complete := modelTurnDispatch(bs, "{{ .Session.ModelName }}", "Initial")
			switchCall := modelTurnExpectModel(t, proc, "initial")
			if !bs.IsPrompting() {
				t.Fatal("pre-render model RPC did not reserve the prompt slot")
			}
			select {
			case err := <-accepted:
				t.Fatalf("PromptWithMeta returned before model preparation finished: %v", err)
			default:
			}
			bs.promptMu.Lock()
			count, first := bs.promptCount, bs.isFirstPrompt
			bs.promptMu.Unlock()
			if count != 0 || !first {
				t.Fatalf("unfinished preparation consumed prompt state: count=%d first=%v", count, first)
			}
			overlap, _ := modelTurnDispatch(bs, "{{ .Session.ModelName }}", "Manual")
			if err := modelTurnReceive(t, overlap); err == nil || !strings.Contains(err.Error(), "prompt already in progress") {
				t.Fatalf("overlapping prompt error = %v", err)
			}
			modelTurnNoRPC(t, proc.models)
			modelTurnNoRPC(t, proc.prompts)
			switchCall.reply <- tc.switchErr
			if err := modelTurnReceive(t, accepted); err != nil {
				t.Fatal(err)
			}
			prompt := modelTurnReceive(t, proc.prompts)
			if prompt.sessionID != switchCall.sessionID || len(prompt.blocks) != 1 || prompt.blocks[0].Text == nil || prompt.blocks[0].Text.Text != tc.rendered {
				t.Fatalf("prompt did not render the confirmed model %q: %+v", tc.rendered, prompt.blocks)
			}
			modelTurnNoRPC(t, proc.models) // no duplicate asynchronous selection
			prompt.reply <- nil
			if tc.switchErr == nil {
				modelTurnExpectModel(t, proc, "default").reply <- nil
			}
			if err := modelTurnReceive(t, complete); err != nil {
				t.Fatal(err)
			}
			modelTurnAssertIdle(t, bs, "default")
			modelTurnNoRPC(t, proc.models)
		})
	}
}

func TestPromptTurn_CancelBlockedModelSwitchRestoresBeforeIdle(t *testing.T) {
	for _, stop := range []string{"Cancel", "ForceReset"} {
		for _, preparation := range []string{"render", "async"} {
			t.Run(stop+"/"+preparation, func(t *testing.T) {
				bs, proc := newModelTurnSession(t)
				if bs.queueForSession() != nil {
					t.Fatal("test requires no queue")
				}
				message := "plain prompt"
				if preparation == "render" {
					message = "{{ .Session.ModelName }}"
				}
				accepted, complete := modelTurnDispatch(bs, message, "Initial")
				switchCall := modelTurnExpectModel(t, proc, "initial")
				if preparation == "async" {
					if err := modelTurnReceive(t, accepted); err != nil {
						t.Fatal(err)
					}
				}
				stopped := make(chan error, 1)
				go func() {
					if stop == "Cancel" {
						stopped <- bs.Cancel()
					} else {
						bs.ForceReset()
						stopped <- nil
					}
				}()
				modelTurnReceive(t, switchCall.ctx.Done())
				if !bs.IsPrompting() {
					t.Fatal("cancellation released the slot while model RPC was unwinding")
				}
				select {
				case err := <-stopped:
					t.Fatalf("stop returned before model RPC unwound: %v", err)
				case <-proc.cancels:
					t.Fatal("cancel notification raced ahead of preparation")
				default:
				}
				modelTurnNoRPC(t, proc.models)
				modelTurnNoRPC(t, proc.prompts)
				// The agent confirms an already-sent switch despite cancellation.
				switchCall.reply <- nil
				restore := modelTurnExpectModel(t, proc, "default")
				if !bs.IsPrompting() || restore.ctx.Err() != nil {
					t.Fatal("restore must have a live context and retain the reservation")
				}
				overlap, _ := modelTurnDispatch(bs, "{{ .Session.ModelName }}", "Manual")
				if err := modelTurnReceive(t, overlap); err == nil || !strings.Contains(err.Error(), "prompt already in progress") {
					t.Fatalf("prompt accepted while cancellation was restoring the baseline: %v", err)
				}
				modelTurnNoRPC(t, proc.models)
				if stop == "Cancel" {
					if id := modelTurnReceive(t, proc.cancels); id != restore.sessionID {
						t.Fatalf("cancel addressed %q instead of %q", id, restore.sessionID)
					}
				}
				restore.reply <- nil
				if err := modelTurnReceive(t, stopped); err != nil {
					t.Fatal(err)
				}
				result := accepted
				if preparation == "async" {
					result = complete
				}
				if err := modelTurnReceive(t, result); !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled preparation returned %v", err)
				}
				modelTurnAssertIdle(t, bs, "default")
				modelTurnNoRPC(t, proc.models)
				modelTurnNoRPC(t, proc.prompts)
				select {
				case <-proc.cancels:
					t.Fatal("unexpected additional cancel notification")
				default:
				}
			})
		}
	}
}

func TestPromptTurn_TerminalResultRestoresBeforeIdle(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "success"},
		{name: "authentication", err: errors.New("Authentication required")},
		{name: "rate_limit", err: errors.New("rate limit exceeded")},
		{name: "context_too_large", err: errors.New("context too large")},
		{name: "other_error", err: errors.New("request rejected")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bs, proc := newModelTurnSession(t)
			streaming := make(chan bool, 8)
			bs.onStreamingStateChanged = func(_ string, active bool) { streaming <- active }
			accepted, complete := modelTurnDispatch(bs, "prompt", "Initial")
			if err := modelTurnReceive(t, accepted); err != nil {
				t.Fatal(err)
			}
			modelTurnExpectModel(t, proc, "initial").reply <- nil
			prompt := modelTurnReceive(t, proc.prompts)
			if !modelTurnReceive(t, streaming) {
				t.Fatal("expected streaming start")
			}
			prompt.reply <- tc.err
			restore := modelTurnExpectModel(t, proc, "default")
			if !bs.IsPrompting() {
				t.Fatal("turn became idle before baseline restore completed")
			}
			select {
			case <-streaming:
				t.Fatal("streaming ended before baseline restore completed")
			case <-complete:
				t.Fatal("OnComplete fired before baseline restore completed")
			default:
			}
			restore.reply <- nil
			if err := modelTurnReceive(t, complete); !errors.Is(err, tc.err) {
				t.Fatalf("completion error = %v, want %v", err, tc.err)
			}
			if modelTurnReceive(t, streaming) {
				t.Fatal("expected streaming stop after restore")
			}
			modelTurnAssertIdle(t, bs, "default")
			modelTurnNoRPC(t, proc.models)
		})
	}
}

func TestPromptTurn_StaleCompletionCannotChangeNewTurn(t *testing.T) {
	for _, oldErr := range []error{nil, errors.New("Authentication required")} {
		name := "success"
		if oldErr != nil {
			name = "error"
		}
		t.Run(name, func(t *testing.T) {
			bs, proc := newModelTurnSession(t)
			terminal := make(chan struct{}, 4)
			bs.AddObserver(&modelTurnCompletionObserver{completed: terminal})
			streaming := make(chan bool, 8)
			bs.onStreamingStateChanged = func(_ string, active bool) { streaming <- active }
			accepted, oldComplete := modelTurnDispatch(bs, "old prompt", "Initial")
			if err := modelTurnReceive(t, accepted); err != nil {
				t.Fatal(err)
			}
			modelTurnExpectModel(t, proc, "initial").reply <- nil
			oldPrompt := modelTurnReceive(t, proc.prompts)
			modelTurnReceive(t, streaming)
			cancelled := make(chan error, 1)
			go func() { cancelled <- bs.Cancel() }()
			modelTurnExpectModel(t, proc, "default").reply <- nil
			if err := modelTurnReceive(t, cancelled); err != nil {
				t.Fatal(err)
			}
			modelTurnReceive(t, proc.cancels)
			modelTurnReceive(t, streaming)
			modelTurnReceive(t, terminal) // Stop finalizes the stream without waiting for the old response.
			accepted, newComplete := modelTurnDispatch(bs, "new prompt", "Manual")
			if err := modelTurnReceive(t, accepted); err != nil {
				t.Fatal(err)
			}
			modelTurnExpectModel(t, proc, "manual").reply <- nil
			newPrompt := modelTurnReceive(t, proc.prompts)
			modelTurnReceive(t, streaming)
			bs.promptMu.Lock()
			newTurn := bs.promptTurn
			bs.promptMu.Unlock()
			// The transport deliberately delays the old response until a new turn
			// is using a different override on the SAME ACP session.
			oldPrompt.reply <- oldErr
			if err := modelTurnReceive(t, oldComplete); !errors.Is(err, context.Canceled) {
				t.Fatalf("stale turn completion = %v, want cancellation", err)
			}
			bs.promptMu.Lock()
			unchanged := bs.promptTurn == newTurn && bs.isPrompting
			bs.promptMu.Unlock()
			if !unchanged || newPrompt.ctx.Err() != nil || bs.cmGetCurrentModelID() != "manual" {
				t.Fatal("stale completion changed the new turn's ownership, context or model")
			}
			modelTurnNoRPC(t, proc.models)
			select {
			case <-terminal:
				t.Fatal("stale completion finalized the new turn's stream")
			case <-streaming:
				t.Fatal("stale completion published idle for the new turn")
			default:
			}
			newPrompt.reply <- nil
			modelTurnExpectModel(t, proc, "default").reply <- nil
			if err := modelTurnReceive(t, newComplete); err != nil {
				t.Fatal(err)
			}
			modelTurnAssertIdle(t, bs, "default")
		})
	}
}

func TestPromptTurn_ManualSelectionDuringOverridePersistsBaseline(t *testing.T) {
	for _, selected := range []string{"manual", "initial"} {
		t.Run(selected, func(t *testing.T) {
			bs, proc := newModelTurnSession(t)
			store, err := session.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if err := store.Create(session.Metadata{SessionID: bs.persistedID, Name: "Model test", BaselineModel: "default"}); err != nil {
				t.Fatal(err)
			}
			bs.store = store
			accepted, complete := modelTurnDispatch(bs, "prompt", "Initial")
			if err := modelTurnReceive(t, accepted); err != nil {
				t.Fatal(err)
			}
			modelTurnExpectModel(t, proc, "initial").reply <- nil
			prompt := modelTurnReceive(t, proc.prompts)
			if selected == "initial" {
				// A tag matching the active override must supersede an older
				// pending dropdown choice, not merely skip the model RPC.
				if err := bs.SetConfigOption(context.Background(), ConfigOptionCategoryModel, "manual"); err != nil {
					t.Fatal(err)
				}
				bs.mittoConfig.Models[0].Tags = []string{"TestInitial"}
				if _, err := bs.ApplyModelTag(context.Background(), "TestInitial"); err != nil {
					t.Fatal(err)
				}
			} else if err := bs.SetConfigOption(context.Background(), ConfigOptionCategoryModel, selected); err != nil {
				t.Fatal(err)
			}
			if bs.GetBaselineModel() != selected || bs.cmGetCurrentModelID() != "initial" {
				t.Fatal("manual selection must change baseline without changing the running turn's agent model")
			}
			metadata, err := store.GetMetadata(bs.persistedID)
			if err != nil || metadata.BaselineModel != selected {
				t.Fatalf("manual baseline was not persisted during override: %q, %v", metadata.BaselineModel, err)
			}
			modelTurnNoRPC(t, proc.models)
			prompt.reply <- nil
			flush := modelTurnExpectModel(t, proc, selected) // never restore the superseded default
			if !bs.IsPrompting() {
				t.Fatal("pending manual choice flushed after releasing the prompt slot")
			}
			flush.reply <- nil
			if err := modelTurnReceive(t, complete); err != nil {
				t.Fatal(err)
			}
			modelTurnAssertIdle(t, bs, selected)
			metadata, err = store.GetMetadata(bs.persistedID)
			if err != nil || metadata.BaselineModel != selected {
				t.Fatalf("completion lost persisted manual model: %q, %v", metadata.BaselineModel, err)
			}
			modelTurnNoRPC(t, proc.models)
			// The next ordinary turn must inherit the choice with no override RPC.
			accepted, complete = modelTurnDispatch(bs, "next prompt", "")
			if err := modelTurnReceive(t, accepted); err != nil {
				t.Fatal(err)
			}
			modelTurnReceive(t, proc.prompts).reply <- nil
			if err := modelTurnReceive(t, complete); err != nil {
				t.Fatal(err)
			}
			modelTurnAssertIdle(t, bs, selected)
			modelTurnNoRPC(t, proc.models)
		})
	}
}

func TestPromptTurn_StartupRestoreIsNotPerTurnOverride(t *testing.T) {
	bs, proc := newModelTurnSession(t)
	// A resumed ACP session advertises its default, but the conversation already
	// owns a manual baseline. Exercise the real asynchronous startup callback.
	bs.cmSetBaselineAndClearOverride("manual")
	bs.setAgentModels(lifecycleModels())
	startup := modelTurnExpectModel(t, proc, "manual")
	if bs.startupConfigConstraintsReady() {
		t.Fatal("startup model RPC must keep the readiness barrier closed")
	}
	accepted, complete := modelTurnDispatch(bs, "{{ .Session.ModelName }}", "Initial")
	startup.reply <- nil
	preference := modelTurnExpectModel(t, proc, "initial")
	if !bs.startupConfigConstraintsReady() || bs.GetBaselineModel() != "manual" {
		t.Fatal("per-turn selection raced startup or replaced its baseline")
	}
	preference.reply <- nil
	if err := modelTurnReceive(t, accepted); err != nil {
		t.Fatal(err)
	}
	prompt := modelTurnReceive(t, proc.prompts)
	if len(prompt.blocks) != 1 || prompt.blocks[0].Text == nil || prompt.blocks[0].Text.Text != "Initial" {
		t.Fatalf("prompt rendered before its own model was confirmed: %+v", prompt.blocks)
	}
	prompt.reply <- nil
	modelTurnExpectModel(t, proc, "manual").reply <- nil
	if err := modelTurnReceive(t, complete); err != nil {
		t.Fatal(err)
	}
	modelTurnAssertIdle(t, bs, "manual")
	modelTurnNoRPC(t, proc.models)
}

func TestPromptTurn_RenderFailureRestoresBeforeRelease(t *testing.T) {
	bs, proc := newModelTurnSession(t)
	bs.SetPromptResolver(func(_, _ string) (string, error) { return "{{ if }}", nil })
	accepted, complete := make(chan error, 1), make(chan error, 1)
	go func() {
		accepted <- bs.PromptWithMeta("", PromptMeta{
			PromptName: "broken-template", PreferredModels: []config.PromptPreferredModel{{ModelName: "Initial"}},
			OnComplete: func(err error) { complete <- err },
		})
	}()
	modelTurnExpectModel(t, proc, "initial").reply <- nil
	restore := modelTurnExpectModel(t, proc, "default")
	if !bs.IsPrompting() {
		t.Fatal("render failure released its reservation before restore")
	}
	modelTurnNoRPC(t, proc.prompts)
	restore.reply <- nil
	if err := modelTurnReceive(t, accepted); err == nil {
		t.Fatal("invalid named template must fail synchronously")
	}
	modelTurnAssertIdle(t, bs, "default")
	bs.promptMu.Lock()
	count, first := bs.promptCount, bs.isFirstPrompt
	bs.promptMu.Unlock()
	if count != 0 || !first {
		t.Fatalf("failed render consumed first-prompt state: count=%d first=%v", count, first)
	}
	select {
	case <-complete:
		t.Fatal("synchronously rejected prompt invoked asynchronous OnComplete")
	default:
	}
	modelTurnNoRPC(t, proc.models)
	modelTurnNoRPC(t, proc.prompts)
}
