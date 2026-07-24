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

	"github.com/inercia/mitto/internal/session"
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
// by Store.UpsertDeltas. The (TSBucket, Metric, SessionID, Workspace, Model)
// tuple is the row key; concurrent Deltas for the same key are summed on
// upsert.
type Delta struct {
	// TSBucket is the start of the hourly bucket the delta belongs to (UTC).
	TSBucket time.Time
	// Metric is one of the exported Metric* constants.
	Metric string
	// SessionID is the source session's identifier.
	SessionID string
	// Workspace is the workspace UUID (empty for workspace-less sessions).
	Workspace string
	// Model is the ACP model attribution for this delta. Empty for metrics
	// that are not model-attributable (prompts, tool_calls_total, mcp_calls,
	// permissions_prompted, errors, agent_turns_completed), for pre-v2 rows,
	// and for sessions without an advertised model.
	Model string
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

// GroupBy dimension names accepted by Query.GroupBy. The empty value keeps the
// legacy (metric,ts) grouping; GroupByModel additionally groups by model so
// callers can surface per-model series (mitto-1ac / mitto-5r5.2).
const (
	GroupByModel = "model"
)

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
	// GroupBy, when non-empty, adds an extra grouping dimension to the query.
	// v1 accepts only GroupByModel (""); other values are rejected by the
	// handler edge, so stores may assume it is either empty or GroupByModel.
	// When set to GroupByModel, returned Points carry Model populated from the
	// underlying row's model column; otherwise Model is always the zero string.
	GroupBy string
}

// Point is a single datum in a Query result: one (ts, metric, value) row.
type Point struct {
	// TS is the start of the bucket this value covers (UTC).
	TS time.Time
	// Metric is one of the exported Metric* constants.
	Metric string
	// Value is the aggregated counter for this bucket.
	Value int64
	// Model is the ACP model attribution for this point. Only populated when
	// the originating Query had GroupBy == GroupByModel; otherwise the empty
	// string. An empty model in a grouped result means the underlying row was
	// not tagged with a model (pre-migration data, or non-model-attributable
	// metrics).
	Model string
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

	// UpsertDeltasWithCursor atomically applies deltas and advances the given
	// session's cursor in a single transaction. Used by the Aggregator's
	// per-session flush to guarantee exactly-once semantics: after a
	// successful call, either both the deltas and the cursor advance are
	// durable, or neither is.
	//
	// deltas may be empty (cursor-only advance is legal). cur.SessionID must
	// be non-empty. Cursor monotonicity rules match SetCursor.
	UpsertDeltasWithCursor(ctx context.Context, deltas []Delta, cur Cursor) error

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

	// Vacuum reclaims free pages from the underlying storage after a Prune
	// removed a significant fraction of rows. Called weekly by the stats.9
	// retention job on Sundays. Best-effort: implementations that cannot
	// meaningfully vacuum (NoopStore) return nil.
	Vacuum(ctx context.Context) error

	// GetMeta returns the value stored under key in stats_meta. When the key
	// does not exist the returned string is empty and err is ErrNotFound.
	// Used by the stats.5 backfiller to persist small operational scalars
	// like last_full_backfill_at without inventing a whole new table.
	GetMeta(ctx context.Context, key string) (string, error)

	// SetMeta upserts key=value in stats_meta. Callers stringify their own
	// values (timestamps in RFC3339, ints via strconv, etc.); the store makes
	// no interpretive claim on the payload.
	SetMeta(ctx context.Context, key, value string) error

	// ResetForEstimatorBump clears every row this store persists and reseeds
	// stats_meta.estimator_version to the current package-level
	// EstimatorVersion constant. Called by the stats.5 backfiller when it
	// detects the persisted estimator_version is behind the code's version;
	// after ResetForEstimatorBump returns, the backfill pass replays every
	// session from scratch to rebuild deltas under the new estimator.
	//
	// schema_version, last_full_backfill_at, and any future non-cursor meta
	// rows are preserved. Callers must hold no concurrent flush in flight —
	// backfillers arrange this via the InProgress guard before calling.
	ResetForEstimatorBump(ctx context.Context) error

	// Close releases the underlying storage. Subsequent calls must return
	// ErrClosed.
	Close() error
}

// SessionContext carries the constant, session-scoped labels the aggregator
// needs to key deltas correctly. Callers (stats.4 SessionObserver adapter,
// stats.5 backfiller) construct it once per session and pass it in on every
// Ingest call, so the aggregator itself never has to reach into the session
// store or the web layer to look up workspace / working-dir / ACP-server for
// the source session.
type SessionContext struct {
	// SessionID identifies the source session.
	SessionID string
	// Workspace is the workspace UUID (empty for workspace-less sessions).
	Workspace string
	// WorkingDir is the session's working directory (informational; v1 stores
	// empty strings in the working_dir column but the schema reserves it).
	WorkingDir string
	// ACPServer is the ACP server name for the session (same informational
	// note as WorkingDir).
	ACPServer string
	// BaselineModel is the user's intended model for this session (untouched
	// by per-prompt overrides). The aggregator seeds its per-session
	// currentModel from this value; subsequent session_change(kind=model)
	// events update it. Empty when no baseline has been set — token deltas
	// then land with Model="" until an explicit model change fires.
	BaselineModel string
	// BaselineModelGetter, when non-nil, is called at first-event seed time
	// to obtain the current baseline. Preferred over BaselineModel when set
	// and returning a non-empty value — used by the live-path observer,
	// which is attached BEFORE the ACP init callback seeds the session's
	// baseline (mitto-9yl). The backfill path leaves this nil and continues
	// to use BaselineModel read from persisted metadata.
	BaselineModelGetter func() string
}

// Aggregator turns a stream of session events (from the live SessionObserver
// adapter or the backfill replayer) into batched Deltas, buffered by (session,
// workspace, bucket) and persisted atomically with a per-session Cursor
// advance. The concrete implementation lives in aggregator.go.
type Aggregator interface {
	// Ingest queues one event for aggregation. Non-blocking: if the internal
	// buffer is full the event is dropped and the drop counter is incremented
	// (see Dropped). Callers must never block the observer path on stats.
	//
	// sc carries the constant session-scoped labels (workspace, working dir,
	// ACP server); ev is the raw session event to classify.
	Ingest(sc SessionContext, ev session.Event)

	// Flush drains all buffered deltas to the underlying Store. Called on the
	// 10s / 500-event boundary and at shutdown.
	Flush(ctx context.Context) error

	// Dropped returns the total number of events dropped due to a full ingest
	// buffer since the aggregator was created. Exposed for the stats.9
	// operational surface and for tests.
	Dropped() uint64

	// Close flushes any pending work and releases resources. Safe to call
	// more than once.
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

	// Start launches the periodic background loop: a delayed first Run after
	// startup, then every configured Interval. Callers may still invoke Run
	// directly; Start is optional wiring for the server's happy path.
	Start(ctx context.Context)

	// Close signals the periodic loop to stop and blocks until it has
	// returned. Idempotent. A Run in progress at Close time completes before
	// Close returns.
	Close() error
}
