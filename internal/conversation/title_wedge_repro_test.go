package conversation

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/session"
)

// wedgeProvider is a minimal auxiliary.ProcessProvider that returns the auggie
// session/new wedge signature on every call and counts invocations. Used only
// by the mitto-ammz.1 reproduction test below.
type wedgeProvider struct {
	calls atomic.Int32
}

func (p *wedgeProvider) PromptAuxiliary(_ context.Context, _, _, _ string) (string, error) {
	p.calls.Add(1)
	// Exact wedge signature captured in the bead: JSON-RPC -32603 whose data
	// carries "context deadline exceeded". This is what acpproc's
	// isAgentInternalDeadlineErr matches (internal/acpproc/shared_acp_process.go:1448).
	wedge := acp.NewInternalError(map[string]any{"error": "context deadline exceeded"})
	// The real path wraps the RPC error the way getOrCreateAuxiliarySession does.
	return "", fmt.Errorf("failed to create session: %w", wedge)
}

func (p *wedgeProvider) PromptAuxiliaryAsync(_ context.Context, _, _, _ string) error {
	return nil
}

func (p *wedgeProvider) CloseWorkspaceAuxiliary(_ string) error {
	return nil
}

// TestGenerateAndSetTitle_WedgeSignal_BoundsRetries is the reproduction test
// for mitto-ammz.1 (Aux-session retry storm).
//
// The bug: the title-generation retry loop is failure-agnostic — it treats the
// auggie agent-internal-deadline wedge (-32603 "context deadline exceeded",
// agent_internal_deadline=true) the same as any transient error and fires
// titleMaxRetries+1 = 4 total attempts. Each attempt burns the full 60s
// extended-MCP budget with near-zero chance of success (the wedge is a wedged
// process, not a transient), producing the storms observed in the bead.
//
// Acceptance criterion #1 from the bead: "No aux-session retry storm exceeds
// 2 attempts against the same agent_internal_deadline=true failure mode."
//
// Expected behavior (post-fix): on the wedge signal, the retry loop stops
// after AT MOST 2 attempts (classify-and-abandon: the next quiescence will
// re-attempt via the normal auto-title path).
//
// Current behavior (this test asserts it fails today): the loop fires 4 total
// attempts, so we assert calls <= 2 and this fails with calls == 4.
//
// The test overrides titleMaxRetries and titleRetryBaseDelay so the entire
// storm completes in ~1ms of wall-clock rather than the production 7½ minutes.
// The failure-mode assertion is invariant to that cadence override — what
// changes across the fix is WHICH attempts the loop makes, not HOW FAST it
// makes them.
func TestGenerateAndSetTitle_WedgeSignal_BoundsRetries(t *testing.T) {
	// Test-seam: shrink the retry cadence so the whole storm fits in the
	// test wall-clock. Restore on exit so parallel tests are not affected.
	origMax, origDelay := titleMaxRetries, titleRetryBaseDelay
	titleMaxRetries = 3 // 4 total attempts, matching production
	titleRetryBaseDelay = time.Microsecond
	t.Cleanup(func() {
		titleMaxRetries = origMax
		titleRetryBaseDelay = origDelay
	})

	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	const sessionID = "wedge-repro-session"
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
		Name:       "", // empty so the auxiliary call is exercised
	}); err != nil {
		t.Fatalf("Create session: %v", err)
	}

	provider := &wedgeProvider{}
	mgr := auxiliary.NewWorkspaceAuxiliaryManager(provider, nil)

	done := make(chan string, 1)
	GenerateAndSetTitle(TitleGenerationConfig{
		Store:            store,
		SessionID:        sessionID,
		Message:          "How do I fix the aux-session retry storm on cgw-managed-tools?",
		WorkspaceUUID:    "test-workspace-uuid",
		AuxiliaryManager: mgr,
		OnTitleGenerated: func(_, title string) {
			select {
			case done <- title:
			default:
			}
		},
	})

	// The quick-title path fires OnTitleGenerated synchronously before the
	// async goroutine starts. Drain that callback so we can wait purely on
	// the retry loop finishing (which fires no further callbacks because
	// every attempt errors out).
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		// No quick title (message may have been rejected by GenerateQuickTitle);
		// that's fine, keep going.
	}

	// Wait until the goroutine has exhausted its retry budget. With
	// titleRetryBaseDelay = 1µs the whole 4-attempt storm completes in
	// well under a millisecond of real sleeping. Poll for stability.
	deadline := time.Now().Add(3 * time.Second)
	var stable int
	var lastCalls int32
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		c := provider.calls.Load()
		if c == lastCalls {
			stable++
			if stable >= 3 {
				break
			}
		} else {
			stable = 0
			lastCalls = c
		}
	}

	got := provider.calls.Load()
	const maxAcceptable = int32(2)
	if got > maxAcceptable {
		t.Fatalf("mitto-ammz.1: retry storm reproduced — GenerateAndSetTitle made %d wedge-signal attempts, want <=%d "+
			"(the loop must classify agent_internal_deadline errors and abandon after at most 2 attempts). "+
			"Every extra attempt burns the full 60s extended-MCP budget in production.", got, maxAcceptable)
	}
	t.Logf("wedge-signal attempts: %d (bound: <=%d)", got, maxAcceptable)
}
