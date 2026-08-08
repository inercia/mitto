package stats

// BeadsSource (mitto-5rm6.2): periodic full re-derivation of beads throughput
// stats (MetricBeadsOpened / MetricBeadsClosed / MetricBeadsCycleSecondsSum /
// MetricBeadsCycleClosedCount) from a `bd list` snapshot per workspace.
//
// Unlike the Backfiller (event-stream replay keyed by a per-session cursor),
// beads are state, not an event stream: every pass is a pure function of the
// CURRENT snapshot, with no cursor. Idempotency and self-healing (a
// reopened/deleted/moved bead) come from Store.ReplaceDeltas, which replaces
// the entire beadsMetrics window atomically rather than upserting.
//
// A single ReplaceDeltas call covers every workspace's data in one
// transaction, because ReplaceDeltas' DELETE is scoped by (metric, ts_bucket)
// only — not by workspace (see stats.go). Consequently a Run pass is
// all-or-nothing: if any workspace's `bd list` fails or returns unparseable
// JSON, the whole pass aborts WITHOUT writing, so a transient failure in one
// workspace cannot wipe another workspace's previously-good data.
//
// Wiring into internal/web (server startup + beads-watcher-driven debounced
// refresh) is mitto-5rm6.3, not this file.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// metaKeyLastBeadsPass records the RFC3339 wall-clock time of the last
// successful BeadsSource pass, mirroring metaKeyLastFullBackfill.
const metaKeyLastBeadsPass = "last_beads_pass_at"

// beadsWindowFrom is the lower bound passed to Store.ReplaceDeltas: fixed at
// the Unix epoch so every historical beads_* row falls inside the window
// regardless of how old the oldest bead in any workspace is. ReplaceDeltas
// only uses this to scope its DELETE and to validate each delta's TSBucket
// is in-window, so it costs nothing to keep it maximally wide.
var beadsWindowFrom = time.Unix(0, 0).UTC()

// beadsMetrics lists every metric this source writes, used to scope
// ReplaceDeltas's DELETE window so a stale row (deleted/reopened/moved bead)
// is evicted rather than left orphaned.
var beadsMetrics = []string{
	MetricBeadsOpened,
	MetricBeadsClosed,
	MetricBeadsCycleSecondsSum,
	MetricBeadsCycleClosedCount,
}

// BeadsLister exposes just enough of beads.Client for this source: a raw
// JSON listing of every bead in a workspace directory (the same payload
// beads.Client.List returns from `bd list --json --all -n 0`). Kept as a
// dedicated, narrow interface — mirroring SessionLister — so tests inject a
// fake without pulling in internal/beads, and so this package keeps its
// no-import constraint on internal/web / internal/conversation.
type BeadsLister interface {
	List(ctx context.Context, dir string) ([]byte, error)
}

// BeadsWorkspace identifies one workspace to scan: its UUID (used as the
// Delta.Workspace attribution) and its working directory (passed to
// BeadsLister.List).
type BeadsWorkspace struct {
	UUID string
	Dir  string
}

// BeadsWorkspaceLister returns the current set of workspaces to scan for
// beads. Injected so this package stays decoupled from
// internal/conversation.SessionManager; the mitto-5rm6.3 wiring implements
// this by reusing the same workspace enumeration + dedup as
// server.go's getBeadsWatchDirs.
type BeadsWorkspaceLister func() []BeadsWorkspace

// beadsItem is the minimal shape read from `bd list --json --all` (see
// internal/beads/cli.go's List) needed for the throughput/cycle-time fold.
//
// Metadata is bd-controlled arbitrary JSON (mitto-049d): bd is free to write
// non-string values (e.g. an integer jira_synced_comments counter) alongside
// the string RFC3339 markers this fold actually reads, so the field is typed
// as map[string]json.RawMessage rather than map[string]string — a
// map[string]string decode fails the ENTIRE json.Unmarshal of the workspace
// snapshot the moment any single bead anywhere carries a non-string value.
// Use metadataString to read a key as a string, degrading to "" (no marker)
// for absent/null/non-string values instead of aborting the parse.
type beadsItem struct {
	ID        string                     `json:"id"`
	Status    string                     `json:"status"`
	CreatedAt string                     `json:"created_at"`
	ClosedAt  string                     `json:"closed_at,omitempty"`
	StartedAt string                     `json:"started_at,omitempty"`
	Metadata  map[string]json.RawMessage `json:"metadata,omitempty"`
}

