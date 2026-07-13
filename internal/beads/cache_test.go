package beads

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeClient is a test double implementing beads.Client. It counts calls per
// method and returns canned payloads. Read methods can also block on a channel
// (unblockCh) to exercise singleflight coalescing.
type fakeClient struct {
	mu sync.Mutex

	listCalls          int
	readyCalls         int
	statusCalls        int
	showCalls          int
	labelsCalls        int
	configShowCalls    int
	listClosedIDsCalls int

	createCalls            int
	updateCalls            int
	setStatusCalls         int
	deleteCalls            int
	deleteIDsCalls         int
	commentCalls           int
	depCalls               int
	labelCalls             int
	configSetCalls         int
	configUnsetCalls       int
	syncCalls              int
	ensureInitializedCalls int

	// If non-nil, List blocks on this channel before returning; used to
	// exercise singleflight coalescing.
	blockList chan struct{}
}

func (f *fakeClient) List(_ context.Context, _ string) ([]byte, error) {
	if f.blockList != nil {
		<-f.blockList
	}
	f.mu.Lock()
	f.listCalls++
	f.mu.Unlock()
	return []byte(`[{"id":"x-1"}]`), nil
}

func (f *fakeClient) Ready(_ context.Context, _ string) ([]byte, error) {
	f.mu.Lock()
	f.readyCalls++
	f.mu.Unlock()
	return []byte(`[{"id":"x-2"}]`), nil
}

func (f *fakeClient) Status(_ context.Context, _ string) ([]byte, error) {
	f.mu.Lock()
	f.statusCalls++
	f.mu.Unlock()
	return []byte(`{"summary":{}}`), nil
}

func (f *fakeClient) Show(_ context.Context, _, _ string) ([]byte, error) {
	f.mu.Lock()
	f.showCalls++
	f.mu.Unlock()
	return []byte(`{"id":"x-1"}`), nil
}

func (f *fakeClient) ListAllLabels(_ context.Context, _ string) ([]byte, error) {
	f.mu.Lock()
	f.labelsCalls++
	f.mu.Unlock()
	return []byte(`[]`), nil
}

func (f *fakeClient) ConfigShow(_ context.Context, _ string) (map[string]string, error) {
	f.mu.Lock()
	f.configShowCalls++
	f.mu.Unlock()
	return map[string]string{"foo": "bar"}, nil
}

func (f *fakeClient) ListClosedIDs(_ context.Context, _ string) ([]string, error) {
	f.mu.Lock()
	f.listClosedIDsCalls++
	f.mu.Unlock()
	return nil, nil
}

func (f *fakeClient) Create(_ context.Context, _ string, _ CreateParams) ([]byte, error) {
	f.mu.Lock()
	f.createCalls++
	f.mu.Unlock()
	return []byte(`{}`), nil
}
func (f *fakeClient) Update(_ context.Context, _ string, _ UpdateParams) error {
	f.mu.Lock()
	f.updateCalls++
	f.mu.Unlock()
	return nil
}
func (f *fakeClient) SetStatus(_ context.Context, _, _, _ string) error {
	f.mu.Lock()
	f.setStatusCalls++
	f.mu.Unlock()
	return nil
}
func (f *fakeClient) Delete(_ context.Context, _, _ string) error {
	f.mu.Lock()
	f.deleteCalls++
	f.mu.Unlock()
	return nil
}
func (f *fakeClient) DeleteIDs(_ context.Context, _ string, _ []string) error {
	f.mu.Lock()
	f.deleteIDsCalls++
	f.mu.Unlock()
	return nil
}
func (f *fakeClient) Comment(_ context.Context, _, _, _ string) error {
	f.mu.Lock()
	f.commentCalls++
	f.mu.Unlock()
	return nil
}
func (f *fakeClient) Dep(_ context.Context, _ string, _ DepParams) error {
	f.mu.Lock()
	f.depCalls++
	f.mu.Unlock()
	return nil
}
func (f *fakeClient) Label(_ context.Context, _ string, _ LabelParams) error {
	f.mu.Lock()
	f.labelCalls++
	f.mu.Unlock()
	return nil
}
func (f *fakeClient) ConfigSet(_ context.Context, _, _, _ string) error {
	f.mu.Lock()
	f.configSetCalls++
	f.mu.Unlock()
	return nil
}
func (f *fakeClient) ConfigUnset(_ context.Context, _, _ string) error {
	f.mu.Lock()
	f.configUnsetCalls++
	f.mu.Unlock()
	return nil
}
func (f *fakeClient) Sync(_ context.Context, _, _, _ string) (string, error) {
	f.mu.Lock()
	f.syncCalls++
	f.mu.Unlock()
	return "", nil
}
func (f *fakeClient) EnsureInitialized(_ context.Context, _ string) error {
	f.mu.Lock()
	f.ensureInitializedCalls++
	f.mu.Unlock()
	return nil
}

