package stats

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNoopStore_UpsertDeltas(t *testing.T) {
	store := &NoopStore{}
	if err := store.UpsertDeltas(context.Background(), []Delta{{Metric: MetricPrompts, Value: 1}}); err != nil {
		t.Errorf("NoopStore.UpsertDeltas() error = %v, want nil", err)
	}
}

func TestNoopStore_ReplaceDeltas(t *testing.T) {
	store := &NoopStore{}
	from := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 4, 1, 1, 0, 0, 0, time.UTC)
	if err := store.ReplaceDeltas(context.Background(), []string{MetricBeadsOpened}, from, to, nil); err != nil {
		t.Errorf("NoopStore.ReplaceDeltas() error = %v, want nil", err)
	}
}

func TestNoopStore_GetCursor(t *testing.T) {
	store := &NoopStore{}
	cur, err := store.GetCursor(context.Background(), "sess-1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("NoopStore.GetCursor() error = %v, want ErrNotFound", err)
	}
	if cur.SessionID != "sess-1" {
		t.Errorf("NoopStore.GetCursor() SessionID = %q, want %q", cur.SessionID, "sess-1")
	}
	if cur.LastEventSeq != 0 || !cur.LastEventAt.IsZero() {
		t.Errorf("NoopStore.GetCursor() returned non-zero cursor fields: %+v", cur)
	}
}

func TestNoopStore_SetCursor(t *testing.T) {
	store := &NoopStore{}
	err := store.SetCursor(context.Background(), Cursor{SessionID: "sess-1", LastEventSeq: 42})
	if err != nil {
		t.Errorf("NoopStore.SetCursor() error = %v, want nil", err)
	}
}

func TestNoopStore_Query(t *testing.T) {
	store := &NoopStore{}
	points, err := store.Query(context.Background(), Query{Bucket: BucketHour})
	if err != nil {
		t.Errorf("NoopStore.Query() error = %v, want nil", err)
	}
	if len(points) != 0 {
		t.Errorf("NoopStore.Query() returned %d points, want 0", len(points))
	}
}

func TestNoopStore_Prune(t *testing.T) {
	store := &NoopStore{}
	rows, err := store.Prune(context.Background(), time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Errorf("NoopStore.Prune() error = %v, want nil", err)
	}
	if rows != 0 {
		t.Errorf("NoopStore.Prune() rows = %d, want 0", rows)
	}
}

func TestNoopStore_Close(t *testing.T) {
	store := &NoopStore{}
	if err := store.Close(); err != nil {
		t.Errorf("NoopStore.Close() error = %v, want nil", err)
	}

	// Every method must return ErrClosed after Close.
	if err := store.UpsertDeltas(context.Background(), nil); !errors.Is(err, ErrClosed) {
		t.Errorf("UpsertDeltas after Close: error = %v, want ErrClosed", err)
	}
	if _, err := store.GetCursor(context.Background(), "sess"); !errors.Is(err, ErrClosed) {
		t.Errorf("GetCursor after Close: error = %v, want ErrClosed", err)
	}
	if err := store.SetCursor(context.Background(), Cursor{SessionID: "sess"}); !errors.Is(err, ErrClosed) {
		t.Errorf("SetCursor after Close: error = %v, want ErrClosed", err)
	}
	if _, err := store.Query(context.Background(), Query{}); !errors.Is(err, ErrClosed) {
		t.Errorf("Query after Close: error = %v, want ErrClosed", err)
	}
	if _, err := store.Prune(context.Background(), time.Now()); !errors.Is(err, ErrClosed) {
		t.Errorf("Prune after Close: error = %v, want ErrClosed", err)
	}
	if err := store.ReplaceDeltas(context.Background(), []string{MetricBeadsOpened}, time.Now(), time.Now().Add(time.Hour), nil); !errors.Is(err, ErrClosed) {
		t.Errorf("ReplaceDeltas after Close: error = %v, want ErrClosed", err)
	}

	// Close is idempotent.
	if err := store.Close(); err != nil {
		t.Errorf("second Close() error = %v, want nil", err)
	}
}