// metadataString returns m[key] decoded as a JSON string, or "" for every
// other case: absent key, nil map, JSON null, or a non-string JSON value
// (number/bool/object/array). Deliberately does not use fmt.Sprint on the
// raw value, which would coerce a number like 55011457 into "5.5011457e+07"-
// style garbage; the two callers (work_started_at / claimed_at) only ever
// expect RFC3339 strings, so any other shape should read the same as "no
// marker" (see foldItem's exclude-from-cycle-time path).
func metadataString(m map[string]json.RawMessage, key string) string {
	raw, ok := m[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// BeadsSourceOptions configures NewBeadsSource. Zero-valued fields fall back
// to the defaults documented on each field.
type BeadsSourceOptions struct {
	// Interval is the period between periodic passes. Default: 6h, matching
	// BackfillerOptions.Interval. A negative value disables the periodic
	// loop entirely (Start becomes a no-op); zero also falls back to 6h.
	Interval time.Duration

	// StartupDelay is how long Start waits before firing the first pass.
	// Default: 30s (matches BackfillerOptions.StartupDelay).
	StartupDelay time.Duration

	// Now returns the current wall-clock time; overridable for tests.
	Now func() time.Time

	// Logger is optional; when non-nil the source emits INFO on each pass
	// boundary and WARN on parse issues / per-pass failures.
	Logger *slog.Logger
}

func (o BeadsSourceOptions) withDefaults() BeadsSourceOptions {
	if o.Interval == 0 {
		o.Interval = 6 * time.Hour
	}
	if o.StartupDelay == 0 {
		o.StartupDelay = 30 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// BeadsSource periodically re-derives beads throughput/cycle-time stats from
// a full bd list snapshot per workspace. Thread-safe: Run coalesces
// concurrent/overlapping calls (startup, periodic, and watcher-debounced
// invocations, mitto-5rm6.3) so a burst of triggers costs at most two full
// passes instead of one per trigger (mitto-2o5e). See Run's doc comment.
type BeadsSource struct {
	store      Store
	lister     BeadsLister
	workspaces BeadsWorkspaceLister
	opts       BeadsSourceOptions

	inProgress atomic.Bool
	pending    atomic.Bool

	closeOnce sync.Once
	closed    chan struct{}
	done      chan struct{}
}

// NewBeadsSource constructs a BeadsSource. store, lister, and workspaces must
// not be nil. The returned source has not started its periodic loop yet;
// call Start to begin, or invoke Run directly (e.g. from server startup or a
// watcher-debounced refresh).
func NewBeadsSource(store Store, lister BeadsLister, workspaces BeadsWorkspaceLister, opts BeadsSourceOptions) *BeadsSource {
	if store == nil {
		panic("stats: NewBeadsSource: store is nil")
	}
	if lister == nil {
		panic("stats: NewBeadsSource: lister is nil")
	}
	if workspaces == nil {
		panic("stats: NewBeadsSource: workspaces is nil")
	}
	return &BeadsSource{
		store:      store,
		lister:     lister,
		workspaces: workspaces,
		opts:       opts.withDefaults(),
		closed:     make(chan struct{}),
		done:       make(chan struct{}),
	}
}

// InProgress reports whether a pass is currently executing.
func (s *BeadsSource) InProgress() bool { return s.inProgress.Load() }

// Start launches the periodic loop: sleeps StartupDelay, runs once, then
// runs every Interval. Safe to call at most once; a negative Interval
// disables the loop but leaves InProgress/Run functional.
func (s *BeadsSource) Start(ctx context.Context) {
	if s.opts.Interval < 0 {
		close(s.done)
		return
	}
	go s.loop(ctx)
}

// Close signals the periodic loop to stop and blocks until it has returned.
// Safe to call multiple times.
func (s *BeadsSource) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
		<-s.done
	})
	return nil
}

// loop is the periodic timer. Waits StartupDelay, fires Run, then loops on
// Interval. Exits on ctx.Done or Close.
func (s *BeadsSource) loop(ctx context.Context) {
	defer close(s.done)

	select {
	case <-s.closed:
		return
	case <-ctx.Done():
		return
	case <-time.After(s.opts.StartupDelay):
	}

	if err := s.Run(ctx); err != nil {
		s.logWarn("startup beads source pass failed", "error", err)
	}

	if s.opts.Interval <= 0 {
		return
	}
	ticker := time.NewTicker(s.opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.closed:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Run(ctx); err != nil {
				s.logWarn("periodic beads source pass failed", "error", err)
			}
		}
	}
}

