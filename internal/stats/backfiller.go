package stats

// Backfiller (mitto-a86b.5): idempotent replay of session events into the
// aggregator/store pipeline. Two goals:
//
//   1. Repair drift between the live path (aggregator drops on full buffer,
//      short crashes, missed observer events during resume) and events.jsonl.
//   2. Rebuild every stats row from scratch when the token estimator changes,
//      keyed off stats_meta.estimator_version vs the code's EstimatorVersion.
//
// Idempotency comes from the per-session Cursor: `Run` reads each session's
// persisted LastEventSeq and only re-ingests events with Seq > that value.
// The aggregator's UpsertDeltasWithCursor collapses in-batch duplicates and
// the SQLite MAX() cursor logic means a concurrent live flush during a
// backfill pass can only advance the cursor forward, never rewind it.
//
// Never uses wall-clock time for bucket keys: replays read the event's
// Timestamp field so a suspend/gap doesn't create phantom "now" buckets.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// backfillReadChunk is the page size used when calling ReadEventsFrom during
// a replay. Kept small enough that a single page fits comfortably in memory
// even for sessions with tens of thousands of events, but large enough that
// syscall / JSON-decode overhead per page is amortised.
const backfillReadChunk = 500

// metaKeyLastFullBackfill records the RFC3339 wall-clock time of the last
// successful Run pass. Exposed to the /api/dashboard/timeseries handler in
// stats.7 so the UI can annotate "data as of ...".
const metaKeyLastFullBackfill = "last_full_backfill_at"

// SessionLister exposes just enough of session.Store for the backfiller: a
// listing of every persisted session and a chunked event reader. Kept as a
// dedicated interface so tests can inject a lightweight fake without pulling
// in the whole session store surface.
type SessionLister interface {
	List() ([]session.Metadata, error)
	ReadEventsFrom(sessionID string, afterSeq int64, limit int) ([]session.Event, error)
}

// WorkspaceResolver returns the SessionContext (workspace UUID + labels) for
// a persisted session. The wiring layer (internal/web) implements this by
// consulting the workspace registry via working_dir + acp_server; the stats
// package keeps the shape narrow so it never has to import conversation.
//
// Returning an empty Workspace is legal — sessions predating workspace UUIDs
// are still worth counting; queries that filter by workspace will simply not
// see them.
type WorkspaceResolver func(meta session.Metadata) SessionContext

// BackfillerOptions configures NewBackfiller. Zero-valued fields fall back
// to the defaults documented on each field.
type BackfillerOptions struct {
	// Interval is the period between periodic Run passes. Default: 6h.
	// A zero or negative value disables the periodic loop; callers still
	// invoke Run manually (e.g. from startup) but no goroutine is spawned.
	Interval time.Duration

	// StartupDelay is how long Start waits before firing the first Run.
	// Default: 30s — long enough that server startup (workspace scan,
	// prompt cache warm) doesn't compete with a full replay.
	StartupDelay time.Duration

	// ChunkSize overrides backfillReadChunk when non-zero.
	ChunkSize int

	// Now returns the current wall-clock time; overridable for tests.
	Now func() time.Time

	// Logger is optional; when non-nil the backfiller emits INFO on each
	// pass boundary (start/finish + estimator-bump detection) so operators
	// can see progress in mitto.log.
	Logger *slog.Logger
}

