package stats

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// -----------------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------------

// fakeStore captures every UpsertDeltasWithCursor call. Safe for concurrent use.
type fakeStore struct {
	NoopStore
	mu      sync.Mutex
	calls   []fakeCall
	failN   int   // fail the first N calls with errFakeFlush
	failErr error // custom error if set; defaults to errFakeFlush
}

type fakeCall struct {
	deltas []Delta
	cursor Cursor
}

var errFakeFlush = errors.New("fake flush failure")

func (f *fakeStore) UpsertDeltasWithCursor(_ context.Context, deltas []Delta, cur Cursor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failN > 0 {
		f.failN--
		e := f.failErr
		if e == nil {
			e = errFakeFlush
		}
		return e
	}
	// Copy slice so post-call buffer mutations don't affect the recorded call.
	cp := make([]Delta, len(deltas))
	copy(cp, deltas)
	f.calls = append(f.calls, fakeCall{deltas: cp, cursor: cur})
	return nil
}

func (f *fakeStore) snapshot() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// sumFor returns the total value across all recorded calls for a given
// (metric, session) pair — bucket-agnostic. Handy for asserting counter totals
// without caring which flush a delta ended up in.
func (f *fakeStore) sumFor(t *testing.T, metric, sessionID string) int64 {
	t.Helper()
	var total int64
	for _, c := range f.snapshot() {
		for _, d := range c.deltas {
			if d.Metric == metric && d.SessionID == sessionID {
				total += d.Value
			}
		}
	}
	return total
}

// mustFlush calls Flush and fails the test if it returns an error.
func mustFlush(t *testing.T, a Aggregator) {
	t.Helper()
	if err := a.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}

// evAt builds a session.Event with the given timestamp / seq / type / data.
func evAt(seq int64, ts time.Time, typ session.EventType, data any) session.Event {
	return session.Event{Seq: seq, Type: typ, Timestamp: ts, Data: data}
}

// sc is a tiny SessionContext factory.
func sc(sessionID, workspace string) SessionContext {
	return SessionContext{SessionID: sessionID, Workspace: workspace}
}

// hour returns a UTC hour-truncated timestamp for reproducible bucket keys.
func hour(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse ts %q: %v", s, err)
	}
	return ts.UTC()
}

// -----------------------------------------------------------------------------
// Delta math per event type
// -----------------------------------------------------------------------------

func TestAggregator_UserPrompt_CountsAndInputTokens(t *testing.T) {
	fs := &fakeStore{}
	a := NewAggregator(fs, AggregatorOptions{FlushInterval: time.Hour, MaxBatch: 1_000_000})
	defer a.Close()

	ts := hour(t, "2026-01-01T00:00:00Z")
	msg := "hello world" // 11 chars → (11+3)/4 = 3 tokens
	a.Ingest(sc("s1", "w1"), evAt(1, ts, session.EventTypeUserPrompt, session.UserPromptData{Message: msg}))
	mustFlush(t, a)

	if got := fs.sumFor(t, MetricPrompts, "s1"); got != 1 {
		t.Errorf("prompts = %d, want 1", got)
	}
	if got := fs.sumFor(t, MetricInputTokensEst, "s1"); got != 3 {
		t.Errorf("input_tokens_est = %d, want 3", got)
	}
}

func TestAggregator_UserPrompt_ImagesAndFilesTokens(t *testing.T) {
	fs := &fakeStore{}
	a := NewAggregator(fs, AggregatorOptions{FlushInterval: time.Hour, MaxBatch: 1_000_000})
	defer a.Close()

	ts := hour(t, "2026-01-01T00:00:00Z")
	data := session.UserPromptData{
		Message: "",
		Images:  []session.ImageRef{{ID: "i1"}, {ID: "i2"}},
		Files:   []session.FileRef{{Name: "a.txt"}},
	}
	a.Ingest(sc("s1", "w1"), evAt(1, ts, session.EventTypeUserPrompt, data))
	mustFlush(t, a)

	// 2 images * 64 = 128, plus file cost: len("a.txt")+16=21 → (21+3)/4=6
	want := int64(128 + 6)
	if got := fs.sumFor(t, MetricInputTokensEst, "s1"); got != want {
		t.Errorf("input_tokens_est = %d, want %d", got, want)
	}
}

func TestAggregator_AgentMessageAndThought_OutputTokens(t *testing.T) {
	fs := &fakeStore{}
	a := NewAggregator(fs, AggregatorOptions{FlushInterval: time.Hour, MaxBatch: 1_000_000})
	defer a.Close()

	ts := hour(t, "2026-01-01T00:00:00Z")
	a.Ingest(sc("s1", "w1"), evAt(1, ts, session.EventTypeAgentMessage, session.AgentMessageData{Text: "abcd"}))     // 4 → 1
	a.Ingest(sc("s1", "w1"), evAt(2, ts, session.EventTypeAgentThought, session.AgentThoughtData{Text: "abcdefgh"})) // 8 → 2
	mustFlush(t, a)

	if got := fs.sumFor(t, MetricOutputTokensEst, "s1"); got != 3 {
		t.Errorf("output_tokens_est = %d, want 3", got)
	}
}

