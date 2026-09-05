package conversation

import (
	"context"
	"sync"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// Inspect admission before promptMu is released, not merely after the setter
// returns: a release/flush can run immediately at that unlock boundary.
type pendingModelAdmissionDeps struct {
	*fakeConfigDeps
	baselineAtUnlock string
	pendingAtUnlock  string
	configAtUnlock   string
}

func (d *pendingModelAdmissionDeps) cmUnlockPromptMu() {
	d.baselineAtUnlock = d.cmGetBaselineModel()
	d.cmLockPendingConfig()
	d.pendingAtUnlock = d.pendingConfig[ConfigOptionCategoryModel]
	d.cmUnlockPendingConfig()
	opt, _ := d.cmFindByID(ConfigOptionCategoryModel)
	d.configAtUnlock = opt.CurrentValue
	d.fakeConfigDeps.cmUnlockPromptMu()
}

func TestConfigManager_DeferredModelSelectionPublishesBeforeUnlock(t *testing.T) {
	d := &pendingModelAdmissionDeps{fakeConfigDeps: newFakeConfigDeps()}
	d.isPrompting = true
	c := configManager{}
	if err := c.setConfigOption(d, context.Background(), ConfigOptionCategoryModel, "m-2"); err != nil {
		t.Fatal(err)
	}
	if d.baselineAtUnlock != "m-2" || d.pendingAtUnlock != "m-2" || d.configAtUnlock != "m-2" {
		t.Fatalf("incomplete admission at prompt unlock: baseline=%q pending=%q config=%q",
			d.baselineAtUnlock, d.pendingAtUnlock, d.configAtUnlock)
	}
}

func newPendingModelSession(t *testing.T) (*BackgroundSession, *modelTurnProcess) {
	t.Helper()
	bs, proc := newModelTurnSession(t)
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Create(session.Metadata{SessionID: bs.persistedID, BaselineModel: "default"}); err != nil {
		t.Fatal(err)
	}
	bs.store = store
	return bs, proc
}

func assertPendingModelBaseline(t *testing.T, bs *BackgroundSession, want string) {
	t.Helper()
	if got := bs.GetBaselineModel(); got != want {
		t.Fatalf("baseline = %q, want latest selection %q", got, want)
	}
	metadata, err := bs.store.GetMetadata(bs.persistedID)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.BaselineModel != want {
		t.Fatalf("persisted baseline = %q, want latest selection %q", metadata.BaselineModel, want)
	}
}

type gatedPendingModelPersistence struct {
	*BackgroundSession
	started chan struct{}
	release chan struct{}
}

func (d *gatedPendingModelPersistence) cmPersistBaselineModel(value string) {
	if value == "initial" {
		close(d.started)
		select {
		case <-d.release:
		case <-d.ctx.Done():
			return
		}
	}
	d.BackgroundSession.cmPersistBaselineModel(value)
}

func TestConfigManager_DeferredModelPersistenceDoesNotRegressNewSelection(t *testing.T) {
	bs, proc := newPendingModelSession(t)
	bs.isPrompting = true
	d := &gatedPendingModelPersistence{
		BackgroundSession: bs, started: make(chan struct{}), release: make(chan struct{}),
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(d.release) }) }
	t.Cleanup(release)
	c := configManager{}
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- c.setConfigOption(d, context.Background(), ConfigOptionCategoryModel, "initial")
	}()
	modelTurnReceive(t, d.started)
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- bs.SetConfigOption(context.Background(), ConfigOptionCategoryModel, "manual")
	}()
	// The first setter must not hold promptMu across the gated persistence.
	if err := modelTurnReceive(t, secondDone); err != nil {
		t.Fatal(err)
	}
	assertPendingModelBaseline(t, bs, "manual")
	release()
	if err := modelTurnReceive(t, firstDone); err != nil {
		t.Fatal(err)
	}
	assertPendingModelBaseline(t, bs, "manual")
	bs.promptMu.Lock()
	hasPending := bs.hasPendingConfigLocked()
	bs.promptMu.Unlock()
	if !hasPending || bs.GetConfigValue(ConfigOptionCategoryModel) != "manual" {
		t.Fatal("delayed persistence replaced the pending selection")
	}
	modelTurnNoRPC(t, proc.models)
}

func TestPromptTurn_PendingModelSelectionDuringFlushDrainsBeforeIdle(t *testing.T) {
	for _, boundary := range []string{"model_rpc", "before_idle"} {
		t.Run(boundary, func(t *testing.T) {
			bs, proc := newPendingModelSession(t)
			beforeIdle, resumeIdle := make(chan struct{}), make(chan struct{})
			var resumeOnce sync.Once
			resume := func() { resumeOnce.Do(func() { close(resumeIdle) }) }
			t.Cleanup(resume)
			if boundary == "before_idle" {
				var notifyOnce sync.Once
				bs.onStreamingStateChanged = func(_ string, active bool) {
					if !active {
						notifyOnce.Do(func() {
							close(beforeIdle)
							select {
							case <-resumeIdle:
							case <-proc.stop:
							}
						})
					}
				}
			}
			accepted, complete := modelTurnDispatch(bs, "prompt", "")
			if err := modelTurnReceive(t, accepted); err != nil {
				t.Fatal(err)
			}
			prompt := modelTurnReceive(t, proc.prompts)
			if err := bs.SetConfigOption(context.Background(), ConfigOptionCategoryModel, "initial"); err != nil {
				t.Fatal(err)
			}
			assertPendingModelBaseline(t, bs, "initial")
			prompt.reply <- nil
			first := modelTurnExpectModel(t, proc, "initial")
			if boundary == "before_idle" {
				first.reply <- nil
				modelTurnReceive(t, beforeIdle)
			}
			secondDone := make(chan error, 1)
			go func() {
				secondDone <- bs.SetConfigOption(context.Background(), ConfigOptionCategoryModel, "manual")
			}()
			if err := modelTurnReceive(t, secondDone); err != nil {
				t.Fatal(err)
			}
			assertPendingModelBaseline(t, bs, "manual")
			if boundary == "model_rpc" {
				first.reply <- nil
			} else {
				resume()
			}
			second := modelTurnExpectModel(t, proc, "manual")
			// A has completed, but B is still blocked. The older completion must
			// not overwrite B's memory/disk baseline or open prompt admission.
			assertPendingModelBaseline(t, bs, "manual")
			if !bs.IsPrompting() || bs.cmGetCurrentModelID() != "initial" {
				t.Fatal("turn became idle before applying the latest pending model")
			}
			if err := bs.PromptWithMeta("must not be admitted", PromptMeta{}); err == nil {
				t.Fatal("new turn admitted while pending model RPC was in flight")
			}
			modelTurnNoRPC(t, proc.prompts)
			second.reply <- nil
			if err := modelTurnReceive(t, complete); err != nil {
				t.Fatal(err)
			}
			modelTurnAssertIdle(t, bs, "manual")
			assertPendingModelBaseline(t, bs, "manual")
			bs.promptMu.Lock()
			hasPending := bs.hasPendingConfigLocked()
			bs.promptMu.Unlock()
			if hasPending {
				t.Fatal("turn became idle with a stranded pending selection")
			}
			modelTurnNoRPC(t, proc.models)
		})
	}
}
