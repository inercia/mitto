// Package coldstart provides diagnostic instrumentation for cold-start flows.
//
// It is a leaf package: it depends only on the Go standard library and
// golang.org/x/sys/unix. It intentionally imports no other internal/*
// package so it can be consumed anywhere in the tree without creating
// import cycles.
//
// A Trace represents one cold-start's correlated timeline. Callers record
// phase boundaries via Phase and finalize with Summary. All Trace methods
// are nil-safe: a nil *Trace behaves as a no-op, so callers never need
// guards. Completed traces are retained in a small ring buffer accessible
// via RecentSummaries for debugging endpoints.
package coldstart

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ringCapacity is the maximum number of completed Summary entries kept
// in memory. Newest entries evict oldest.
const ringCapacity = 64

// PhaseRecord is one recorded phase boundary.
type PhaseRecord struct {
	Name      string    `json:"name"`
	ElapsedMs int64     `json:"elapsed_ms"`
	PhaseMs   int64     `json:"phase_ms"`
	At        time.Time `json:"at"`
}

// Summary is a completed trace snapshot kept in the ring buffer.
type Summary struct {
	ID            string        `json:"cold_start_id"`
	SessionID     string        `json:"session_id"`
	WorkspaceUUID string        `json:"workspace_uuid"`
	Outcome       string        `json:"outcome"`
	TotalMs       int64         `json:"total_ms"`
	Phases        []PhaseRecord `json:"phases"`
	At            time.Time     `json:"at"`
}

// Trace is one cold-start's correlated timeline.
type Trace struct {
	id            string
	sessionID     string
	workspaceUUID string
	logger        *slog.Logger

	mu        sync.Mutex
	begin     time.Time
	lastPhase time.Time
	phases    []PhaseRecord
	done      bool
}

// New creates a Trace with a fresh short random cold_start_id.
// logger may be nil.
func New(logger *slog.Logger, sessionID, workspaceUUID string) *Trace {
	return &Trace{
		id:            newID(),
		sessionID:     sessionID,
		workspaceUUID: workspaceUUID,
		logger:        logger,
	}
}

// newID returns a short random id (~12 hex chars from 6 random bytes),
// falling back to a time-based id if the RNG fails.
func newID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("t%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// ID returns the trace id, or "" if the trace is nil.
func (t *Trace) ID() string {
	if t == nil {
		return ""
	}
	return t.id
}

// Phase records a phase boundary and logs "cold_start_phase" at INFO.
// The first Phase() call is the trace's begin: it initializes timing
// and attaches a ContentionSnapshot to the log line. Nil-safe.
func (t *Trace) Phase(name string, kv ...any) {
	if t == nil {
		return
	}
	now := time.Now()

	t.mu.Lock()
	first := t.begin.IsZero()
	if first {
		t.begin = now
		t.lastPhase = now
	}
	elapsed := now.Sub(t.begin)
	phase := now.Sub(t.lastPhase)
	t.lastPhase = now
	t.phases = append(t.phases, PhaseRecord{
		Name:      name,
		ElapsedMs: elapsed.Milliseconds(),
		PhaseMs:   phase.Milliseconds(),
		At:        now,
	})
	t.mu.Unlock()

	if t.logger == nil {
		return
	}
	attrs := make([]any, 0, 12+len(kv))
	attrs = append(attrs,
		"cold_start_id", t.id,
		"session_id", t.sessionID,
		"workspace_uuid", t.workspaceUUID,
		"phase", name,
		"elapsed_ms", elapsed.Milliseconds(),
		"phase_ms", phase.Milliseconds(),
	)
	if first {
		attrs = append(attrs, Contention().LogAttrs()...)
	}
	attrs = append(attrs, kv...)
	t.logger.Info("cold_start_phase", attrs...)
}

// Summary finalizes the trace, logs "cold_start_summary" at INFO, and
// stores the resulting Summary in the ring buffer. Idempotent and
// nil-safe.
func (t *Trace) Summary(outcome string, kv ...any) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.done {
		t.mu.Unlock()
		return
	}
	t.done = true
	begin := t.begin
	phasesCopy := make([]PhaseRecord, len(t.phases))
	copy(phasesCopy, t.phases)
	t.mu.Unlock()

	now := time.Now()
	var totalMs int64
	if !begin.IsZero() {
		totalMs = now.Sub(begin).Milliseconds()
	}

	sum := Summary{
		ID:            t.id,
		SessionID:     t.sessionID,
		WorkspaceUUID: t.workspaceUUID,
		Outcome:       outcome,
		TotalMs:       totalMs,
		Phases:        phasesCopy,
		At:            now,
	}
	ringAppend(sum)

	if t.logger != nil {
		attrs := make([]any, 0, 10+len(kv))
		attrs = append(attrs,
			"cold_start_id", t.id,
			"session_id", t.sessionID,
			"workspace_uuid", t.workspaceUUID,
			"outcome", outcome,
			"total_ms", totalMs,
			"phases", phasesCopy,
		)
		attrs = append(attrs, kv...)
		t.logger.Info("cold_start_summary", attrs...)
	}
}

type traceCtxKey struct{}

// WithTrace returns a derived context carrying t.
func WithTrace(ctx context.Context, t *Trace) context.Context {
	return context.WithValue(ctx, traceCtxKey{}, t)
}

// FromContext returns the Trace attached to ctx, or nil if none.
func FromContext(ctx context.Context) *Trace {
	if ctx == nil {
		return nil
	}
	t, _ := ctx.Value(traceCtxKey{}).(*Trace)
	return t
}

// --- ring buffer ---------------------------------------------------------

var (
	ringMu   sync.Mutex
	ringBuf  [ringCapacity]Summary
	ringLen  int
	ringNext int // index of next write slot
)

func ringAppend(s Summary) {
	ringMu.Lock()
	ringBuf[ringNext] = s
	ringNext = (ringNext + 1) % ringCapacity
	if ringLen < ringCapacity {
		ringLen++
	}
	ringMu.Unlock()
}

// RecentSummaries returns up to k most-recent completed summaries,
// newest first. k<=0 returns all held summaries.
func RecentSummaries(k int) []Summary {
	ringMu.Lock()
	defer ringMu.Unlock()
	n := ringLen
	if k > 0 && k < n {
		n = k
	}
	out := make([]Summary, 0, n)
	// Walk backwards from most-recent write.
	idx := ringNext - 1
	for i := 0; i < n; i++ {
		if idx < 0 {
			idx += ringCapacity
		}
		out = append(out, ringBuf[idx])
		idx--
	}
	return out
}