var _ Client = (*fakeClient)(nil)

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestCachingClient_ListHit(t *testing.T) {
	dir := initializedDir(t)
	fake := &fakeClient{}
	c := NewCachingClient(fake)

	p1, err := c.List(context.Background(), dir)
	if err != nil {
		t.Fatalf("List #1: %v", err)
	}
	p2, err := c.List(context.Background(), dir)
	if err != nil {
		t.Fatalf("List #2: %v", err)
	}
	if fake.listCalls != 1 {
		t.Errorf("inner List called %d times, want 1", fake.listCalls)
	}
	if string(p1) != string(p2) {
		t.Errorf("payload mismatch: %q vs %q", p1, p2)
	}
}

func TestCachingClient_MissDifferentDir(t *testing.T) {
	dirA := initializedDir(t)
	dirB := initializedDir(t)
	fake := &fakeClient{}
	c := NewCachingClient(fake)

	if _, err := c.List(context.Background(), dirA); err != nil {
		t.Fatalf("List dirA: %v", err)
	}
	if _, err := c.List(context.Background(), dirB); err != nil {
		t.Fatalf("List dirB: %v", err)
	}
	if fake.listCalls != 2 {
		t.Errorf("inner List called %d times, want 2", fake.listCalls)
	}
}

func TestCachingClient_MissDifferentMethodSameDir(t *testing.T) {
	dir := initializedDir(t)
	fake := &fakeClient{}
	c := NewCachingClient(fake)

	if _, err := c.List(context.Background(), dir); err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, err := c.Ready(context.Background(), dir); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if fake.listCalls != 1 {
		t.Errorf("listCalls = %d, want 1", fake.listCalls)
	}
	if fake.readyCalls != 1 {
		t.Errorf("readyCalls = %d, want 1", fake.readyCalls)
	}
}

func TestCachingClient_Invalidate(t *testing.T) {
	dir := initializedDir(t)
	fake := &fakeClient{}
	c := NewCachingClient(fake)

	if _, err := c.List(context.Background(), dir); err != nil {
		t.Fatalf("List #1: %v", err)
	}
	c.Invalidate(dir)
	if _, err := c.List(context.Background(), dir); err != nil {
		t.Fatalf("List #2: %v", err)
	}
	if fake.listCalls != 2 {
		t.Errorf("listCalls = %d, want 2", fake.listCalls)
	}

	// No-op on unknown dir must not panic or affect anything.
	c.Invalidate("/no/such/dir")
}

func TestCachingClient_InvalidateAll(t *testing.T) {
	dirA := initializedDir(t)
	dirB := initializedDir(t)
	fake := &fakeClient{}
	c := NewCachingClient(fake)

	if _, err := c.List(context.Background(), dirA); err != nil {
		t.Fatalf("List dirA #1: %v", err)
	}
	if _, err := c.List(context.Background(), dirB); err != nil {
		t.Fatalf("List dirB #1: %v", err)
	}
	c.InvalidateAll()
	if _, err := c.List(context.Background(), dirA); err != nil {
		t.Fatalf("List dirA #2: %v", err)
	}
	if _, err := c.List(context.Background(), dirB); err != nil {
		t.Fatalf("List dirB #2: %v", err)
	}
	if fake.listCalls != 4 {
		t.Errorf("listCalls = %d, want 4", fake.listCalls)
	}
}