func (o BackfillerOptions) withDefaults() BackfillerOptions {
	if o.Interval == 0 {
		o.Interval = 6 * time.Hour
	}
	if o.StartupDelay == 0 {
		o.StartupDelay = 30 * time.Second
	}
	if o.ChunkSize <= 0 {
		o.ChunkSize = backfillReadChunk
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// backfiller implements Backfiller. Thread-safe: Run serialises on runMu so
// startup + periodic + manual invocations never overlap, avoiding the "two
// passes racing on the same cursor" scenario the epic warns about.
type backfiller struct {
	store    Store
	agg      Aggregator
	lister   SessionLister
	resolver WorkspaceResolver
	opts     BackfillerOptions

	runMu      sync.Mutex  // held for the duration of one Run pass
	inProgress atomic.Bool // exposed via InProgress()

	closeOnce sync.Once
	closed    chan struct{}
	done      chan struct{}
}

// NewBackfiller constructs a Backfiller. store must not be nil. When agg is
// nil the backfiller falls back to Store.UpsertDeltasWithCursor batches
// classified inline by re-using the aggregator's fold rules — but that path
// is not v1: v1 always pairs the backfiller with the same aggregator the
// live path uses, so classification stays in one place.
//
// The returned Backfiller has not started its periodic loop yet; call Start
// to begin. Run may still be invoked directly (e.g. from server startup).
func NewBackfiller(store Store, agg Aggregator, lister SessionLister, resolver WorkspaceResolver, opts BackfillerOptions) Backfiller {
	if store == nil {
		panic("stats: NewBackfiller: store is nil")
	}
	if agg == nil {
		panic("stats: NewBackfiller: aggregator is nil (v1 requires a live-path aggregator)")
	}
	if lister == nil {
		panic("stats: NewBackfiller: lister is nil")
	}
	if resolver == nil {
		// Fallback: emit sessions with empty workspace. Legal but log-worthy.
		resolver = func(m session.Metadata) SessionContext {
			return SessionContext{SessionID: m.SessionID, WorkingDir: m.WorkingDir, ACPServer: m.ACPServer}
		}
	}
	return &backfiller{
		store:    store,
		agg:      agg,
		lister:   lister,
		resolver: resolver,
		opts:     opts.withDefaults(),
		closed:   make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start launches the periodic loop: sleeps StartupDelay, runs once, then
// runs every Interval. Safe to call at most once; subsequent calls are
// no-ops. Interval == 0 (via opts) disables the periodic loop but leaves
// InProgress/Run functional.
func (b *backfiller) Start(ctx context.Context) {
	if b.opts.Interval < 0 {
		close(b.done)
		return
	}
	go b.loop(ctx)
}

// Close signals the periodic loop to stop and blocks until it has returned.
// Safe to call multiple times; the second call short-circuits on closeOnce.
func (b *backfiller) Close() error {
	b.closeOnce.Do(func() {
		close(b.closed)
		<-b.done
	})
	return nil
}

// InProgress reports whether a Run pass is currently executing. The stats.7
// dashboard handler exposes this so the frontend can render a
// "backfill_in_progress" badge on partial data.
func (b *backfiller) InProgress() bool { return b.inProgress.Load() }

// loop is the periodic timer. Waits StartupDelay, fires Run, then loops on
// Interval. Exits on ctx.Done or Close.
func (b *backfiller) loop(ctx context.Context) {
	defer close(b.done)

	select {
	case <-b.closed:
		return
	case <-ctx.Done():
		return
	case <-time.After(b.opts.StartupDelay):
	}

	// First pass at startup.
	if err := b.Run(ctx); err != nil {
		b.logWarn("startup backfill failed", "error", err)
	}

	if b.opts.Interval <= 0 {
		return
	}
	ticker := time.NewTicker(b.opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-b.closed:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := b.Run(ctx); err != nil {
				b.logWarn("periodic backfill failed", "error", err)
			}
		}
	}
}

// Run performs one full pass over every persisted session. Idempotent:
// events at or before each session's stored cursor are skipped. Concurrent
// Run calls serialise on runMu (later callers wait rather than skip so a
// tester can drive the pass boundary deterministically).
func (b *backfiller) Run(ctx context.Context) error {
	b.runMu.Lock()
	defer b.runMu.Unlock()

	if !b.inProgress.CompareAndSwap(false, true) {
		// Guarded by runMu above so this branch is effectively unreachable,
		// but the atomic makes InProgress safe to read from other goroutines.
		return nil
	}
	defer b.inProgress.Store(false)

	// Estimator-version gate: if the persisted estimator_version is behind
	// the code, wipe every row and re-ingest from scratch. Detect before
	// touching sessions so the cursor read below sees a clean slate.
	if err := b.maybeBumpEstimator(ctx); err != nil {
		return fmt.Errorf("stats: backfill estimator gate: %w", err)
	}

	metas, err := b.lister.List()
	if err != nil {
		return fmt.Errorf("stats: backfill list sessions: %w", err)
	}
	b.logInfo("backfill pass starting", "sessions", len(metas))

	var (
		replayed int
		skipped  int
		firstErr error
	)
	for _, m := range metas {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.closed:
			return nil
		default:
		}

		if m.SessionID == "" {
			continue
		}
		did, err := b.replaySession(ctx, m)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			b.logWarn("backfill session failed", "session_id", m.SessionID, "error", err)
			continue
		}
		if did {
			replayed++
		} else {
			skipped++
		}
	}

	// Ensure the aggregator's per-session accumulators land in the store
	// before we stamp last_full_backfill_at: without this flush, a crash
	// between "aggregator has buffered deltas" and "next timer tick" would
	// leave the meta row pointing at data that isn't durable yet.
	if err := b.agg.Flush(ctx); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("stats: backfill final flush: %w", err)
	}

	if firstErr == nil {
		if err := b.store.SetMeta(ctx, metaKeyLastFullBackfill,
			b.opts.Now().UTC().Format(time.RFC3339)); err != nil {
			b.logWarn("backfill meta stamp failed", "error", err)
		}
	}
	b.logInfo("backfill pass done",
		"replayed", replayed, "skipped_up_to_date", skipped,
		"error", firstErr)
	return firstErr
}

