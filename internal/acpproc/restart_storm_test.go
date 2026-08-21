package acpproc

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// findMockACPServerBinaryForRestartTest walks upward from the current working
// directory looking for the built mock ACP server binary, mirroring
// tests/integration/inprocess/setup_test.go's findRepoFile helper. Kept
// package-local since internal/acpproc must not import the integration test
// package. Skips the test (with a build hint) if the binary is missing —
// build it first with `make build-mock-acp`.
func findMockACPServerBinaryForRestartTest(t *testing.T) string {
	t.Helper()

	relPath := filepath.Join("tests", "mocks", "acp-server", "mock-acp-server")
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error: %v", err)
	}

	for {
		candidate := filepath.Join(dir, relPath)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("mock-acp-server not found. Run 'make build-mock-acp' first")
		}
		dir = parent
	}
}

// TestRestart_ConcurrentCallersEachKillTheHealthyReplacement is the mitto-x611
// regression test.
//
// Root cause (pre-fix): SharedACPProcess.Restart() had no idempotency guard
// against concurrent callers that all observed the same underlying OS process
// death. It only called canRestart(), which is a sliding-window RATE LIMIT
// (MaxACPRestarts=3 per ACPRestartWindow=5m, internal/acp/errors.go), not a
// dedup — it permitted every concurrent caller through as long as the limit
// wasn't exhausted. Each caller then unconditionally killed whatever process
// p.cmd currently pointed at (killProcess()) and started a new one, even if
// that process was the healthy replacement another caller just started a
// moment ago. This was the "restart storm" reported in mitto-x611 (3 SIGKILLs
// in 17s when N sessions sharing one process each independently detect its
// death and call Restart()).
//
// Fix: Restart() now takes an observedGen parameter (see Generation()).
// Production callers (internal/conversation/bgsession_acp_process.go,
// background_session.go) snapshot Generation() as early as possible — before
// their own backoff/rate-limit delay — so that every caller reacting to the
// SAME death agrees on the generation they intend to replace. Restart()
// re-checks the generation under its process mutex immediately before
// killing; if another caller already bumped it (i.e. already remediated this
// death), the call no-ops instead of tearing down the healthy replacement.
//
// This test mirrors that calling convention directly: it snapshots
// Generation() once (as the two production call sites do) and fires two
// goroutines that both call Restart(observedGen) concurrently off a shared
// start barrier. Only one should actually execute a kill+start cycle — i.e.
// the process's internal restart counter must be 1, not 2, after both calls
// return.
func TestRestart_ConcurrentCallersEachKillTheHealthyReplacement(t *testing.T) {
	mockCmd := findMockACPServerBinaryForRestartTest(t)

	p, err := NewSharedACPProcess(context.Background(), SharedACPProcessConfig{
		ACPCommand: mockCmd,
		ACPServer:  "mock-acp",
	})
	if err != nil {
		t.Fatalf("NewSharedACPProcess() error = %v", err)
	}
	defer p.Close()

	// Both sessions detected the SAME process death, so both snapshot the
	// SAME generation before calling Restart() — exactly as
	// bgsession_acp_process.go:restartACPProcess and
	// background_session.go:ResumeBackgroundSession do today.
	observedGen := p.Generation()

	const callers = 2
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = p.Restart(observedGen)
		}()
	}
	close(start)
	wg.Wait()

	for i, callErr := range errs {
		if callErr != nil {
			t.Fatalf("Restart() call %d returned unexpected error: %v", i, callErr)
		}
	}

	p.restartMu.Lock()
	restartCount := p.restartCount
	p.restartMu.Unlock()

	if restartCount != 1 {
		t.Errorf("mitto-x611: expected exactly 1 actual restart when %d sessions concurrently "+
			"detect the same process death, got restartCount=%d — each concurrent Restart() call "+
			"unconditionally kills whatever process is currently running (even a healthy replacement "+
			"just started by another caller), producing the reported restart storm",
			callers, restartCount)
	}
}
