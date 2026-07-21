// Package stats defines the interfaces and value types for Mitto's dashboard
// time-series stats subsystem (parent epic mitto-a86b).
//
// This package is a skeleton: it exports the shapes that downstream subtasks
// build against, but contains no runtime behavior of its own beyond a NoopStore
// used in tests. Concrete implementations are added in later subtasks:
//
//   - stats.2 — SQLite-backed Store (modernc.org/sqlite, schema v1, migrations)
//   - stats.3 — Aggregator (event → bucket math + upsert batcher)
//   - stats.4 — Live ingest via a SessionObserver adapter in internal/web
//   - stats.5 — Backfiller (startup + 6h periodic; estimator-version recompute)
//   - stats.6 — Token estimator + MCP-call classifier
//   - stats.7 — GET /api/dashboard/timeseries handler + cache
//
// Design constraints held here:
//
//   - The package has no dependencies on internal/web or internal/conversation.
//     stats.4 will define a small adapter in internal/web that translates ACP
//     events into whatever event type stats.3 finalises and calls Aggregator.
//   - Metric names are exported string constants (not free strings), so the
//     observer wiring, the API handler, and the chart legend all reference the
//     same canonical set.
//   - Storage of length-only counters is deliberate: no message text ever
//     lands in the stats DB, keeping it safe for support bundles.
package stats

import (
	"context"
	"errors"
	"time"
)

// EstimatorVersion is the current token-estimator version. Bumping it forces
// stats.5's backfiller to recompute historical rows so length-based token
// counts stay consistent when the estimator changes (e.g. if ACP ever exposes
// real usage counters and the estimator is retired).
const EstimatorVersion = 1

// v1 metric names. These are stored as-is in the SQLite `metric` column and
// echoed to the frontend in the API response, so any rename is a schema break.
const (
	// MetricInputTokensEst is a length-based estimate derived from user_prompt.
	MetricInputTokensEst = "input_tokens_est"
	// MetricOutputTokensEst is a length-based estimate derived from
	// agent_message and agent_thought.
	MetricOutputTokensEst = "output_tokens_est"
	// MetricPrompts counts user_prompt events.
	MetricPrompts = "prompts"
	// MetricAgentTurnsCompleted counts completed agent turns.
	MetricAgentTurnsCompleted = "agent_turns_completed"
	// MetricToolCallsTotal counts every tool call, MCP or otherwise.
	MetricToolCallsTotal = "tool_calls_total"
	// MetricMCPCalls counts the MCP-classified subset of tool calls.
	MetricMCPCalls = "mcp_calls"
	// MetricPermissionsPrompted counts permission requests shown to the user.
	MetricPermissionsPrompted = "permissions_prompted"
	// MetricErrors counts error events.
	MetricErrors = "errors"
)

// Bucket names the time-bucket size used by a query.
type Bucket string

const (
	// BucketHour aggregates by hour; the ingest path always writes hourly rows.
	BucketHour Bucket = "hour"
	// BucketDay aggregates by day; computed at query time from hourly rows.
	BucketDay Bucket = "day"
)

// Package-level errors. Stores must return these where documented so callers
// can branch on identity via errors.Is.
var (
	// ErrNotFound is returned by GetCursor when no cursor exists for the given
	// session, and by Query for callers that treat empty results as an error.
	ErrNotFound = errors.New("stats: not found")
	// ErrClosed is returned by any Store method invoked after Close.
	ErrClosed = errors.New("stats: store closed")
)

// Delta is a single batched, per-bucket metric increment ready to be persisted
// by Store.UpsertDeltas. The (TSBucket, Metric, SessionID, Workspace) tuple is
// the row key; concurrent Deltas for the same key are summed on upsert.
type Delta struct {
	// TSBucket is the start of the hourly bucket the delta belongs to (UTC).
	TSBucket time.Time
	// Metric is one of the exported Metric* constants.
	Metric string
	// SessionID is the source session's identifier.
	SessionID string
	// Workspace is the workspace UUID (empty for workspace-less sessions).
	Workspace string
	// Value is the counter increment. Always non-negative in v1.
	Value int64
}