func TestAggregator_ToolCall_TotalAndMCP(t *testing.T) {
	fs := &fakeStore{}
	a := NewAggregator(fs, AggregatorOptions{FlushInterval: time.Hour, MaxBatch: 1_000_000})
	defer a.Close()

	ts := hour(t, "2026-01-01T00:00:00Z")
	a.Ingest(sc("s1", "w1"), evAt(1, ts, session.EventTypeToolCall, session.ToolCallData{Title: "read_file"}))              // non-MCP
	a.Ingest(sc("s1", "w1"), evAt(2, ts, session.EventTypeToolCall, session.ToolCallData{Title: "mitto_conversation_new"})) // MCP via title
	a.Ingest(sc("s1", "w1"), evAt(3, ts, session.EventTypeToolCall, session.ToolCallData{Title: "whatever", Kind: "mcp"}))  // MCP via Kind
	mustFlush(t, a)

	if got := fs.sumFor(t, MetricToolCallsTotal, "s1"); got != 3 {
		t.Errorf("tool_calls_total = %d, want 3", got)
	}
	if got := fs.sumFor(t, MetricMCPCalls, "s1"); got != 2 {
		t.Errorf("mcp_calls = %d, want 2", got)
	}
}

func TestAggregator_PermissionAndError(t *testing.T) {
	fs := &fakeStore{}
	a := NewAggregator(fs, AggregatorOptions{FlushInterval: time.Hour, MaxBatch: 1_000_000})
	defer a.Close()

	ts := hour(t, "2026-01-01T00:00:00Z")
	a.Ingest(sc("s1", "w1"), evAt(1, ts, session.EventTypePermission, session.PermissionData{Title: "read?"}))
	a.Ingest(sc("s1", "w1"), evAt(2, ts, session.EventTypeError, session.ErrorData{Message: "boom"}))
	mustFlush(t, a)

	if got := fs.sumFor(t, MetricPermissionsPrompted, "s1"); got != 1 {
		t.Errorf("permissions_prompted = %d, want 1", got)
	}
	if got := fs.sumFor(t, MetricErrors, "s1"); got != 1 {
		t.Errorf("errors = %d, want 1", got)
	}
}

func TestAggregator_TurnCompletion(t *testing.T) {
	fs := &fakeStore{}
	a := NewAggregator(fs, AggregatorOptions{FlushInterval: time.Hour, MaxBatch: 1_000_000})
	defer a.Close()

	ts := hour(t, "2026-01-01T00:00:00Z")
	// Turn 1: prompt → agent → prompt (closes turn 1)
	a.Ingest(sc("s1", "w1"), evAt(1, ts, session.EventTypeUserPrompt, session.UserPromptData{Message: "a"}))
	a.Ingest(sc("s1", "w1"), evAt(2, ts, session.EventTypeAgentMessage, session.AgentMessageData{Text: "b"}))
	a.Ingest(sc("s1", "w1"), evAt(3, ts, session.EventTypeUserPrompt, session.UserPromptData{Message: "c"}))
	// A prompt without any preceding agent activity does not increment turns.
	a.Ingest(sc("s2", "w1"), evAt(1, ts, session.EventTypeUserPrompt, session.UserPromptData{Message: "d"}))
	mustFlush(t, a)

	if got := fs.sumFor(t, MetricAgentTurnsCompleted, "s1"); got != 1 {
		t.Errorf("s1 agent_turns_completed = %d, want 1", got)
	}
	if got := fs.sumFor(t, MetricAgentTurnsCompleted, "s2"); got != 0 {
		t.Errorf("s2 agent_turns_completed = %d, want 0 (no preceding agent activity)", got)
	}
}

// -----------------------------------------------------------------------------
// Bucketing
// -----------------------------------------------------------------------------

