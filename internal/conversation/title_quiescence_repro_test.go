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
	busy  atomic.Bool
	calls atomic.Int32
}

func (p *quiescenceTitleProvider) PromptAuxiliary(_ context.Context, _, _, _ string) (string, error) {
	p.calls.Add(1)
	if p.busy.Load() {
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
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if p.calls.Load() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d title attempts; got %d", want, p.calls.Load())
}

// TestGenerateAndSetTitle_BusyAttemptsRecoverAfterQuiescence reproduces mitto-juzb.
// An initial attempt and the prompt-completion recovery both encounter transient
// shared-process load shedding. Once that load clears, one retained pending job
// must upgrade the fallback without requiring another user prompt.
func TestGenerateAndSetTitle_BusyAttemptsRecoverAfterQuiescence(t *testing.T) {
	origMax, origDelay := titleMaxRetries, titleRetryBaseDelay
	titleMaxRetries = 3
	titleRetryBaseDelay = 10 * time.Millisecond
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

	provider := &quiescenceTitleProvider{}
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
	waitForTitleAttempts(t, provider, 2)

	provider.busy.Store(false) // shared process becomes quiescent; no new prompt arrives
	select {
	case <-completed:
	case <-time.After(750 * time.Millisecond):
		t.Fatalf("fallback was not upgraded after quiescence: attempts=%d callbacks=%d",
			provider.calls.Load(), callbacks.Load())
	}
	time.Sleep(100 * time.Millisecond) // catch duplicate pending jobs

	if got := provider.calls.Load(); got != 3 {
		t.Fatalf("expected two busy attempts plus one coalesced recovery; got %d attempts", got)
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
