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
func (f *fakeStatsStore) Close() error                                  { return nil }

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
