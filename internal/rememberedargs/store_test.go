package rememberedargs

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestStore_EmptyIdentifiers_NoOp(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	if err := s.Set("", "prompt", map[string]string{"a": "1"}); err != nil {
		t.Fatalf("Set with empty uuid: %v", err)
	}
	if err := s.Set("ws", "", map[string]string{"a": "1"}); err != nil {
		t.Fatalf("Set with empty prompt: %v", err)
	}
	if err := s.Set("ws", "p", nil); err != nil {
		t.Fatalf("Set with nil args: %v", err)
	}

	got, err := s.Get("", "p")
	if err != nil || len(got) != 0 {
		t.Fatalf("Get empty uuid: got=%v err=%v", got, err)
	}
	got, err = s.Get("ws", "")
	if err != nil || len(got) != 0 {
		t.Fatalf("Get empty prompt: got=%v err=%v", got, err)
	}
}

func TestStore_InertWhenBaseDirEmpty(t *testing.T) {
	s := NewStore("")
	if err := s.Set("ws", "p", map[string]string{"a": "1"}); err != nil {
		t.Fatalf("Set inert: %v", err)
	}
	got, err := s.Get("ws", "p")
	if err != nil || len(got) != 0 {
		t.Fatalf("Get inert: got=%v err=%v", got, err)
	}
}

func TestStore_RoundTrip_And_MergesExisting(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	if err := s.Set("ws1", "commit", map[string]string{"Message": "first", "Amend": "true"}); err != nil {
		t.Fatalf("Set first: %v", err)
	}
	got, err := s.Get("ws1", "commit")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got["Message"] != "first" || got["Amend"] != "true" {
		t.Fatalf("round-trip mismatch: %v", got)
	}

	// Merge: existing "Amend" preserved; "Message" overwritten.
	if err := s.Set("ws1", "commit", map[string]string{"Message": "second"}); err != nil {
		t.Fatalf("Set second: %v", err)
	}
	got, _ = s.Get("ws1", "commit")
	if got["Message"] != "second" {
		t.Fatalf("Message not overwritten: %v", got)
	}
	if got["Amend"] != "true" {
		t.Fatalf("Amend not preserved on merge: %v", got)
	}

	// Different prompt name is isolated.
	if err := s.Set("ws1", "review", map[string]string{"Kind": "quick"}); err != nil {
		t.Fatalf("Set review: %v", err)
	}
	got, _ = s.Get("ws1", "commit")
	if _, has := got["Kind"]; has {
		t.Fatalf("prompt isolation broken: %v", got)
	}

	// Returned map is a copy — mutating it must not affect subsequent Gets.
	got["Message"] = "MUTATED"
	got2, _ := s.Get("ws1", "commit")
	if got2["Message"] == "MUTATED" {
		t.Fatalf("Get did not return a copy")
	}
}

func TestStore_PersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	s1 := NewStore(dir)
	if err := s1.Set("ws2", "run", map[string]string{"Target": "prod"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Ensure the file materialised where we expect.
	if _, err := readAllFile(t, filepath.Join(dir, "ws2.json")); err != nil {
		t.Fatalf("expected snapshot file: %v", err)
	}

	s2 := NewStore(dir)
	got, err := s2.Get("ws2", "run")
	if err != nil {
		t.Fatalf("Get on fresh Store: %v", err)
	}
	if got["Target"] != "prod" {
		t.Fatalf("persistence broken: %v", got)
	}
}

func TestStore_MissingFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	got, err := s.Get("nope", "p")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestStore_ConcurrentSetGet(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			_ = s.Set("wsC", "p", map[string]string{"k": "v"})
		}(i)
		go func() {
			defer wg.Done()
			_, _ = s.Get("wsC", "p")
		}()
	}
	wg.Wait()

	got, _ := s.Get("wsC", "p")
	if got["k"] != "v" {
		t.Fatalf("final state wrong: %v", got)
	}
}

func readAllFile(t *testing.T, path string) ([]byte, error) {
	t.Helper()
	return readFileHelper(path)
}
