package conversation

// Tests for the live stats path wiring (mitto-a86b.4). The acceptance
// criterion is: seed 10 tool_calls + 5 user_prompts through the observer,
// call aggregator.Flush(ctx), and verify the persisted deltas have matching
// counts. We exercise the full observer → aggregator → Store pipeline with an
// in-test fake Store so the assertion sees exactly what would have been
// written to SQLite in production, without the sqlite3 driver overhead.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/stats"
)

// captureStore is a stats.Store fake used solely by these tests. It records
// every UpsertDeltasWithCursor call so tests can sum metric deltas across all
// flushes. The rest of the Store interface is inherited from stats.NoopStore.
type captureStore struct {
	stats.NoopStore
	mu     sync.Mutex
	deltas []stats.Delta
}

func (c *captureStore) UpsertDeltasWithCursor(_ context.Context, deltas []stats.Delta, _ stats.Cursor) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Copy so post-call buffer resets in the aggregator do not mutate our record.
	cp := make([]stats.Delta, len(deltas))
	copy(cp, deltas)
	c.deltas = append(c.deltas, cp...)
	return nil
}

func (c *captureStore) sumFor(metric, sessionID string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var total int64
	for _, d := range c.deltas {
		if d.Metric == metric && (sessionID == "" || d.SessionID == sessionID) {
			total += d.Value
		}
	}
	return total
}

// newTestAggregator builds an aggregator with test-friendly options: never
// timer-flush during the test (interval = 1h), never batch-flush (MaxBatch
// huge), plenty of buffer. Callers must Close(a) to drain the goroutine.
func newTestAggregator(store stats.Store) stats.Aggregator {
	return stats.NewAggregator(store, stats.AggregatorOptions{
		FlushInterval: time.Hour,
		MaxBatch:      1_000_000,
		BufferSize:    4096,
	})
}

// TestStatsObserver_AcceptanceCriteria_10ToolCalls_5UserPrompts verifies the
// exact acceptance-criteria scenario from mitto-a86b.4: seed 10 tool_calls +
// 5 user_prompts through the SessionObserver adapter, Flush, and confirm the
// persisted deltas have matching counts.
func TestStatsObserver_AcceptanceCriteria_10ToolCalls_5UserPrompts(t *testing.T) {
	store := &captureStore{}
	agg := newTestAggregator(store)
	defer func() { _ = agg.Close() }()

	sc := stats.SessionContext{
		SessionID:  "test-session-1",
		Workspace:  "ws-uuid-1",
		WorkingDir: "/tmp/x",
		ACPServer:  "Auggie (Opus)",
	}
	obs := newStatsObserver(agg, sc)
	if obs == nil {
		t.Fatalf("newStatsObserver returned nil for a non-nil aggregator")
	}

	// 5 user prompts (varied to exercise message text, files, images branches).
	for i := int64(1); i <= 5; i++ {
		obs.OnUserPrompt(i, "sender", "prompt-id", "hello world", nil, nil, "", 0)
	}
	// 10 tool calls (mix of MCP and non-MCP titles; MCP subset is not part of
	// the acceptance criterion but we sanity-check it below).
	for i := int64(6); i <= 15; i++ {
		title := "read_file"
		if i%2 == 0 {
			title = "mitto_conversation_new" // classified MCP by title regex
		}
		obs.OnToolCall(i, "tc-id", title, "completed")
	}

	if err := agg.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if got := store.sumFor(stats.MetricPrompts, sc.SessionID); got != 5 {
		t.Errorf("MetricPrompts = %d, want 5", got)
	}
	if got := store.sumFor(stats.MetricToolCallsTotal, sc.SessionID); got != 10 {
		t.Errorf("MetricToolCallsTotal = %d, want 10", got)
	}
	// Sanity: exactly half the tool-call titles ("mitto_*") match the MCP regex.
	if got := store.sumFor(stats.MetricMCPCalls, sc.SessionID); got != 5 {
		t.Errorf("MetricMCPCalls = %d, want 5", got)
	}
}

// TestStatsObserver_AgentMessageAndThought_ProduceOutputTokens verifies the
// two output-token callbacks route into the aggregator's output-tokens bucket.
func TestStatsObserver_AgentMessageAndThought_ProduceOutputTokens(t *testing.T) {
	store := &captureStore{}
	agg := newTestAggregator(store)
	defer func() { _ = agg.Close() }()

	sc := stats.SessionContext{SessionID: "s1", Workspace: "w1"}
	obs := newStatsObserver(agg, sc)

	// A prompt has to arrive first so the aggregator's turn-heuristic has a
	// session accumulator, but that is not strictly required — cover both.
	obs.OnAgentMessage(1, "hello from the agent")            // 20 chars → 5 tokens
	obs.OnAgentThought(2, "let me think about this problem") // 32 chars → 8 tokens

	if err := agg.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	got := store.sumFor(stats.MetricOutputTokensEst, sc.SessionID)
	if got <= 0 {
		t.Errorf("MetricOutputTokensEst = %d, want > 0", got)
	}
}

// TestStatsObserver_NilAggregator_ReturnsNilObserver documents the nil-safety
// contract callers rely on to disable live stats without extra branches at
// the call site.
func TestStatsObserver_NilAggregator_ReturnsNilObserver(t *testing.T) {
	if obs := newStatsObserver(nil, stats.SessionContext{SessionID: "s1"}); obs != nil {
		t.Errorf("newStatsObserver(nil, ...) = %v, want nil", obs)
	}
}