func TestAggregator_HourBucketing_SplitAcrossHours(t *testing.T) {
	fs := &fakeStore{}
	a := NewAggregator(fs, AggregatorOptions{FlushInterval: time.Hour, MaxBatch: 1_000_000})
	defer a.Close()

	t1 := hour(t, "2026-01-01T00:30:00Z") // bucket 00:00
	t2 := hour(t, "2026-01-01T01:15:00Z") // bucket 01:00
	a.Ingest(sc("s1", "w1"), evAt(1, t1, session.EventTypeUserPrompt, session.UserPromptData{Message: ""}))
	a.Ingest(sc("s1", "w1"), evAt(2, t2, session.EventTypeUserPrompt, session.UserPromptData{Message: ""}))
	mustFlush(t, a)

	// Two distinct bucket keys → two delta rows for MetricPrompts.
	var buckets []time.Time
	for _, c := range fs.snapshot() {
		for _, d := range c.deltas {
			if d.Metric == MetricPrompts {
				buckets = append(buckets, d.TSBucket)
			}
		}
	}
	if len(buckets) != 2 {
		t.Fatalf("prompt buckets = %v, want 2 rows", buckets)
	}
	want0 := hour(t, "2026-01-01T00:00:00Z")
	want1 := hour(t, "2026-01-01T01:00:00Z")
	seen := map[int64]bool{buckets[0].Unix(): true, buckets[1].Unix(): true}
	if !seen[want0.Unix()] || !seen[want1.Unix()] {
		t.Errorf("buckets = %v, want %v and %v", buckets, want0, want1)
	}
}

// -----------------------------------------------------------------------------
// Batching: timer-based and count-based
// -----------------------------------------------------------------------------

func TestAggregator_MaxBatchFlush(t *testing.T) {
	fs := &fakeStore{}
	// MaxBatch=5 → the 5th event triggers a flush before we call Flush ourselves.
	a := NewAggregator(fs, AggregatorOptions{FlushInterval: time.Hour, MaxBatch: 5})
	defer a.Close()

	ts := hour(t, "2026-01-01T00:00:00Z")
	for i := 0; i < 5; i++ {
		a.Ingest(sc("s1", "w1"), evAt(int64(i+1), ts, session.EventTypeUserPrompt, session.UserPromptData{Message: "x"}))
	}
	// Give the background goroutine time to observe the threshold.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fs.sumFor(t, MetricPrompts, "s1") == 5 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := fs.sumFor(t, MetricPrompts, "s1"); got != 5 {
		t.Errorf("prompts after count-based flush = %d, want 5", got)
	}
}

func TestAggregator_TimerFlush(t *testing.T) {
	fs := &fakeStore{}
	a := NewAggregator(fs, AggregatorOptions{FlushInterval: 20 * time.Millisecond, MaxBatch: 1_000_000})
	defer a.Close()

	ts := hour(t, "2026-01-01T00:00:00Z")
	a.Ingest(sc("s1", "w1"), evAt(1, ts, session.EventTypeUserPrompt, session.UserPromptData{Message: "x"}))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(fs.snapshot()) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(fs.snapshot()) == 0 {
		t.Errorf("expected timer-driven flush, got no store calls")
	}
}

// -----------------------------------------------------------------------------
// Idempotency (replay): double Ingest → still one row per (bucket, metric)
// -----------------------------------------------------------------------------

