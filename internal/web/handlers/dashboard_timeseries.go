package handlers

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/stats"
)

// tsCacheTTL is the fixed 30s freshness window for cached timeseries
// responses. Kept as a var only so tests can shorten it; treat as const.
var tsCacheTTL = 30 * time.Second

// v1MetricSet enumerates every metric name accepted by the ?metrics= query
// parameter. An empty/missing param resolves to all of these in a stable order.
var v1MetricSet = []string{
	stats.MetricInputTokensEst,
	stats.MetricOutputTokensEst,
	stats.MetricPrompts,
	stats.MetricAgentTurnsCompleted,
	stats.MetricToolCallsTotal,
	stats.MetricMCPCalls,
	stats.MetricPermissionsPrompted,
	stats.MetricErrors,
	stats.MetricBeadsOpened,
	stats.MetricBeadsClosed,
	stats.MetricBeadsCycleSecondsSum,
	stats.MetricBeadsCycleClosedCount,
}

// tsPoint is one (unix-seconds, value) datum in a series.
type tsPoint struct {
	T int64 `json:"t"`
	V int64 `json:"v"`
}

// tsRange is the resolved query window echoed back to the caller.
type tsRange struct {
	From          int64 `json:"from"`
	To            int64 `json:"to"`
	BucketSeconds int   `json:"bucket_seconds"`
}

// tsMeta carries subtle correctness signals for the chart UI.
type tsMeta struct {
	EstimatorVersion   string `json:"estimator_version"`
	BackfillInProgress bool   `json:"backfill_in_progress"`
	Note               string `json:"note"`
}

// tsResponse is the JSON payload of GET /api/dashboard/timeseries.
type tsResponse struct {
	Range  tsRange              `json:"range"`
	Series map[string][]tsPoint `json:"series"`
	Meta   tsMeta               `json:"meta"`
}

// timeseriesNote is the fixed disclaimer surfaced in meta.note. Kept short so
// the frontend can render it verbatim under the chart. The wording matches
// the epic-level bead body.
const timeseriesNote = "Token counts are length-based estimates from user_prompt / agent_message / agent_thought; not billed usage."

// timeseriesCache is a tiny per-handler cache that stores already-encoded JSON
// bodies keyed by the effective query parameters (plus the backfill flag).
// Modeled after internal/beads/cache.go but simpler: no singleflight, no
// per-workspace map, no counters — v1 traffic is tiny.
type timeseriesCache struct {
	mu    sync.Mutex
	ttl   time.Duration
	now   func() time.Time
	items map[string]tsCacheEntry
}

type tsCacheEntry struct {
	payload   []byte
	expiresAt time.Time
}

func newTimeseriesCache(ttl time.Duration) *timeseriesCache {
	return &timeseriesCache{ttl: ttl, now: time.Now, items: make(map[string]tsCacheEntry)}
}

// get returns the cached payload and true if the key exists and has not
// expired; otherwise nil, false. Callers must not mutate the returned slice.
func (c *timeseriesCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok || c.now().After(e.expiresAt) {
		return nil, false
	}
	return e.payload, true
}

// put stores payload under key with a fresh TTL. Overwrites any prior entry.
func (c *timeseriesCache) put(key string, payload []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = tsCacheEntry{payload: payload, expiresAt: c.now().Add(c.ttl)}
}

// tsQueryOpts holds the parsed, validated query parameters. Kept internal.
type tsQueryOpts struct {
	rangeDur   time.Duration
	bucket     stats.Bucket
	bucketDur  time.Duration
	metrics    []string
	workspace  string
	groupBy    string
	backfill   bool
	rangeLabel string
	cacheKey   string
}

