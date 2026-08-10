package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/stats"
)

// fakeStatsStore is a stats.Store test double that returns canned points and
// records the last Query it saw. Only Query and the plumbing needed to satisfy
// the Store interface are implemented; every other method is a no-op the
// timeseries handler never calls.
type fakeStatsStore struct {
	points    []stats.Point
	err       error
	queryHits int32
	lastQuery stats.Query
}

func (f *fakeStatsStore) UpsertDeltas(context.Context, []stats.Delta) error {
	return nil
}
func (f *fakeStatsStore) UpsertDeltasWithCursor(context.Context, []stats.Delta, stats.Cursor) error {
	return nil
}
func (f *fakeStatsStore) GetCursor(_ context.Context, id string) (stats.Cursor, error) {
	return stats.Cursor{SessionID: id}, stats.ErrNotFound
}
func (f *fakeStatsStore) SetCursor(context.Context, stats.Cursor) error { return nil }
func (f *fakeStatsStore) Query(_ context.Context, q stats.Query) ([]stats.Point, error) {
	atomic.AddInt32(&f.queryHits, 1)
	f.lastQuery = q
	if f.err != nil {
		return nil, f.err
	}
	return f.points, nil
}
func (f *fakeStatsStore) Prune(context.Context, time.Time) (int64, error) { return 0, nil }
func (f *fakeStatsStore) Vacuum(context.Context) error                    { return nil }
func (f *fakeStatsStore) GetMeta(context.Context, string) (string, error) {
	return "", stats.ErrNotFound
}
func (f *fakeStatsStore) SetMeta(context.Context, string, string) error { return nil }
func (f *fakeStatsStore) ResetForEstimatorBump(context.Context) error   { return nil }
func (f *fakeStatsStore) ReplaceDeltas(context.Context, []string, time.Time, time.Time, []stats.Delta) error {
	return nil
}
func (f *fakeStatsStore) Close() error { return nil }

// newTimeseriesTestHandler wires a Handlers with a fresh cache pre-installed
// (bypassing the sync.Once) so tests can control the clock via c.now.
func newTimeseriesTestHandler(store stats.Store, backfill func() bool) (*Handlers, *timeseriesCache) {
	h := New(Deps{StatsStore: store, StatsBackfillerInProgress: backfill})
	cache := newTimeseriesCache(tsCacheTTL)
	h.tsCache = cache
	h.tsCacheOnce.Do(func() {}) // consume the Once so HandleDashboardTimeseries reuses ours
	return h, cache
}

func decodeTimeseriesBody(t *testing.T, w *httptest.ResponseRecorder) tsResponse {
	t.Helper()
	var r tsResponse
	if err := json.NewDecoder(w.Body).Decode(&r); err != nil {
		t.Fatalf("decode timeseries body: %v; raw=%s", err, w.Body.String())
	}
	return r
}

// --- HandleDashboardTimeseries ----------------------------------------------