// Cursor tracks how far a per-session ingest (live or backfill) has advanced
// so restarts and mixed live/backfill paths do not double-count events.
type Cursor struct {
	// SessionID identifies the session this cursor tracks.
	SessionID string
	// LastEventSeq is the seq of the last event ingested into stats.
	LastEventSeq int64
	// LastEventAt is the timestamp of the last event ingested (from the event
	// itself, never wall-clock — see the epic's suspend-safe requirement).
	LastEventAt time.Time
	// EstimatorVersion records which estimator version produced the counts up
	// to LastEventSeq. Bumping the package-level EstimatorVersion triggers a
	// full recompute in stats.5.
	EstimatorVersion int
	// UpdatedAt is the wall-clock time this cursor was last persisted.
	UpdatedAt time.Time
}

// Query describes a timeseries read against a Store.
type Query struct {
	// RangeFrom is the inclusive start of the queried window (UTC).
	RangeFrom time.Time
	// RangeTo is the exclusive end of the queried window (UTC).
	RangeTo time.Time
	// Bucket is BucketHour or BucketDay.
	Bucket Bucket
	// Metrics lists the metric names to return. Empty means all v1 metrics.
	Metrics []string
	// Workspace, when non-empty, restricts the query to a single workspace UUID.
	Workspace string
}

// Point is a single datum in a Query result: one (ts, metric, value) row.
type Point struct {
	// TS is the start of the bucket this value covers (UTC).
	TS time.Time
	// Metric is one of the exported Metric* constants.
	Metric string
	// Value is the aggregated counter for this bucket.
	Value int64
}

// Store persists batched stats deltas and per-session cursors, and answers
// timeseries queries. The SQLite-backed implementation is added in stats.2.
//
// Implementations must be safe for concurrent use. All methods must return
// ErrClosed once Close has been invoked.
type Store interface {
	// UpsertDeltas idempotently adds each delta's Value to its (TSBucket,
	// Metric, SessionID, Workspace) row. Deltas whose Value is zero are
	// silently skipped.
	UpsertDeltas(ctx context.Context, deltas []Delta) error

	// GetCursor returns the ingest cursor for a session. If no cursor has been
	// persisted yet the returned Cursor has SessionID set to sessionID and
	// zero values for all other fields, with err == ErrNotFound.
	GetCursor(ctx context.Context, sessionID string) (Cursor, error)

	// SetCursor persists cur, overwriting any prior cursor for cur.SessionID.
	SetCursor(ctx context.Context, cur Cursor) error

	// Query returns the aggregated points for q, sorted by (TS, Metric).
	Query(ctx context.Context, q Query) ([]Point, error)

	// Prune deletes hourly rows strictly older than olderThan and returns the
	// number of rows removed. Retention is enforced by the stats.9 job.
	Prune(ctx context.Context, olderThan time.Time) (rows int64, err error)

	// Close releases the underlying storage. Subsequent calls must return
	// ErrClosed.
	Close() error
}

// Aggregator turns a stream of session events (from the live SessionObserver
// adapter or the backfill replayer) into batched Deltas, buffered by (session,
// workspace, bucket). The real implementation is added in stats.3.
//
// The Observe(...) signature depends on the event-payload shape decided in
// stats.3/stats.6, so it is intentionally not part of this skeleton — pinning
// it now would force a churn later.
type Aggregator interface {
	// Flush drains all buffered deltas to the underlying Store. Called on the
	// 10s / 500-event boundary and at shutdown.
	Flush(ctx context.Context) error

	// Close flushes any pending work and releases resources.
	Close() error
}

// Backfiller replays each session's events.jsonl from its stored cursor
// forward using event timestamps (never wall-clock), so long suspends or
// offline periods produce empty buckets rather than spikes. The real
// implementation is added in stats.5 and is invoked at startup and every 6h.
type Backfiller interface {
	// Run performs one full backfill pass. Idempotent: only events past each
	// session's Cursor are ingested. Safe to run concurrently with the live
	// ingest path because both share the same Cursor.
	Run(ctx context.Context) error

	// InProgress reports whether a backfill pass is currently running. The
	// /api/dashboard/timeseries handler exposes this so the frontend can show
	// a `backfill_in_progress: true` badge on partial data.
	InProgress() bool
}