// TestStatsObserver_NilReceiver_MethodsSafe verifies calling any of the live
// callbacks on a nil observer is a no-op (does not panic). This mirrors the
// early-return in the ingest fan-in and lets callers keep the observer field
// unset on paths that never wire stats.
func TestStatsObserver_NilReceiver_MethodsSafe(t *testing.T) {
	var obs *statsObserver
	// If any of these panic, the test fails; there is no assertion beyond
	// "did not panic".
	obs.OnUserPrompt(1, "s", "p", "hi", nil, nil, "", 0)
	obs.OnAgentMessage(2, "text")
	obs.OnAgentThought(3, "thought")
	obs.OnToolCall(4, "id", "title", "status")
	obs.OnError("boom")
	obs.OnPromptComplete(0)
	obs.OnACPStarted()
	obs.OnACPStopped("reason")
}

// TestSessionManager_SetStatsAggregator_AttachObserver verifies the manager
// wiring: after SetStatsAggregator on a manager with a pre-seeded session,
// attachStatsObserver adds exactly one observer to that session, and driving
// callbacks flows through to the store.
func TestSessionManager_SetStatsAggregator_AttachObserver(t *testing.T) {
	sm := &SessionManager{
		sessions:       map[string]*BackgroundSession{},
		pendingResumes: map[string]*pendingResumeResult{},
	}
	bs := &BackgroundSession{
		persistedID:   "test-session-2",
		workspaceUUID: "ws-uuid-2",
		workingDir:    "/tmp/y",
		acpServer:     "Auggie (Sonnet)",
	}
	sm.AddSessionForTest(bs)

	store := &captureStore{}
	agg := newTestAggregator(store)
	defer func() { _ = agg.Close() }()

	sm.SetStatsAggregator(agg)
	sm.attachStatsObserver(bs)

	if got := bs.ObserverCount(); got != 1 {
		t.Fatalf("ObserverCount after attach = %d, want 1", got)
	}

	// Drive one prompt through the attached observer via notifyObservers so
	// the whole wiring — not just direct observer calls — is exercised.
	bs.notifyObservers(func(o SessionObserver) {
		o.OnUserPrompt(1, "sender", "pid", "hello", nil, nil, "", 0)
	})
	bs.notifyObservers(func(o SessionObserver) {
		o.OnToolCall(2, "tc", "read_file", "completed")
	})

	if err := agg.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := store.sumFor(stats.MetricPrompts, bs.GetSessionID()); got != 1 {
		t.Errorf("MetricPrompts via notifyObservers = %d, want 1", got)
	}
	if got := store.sumFor(stats.MetricToolCallsTotal, bs.GetSessionID()); got != 1 {
		t.Errorf("MetricToolCallsTotal via notifyObservers = %d, want 1", got)
	}
}

// TestSessionManager_NoAggregator_AttachIsNoop verifies that when no
// aggregator has been configured, attachStatsObserver does not add an
// observer — the wiring must be safe on --no-stats / test paths.
func TestSessionManager_NoAggregator_AttachIsNoop(t *testing.T) {
	sm := &SessionManager{
		sessions:       map[string]*BackgroundSession{},
		pendingResumes: map[string]*pendingResumeResult{},
	}
	bs := &BackgroundSession{persistedID: "no-stats-session"}
	sm.AddSessionForTest(bs)

	sm.attachStatsObserver(bs) // no SetStatsAggregator call → aggregator is nil

	if got := bs.ObserverCount(); got != 0 {
		t.Errorf("ObserverCount with no aggregator = %d, want 0", got)
	}
}

// TestStatsObserver_OnSessionChange_ForwardsModel verifies that a
// session_change(kind=model) event routed through the observer reaches the
// aggregator and retags subsequent token deltas — the live-path equivalent of
// the backfiller's model-fold on persisted events.
func TestStatsObserver_OnSessionChange_ForwardsModel(t *testing.T) {
	store := &captureStore{}
	agg := newTestAggregator(store)
	defer func() { _ = agg.Close() }()

	sc := stats.SessionContext{SessionID: "s-model", Workspace: "w1", BaselineModel: "modelA"}
	obs := newStatsObserver(agg, sc)
	if obs == nil {
		t.Fatalf("newStatsObserver returned nil")
	}

	// Under baseline modelA.
	obs.OnUserPrompt(1, "sender", "pid", "abcd", nil, nil, "", 0)
	obs.OnAgentMessage(2, "hello agent")
	// Switch to modelB.
	obs.OnSessionChange(3, session.SessionChangeData{Kind: "model", Value: "modelB", PreviousValue: "modelA"})
	// Under modelB.
	obs.OnUserPrompt(4, "sender", "pid", "efgh", nil, nil, "", 0)
	obs.OnAgentMessage(5, "reply under B")

	if err := agg.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	for _, metric := range []string{stats.MetricInputTokensEst, stats.MetricOutputTokensEst} {
		byModel := map[string]int64{}
		store.mu.Lock()
		for _, d := range store.deltas {
			if d.Metric == metric && d.SessionID == sc.SessionID {
				byModel[d.Model] += d.Value
			}
		}
		store.mu.Unlock()
		if byModel["modelA"] == 0 {
			t.Errorf("%s: no rows for modelA (byModel=%v)", metric, byModel)
		}
		if byModel["modelB"] == 0 {
			t.Errorf("%s: no rows for modelB — session_change did not propagate (byModel=%v)", metric, byModel)
		}
		if byModel[""] != 0 {
			t.Errorf("%s: unexpected empty-model rows (byModel=%v)", metric, byModel)
		}
	}
}