// parseTimeseriesQuery validates the query parameters and returns the
// resolved options. On error it writes a 400 error envelope and returns nil.
func parseTimeseriesQuery(w http.ResponseWriter, r *http.Request, backfill bool) *tsQueryOpts {
	q := r.URL.Query()

	rangeLabel := strings.ToLower(strings.TrimSpace(q.Get("range")))
	if rangeLabel == "" {
		rangeLabel = "24h"
	}
	var rangeDur time.Duration
	var defaultBucket stats.Bucket
	switch rangeLabel {
	case "24h":
		rangeDur = 24 * time.Hour
		defaultBucket = stats.BucketHour
	case "7d":
		rangeDur = 7 * 24 * time.Hour
		defaultBucket = stats.BucketHour
	case "30d":
		rangeDur = 30 * 24 * time.Hour
		defaultBucket = stats.BucketDay
	default:
		writeErrorJSON(w, http.StatusBadRequest, "", "invalid range: must be one of 24h, 7d, 30d")
		return nil
	}

	bucketRaw := strings.ToLower(strings.TrimSpace(q.Get("bucket")))
	bucket := defaultBucket
	switch bucketRaw {
	case "":
		// keep defaultBucket
	case "hour":
		bucket = stats.BucketHour
	case "day":
		bucket = stats.BucketDay
	default:
		writeErrorJSON(w, http.StatusBadRequest, "", "invalid bucket: must be hour or day")
		return nil
	}

	var bucketDur time.Duration
	if bucket == stats.BucketDay {
		bucketDur = 24 * time.Hour
	} else {
		bucketDur = time.Hour
	}

	metricsRaw := strings.TrimSpace(q.Get("metrics"))
	var metrics []string
	if metricsRaw == "" {
		metrics = append(metrics, v1MetricSet...)
	} else {
		allow := make(map[string]struct{}, len(v1MetricSet))
		for _, m := range v1MetricSet {
			allow[m] = struct{}{}
		}
		seen := make(map[string]struct{})
		for _, part := range strings.Split(metricsRaw, ",") {
			name := strings.TrimSpace(part)
			if name == "" {
				continue
			}
			if _, ok := allow[name]; !ok {
				writeErrorJSON(w, http.StatusBadRequest, "", "invalid metric: "+name)
				return nil
			}
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			metrics = append(metrics, name)
		}
		if len(metrics) == 0 {
			metrics = append(metrics, v1MetricSet...)
		}
	}

	workspace := strings.TrimSpace(q.Get("workspace"))

	groupByRaw := strings.ToLower(strings.TrimSpace(q.Get("groupBy")))
	var groupBy string
	switch groupByRaw {
	case "":
		// aggregate across models (legacy behavior)
	case stats.GroupByModel:
		groupBy = stats.GroupByModel
	default:
		writeErrorJSON(w, http.StatusBadRequest, "", "invalid groupBy: only 'model' is supported")
		return nil
	}

	// Cache key uses a sorted metric list so ?metrics=a,b and ?metrics=b,a
	// share the same cache slot. Backfill flag is part of the key so the
	// response flips true→false immediately when a pass finishes. groupBy is
	// part of the key so grouped and ungrouped responses live in separate
	// cache slots (mitto-1ac acceptance criteria).
	sortedMetrics := append([]string(nil), metrics...)
	sort.Strings(sortedMetrics)
	key := rangeLabel + "|" + string(bucket) + "|" + strings.Join(sortedMetrics, ",") + "|" + workspace + "|" + groupBy + "|" + strconv.FormatBool(backfill)

	return &tsQueryOpts{
		rangeDur:   rangeDur,
		bucket:     bucket,
		bucketDur:  bucketDur,
		metrics:    metrics,
		workspace:  workspace,
		groupBy:    groupBy,
		backfill:   backfill,
		rangeLabel: rangeLabel,
		cacheKey:   key,
	}
}

// HandleDashboardTimeseries handles GET /api/dashboard/timeseries.
//
// It returns dense (zero-filled), aligned time-series data for the dashboard's
// activity chart. The response covers the last `range` (24h|7d|30d) window
// aligned to bucket boundaries and one series per requested metric.
//
// Query parameters:
//   - range (optional, default 24h): 24h|7d|30d
//   - bucket (optional): hour|day (defaults: hour for 24h/7d, day for 30d)
//   - metrics (optional, default all): comma-separated allowlist of metric names
//   - workspace (optional): restricts the query to a single workspace UUID
//
// Responses are cached for 30s per unique parameter set (including the
// backfill-in-progress flag) so the dashboard can poll cheaply.
func (h *Handlers) HandleDashboardTimeseries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	backfill := false
	if h.deps.StatsBackfillerInProgress != nil {
		backfill = h.deps.StatsBackfillerInProgress()
	}

	opts := parseTimeseriesQuery(w, r, backfill)
	if opts == nil {
		return
	}

	h.tsCacheOnce.Do(func() {
		h.tsCache = newTimeseriesCache(tsCacheTTL)
	})
	if payload, ok := h.tsCache.get(opts.cacheKey); ok {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
		return
	}

	payload, err := h.buildTimeseriesPayload(r, opts)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "failed to build timeseries: "+err.Error())
		return
	}

	h.tsCache.put(opts.cacheKey, payload)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