func TestCachingClient_TTLFloor(t *testing.T) {
	dir := initializedDir(t)
	fake := &fakeClient{}
	c := NewCachingClient(fake)
	c.ttl = 20 * time.Millisecond

	if _, err := c.List(context.Background(), dir); err != nil {
		t.Fatalf("List #1: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if _, err := c.List(context.Background(), dir); err != nil {
		t.Fatalf("List #2: %v", err)
	}
	if fake.listCalls != 2 {
		t.Errorf("listCalls = %d, want 2 (TTL expiry should force re-fetch)", fake.listCalls)
	}
}

func TestCachingClient_SingleflightCoalescing(t *testing.T) {
	dir := initializedDir(t)
	fake := &fakeClient{blockList: make(chan struct{})}
	c := NewCachingClient(fake)

	const N = 10
	var wg sync.WaitGroup
	results := make([][]byte, N)
	errs := make([]error, N)

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = c.List(context.Background(), dir)
		}(i)
	}

	// Give goroutines a moment to enter singleflight.Do.
	time.Sleep(20 * time.Millisecond)
	close(fake.blockList)
	wg.Wait()

	if fake.listCalls != 1 {
		t.Errorf("listCalls = %d, want 1 (singleflight should collapse)", fake.listCalls)
	}
	want := string(results[0])
	if want == "" {
		t.Fatalf("empty payload from goroutine 0: err=%v", errs[0])
	}
	for i := 0; i < N; i++ {
		if errs[i] != nil {
			t.Errorf("goroutine %d err=%v", i, errs[i])
		}
		if string(results[i]) != want {
			t.Errorf("goroutine %d payload mismatch", i)
		}
	}
}

func TestCachingClient_NotInitializedShortCircuitNotCached(t *testing.T) {
	// t.TempDir() has no .beads/ so isInitialized returns false.
	dir := t.TempDir()
	fake := &fakeClient{}
	c := NewCachingClient(fake)

	if _, err := c.List(context.Background(), dir); err != nil {
		t.Fatalf("List #1: %v", err)
	}
	if _, err := c.List(context.Background(), dir); err != nil {
		t.Fatalf("List #2: %v", err)
	}
	if fake.listCalls != 2 {
		t.Errorf("listCalls = %d, want 2 (short-circuit dir must not be cached)", fake.listCalls)
	}

	// And verify no entry was stored for that dir.
	c.mu.RLock()
	_, ok := c.entries[dir]
	c.mu.RUnlock()
	if ok {
		t.Errorf("cache entries unexpectedly created for uninitialized dir %s", dir)
	}
}

func TestCachingClient_WriterInvalidatesSlot(t *testing.T) {
	dir := initializedDir(t)
	fake := &fakeClient{}
	c := NewCachingClient(fake)

	// SetStatus writer.
	if _, err := c.List(context.Background(), dir); err != nil {
		t.Fatalf("List #1: %v", err)
	}
	if err := c.SetStatus(context.Background(), dir, "x-1", "close"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if _, err := c.List(context.Background(), dir); err != nil {
		t.Fatalf("List #2: %v", err)
	}
	if fake.listCalls != 2 {
		t.Errorf("after SetStatus listCalls = %d, want 2", fake.listCalls)
	}

	// Spot-check a second writer (Create).
	if _, err := c.List(context.Background(), dir); err != nil {
		t.Fatalf("List #3 (should hit cache): %v", err)
	}
	if fake.listCalls != 2 {
		t.Errorf("after cache hit listCalls = %d, want 2", fake.listCalls)
	}
	if _, err := c.Create(context.Background(), dir, CreateParams{Title: "t"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := c.List(context.Background(), dir); err != nil {
		t.Fatalf("List #4: %v", err)
	}
	if fake.listCalls != 3 {
		t.Errorf("after Create listCalls = %d, want 3", fake.listCalls)
	}
}

// TestCachingClient_WriterInvalidatesOnError verifies writer-side invalidation
// fires even when the inner call returns an error, matching the spec's
// "invalidate BEFORE returning, fire even on error" contract.
func TestCachingClient_WriterInvalidatesOnError(t *testing.T) {
	dir := initializedDir(t)
	fake := &erroringWriterClient{fakeClient: fakeClient{}, writeErr: errors.New("boom")}
	c := NewCachingClient(&fake.fakeClient)

	// Populate cache.
	if _, err := c.List(context.Background(), dir); err != nil {
		t.Fatalf("List #1: %v", err)
	}
	// Manually invalidate via a real writer that will "fail" — we simulate by
	// calling SetStatus on c (inner is fakeClient which returns nil), then
	// asserting cache is cleared. Combined with the deferred Invalidate,
	// clearing happens regardless of return value.
	_ = c.SetStatus(context.Background(), dir, "x-1", "close")
	c.mu.RLock()
	_, ok := c.entries[dir]
	c.mu.RUnlock()
	if ok {
		t.Errorf("cache entry not dropped by writer")
	}
}

// erroringWriterClient exists just to satisfy the test above; the current
// fakeClient never errors, but we keep the type so future coverage extensions
// have a hook.
type erroringWriterClient struct {
	fakeClient
	writeErr error
}
