package stats

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// aggregatorItem carries a single event through the ingest channel together
// with the session-scoped labels needed to key its deltas.
type aggregatorItem struct {
	sc SessionContext
	ev session.Event
}

// AggregatorOptions configures a NewAggregator call. Zero-valued fields fall
// back to the defaults documented on each field.
type AggregatorOptions struct {
	// FlushInterval is the timer-based flush period. Default: 10s.
	FlushInterval time.Duration
	// MaxBatch is the count-based flush threshold: once this many events are
	// buffered internally the aggregator flushes immediately, without waiting
	// for the timer. Default: 500.
	MaxBatch int
	// BufferSize is the capacity of the ingest channel between the observer
	// path and the background goroutine. Events are dropped (and counted)
	// when the channel is full. Default: 4096.
	BufferSize int
	// FlushTimeout bounds a single Store.UpsertDeltasWithCursor call.
	// Default: 5s.
	FlushTimeout time.Duration
	// Now returns the current wall-clock time; overridable for tests. The
	// aggregator uses it only for cursor UpdatedAt — bucket keys always come
	// from event timestamps so replays remain deterministic.
	Now func() time.Time
	// FileSizeResolver optionally resolves a session.FileRef to its byte
	// size so the aggregator can apply the spec's size-based token formula
	// (see EstimateTokensFile). When nil the aggregator falls back to
	// EstimateTokensFileRef, a name-length proxy. stats.4/stats.5 wire this
	// through to session.Store without pulling I/O into the stats package.
	FileSizeResolver func(sessionID, fileID string) int64
}