func TestAggregator_ReplayIdempotent_ViaSQLiteStore(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "stats.db")
	ctx := context.Background()

	// First pass: aggregate & flush.
	s1, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	a1 := NewAggregator(s1, AggregatorOptions{FlushInterval: time.Hour, MaxBatch: 1_000_000})
	ts := hour(t, "2026-01-01T00:00:00Z")
	for i := 0; i < 3; i++ {
		a1.Ingest(sc("s1", "w1"), evAt(int64(i+1), ts, session.EventTypeUserPrompt, session.UserPromptData{Message: "x"}))
	}
	if err := a1.Close(); err != nil {
		t.Fatalf("a1.Close: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("s1.Close: %v", err)
	}

	// Reopen and replay the SAME events. Because UpsertDeltasWithCursor uses
	// an INSERT ... ON CONFLICT REPLACE, the same batch produces the same row
	// value (3) — not doubled to 6.
	s2, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	a2 := NewAggregator(s2, AggregatorOptions{FlushInterval: time.Hour, MaxBatch: 1_000_000})
	for i := 0; i < 3; i++ {
		a2.Ingest(sc("s1", "w1"), evAt(int64(i+1), ts, session.EventTypeUserPrompt, session.UserPromptData{Message: "x"}))
	}
	if err := a2.Close(); err != nil {
		t.Fatalf("a2.Close: %v", err)
	}

	pts, err := s2.Query(ctx, Query{
		RangeFrom: ts.Add(-time.Hour), RangeTo: ts.Add(time.Hour),
		Bucket: BucketHour, Metrics: []string{MetricPrompts},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(pts) != 1 || pts[0].Value != 3 {
		t.Errorf("replay produced Points=%+v, want single Point{Value=3} (idempotent)", pts)
	}

	// Cursor also advanced.
	cur, err := s2.GetCursor(ctx, "s1")
	if err != nil {
		t.Fatalf("GetCursor: %v", err)
	}
	if cur.LastEventSeq != 3 {
		t.Errorf("cursor.LastEventSeq = %d, want 3", cur.LastEventSeq)
	}
	if !cur.LastEventAt.Equal(ts) {
		t.Errorf("cursor.LastEventAt = %v, want %v", cur.LastEventAt, ts)
	}
	if cur.EstimatorVersion != EstimatorVersion {
		t.Errorf("cursor.EstimatorVersion = %d, want %d", cur.EstimatorVersion, EstimatorVersion)
	}
}

// -----------------------------------------------------------------------------
// Multi-session isolation
// -----------------------------------------------------------------------------

func TestAggregator_MultiSession_SeparateCursors(t *testing.T) {
	fs := &fakeStore{}
	a := NewAggregator(fs, AggregatorOptions{FlushInterval: time.Hour, MaxBatch: 1_000_000})
	defer a.Close()

	ts := hour(t, "2026-01-01T00:00:00Z")
	a.Ingest(sc("sA", "w1"), evAt(10, ts, session.EventTypeUserPrompt, session.UserPromptData{Message: "a"}))
	a.Ingest(sc("sB", "w2"), evAt(20, ts.Add(time.Minute), session.EventTypeUserPrompt, session.UserPromptData{Message: "b"}))
	mustFlush(t, a)

	seen := map[string]Cursor{}
	for _, c := range fs.snapshot() {
		seen[c.cursor.SessionID] = c.cursor
	}
	if seen["sA"].LastEventSeq != 10 {
		t.Errorf("sA cursor seq = %d, want 10", seen["sA"].LastEventSeq)
	}
	if seen["sB"].LastEventSeq != 20 {
		t.Errorf("sB cursor seq = %d, want 20", seen["sB"].LastEventSeq)
	}
}

// -----------------------------------------------------------------------------
// Non-blocking ingest under a full buffer
// -----------------------------------------------------------------------------

// blockingStore blocks every UpsertDeltasWithCursor call until unblock is closed.
// This lets us wedge the aggregator's background goroutine mid-flush so we can
// prove that Ingest is truly non-blocking when the ingest channel fills up.
type blockingStore struct {
	NoopStore
	unblock chan struct{}
	entered atomic.Int32
}

func (b *blockingStore) UpsertDeltasWithCursor(ctx context.Context, _ []Delta, _ Cursor) error {
	b.entered.Add(1)
	select {
	case <-b.unblock:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestAggregator_Ingest_NonBlocking_DropsOnFullBuffer(t *testing.T) {
	bs := &blockingStore{unblock: make(chan struct{})}
	// Tiny buffer + tiny MaxBatch so we can wedge the goroutine quickly.
	a := NewAggregator(bs, AggregatorOptions{
		FlushInterval: time.Hour,
		MaxBatch:      1,
		BufferSize:    2,
		FlushTimeout:  time.Hour,
	})
	defer func() {
		close(bs.unblock)
		_ = a.Close()
	}()

	ts := hour(t, "2026-01-01T00:00:00Z")
	ev := evAt(1, ts, session.EventTypeUserPrompt, session.UserPromptData{Message: "x"})

	// The first Ingest fills the buffer + trips MaxBatch; the goroutine picks
	// it up and blocks inside the store call. Give it a moment to enter.
	a.Ingest(sc("s1", "w1"), ev)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && bs.entered.Load() == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	if bs.entered.Load() == 0 {
		t.Fatal("background goroutine never entered blocking store call")
	}

	// Now flood ingest well past the buffer capacity. All calls must return
	// promptly without deadlocking, and Dropped() must climb.
	const flood = 1000
	done := make(chan struct{})
	go func() {
		for i := 0; i < flood; i++ {
			a.Ingest(sc("s1", "w1"), ev)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("Ingest blocked — expected non-blocking behaviour on full buffer")
	}
	if a.Dropped() == 0 {
		t.Errorf("Dropped() = 0, want > 0 after flooding a full buffer")
	}
}

// -----------------------------------------------------------------------------
// Close: idempotent + final flush
// -----------------------------------------------------------------------------

func TestAggregator_Close_FlushesPending(t *testing.T) {
	fs := &fakeStore{}
	a := NewAggregator(fs, AggregatorOptions{FlushInterval: time.Hour, MaxBatch: 1_000_000})

	ts := hour(t, "2026-01-01T00:00:00Z")
	a.Ingest(sc("s1", "w1"), evAt(1, ts, session.EventTypeUserPrompt, session.UserPromptData{Message: "x"}))
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if fs.sumFor(t, MetricPrompts, "s1") != 1 {
		t.Errorf("prompts after Close = %d, want 1", fs.sumFor(t, MetricPrompts, "s1"))
	}
	// Second Close is a no-op.
	if err := a.Close(); err != nil {
		t.Errorf("second Close: %v, want nil", err)
	}
	// Post-close Ingest must be silently dropped, not panic.
	a.Ingest(sc("s1", "w1"), evAt(2, ts, session.EventTypeUserPrompt, session.UserPromptData{Message: "y"}))
}
