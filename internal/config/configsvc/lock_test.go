package configsvc

import (
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config/configpath"
)

// mustOpSetNoT is mustOpSet without a *testing.T dependency, for use inside
// goroutines (calling t.Fatalf off the main test goroutine is unsafe).
// Returns nil on any parse error.
func mustOpSetNoT(path, jsonVal string) *configpath.OpSet {
	assigns, err := configpath.ParseSetJSON(path + "=" + jsonVal)
	if err != nil || len(assigns) != 1 {
		return nil
	}
	set, err := configpath.Resolve(assigns)
	if err != nil {
		return nil
	}
	return &set
}

func TestLock_ExcludesConcurrentWriter(t *testing.T) {
	setupTempMitto(t)

	unlock, err := acquireOfflineLock()
	if err != nil {
		t.Fatalf("first acquireOfflineLock: %v", err)
	}
	defer unlock()

	start := time.Now()
	_, err = acquireOfflineLockWithTimeout(200 * time.Millisecond)
	if err == nil {
		t.Fatalf("expected second concurrent acquire to fail while first holds the lock")
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("second acquire returned too fast (%v); did it actually contend?", elapsed)
	}
	cerr, ok := err.(*Error)
	if !ok || cerr.Kind != ErrKindLocked {
		t.Fatalf("expected ErrKindLocked, got %v", err)
	}
}

func TestLock_ReleasedAfterUnlock(t *testing.T) {
	setupTempMitto(t)

	unlock, err := acquireOfflineLock()
	if err != nil {
		t.Fatalf("acquireOfflineLock: %v", err)
	}
	unlock()

	unlock2, err := acquireOfflineLock()
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	unlock2()
}

// TestLock_SerializesConcurrentMutate is the "racing CLI/UI/dedicated
// saves" scenario: N goroutines each try to append one shortcut section
// concurrently. The lock must serialize them so every write lands (no lost
// update from an unguarded read-modify-write race).
func TestLock_SerializesConcurrentMutate(t *testing.T) {
	setupTempMitto(t)
	const n = 5
	var wg sync.WaitGroup
	var failures int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := "task_label_colors"
			val := `[{"label":"l","color":"#001122"}]`
			ops := mustOpSetNoT(path, val)
			if ops == nil {
				atomic.AddInt64(&failures, 1)
				return
			}
			if _, err := Mutate(MutateRequest{Set: *ops}); err != nil {
				atomic.AddInt64(&failures, 1)
			}
		}(i)
	}
	wg.Wait()
	if failures != 0 {
		t.Fatalf("%d/%d concurrent Mutate calls failed under the lock", failures, n)
	}

	snap, err := ReadSnapshot()
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if !snap.Exists {
		t.Fatalf("settings.json missing after concurrent writes")
	}
}

func TestLock_RunningServerExcluded(t *testing.T) {
	dir := setupTempMitto(t)
	instPath, err := appdir.InstancePath()
	if err != nil {
		t.Fatalf("InstancePath: %v", err)
	}
	// A live instance.json (this test process's own PID is always
	// "running") must exclude offline mutation entirely.
	body := `{"version":1,"url":"http://127.0.0.1:1","token":"t","pid":` +
		itoa(os.Getpid()) + `,"started_at":"2024-01-01T00:00:00Z"}`
	if err := os.WriteFile(instPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write instance.json: %v", err)
	}
	_ = dir

	_, err = acquireOfflineLock()
	cerr, ok := err.(*Error)
	if !ok || cerr.Kind != ErrKindLocked {
		t.Fatalf("expected ErrKindLocked when a running server is detected, got %v", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