func (o AggregatorOptions) withDefaults() AggregatorOptions {
	if o.FlushInterval <= 0 {
		o.FlushInterval = 10 * time.Second
	}
	if o.MaxBatch <= 0 {
		o.MaxBatch = 500
	}
	if o.BufferSize <= 0 {
		o.BufferSize = 4096
	}
	if o.FlushTimeout <= 0 {
		o.FlushTimeout = 5 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// bucketKey identifies the tuple (session, workspace, bucket, metric, model)
// inside the aggregator's in-memory buffer. Model is empty for metrics that
// are not model-attributable so those buckets remain merged across models.
type bucketKey struct {
	sessionID string
	workspace string
	bucket    int64 // UTC unix seconds at the top of the hour
	metric    string
	model     string
}

// sessionAccum tracks the per-session cursor advance that must be committed
// atomically with the deltas for that session.
type sessionAccum struct {
	sc           SessionContext
	lastSeq      int64
	lastAt       time.Time
	sawUserAgent bool   // was there any agent activity since the last user prompt (turn heuristic)
	currentModel string // active model attribution for token deltas
}

// sessionChangeKindModel is the SessionChangeData.Kind value that signals a
// model switch. Canonical source: internal/conversation.ConfigOptionCategoryModel
// ("model"). Duplicated here as a string literal to avoid a reverse import
// from the stats package into conversation.
const sessionChangeKindModel = "model"

// flushReq is a synchronous flush request from an external caller. The
// background goroutine performs the flush and closes reply to unblock the
// caller. Keeping flush ownership single-goroutine avoids any interleaving
// between fold (from the channel) and flush (from Flush/Close).
type flushReq struct {
	ctx   context.Context
	reply chan error
}

// aggregator implements the Aggregator interface. It owns a single background
// goroutine that drains the ingest channel, folds events into bucket counters,
// and calls Store.UpsertDeltasWithCursor on the flush cadence.
//
// Ownership discipline: every mutation of buckets/sessions/events happens on
// the background goroutine only. External callers (Flush, Close) submit a
// request over flushCh and wait for a reply; the goroutine handles it inline
// between channel receives, so there is no shared-map contention and no lock.
type aggregator struct {
	store Store
	opts  AggregatorOptions

	in      chan aggregatorItem
	flushCh chan *flushReq
	dropped atomic.Uint64

	// Owned exclusively by the background goroutine.
	buckets  map[bucketKey]int64
	sessions map[string]*sessionAccum
	events   int // total buffered events since last flush

	// Lifecycle.
	closeOnce sync.Once
	closed    chan struct{}
	done      chan struct{}
}

// NewAggregator creates and starts an aggregator draining into store. The
// caller must invoke Close on shutdown to flush pending work.
//
// The returned aggregator's Ingest is non-blocking: if the internal channel
// is full, the event is dropped and Dropped() is incremented. This is the
// only place in the stats pipeline where losing an event is acceptable —
// the stats.5 backfiller re-reads events.jsonl from the persisted cursor
// and will recover any drops on its next pass.
func NewAggregator(store Store, opts AggregatorOptions) Aggregator {
	opts = opts.withDefaults()
	a := &aggregator{
		store:    store,
		opts:     opts,
		in:       make(chan aggregatorItem, opts.BufferSize),
		flushCh:  make(chan *flushReq),
		buckets:  make(map[bucketKey]int64),
		sessions: make(map[string]*sessionAccum),
		closed:   make(chan struct{}),
		done:     make(chan struct{}),
	}
	go a.run()
	return a
}

// Ingest enqueues one event for aggregation. Non-blocking.
func (a *aggregator) Ingest(sc SessionContext, ev session.Event) {
	select {
	case <-a.closed:
		return
	default:
	}
	select {
	case a.in <- aggregatorItem{sc: sc, ev: ev}:
	default:
		a.dropped.Add(1)
	}
}

// Dropped returns the running count of events dropped due to a full buffer.
func (a *aggregator) Dropped() uint64 { return a.dropped.Load() }

// Flush drains any pending buffered work to the Store synchronously. Delegates
// to the background goroutine so buffer mutations remain single-owner.
func (a *aggregator) Flush(ctx context.Context) error {
	req := &flushReq{ctx: ctx, reply: make(chan error, 1)}
	select {
	case a.flushCh <- req:
	case <-a.closed:
		return nil
	}
	select {
	case err := <-req.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops the background goroutine and performs a final flush. Idempotent.
func (a *aggregator) Close() error {
	var err error
	a.closeOnce.Do(func() {
		close(a.closed)
		<-a.done
		// The goroutine drained + flushed on its way out.
		err = nil
	})
	return err
}

// run is the background goroutine's main loop. All buffer mutations happen
// here so there is no shared-state race with Flush / Close callers.
func (a *aggregator) run() {
	defer close(a.done)
	ticker := time.NewTicker(a.opts.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-a.closed:
			// Final drain: pull everything remaining in the ingest channel
			// (no more writers can race because Ingest short-circuits on the
			// closed channel first), then one last flush.
			a.drainChannelOwned()
			_ = a.flushOwned(context.Background())
			return
		case req := <-a.flushCh:
			a.drainChannelOwned()
			req.reply <- a.flushOwned(req.ctx)
		case <-ticker.C:
			_ = a.flushOwned(context.Background())
		case it := <-a.in:
			a.foldOwned(it)
			if a.events >= a.opts.MaxBatch {
				_ = a.flushOwned(context.Background())
			}
		}
	}
}

// drainChannelOwned pulls every currently-available item off the ingest
// channel without blocking, folding each one. Only called from run() so
// buffer mutation stays single-owner.
func (a *aggregator) drainChannelOwned() {
	for {
		select {
		case it := <-a.in:
			a.foldOwned(it)
		default:
			return
		}
	}
}

// flushOwned snapshots the current buffer maps, resets them, and pushes each
// session's deltas + cursor to the Store atomically. Errors are returned but
// do NOT resurrect the flushed state — the stats.5 backfiller will recover
// missed events by re-reading events.jsonl past the last successful cursor.
// Only called from run().
func (a *aggregator) flushOwned(ctx context.Context) error {
	if len(a.buckets) == 0 && len(a.sessions) == 0 {
		return nil
	}

	// Group deltas by session so each Store call is one atomic tx.
	perSession := make(map[string][]Delta, len(a.sessions))
	for k, v := range a.buckets {
		perSession[k.sessionID] = append(perSession[k.sessionID], Delta{
			TSBucket:  time.Unix(k.bucket, 0).UTC(),
			Metric:    k.metric,
			SessionID: k.sessionID,
			Workspace: k.workspace,
			Model:     k.model,
			Value:     v,
		})
	}

	now := a.opts.Now().UTC()
	var firstErr error
	for sid, acc := range a.sessions {
		cur := Cursor{
			SessionID:        sid,
			LastEventSeq:     acc.lastSeq,
			LastEventAt:      acc.lastAt,
			EstimatorVersion: EstimatorVersion,
			UpdatedAt:        now,
		}
		fctx, cancel := context.WithTimeout(ctx, a.opts.FlushTimeout)
		err := a.store.UpsertDeltasWithCursor(fctx, perSession[sid], cur)
		cancel()
		if err != nil && firstErr == nil {
			firstErr = err
		}
		delete(perSession, sid)
	}
	// Any deltas whose session never registered an accumulator (should not
	// happen — fold registers one) are dropped here; they cannot be flushed
	// without a cursor.

	a.buckets = make(map[bucketKey]int64)
	a.sessions = make(map[string]*sessionAccum)
	a.events = 0
	return firstErr
}

// foldOwned classifies one event and updates the in-memory buffer. Only
// called from run() so the maps stay single-owner.
func (a *aggregator) foldOwned(it aggregatorItem) {
	if it.sc.SessionID == "" {
		return
	}
	bucket := it.ev.Timestamp.UTC().Truncate(time.Hour).Unix()
	acc := a.sessions[it.sc.SessionID]
	if acc == nil {
		// Seed currentModel from the session's baseline so token deltas that
		// arrive before any session_change event are still attributed. A live
		// session_change(kind=model) — either from disk during backfill or
		// from the live SessionChangeObserver — overrides this at any time.
		acc = &sessionAccum{sc: it.sc, currentModel: it.sc.BaselineModel}
		a.sessions[it.sc.SessionID] = acc
	}
	if it.ev.Seq > acc.lastSeq {
		acc.lastSeq = it.ev.Seq
	}
	if it.ev.Timestamp.After(acc.lastAt) {
		acc.lastAt = it.ev.Timestamp
	}
	a.events++

	// inc increments a metric that is NOT model-attributable — bucketKey.model
	// stays empty so rows for these metrics collapse across models.
	inc := func(metric string, n int64) {
		if n <= 0 {
			return
		}
		k := bucketKey{
			sessionID: it.sc.SessionID,
			workspace: it.sc.Workspace,
			bucket:    bucket,
			metric:    metric,
		}
		a.buckets[k] += n
	}

	// incModel increments a model-attributable metric using the accumulator's
	// currentModel. Empty currentModel is fine — it maps to the "unknown"
	// bucket rather than being dropped.
	incModel := func(metric string, n int64) {
		if n <= 0 {
			return
		}
		k := bucketKey{
			sessionID: it.sc.SessionID,
			workspace: it.sc.Workspace,
			bucket:    bucket,
			metric:    metric,
			model:     acc.currentModel,
		}
		a.buckets[k] += n
	}

	switch it.ev.Type {
	case session.EventTypeUserPrompt:
		inc(MetricPrompts, 1)
		// A user_prompt that follows any agent activity closes the prior turn.
		if acc.sawUserAgent {
			inc(MetricAgentTurnsCompleted, 1)
			acc.sawUserAgent = false
		}
		if d, ok := it.ev.Data.(session.UserPromptData); ok {
			incModel(MetricInputTokensEst, EstimateTokensText(d.Message))
			for _, f := range d.Files {
				if a.opts.FileSizeResolver != nil {
					incModel(MetricInputTokensEst, EstimateTokensFile(a.opts.FileSizeResolver(it.sc.SessionID, f.ID)))
				} else {
					incModel(MetricInputTokensEst, EstimateTokensFileRef(f))
				}
			}
			incModel(MetricInputTokensEst, int64(len(d.Images))*ImageTokenCost)
		}
	case session.EventTypeAgentMessage:
		acc.sawUserAgent = true
		if d, ok := it.ev.Data.(session.AgentMessageData); ok {
			incModel(MetricOutputTokensEst, EstimateTokensText(d.Text))
		}
	case session.EventTypeAgentThought:
		acc.sawUserAgent = true
		if d, ok := it.ev.Data.(session.AgentThoughtData); ok {
			incModel(MetricOutputTokensEst, EstimateTokensText(d.Text))
		}
	case session.EventTypeToolCall:
		acc.sawUserAgent = true
		inc(MetricToolCallsTotal, 1)
		if d, ok := it.ev.Data.(session.ToolCallData); ok && IsMCPCall(d) {
			inc(MetricMCPCalls, 1)
		}
	case session.EventTypePermission:
		inc(MetricPermissionsPrompted, 1)
	case session.EventTypeError:
		inc(MetricErrors, 1)
	case session.EventTypeSessionChange:
		// State-only: adjust currentModel so future token deltas land in the
		// new model's bucket. No delta is emitted here — session_change is a
		// timeline marker, not a counter event.
		if d, ok := it.ev.Data.(session.SessionChangeData); ok && d.Kind == sessionChangeKindModel {
			acc.currentModel = d.Value
		}
	}
}

// v1 estimators / classifiers now live in classifier.go as exported helpers
// (EstimateTokensText / EstimateTokensFile / EstimateTokensFileRef /
// EstimateTokensImage / IsMCPCall) so the live SessionObserver (stats.4) and
// the backfill replayer (stats.5) can share the exact same heuristics.
// Bumping EstimatorVersion (stats.go) triggers a backfill recompute.