// replaySession pages through one session's events.jsonl from its stored
// cursor forward. Returns (replayed=true, nil) when at least one event was
// forwarded; (false, nil) when the session was already up to date; and any
// non-transient read error otherwise.
func (b *backfiller) replaySession(ctx context.Context, m session.Metadata) (bool, error) {
	cur, err := b.store.GetCursor(ctx, m.SessionID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return false, err
	}
	afterSeq := cur.LastEventSeq

	sc := b.resolver(m)
	// The resolver may return an empty SessionID (e.g. for a fallback
	// implementation); the aggregator drops those anyway, but the
	// SessionContext we own is authoritative — inject the metadata id here
	// so foldOwned records the right session.
	sc.SessionID = m.SessionID

	replayed := false
	for {
		select {
		case <-ctx.Done():
			return replayed, ctx.Err()
		case <-b.closed:
			return replayed, nil
		default:
		}

		events, err := b.lister.ReadEventsFrom(m.SessionID, afterSeq, b.opts.ChunkSize)
		if err != nil {
			// A missing events file is not fatal — the session was created
			// but never wrote a prompt (very short-lived). Treat as up to date.
			if errors.Is(err, session.ErrSessionNotFound) {
				return replayed, nil
			}
			return replayed, fmt.Errorf("read events: %w", err)
		}
		if len(events) == 0 {
			return replayed, nil
		}
		for _, ev := range events {
			b.agg.Ingest(sc, ev)
			if ev.Seq > afterSeq {
				afterSeq = ev.Seq
			}
			replayed = true
		}
		// Fewer than ChunkSize events → we're at end of file.
		if len(events) < b.opts.ChunkSize {
			return replayed, nil
		}
	}
}

// maybeBumpEstimator inspects stats_meta.estimator_version and, if it is
// behind the code's EstimatorVersion, calls Store.ResetForEstimatorBump so
// the pending Run rebuilds every row under the new estimator. First-boot
// (no meta row yet) seeds the meta row without wiping — there is nothing
// to rebuild yet and every subsequent event will carry the current version.
func (b *backfiller) maybeBumpEstimator(ctx context.Context) error {
	v, err := b.store.GetMeta(ctx, "estimator_version")
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if errors.Is(err, ErrNotFound) {
		// Seed but don't reset — a fresh DB has no stale rows.
		return b.store.SetMeta(ctx, "estimator_version", strconv.Itoa(EstimatorVersion))
	}
	persisted, parseErr := strconv.Atoi(v)
	if parseErr != nil {
		// Corrupt meta value — treat as unknown and reset to be safe.
		b.logWarn("estimator_version meta unreadable; forcing reset", "value", v, "error", parseErr)
		return b.store.ResetForEstimatorBump(ctx)
	}
	if persisted >= EstimatorVersion {
		return nil
	}
	b.logInfo("estimator version bump detected; resetting stats",
		"persisted", persisted, "current", EstimatorVersion)
	return b.store.ResetForEstimatorBump(ctx)
}

// logInfo emits an INFO line via the configured logger, or silently drops
// it when no logger was provided (matches the "opts.Logger optional" contract).
func (b *backfiller) logInfo(msg string, args ...any) {
	if b.opts.Logger != nil {
		b.opts.Logger.Info(msg, args...)
	}
}

// logWarn is the WARN counterpart of logInfo.
func (b *backfiller) logWarn(msg string, args ...any) {
	if b.opts.Logger != nil {
		b.opts.Logger.Warn(msg, args...)
	}
}

// Compile-time assertion that backfiller satisfies the Backfiller contract.
var _ Backfiller = (*backfiller)(nil)