// beadsBucketKey identifies one (hour bucket, workspace) accumulator.
type beadsBucketKey struct {
	bucket    int64 // UTC unix seconds, hour-truncated
	workspace string
}

// beadsBucketAgg accumulates the four beads metrics for one bucket/workspace.
type beadsBucketAgg struct {
	opened           int64
	closed           int64
	cycleSecondsSum  int64
	cycleClosedCount int64
}

// Run performs one or more full passes, each of which lists every
// workspace's current bd snapshot, folds every bead into hourly buckets, and
// replaces the entire beadsMetrics window in one Store.ReplaceDeltas call.
//
// Coalescing guard (mitto-2o5e): the CompareAndSwap on inProgress is taken
// BEFORE any work begins, not after acquiring a mutex. The previous
// implementation locked a mutex first and only then checked/set inProgress,
// which meant every waiter acquired the mutex only after the prior holder's
// deferred inProgress.Store(false) had already run (defers execute LIFO, so
// Store(false) always precedes Unlock) — the CAS could therefore never
// observe "true", the guard never coalesced anything, and N overlapping
// triggers ran N full sequential passes. Here, a caller that loses the CAS
// simply records `pending` and returns immediately without touching the
// lister/store at all; the winning caller, after each pass, checks pending
// and loops for exactly one more pass if anything arrived while it was
// running. A burst of N overlapping triggers therefore costs at most two
// full passes — the one already in flight, plus one more that captures
// everything queued up behind it — instead of N. (There is a small
// unavoidable race where a trigger arriving in the narrow window between the
// final pending check and inProgress being cleared is missed; the periodic
// ticker and/or the next watcher event are the backstop for that case.)
func (s *BeadsSource) Run(ctx context.Context) error {
	if !s.inProgress.CompareAndSwap(false, true) {
		s.pending.Store(true)
		return nil
	}
	defer s.inProgress.Store(false)

	for {
		s.pending.Store(false)
		if err := s.runOnce(ctx); err != nil {
			return err
		}
		if !s.pending.Load() {
			return nil
		}
	}
}

// runOnce performs exactly one full pass. Split out of Run so the
// coalescing loop there can re-invoke it without re-running the CAS gate.
//
// All-or-nothing: if any workspace's List or JSON parse fails, runOnce
// returns that error immediately WITHOUT calling ReplaceDeltas, so a
// transient failure never wipes previously-good data for an unrelated
// workspace (see the package doc comment for why a partial write is unsafe
// here).
func (s *BeadsSource) runOnce(ctx context.Context) error {
	workspaces := s.workspaces()
	s.logInfo("beads source pass starting", "workspaces", len(workspaces))

	agg := make(map[beadsBucketKey]*beadsBucketAgg)
	for _, ws := range workspaces {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.closed:
			return nil
		default:
		}
		if ws.Dir == "" {
			continue
		}
		raw, err := s.lister.List(ctx, ws.Dir)
		if err != nil {
			return fmt.Errorf("stats: beads source: list %s: %w", ws.Dir, err)
		}
		var items []beadsItem
		if err := json.Unmarshal(raw, &items); err != nil {
			return fmt.Errorf("stats: beads source: parse %s: %w", ws.Dir, err)
		}
		for _, it := range items {
			s.foldItem(it, ws.UUID, agg)
		}
	}

	now := s.opts.Now().UTC()
	to := now.Truncate(time.Hour).Add(time.Hour) // exclusive; covers the current partial hour
	deltas := make([]Delta, 0, len(agg)*4)
	for key, a := range agg {
		bucket := time.Unix(key.bucket, 0).UTC()
		if a.opened != 0 {
			deltas = append(deltas, Delta{TSBucket: bucket, Metric: MetricBeadsOpened, SessionID: BeadsSentinelSessionID, Workspace: key.workspace, Value: a.opened})
		}
		if a.closed != 0 {
			deltas = append(deltas, Delta{TSBucket: bucket, Metric: MetricBeadsClosed, SessionID: BeadsSentinelSessionID, Workspace: key.workspace, Value: a.closed})
		}
		if a.cycleSecondsSum != 0 {
			deltas = append(deltas, Delta{TSBucket: bucket, Metric: MetricBeadsCycleSecondsSum, SessionID: BeadsSentinelSessionID, Workspace: key.workspace, Value: a.cycleSecondsSum})
		}
		if a.cycleClosedCount != 0 {
			deltas = append(deltas, Delta{TSBucket: bucket, Metric: MetricBeadsCycleClosedCount, SessionID: BeadsSentinelSessionID, Workspace: key.workspace, Value: a.cycleClosedCount})
		}
	}

	if err := s.store.ReplaceDeltas(ctx, beadsMetrics, beadsWindowFrom, to, deltas); err != nil {
		return fmt.Errorf("stats: beads source: replace deltas: %w", err)
	}
	if err := s.store.SetMeta(ctx, metaKeyLastBeadsPass, now.Format(time.RFC3339)); err != nil {
		s.logWarn("beads source meta stamp failed", "error", err)
	}
	s.logInfo("beads source pass done", "buckets", len(agg), "deltas", len(deltas))
	return nil
}

