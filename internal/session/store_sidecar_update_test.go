package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestUpdateSessionSidecarJSON(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Create(Metadata{SessionID: "update-session"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var seen []byte
	if err := store.UpdateSessionSidecarJSON("update-session", "state.json", func(current []byte) ([]byte, error) {
		seen = current
		return nil, nil
	}); err != nil {
		t.Fatalf("no-op update: %v", err)
	}
	if seen != nil {
		t.Fatalf("absent sidecar passed as %q, want nil", seen)
	}
	if _, err := os.Stat(filepath.Join(store.SessionDir("update-session"), "state.json")); !os.IsNotExist(err) {
		t.Fatalf("nil update created the sidecar: %v", err)
	}

	if err := store.UpdateSessionSidecarJSON("update-session", "state.json", func([]byte) ([]byte, error) {
		return []byte(`{"n":1}`), nil
	}); err != nil {
		t.Fatalf("write update: %v", err)
	}

	boom := errors.New("convert failed")
	if err := store.UpdateSessionSidecarJSON("update-session", "state.json", func([]byte) ([]byte, error) {
		return []byte(`{"n":2}`), boom
	}); !errors.Is(err, boom) {
		t.Fatalf("error update returned %v, want %v", err, boom)
	}
	if err := store.UpdateSessionSidecarJSON("update-session", "state.json", func([]byte) ([]byte, error) {
		return []byte(`not json`), nil
	}); err == nil {
		t.Fatal("accepted invalid JSON replacement")
	}

	var got map[string]int
	if err := store.ReadSessionSidecarJSON("update-session", "state.json", &got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got["n"] != 1 {
		t.Fatalf("n=%d after failed updates, want 1", got["n"])
	}

	if err := store.UpdateSessionSidecarJSON("update-session", "../escape.json", func([]byte) ([]byte, error) { return nil, nil }); err == nil {
		t.Fatal("accepted unsafe sidecar name")
	}
	if err := store.UpdateSessionSidecarJSON("missing-session", "state.json", func([]byte) ([]byte, error) {
		return []byte(`{}`), nil
	}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("missing session error=%v, want ErrSessionNotFound", err)
	}
}

func TestUpdateSessionSidecarJSON_ConcurrentIncrementsAreNotLost(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Create(Metadata{SessionID: "race-session"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- store.UpdateSessionSidecarJSON("race-session", "counter.json", func(current []byte) ([]byte, error) {
				var doc map[string]int
				if current != nil {
					if err := json.Unmarshal(current, &doc); err != nil {
						return nil, err
					}
				}
				if doc == nil {
					doc = map[string]int{}
				}
				doc[fmt.Sprintf("w%d", i)] = 1
				return json.Marshal(doc)
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("update: %v", err)
		}
	}

	var got map[string]int
	if err := store.ReadSessionSidecarJSON("race-session", "counter.json", &got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != workers {
		t.Fatalf("kept %d of %d concurrent writes", len(got), workers)
	}
}