func TestTimeseries_MethodNotAllowed(t *testing.T) {
	h, _ := newTimeseriesTestHandler(&fakeStatsStore{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/timeseries", nil)
	w := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestTimeseries_BadRange(t *testing.T) {
	h, _ := newTimeseriesTestHandler(&fakeStatsStore{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?range=1h", nil)
	w := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestTimeseries_BadBucket(t *testing.T) {
	h, _ := newTimeseriesTestHandler(&fakeStatsStore{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?bucket=week", nil)
	w := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestTimeseries_BadMetric(t *testing.T) {
	h, _ := newTimeseriesTestHandler(&fakeStatsStore{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?metrics=bogus", nil)
	w := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestTimeseries_DefaultBucketsPerRange(t *testing.T) {
	cases := []struct {
		rangeParam string
		wantBucket int
		wantPoints int
	}{
		{"24h", 3600, 24},
		{"7d", 3600, 7 * 24},
		{"30d", 86400, 30},
	}
	for _, tc := range cases {
		t.Run(tc.rangeParam, func(t *testing.T) {
			h, _ := newTimeseriesTestHandler(&fakeStatsStore{}, nil)
			req := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?range="+tc.rangeParam, nil)
			w := httptest.NewRecorder()
			h.HandleDashboardTimeseries(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
			}
			body := decodeTimeseriesBody(t, w)
			if body.Range.BucketSeconds != tc.wantBucket {
				t.Errorf("bucket_seconds = %d, want %d", body.Range.BucketSeconds, tc.wantBucket)
			}
			// Every metric series must be dense with wantPoints entries.
			for m, pts := range body.Series {
				if len(pts) != tc.wantPoints {
					t.Errorf("series[%s] length = %d, want %d", m, len(pts), tc.wantPoints)
				}
			}
		})
	}
}

func TestTimeseries_MetricsFilterAndAllowlist(t *testing.T) {
	store := &fakeStatsStore{}
	h, _ := newTimeseriesTestHandler(store, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?metrics=prompts,errors", nil)
	w := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := decodeTimeseriesBody(t, w)
	if len(body.Series) != 2 {
		t.Fatalf("series count = %d, want 2 (prompts,errors); got keys=%v", len(body.Series), keys(body.Series))
	}
	if _, ok := body.Series[stats.MetricPrompts]; !ok {
		t.Errorf("missing series 'prompts'")
	}
	if _, ok := body.Series[stats.MetricErrors]; !ok {
		t.Errorf("missing series 'errors'")
	}
	// The store query must be scoped to the same 2 metrics.
	if len(store.lastQuery.Metrics) != 2 {
		t.Errorf("store query metrics = %v, want 2 entries", store.lastQuery.Metrics)
	}
}

// TestTimeseries_DefaultMetricSetIncludesBeadsMetrics asserts the beads
// metrics (mitto-5rm6, plus the active-cycle pair and the uptime heartbeat
// added by mitto-c45m) are part of v1MetricSet's default (no ?metrics= param)
// response, alongside the pre-existing conversation metrics. A regression
// that drops one of them from v1MetricSet would silently break the
// beads_activity / beads_cycle_time dashboard charts, which request them
// with no explicit ?metrics= filter.
func TestTimeseries_DefaultMetricSetIncludesBeadsMetrics(t *testing.T) {
	h, _ := newTimeseriesTestHandler(&fakeStatsStore{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries", nil)
	w := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := decodeTimeseriesBody(t, w)
	for _, m := range []string{
		stats.MetricBeadsOpened,
		stats.MetricBeadsClosed,
		stats.MetricBeadsCycleSecondsSum,
		stats.MetricBeadsCycleClosedCount,
		stats.MetricBeadsActiveCycleSecondsSum,
		stats.MetricBeadsActiveCycleClosedCount,
		stats.MetricUptimeSeconds,
	} {
		if _, ok := body.Series[m]; !ok {
			t.Errorf("default response missing beads metric %q; got keys=%v", m, keys(body.Series))
		}
	}
}

// TestTimeseries_BeadsMetricsFilterAndValues asserts the four beads metrics
// (mitto-5rm6) can be requested explicitly via ?metrics= and round-trip
// store values unchanged. The handler must NOT pre-average
// beads_cycle_seconds_sum / beads_cycle_closed_count — the sum+count pair is
// returned as-is so the frontend can derive sum/count per bucket (the plan's
// "thin series reads as thin" requirement needs the raw count, not a
// collapsed average).
func TestTimeseries_BeadsMetricsFilterAndValues(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Hour)
	windowStart := now.Add(-24 * time.Hour)
	ts := windowStart.Add(4 * time.Hour)
	store := &fakeStatsStore{points: []stats.Point{
		{TS: ts, Metric: stats.MetricBeadsOpened, Value: 3},
		{TS: ts, Metric: stats.MetricBeadsClosed, Value: 2},
		{TS: ts, Metric: stats.MetricBeadsCycleSecondsSum, Value: 7200},
		{TS: ts, Metric: stats.MetricBeadsCycleClosedCount, Value: 2},
	}}
	h, _ := newTimeseriesTestHandler(store, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?metrics=beads_opened,beads_closed,beads_cycle_seconds_sum,beads_cycle_closed_count", nil)
	w := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := decodeTimeseriesBody(t, w)
	if len(body.Series) != 4 {
		t.Fatalf("series count = %d, want 4; got keys=%v", len(body.Series), keys(body.Series))
	}
	wantAt := map[string]int64{
		stats.MetricBeadsOpened:           3,
		stats.MetricBeadsClosed:           2,
		stats.MetricBeadsCycleSecondsSum:  7200,
		stats.MetricBeadsCycleClosedCount: 2,
	}
	for metric, want := range wantAt {
		pts, ok := body.Series[metric]
		if !ok {
			t.Fatalf("missing series %q; got keys=%v", metric, keys(body.Series))
		}
		if len(pts) != 24 {
			t.Fatalf("series[%s] length = %d, want 24 (dense zero-fill)", metric, len(pts))
		}
		found := false
		for _, p := range pts {
			if p.T == ts.Unix() {
				found = true
				if p.V != want {
					t.Errorf("series[%s] at bucket = %d, want %d", metric, p.V, want)
				}
			} else if p.V != 0 {
				t.Errorf("series[%s] at %d = %d, want 0 (zero-filled)", metric, p.T, p.V)
			}
		}
		if !found {
			t.Errorf("series[%s] never contained the seeded bucket %d", metric, ts.Unix())
		}
	}
}

func TestTimeseries_WorkspaceIsForwardedToStore(t *testing.T) {
	store := &fakeStatsStore{}
	h, _ := newTimeseriesTestHandler(store, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?workspace=ws-42", nil)
	w := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if store.lastQuery.Workspace != "ws-42" {
		t.Errorf("store query workspace = %q, want %q", store.lastQuery.Workspace, "ws-42")
	}
}

func TestTimeseries_ZeroFillPreservesStoreValues(t *testing.T) {
	// Craft two points inside the 24h window aligned to hour boundaries.
	// Every hour that has no matching point must appear with value 0.
	now := time.Now().UTC().Truncate(time.Hour)
	windowStart := now.Add(-24 * time.Hour)
	p1TS := windowStart.Add(2 * time.Hour)
	p2TS := windowStart.Add(5 * time.Hour)
	store := &fakeStatsStore{points: []stats.Point{
		{TS: p1TS, Metric: stats.MetricPrompts, Value: 7},
		{TS: p2TS, Metric: stats.MetricPrompts, Value: 3},
	}}
	h, _ := newTimeseriesTestHandler(store, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?metrics=prompts", nil)
	w := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := decodeTimeseriesBody(t, w)
	pts := body.Series[stats.MetricPrompts]
	if len(pts) != 24 {
		t.Fatalf("dense series length = %d, want 24", len(pts))
	}
	nonZero := 0
	for _, p := range pts {
		if p.T == p1TS.Unix() && p.V != 7 {
			t.Errorf("point at p1 = %d, want 7", p.V)
		}
		if p.T == p2TS.Unix() && p.V != 3 {
			t.Errorf("point at p2 = %d, want 3", p.V)
		}
		if p.V != 0 {
			nonZero++
		}
	}
	if nonZero != 2 {
		t.Errorf("nonZero points = %d, want 2 (rest must be zero-filled)", nonZero)
	}
}

func TestTimeseries_DayBucketAggregatesHourlyPoints(t *testing.T) {
	// Two hourly points in the same UTC day must sum into one day bucket.
	now := time.Now().UTC().Truncate(24 * time.Hour)
	windowStart := now.Add(-30 * 24 * time.Hour)
	dayTS := windowStart.Add(5 * 24 * time.Hour)
	store := &fakeStatsStore{points: []stats.Point{
		{TS: dayTS.Add(2 * time.Hour), Metric: stats.MetricPrompts, Value: 4},
		{TS: dayTS.Add(3 * time.Hour), Metric: stats.MetricPrompts, Value: 6},
	}}
	h, _ := newTimeseriesTestHandler(store, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?range=30d&metrics=prompts", nil)
	w := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := decodeTimeseriesBody(t, w)
	if body.Range.BucketSeconds != 86400 {
		t.Fatalf("bucket_seconds = %d, want 86400", body.Range.BucketSeconds)
	}
	pts := body.Series[stats.MetricPrompts]
	if len(pts) != 30 {
		t.Fatalf("series length = %d, want 30", len(pts))
	}
	found := false
	for _, p := range pts {
		if p.T == dayTS.Unix() {
			if p.V != 10 {
				t.Errorf("day bucket value = %d, want 10 (4+6 rollup)", p.V)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("expected day bucket at %d in series; got timestamps=%v", dayTS.Unix(), timestamps(pts))
	}
}

func TestTimeseries_BackfillFlagPropagates(t *testing.T) {
	store := &fakeStatsStore{}
	h, _ := newTimeseriesTestHandler(store, func() bool { return true })
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries", nil)
	w := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := decodeTimeseriesBody(t, w)
	if !body.Meta.BackfillInProgress {
		t.Errorf("meta.backfill_in_progress = false, want true")
	}
}

func TestTimeseries_CacheServesRepeatedRequestsWithoutRequerying(t *testing.T) {
	store := &fakeStatsStore{}
	h, _ := newTimeseriesTestHandler(store, nil)
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?range=24h&metrics=prompts", nil)
		w := httptest.NewRecorder()
		h.HandleDashboardTimeseries(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("iter %d: status = %d, want 200", i, w.Code)
		}
	}
	if got := atomic.LoadInt32(&store.queryHits); got != 1 {
		t.Errorf("store.Query hits = %d, want 1 (subsequent calls should hit cache)", got)
	}
}

func TestTimeseries_CacheKeyDistinguishesBackfillFlag(t *testing.T) {
	store := &fakeStatsStore{}
	flag := false
	h, _ := newTimeseriesTestHandler(store, func() bool { return flag })

	req1 := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?range=24h&metrics=prompts", nil)
	w1 := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w1, req1)

	flag = true // flip the backfill flag; cache key must diverge
	req2 := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?range=24h&metrics=prompts", nil)
	w2 := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w2, req2)

	if got := atomic.LoadInt32(&store.queryHits); got != 2 {
		t.Errorf("store.Query hits = %d, want 2 (backfill flip must miss cache)", got)
	}
	b1 := decodeTimeseriesBody(t, w1)
	b2 := decodeTimeseriesBody(t, w2)
	if b1.Meta.BackfillInProgress != false || b2.Meta.BackfillInProgress != true {
		t.Errorf("backfill flags: b1=%v b2=%v; want false,true", b1.Meta.BackfillInProgress, b2.Meta.BackfillInProgress)
	}
}

func TestTimeseries_CacheExpiresAfterTTL(t *testing.T) {
	store := &fakeStatsStore{}
	h, cache := newTimeseriesTestHandler(store, nil)
	// Freeze the cache clock so the second request lands past TTL.
	baseTime := time.Now()
	cache.now = func() time.Time { return baseTime }

	req1 := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?range=24h&metrics=prompts", nil)
	w1 := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w1, req1)

	// Advance past TTL.
	cache.now = func() time.Time { return baseTime.Add(tsCacheTTL + time.Second) }

	req2 := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?range=24h&metrics=prompts", nil)
	w2 := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w2, req2)

	if got := atomic.LoadInt32(&store.queryHits); got != 2 {
		t.Errorf("store.Query hits = %d, want 2 (expired cache entry must re-query)", got)
	}
}

// --- groupBy=model (mitto-1ac) ---------------------------------------------

// TestTimeseries_GroupByModel_ReturnsPerModelSeries seeds the fake store with
// two models across two buckets and asserts the handler emits one composite
// "<metric>:<model>" series per model, each dense/zero-filled to the range.
func TestTimeseries_GroupByModel_ReturnsPerModelSeries(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Hour)
	windowStart := now.Add(-24 * time.Hour)
	tsA := windowStart.Add(2 * time.Hour)
	tsB := windowStart.Add(5 * time.Hour)
	store := &fakeStatsStore{points: []stats.Point{
		{TS: tsA, Metric: stats.MetricOutputTokensEst, Model: "modelA", Value: 11},
		{TS: tsB, Metric: stats.MetricOutputTokensEst, Model: "modelB", Value: 22},
	}}
	h, _ := newTimeseriesTestHandler(store, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?range=24h&metrics=output_tokens_est&groupBy=model", nil)
	w := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if store.lastQuery.GroupBy != stats.GroupByModel {
		t.Errorf("store query GroupBy = %q, want %q", store.lastQuery.GroupBy, stats.GroupByModel)
	}

	body := decodeTimeseriesBody(t, w)
	seriesA, okA := body.Series["output_tokens_est:modelA"]
	if !okA {
		t.Fatalf("missing series key 'output_tokens_est:modelA'; got keys=%v", keys(body.Series))
	}
	seriesB, okB := body.Series["output_tokens_est:modelB"]
	if !okB {
		t.Fatalf("missing series key 'output_tokens_est:modelB'; got keys=%v", keys(body.Series))
	}
	if len(seriesA) != 24 || len(seriesB) != 24 {
		t.Errorf("series lengths = %d/%d, want 24/24 (dense zero-fill)", len(seriesA), len(seriesB))
	}
	sumSeries := func(pts []tsPoint) int64 {
		var s int64
		for _, p := range pts {
			s += p.V
		}
		return s
	}
	if sumSeries(seriesA) != 11 {
		t.Errorf("modelA total = %d, want 11", sumSeries(seriesA))
	}
	if sumSeries(seriesB) != 22 {
		t.Errorf("modelB total = %d, want 22", sumSeries(seriesB))
	}
	// modelA's value must land in the tsA bucket, modelB's in tsB — grouping
	// must not smear values across the wrong buckets.
	for _, p := range seriesA {
		if p.T == tsA.Unix() && p.V != 11 {
			t.Errorf("modelA at tsA = %d, want 11", p.V)
		}
	}
	for _, p := range seriesB {
		if p.T == tsB.Unix() && p.V != 22 {
			t.Errorf("modelB at tsB = %d, want 22", p.V)
		}
	}
}

// TestTimeseries_GroupByModel_EmptyModelBecomesUnknown asserts that rows with
// Model=="" land in a "<metric>:unknown" composite series so pre-migration /
// non-attributed data still surfaces to the client.
func TestTimeseries_GroupByModel_EmptyModelBecomesUnknown(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Hour)
	windowStart := now.Add(-24 * time.Hour)
	ts := windowStart.Add(3 * time.Hour)
	store := &fakeStatsStore{points: []stats.Point{
		{TS: ts, Metric: stats.MetricOutputTokensEst, Model: "", Value: 42},
	}}
	h, _ := newTimeseriesTestHandler(store, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?range=24h&metrics=output_tokens_est&groupBy=model", nil)
	w := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := decodeTimeseriesBody(t, w)
	pts, ok := body.Series["output_tokens_est:unknown"]
	if !ok {
		t.Fatalf("missing 'output_tokens_est:unknown' series; got keys=%v", keys(body.Series))
	}
	if len(pts) != 24 {
		t.Errorf("unknown-series length = %d, want 24 (dense zero-fill)", len(pts))
	}
	var total int64
	for _, p := range pts {
		total += p.V
		if p.T == ts.Unix() && p.V != 42 {
			t.Errorf("unknown at ts = %d, want 42", p.V)
		}
	}
	if total != 42 {
		t.Errorf("unknown-series total = %d, want 42", total)
	}
	// No stray '<metric>:' key with empty-model suffix — the collapse target
	// is 'unknown', not the literal empty string.
	if _, bad := body.Series["output_tokens_est:"]; bad {
		t.Errorf("unexpected 'output_tokens_est:' series key: empty model must collapse to 'unknown'")
	}
}

// TestTimeseries_GroupByModel_UnknownValue_Returns400 asserts unknown groupBy
// values are rejected at the handler edge (allowlist == {"", "model"}).
func TestTimeseries_GroupByModel_UnknownValue_Returns400(t *testing.T) {
	store := &fakeStatsStore{}
	h, _ := newTimeseriesTestHandler(store, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?groupBy=session", nil)
	w := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if atomic.LoadInt32(&store.queryHits) != 0 {
		t.Errorf("store.Query hits = %d, want 0 (invalid groupBy must reject before touching store)", store.queryHits)
	}
}

// TestTimeseries_GroupByModel_CacheKeyIsolation asserts that two requests
// differing only in groupBy live in separate cache slots (the ungrouped
// response cannot be served for a grouped request or vice versa).
func TestTimeseries_GroupByModel_CacheKeyIsolation(t *testing.T) {
	store := &fakeStatsStore{}
	h, _ := newTimeseriesTestHandler(store, nil)

	// Ungrouped first.
	req1 := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?range=24h&metrics=output_tokens_est", nil)
	w1 := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("ungrouped status = %d, want 200; body=%s", w1.Code, w1.Body.String())
	}

	// Same range/metrics but with groupBy=model — must miss the ungrouped
	// cache slot and hit the store again.
	req2 := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?range=24h&metrics=output_tokens_est&groupBy=model", nil)
	w2 := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("grouped status = %d, want 200; body=%s", w2.Code, w2.Body.String())
	}
	if got := atomic.LoadInt32(&store.queryHits); got != 2 {
		t.Errorf("store.Query hits = %d, want 2 (groupBy flip must miss cache)", got)
	}
	// Re-fire the grouped request — this time the cache must serve it.
	req3 := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?range=24h&metrics=output_tokens_est&groupBy=model", nil)
	w3 := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w3, req3)
	if got := atomic.LoadInt32(&store.queryHits); got != 2 {
		t.Errorf("store.Query hits after repeat grouped = %d, want 2 (grouped slot must cache too)", got)
	}
}

// TestTimeseries_GroupByModel_UngroupedPreservesLegacyKeys asserts the
// "byte-identical" acceptance criterion for the ungrouped path: an
// unqualified request keeps the flat "<metric>" keys, never emitting any
// composite "<metric>:...".
func TestTimeseries_GroupByModel_UngroupedPreservesLegacyKeys(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Hour)
	windowStart := now.Add(-24 * time.Hour)
	ts := windowStart.Add(2 * time.Hour)
	store := &fakeStatsStore{points: []stats.Point{
		// Store rows are still tagged with a model (the store doesn't know
		// whether the caller wanted grouping), but the ungrouped path must
		// ignore Model and collapse into a bare "<metric>" key.
		{TS: ts, Metric: stats.MetricOutputTokensEst, Model: "modelA", Value: 5},
	}}
	h, _ := newTimeseriesTestHandler(store, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeseries?range=24h&metrics=output_tokens_est", nil)
	w := httptest.NewRecorder()
	h.HandleDashboardTimeseries(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if store.lastQuery.GroupBy != "" {
		t.Errorf("store query GroupBy = %q, want empty (ungrouped path)", store.lastQuery.GroupBy)
	}
	body := decodeTimeseriesBody(t, w)
	if _, ok := body.Series[stats.MetricOutputTokensEst]; !ok {
		t.Fatalf("missing bare '%s' key in ungrouped response; got keys=%v", stats.MetricOutputTokensEst, keys(body.Series))
	}
	for k := range body.Series {
		if k != stats.MetricOutputTokensEst {
			t.Errorf("unexpected series key %q in ungrouped response; only bare metric keys allowed", k)
		}
	}
}

// --- helpers ---------------------------------------------------------------

func keys(m map[string][]tsPoint) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func timestamps(pts []tsPoint) []int64 {
	out := make([]int64, len(pts))
	for i, p := range pts {
		out[i] = p.T
	}
	return out
}