// foldItem folds one bead into agg. created_at always contributes a
// MetricBeadsOpened increment (skipped, with a warning, if unparseable).
// closed_at only contributes MetricBeadsClosed (and, if a work-start marker
// resolves, the cycle-time pair) when status is "closed" and closed_at is
// present and parseable.
//
// Marker precedence (non-negotiable, per the mitto-5rm6 plan):
// metadata.work_started_at ?? metadata.claimed_at ?? started_at. A closed
// bead with none of the three is excluded from the cycle-time series
// entirely — no created_at lead-time fallback, since that would silently mix
// backlog dwell time into cycle time.
func (s *BeadsSource) foldItem(it beadsItem, workspace string, agg map[beadsBucketKey]*beadsBucketAgg) {
	if it.CreatedAt != "" {
		if created, err := time.Parse(time.RFC3339, it.CreatedAt); err == nil {
			beadsBucketFor(agg, created, workspace).opened++
		} else {
			s.logWarn("beads source: unparseable created_at", "id", it.ID, "value", it.CreatedAt, "error", err)
		}
	}

	if it.Status != "closed" || it.ClosedAt == "" {
		return
	}
	closed, err := time.Parse(time.RFC3339, it.ClosedAt)
	if err != nil {
		s.logWarn("beads source: unparseable closed_at", "id", it.ID, "value", it.ClosedAt, "error", err)
		return
	}
	a := beadsBucketFor(agg, closed, workspace)
	a.closed++

	startStr := metadataString(it.Metadata, "work_started_at")
	if startStr == "" {
		startStr = metadataString(it.Metadata, "claimed_at")
	}
	if startStr == "" {
		startStr = it.StartedAt
	}
	if startStr == "" {
		return
	}
	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		s.logWarn("beads source: unparseable work-start marker", "id", it.ID, "value", startStr, "error", err)
		return
	}
	cycle := closed.Sub(start)
	if cycle < 0 {
		// Clock skew or stale metadata (e.g. a marker surviving a reopen).
		// Exclude rather than contribute a negative sum.
		s.logWarn("beads source: negative cycle time excluded", "id", it.ID, "start", start, "closed", closed)
		return
	}
	a.cycleSecondsSum += int64(cycle.Seconds())
	a.cycleClosedCount++
}

// beadsBucketFor returns the accumulator for (hour bucket of ts, workspace),
// creating it on first access.
func beadsBucketFor(agg map[beadsBucketKey]*beadsBucketAgg, ts time.Time, workspace string) *beadsBucketAgg {
	key := beadsBucketKey{bucket: ts.UTC().Truncate(time.Hour).Unix(), workspace: workspace}
	a := agg[key]
	if a == nil {
		a = &beadsBucketAgg{}
		agg[key] = a
	}
	return a
}

// logInfo emits an INFO line via the configured logger, or silently drops it
// when no logger was provided.
func (s *BeadsSource) logInfo(msg string, args ...any) {
	if s.opts.Logger != nil {
		s.opts.Logger.Info(msg, args...)
	}
}

// logWarn is the WARN counterpart of logInfo.
func (s *BeadsSource) logWarn(msg string, args ...any) {
	if s.opts.Logger != nil {
		s.opts.Logger.Warn(msg, args...)
	}
}
