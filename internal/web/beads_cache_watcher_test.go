package web

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/beads"
	"github.com/inercia/mitto/internal/beads/watcher"
)

// stubBeadsClient is a minimal beads.Client for the adapter test. Only List is
// exercised; every other method returns zero values so the type satisfies the
// interface. listCalls counts List invocations so the test can assert that a
// second read after Invalidate hits the inner client again.
type stubBeadsClient struct {
	listCalls int64
	payload   []byte
}

func (s *stubBeadsClient) List(_ context.Context, _ string) ([]byte, error) {
	atomic.AddInt64(&s.listCalls, 1)
	return s.payload, nil
}
func (s *stubBeadsClient) Ready(context.Context, string) ([]byte, error)  { return nil, nil }
func (s *stubBeadsClient) Status(context.Context, string) ([]byte, error) { return nil, nil }
func (s *stubBeadsClient) Show(context.Context, string, string) ([]byte, error) {
	return nil, nil
}
func (s *stubBeadsClient) Create(context.Context, string, beads.CreateParams) ([]byte, error) {
	return nil, nil
}
func (s *stubBeadsClient) Delete(context.Context, string, string) error { return nil }
func (s *stubBeadsClient) ListClosedIDs(context.Context, string) ([]string, error) {
	return nil, nil
}
func (s *stubBeadsClient) Statuses(context.Context, string, []string) (map[string]string, error) {
	return nil, nil
}
func (s *stubBeadsClient) DeleteIDs(context.Context, string, []string) error       { return nil }
func (s *stubBeadsClient) SetStatus(context.Context, string, string, string) error { return nil }
func (s *stubBeadsClient) Update(context.Context, string, beads.UpdateParams) error {
	return nil
}
func (s *stubBeadsClient) Comment(context.Context, string, string, string) error { return nil }
func (s *stubBeadsClient) Dep(context.Context, string, beads.DepParams) error    { return nil }
func (s *stubBeadsClient) Label(context.Context, string, beads.LabelParams) error {
	return nil
}
func (s *stubBeadsClient) ListAllLabels(context.Context, string) ([]byte, error) {
	return nil, nil
}
func (s *stubBeadsClient) ConfigShow(context.Context, string) (map[string]string, error) {
	return nil, nil
}
func (s *stubBeadsClient) ConfigSet(context.Context, string, string, string) error { return nil }
func (s *stubBeadsClient) ConfigUnset(context.Context, string, string) error       { return nil }
func (s *stubBeadsClient) EnsureInitialized(context.Context, string) error         { return nil }
func (s *stubBeadsClient) Sync(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (s *stubBeadsClient) MigrateRemote(context.Context, string) ([]byte, error) {
	return []byte(`{}`), nil
}
func (s *stubBeadsClient) Bootstrap(context.Context, string) ([]byte, error) {
	return []byte(`{}`), nil
}

// TestBeadsCacheWatcherSubscriber_InvalidatesOnEvent verifies that
// beadsCacheWatcherSubscriber.OnBeadsChanged drops the cache slot for every
// dir in event.WorkingDirs, forcing the next read to hit the inner client
// again. This is the mitto-is2.3 wiring — the adapter that translates
// BeadsWatcher fan-out into CachingClient.Invalidate calls.
func TestBeadsCacheWatcherSubscriber_InvalidatesOnEvent(t *testing.T) {
	// Build a workspace dir that passes beads.isInitialized so CachingClient
	// actually stores the read payload (uninitialised dirs bypass the cache).
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "config.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}

	stub := &stubBeadsClient{payload: []byte(`[{"id":"x-1"}]`)}
	cache := beads.NewCachingClient(stub)

	// Populate the cache slot for dir.
	if _, err := cache.List(context.Background(), dir); err != nil {
		t.Fatalf("first List: %v", err)
	}
	if got := atomic.LoadInt64(&stub.listCalls); got != 1 {
		t.Fatalf("after first List want listCalls=1, got %d", got)
	}

	// Sanity: a second List is a cache hit (no new inner call).
	if _, err := cache.List(context.Background(), dir); err != nil {
		t.Fatalf("second List: %v", err)
	}
	if got := atomic.LoadInt64(&stub.listCalls); got != 1 {
		t.Fatalf("after second List want listCalls=1 (cache hit), got %d", got)
	}

	// Fire the adapter with an event whose WorkingDirs contains dir.
	adapter := &beadsCacheWatcherSubscriber{cache: cache}
	adapter.OnBeadsChanged(watcher.BeadsChangeEvent{
		WorkingDirs: []string{dir},
		ChangedDirs: []string{filepath.Join(dir, ".beads")},
		Timestamp:   time.Now(),
	})

	// Next read must hit the inner client again — the slot was invalidated.
	if _, err := cache.List(context.Background(), dir); err != nil {
		t.Fatalf("third List: %v", err)
	}
	if got := atomic.LoadInt64(&stub.listCalls); got != 2 {
		t.Fatalf("after OnBeadsChanged want listCalls=2, got %d", got)
	}
}

// TestBeadsCacheWatcherSubscriber_NilSafe covers the nil-guard paths so a
// mis-wired subscriber (nil adapter or nil cache) does not panic if the
// watcher fires an event.
func TestBeadsCacheWatcherSubscriber_NilSafe(t *testing.T) {
	var nilAdapter *beadsCacheWatcherSubscriber
	nilAdapter.OnBeadsChanged(watcher.BeadsChangeEvent{WorkingDirs: []string{"/x"}})

	empty := &beadsCacheWatcherSubscriber{}
	empty.OnBeadsChanged(watcher.BeadsChangeEvent{WorkingDirs: []string{"/x"}})
}
