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

type quiescenceTitleProvider struct {
	busy      atomic.Bool
	calls     atomic.Int32
	attempted chan int32
}

func (p *quiescenceTitleProvider) PromptAuxiliary(_ context.Context, _, _, _ string) (string, error) {
	call := p.calls.Add(1)
	busy := p.busy.Load()
	p.attempted <- call
	if busy {
		return "", acperrors.ErrProcessBusy
	}
	return "Recovered Final Title", nil
}

func (*quiescenceTitleProvider) PromptAuxiliaryAsync(context.Context, string, string, string) error {
	return nil
}

func (*quiescenceTitleProvider) CloseWorkspaceAuxiliary(string) error { return nil }

func waitForTitleAttempts(t *testing.T, p *quiescenceTitleProvider, want int32) {
	t.Helper()
	deadline := time.NewTimer(500 * time.Millisecond)
	defer deadline.Stop()
	for {
		select {
		case got := <-p.attempted:
			if got >= want {
				return
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for %d title attempts; got %d", want, p.calls.Load())
			return
		}
	}
}

// TestGenerateAndSetTitle_BusyAttemptsRecoverAfterQuiescence reproduces mitto-juzb.
// An initial attempt and the prompt-completion recovery both encounter transient
// shared-process load shedding. Once that load clears, one retained pending job
// must upgrade the fallback without requiring another user prompt.
func TestGenerateAndSetTitle_BusyAttemptsRecoverAfterQuiescence(t *testing.T) {
	origMax, origDelay := titleMaxRetries, titleRetryBaseDelay
	titleMaxRetries = 3
	titleRetryBaseDelay = time.Millisecond
	t.Cleanup(func() {
		titleMaxRetries = origMax
		titleRetryBaseDelay = origDelay
	})

	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const sessionID = "mitto-juzb-repro"
	if err := store.Create(session.Metadata{
		SessionID: sessionID, ACPServer: "test", WorkingDir: t.TempDir(),
		Name: "Quick fallback title", NameIsFallback: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	provider := &quiescenceTitleProvider{attempted: make(chan int32, titleMaxRetries+2)}
	provider.busy.Store(true)
	mgr := auxiliary.NewWorkspaceAuxiliaryManager(provider, nil)
	completed := make(chan struct{}, 2)
	var callbacks atomic.Int32
	cfg := TitleGenerationConfig{
		Store: store, SessionID: sessionID, Message: "Explain the title recovery bug",
		WorkspaceUUID: "test-workspace", AuxiliaryManager: mgr,
		OnTitleGenerated: func(string, string) {
			callbacks.Add(1)
			completed <- struct{}{}
		},
	}

	GenerateAndSetTitle(cfg) // initial title attempt
	waitForTitleAttempts(t, provider, 1)
	GenerateAndSetTitle(cfg) // prompt-completion recovery attempt
	waitForTitleAttempts(t, provider, int32(titleMaxRetries+1))

	provider.busy.Store(false) // quiescence arrives after the bounded retries; no new prompt arrives
	select {
	case <-completed:
	case <-time.After(750 * time.Millisecond):
		t.Fatalf("fallback was not upgraded after quiescence: attempts=%d callbacks=%d",
			provider.calls.Load(), callbacks.Load())
	}
	time.Sleep(100 * time.Millisecond) // catch duplicate pending jobs

	if got := provider.calls.Load(); got != int32(titleMaxRetries+2) {
		t.Fatalf("expected bounded busy attempts plus one pending recovery; got %d attempts", got)
	}
	if got := callbacks.Load(); got != 1 {
		t.Fatalf("expected one final-title callback, got %d", got)
	}
	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if meta.Name != "Recovered Final Title" || meta.NameIsFallback {
		t.Fatalf("fallback not upgraded: name=%q name_is_fallback=%v", meta.Name, meta.NameIsFallback)
	}
}