// buildTimeseriesPayload runs the store query, rolls up to the requested
// bucket, zero-fills gaps, and returns the JSON-encoded response body.
func (h *Handlers) buildTimeseriesPayload(r *http.Request, opts *tsQueryOpts) ([]byte, error) {
	// Align RangeTo to the bucket boundary so the "current" partial bucket is
	// excluded — otherwise polling every 30s inside the same hour would keep
	// showing a growing partial value at the tail.
	now := time.Now().UTC()
	rangeTo := now.Truncate(opts.bucketDur)
	rangeFrom := rangeTo.Add(-opts.rangeDur)

	// The SQLite store aggregates at hourly granularity only; for day-bucket
	// responses we roll up in-process below. See sqlite_store.go query docs.
	storeQuery := stats.Query{
		RangeFrom: rangeFrom,
		RangeTo:   rangeTo,
		Bucket:    stats.BucketHour,
		Metrics:   opts.metrics,
		Workspace: opts.workspace,
		GroupBy:   opts.groupBy,
	}

	var points []stats.Point
	if h.deps.StatsStore != nil {
		p, err := h.deps.StatsStore.Query(r.Context(), storeQuery)
		if err != nil {
			return nil, err
		}
		points = p
	}

	series := h.buildSeries(opts, rangeFrom, rangeTo, points)

	resp := tsResponse{
		Range: tsRange{
			From:          rangeFrom.Unix(),
			To:            rangeTo.Unix(),
			BucketSeconds: int(opts.bucketDur.Seconds()),
		},
		Series: series,
		Meta: tsMeta{
			EstimatorVersion:   strconv.Itoa(stats.EstimatorVersion),
			BackfillInProgress: opts.backfill,
			Note:               timeseriesNote,
		},
	}
	return json.Marshal(resp)
}

// buildSeries rolls up the store's hourly points into the requested bucket and
// returns a dense (zero-filled) series map keyed by metric (ungrouped) or by
// "<metric>:<model>" composite key (grouped by model). Every metric requested
// in opts is present in the returned map — grouped requests always emit the
// synthetic "<metric>:unknown" and "<metric>:" keys required by the response
// envelope contract (mitto-1ac).
func (h *Handlers) buildSeries(opts *tsQueryOpts, rangeFrom, rangeTo time.Time, points []stats.Point) map[string][]tsPoint {
	// Composite series key = "<metric>" (ungrouped) or "<metric>:<model>" (grouped).
	// Bucket the store points into per-key aligned totals; alignment is a no-op
	// for hourly buckets and truncates to the day boundary for daily buckets.
	metricAllowed := make(map[string]struct{}, len(opts.metrics))
	for _, m := range opts.metrics {
		metricAllowed[m] = struct{}{}
	}

	grouped := opts.groupBy == stats.GroupByModel

	buckets := make(map[string]map[time.Time]int64)
	seenKeys := make(map[string]struct{})

	seedKey := func(k string) {
		if _, ok := buckets[k]; !ok {
			buckets[k] = make(map[time.Time]int64)
		}
		seenKeys[k] = struct{}{}
	}

	// Seed one baseline key per requested metric so every metric always shows
	// up in the response (dense envelope). Grouped mode uses the synthetic
	// "<metric>:unknown" slot for empty-model rows so pre-migration data is
	// still surfaced; a bare "<metric>:" slot is also seeded so metrics that
	// carry no model dimension (permissions, errors, tool_calls, prompts,
	// agent_turns_completed, mcp_calls) render alongside the model-tagged ones.
	for _, m := range opts.metrics {
		if grouped {
			seedKey(seriesKeyForModel(m, ""))
		} else {
			seedKey(m)
		}
	}

	for _, p := range points {
		if _, ok := metricAllowed[p.Metric]; !ok {
			continue
		}
		var key string
		if grouped {
			key = seriesKeyForModel(p.Metric, p.Model)
		} else {
			key = p.Metric
		}
		seedKey(key)
		ts := p.TS.UTC().Truncate(opts.bucketDur)
		buckets[key][ts] += p.Value
	}

	// Build the dense grid: one point per bucket boundary in [from, to), for
	// every seen key. Zero-filled where the store had no data.
	series := make(map[string][]tsPoint, len(seenKeys))
	for k := range seenKeys {
		grid := make([]tsPoint, 0, int(opts.rangeDur/opts.bucketDur)+1)
		for ts := rangeFrom; ts.Before(rangeTo); ts = ts.Add(opts.bucketDur) {
			grid = append(grid, tsPoint{T: ts.Unix(), V: buckets[k][ts]})
		}
		series[k] = grid
	}
	return series
}

// seriesKeyForModel builds the composite series key used when groupBy=model.
// Empty model becomes "unknown" so the client can render pre-migration and
// non-model-attributable rows as a labeled grey line.
func seriesKeyForModel(metric, model string) string {
	if model == "" {
		return metric + ":unknown"
	}
	return metric + ":" + model
}