// TestConstants pins the exported metric-name string values. Any accidental
// rename here would break the API JSON schema and the chart legend, so it
// must break a test rather than a downstream consumer.
func TestConstants(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"MetricInputTokensEst", MetricInputTokensEst, "input_tokens_est"},
		{"MetricOutputTokensEst", MetricOutputTokensEst, "output_tokens_est"},
		{"MetricPrompts", MetricPrompts, "prompts"},
		{"MetricAgentTurnsCompleted", MetricAgentTurnsCompleted, "agent_turns_completed"},
		{"MetricToolCallsTotal", MetricToolCallsTotal, "tool_calls_total"},
		{"MetricMCPCalls", MetricMCPCalls, "mcp_calls"},
		{"MetricPermissionsPrompted", MetricPermissionsPrompted, "permissions_prompted"},
		{"MetricErrors", MetricErrors, "errors"},
		{"MetricBeadsOpened", MetricBeadsOpened, "beads_opened"},
		{"MetricBeadsClosed", MetricBeadsClosed, "beads_closed"},
		{"MetricBeadsCycleSecondsSum", MetricBeadsCycleSecondsSum, "beads_cycle_seconds_sum"},
		{"MetricBeadsCycleClosedCount", MetricBeadsCycleClosedCount, "beads_cycle_closed_count"},
		{"BeadsSentinelSessionID", BeadsSentinelSessionID, "__beads__"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}

	if BucketHour != "hour" {
		t.Errorf("BucketHour = %q, want %q", BucketHour, "hour")
	}
	if BucketDay != "day" {
		t.Errorf("BucketDay = %q, want %q", BucketDay, "day")
	}
	if EstimatorVersion != 1 {
		t.Errorf("EstimatorVersion = %d, want 1", EstimatorVersion)
	}
}

// TestNoopStore_ConcurrentAccess exercises the atomic closed flag under
// contention. The package doc claims NoopStore is safe for concurrent use, so
// interleaving reads/writes with Close must never race (verified with -race)
// and post-Close callers must all observe ErrClosed.
func TestNoopStore_ConcurrentAccess(t *testing.T) {
	store := &NoopStore{}
	const writers = 8
	const iterations = 200

	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			ctx := context.Background()
			for j := 0; j < iterations; j++ {
				_ = store.UpsertDeltas(ctx, []Delta{{Metric: MetricPrompts, Value: 1}})
				_, _ = store.GetCursor(ctx, "sess")
				_ = store.SetCursor(ctx, Cursor{SessionID: "sess"})
				_, _ = store.Query(ctx, Query{Bucket: BucketHour})
			}
		}()
	}

	// Close concurrently with the writers. All writers must terminate cleanly
	// (either seeing no error or ErrClosed once the flag flips).
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}
	wg.Wait()

	// After all writers finished, every method must consistently report ErrClosed.
	if err := store.UpsertDeltas(context.Background(), nil); !errors.Is(err, ErrClosed) {
		t.Errorf("post-concurrent UpsertDeltas: error = %v, want ErrClosed", err)
	}
}

// TestPackageDependencies_NoWebOrConversation enforces the acceptance-criteria
// invariant that internal/stats has no dependency on internal/web or
// internal/conversation. Uses `go list -deps -json` so the check catches
// transitive imports, not just direct ones.
//
// Skipped under `-short` (the test shells out to the Go toolchain and takes a
// couple hundred milliseconds).
func TestPackageDependencies_NoWebOrConversation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping dependency scan in short mode")
	}

	cmd := exec.Command("go", "list", "-deps", "-json", ".")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list failed: %v\nstderr: %s", err, ee.Stderr)
		}
		t.Fatalf("go list failed: %v", err)
	}

	// `go list -deps -json` emits a stream of JSON objects (not a JSON array),
	// so decode iteratively.
	dec := json.NewDecoder(strings.NewReader(string(out)))
	forbidden := []string{
		"github.com/inercia/mitto/internal/web",
		"github.com/inercia/mitto/internal/conversation",
	}
	sawSelf := false
	for dec.More() {
		var pkg struct {
			ImportPath string
			Imports    []string
		}
		if err := dec.Decode(&pkg); err != nil {
			t.Fatalf("decoding go list output: %v", err)
		}
		if pkg.ImportPath == "github.com/inercia/mitto/internal/stats" {
			sawSelf = true
		}
		for _, bad := range forbidden {
			if pkg.ImportPath == bad || strings.HasPrefix(pkg.ImportPath, bad+"/") {
				t.Errorf("internal/stats transitively depends on %s (via %s); the package must remain independent of the web/conversation layers", bad, pkg.ImportPath)
			}
			for _, imp := range pkg.Imports {
				if imp == bad || strings.HasPrefix(imp, bad+"/") {
					t.Errorf("package %s imports %s; internal/stats must not reach it transitively", pkg.ImportPath, imp)
				}
			}
		}
	}
	if !sawSelf {
		t.Fatalf("go list -deps did not include internal/stats itself in its output")
	}
}
