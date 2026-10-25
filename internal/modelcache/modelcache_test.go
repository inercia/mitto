package modelcache

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "model-cache.json")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return s, path
}

func obs(server, dir string, gen uint64, at time.Time, ids ...string) Observation {
	var ms []Model
	for _, id := range ids {
		ms = append(ms, Model{ID: id, Name: id})
	}
	return Observation{Server: server, WorkingDir: dir, Models: ms, Complete: true, Generation: gen, ObservedAt: at}
}

func TestScopeIsolationAndReload(t *testing.T) {
	s, path := newStore(t)
	now := time.Now()
	for _, o := range []Observation{
		obs("a", "/x", 1, now, "m1"),
		obs("a", "/y", 1, now, "m2"),
		obs("b", "/x", 1, now, "m3"),
	} {
		if err := s.Record(o); err != nil {
			t.Fatal(err)
		}
	}
	r, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ s, d, id string }{{"a", "/x", "m1"}, {"a", "/y", "m2"}, {"b", "/x", "m3"}, {"a", "/x/", "m1"}} {
		got, ok := r.Get(c.s, c.d)
		if !ok || len(got.Models) != 1 || got.Models[0].ID != c.id {
			t.Errorf("Get(%q,%q) = %+v,%v", c.s, c.d, got, ok)
		}
	}
	if _, ok := r.Get("b", "/y"); ok {
		t.Error("unexpected cross-scope hit")
	}
	if _, ok := r.Get("a", ""); ok {
		t.Error("empty dir must be absent")
	}
}

func TestInvalidScopeAndIncompleteRejected(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Record(obs("", "/x", 1, time.Now(), "m")); !errors.Is(err, ErrInvalidScope) {
		t.Errorf("empty server err = %v", err)
	}
	if err := s.Record(obs("a", " ", 1, time.Now(), "m")); !errors.Is(err, ErrInvalidScope) {
		t.Errorf("empty dir err = %v", err)
	}
	o := obs("a", "/x", 1, time.Now(), "m")
	o.Complete = false
	if err := s.Record(o); !errors.Is(err, ErrIncomplete) {
		t.Errorf("incomplete err = %v", err)
	}
	if _, ok := s.Get("a", "/x"); ok {
		t.Error("rejected observation stored")
	}
}

func TestCompleteReplacesAndIncompleteKeepsLastKnownGood(t *testing.T) {
	s, _ := newStore(t)
	t0 := time.Now().Add(-time.Hour)
	if err := s.Record(obs("a", "/x", 1, t0, "m1", "m2")); err != nil {
		t.Fatal(err)
	}
	inc := obs("a", "/x", 2, t0.Add(time.Minute))
	inc.Complete = false
	if err := s.Record(inc); err == nil {
		t.Fatal("incomplete accepted")
	}
	if c, _ := s.Get("a", "/x"); len(c.Models) != 2 {
		t.Fatalf("last-known-good lost: %+v", c)
	}
	if err := s.Record(obs("a", "/x", 1, t0.Add(time.Minute), "m2")); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Get("a", "/x")
	if len(c.Models) != 1 || c.Models[0].ID != "m2" || !c.UpdatedAt.Equal(t0.Add(time.Minute).UTC()) {
		t.Fatalf("removal/time not applied: %+v", c)
	}
	if err := s.Record(obs("a", "/x", 1, t0.Add(2*time.Minute))); err != nil {
		t.Fatal(err)
	}
	c, ok := s.Get("a", "/x")
	if !ok || c.Models == nil || len(c.Models) != 0 {
		t.Fatalf("explicit empty must be present: %+v %v", c, ok)
	}
}

func TestStaleRejected(t *testing.T) {
	s, _ := newStore(t)
	now := time.Now()
	if err := s.Record(obs("a", "/x", 5, now, "new")); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(obs("a", "/x", 4, now.Add(time.Hour), "old")); !errors.Is(err, ErrStale) {
		t.Errorf("older generation err = %v", err)
	}
	if err := s.Record(obs("a", "/x", 5, now.Add(-time.Second), "old")); !errors.Is(err, ErrStale) {
		t.Errorf("older time err = %v", err)
	}
	if err := s.Record(obs("a", "/y", 1, now.Add(-time.Hour), "other")); err != nil {
		t.Errorf("other scope should be independent: %v", err)
	}
	if c, _ := s.Get("a", "/x"); c.Models[0].ID != "new" {
		t.Errorf("got %+v", c)
	}
}

func TestVersionInvalidation(t *testing.T) {
	cases := map[string]string{
		"legacy": `{"a":{"models":[{"id":"m","name":"M"}],"updated_at":"2024-01-01T00:00:00Z"}}`,
		"old":    `{"version":1,"entries":{"a\u0000/x":{"models":[{"id":"m","name":"M"}]}}}`,
		"none":   `{"entries":{"a\u0000/x":{"models":[]}}}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "c.json")
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			s, err := New(path)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if _, ok := s.Get("a", "/x"); ok {
				t.Error("old entry must be invalidated")
			}
		})
	}
}

func TestCorruptFileStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(path)
	if err == nil {
		t.Error("expected load error")
	}
	if s == nil {
		t.Fatal("store must be usable")
	}
	if err := s.Record(obs("a", "/x", 1, time.Now(), "m")); err != nil {
		t.Errorf("Record after corrupt: %v", err)
	}
}

func TestMissingFileAndNilStore(t *testing.T) {
	s, _ := newStore(t)
	if _, ok := s.Get("a", "/x"); ok {
		t.Error("expected absent")
	}
	var n *Store
	if _, ok := n.Get("a", "/x"); ok {
		t.Error("nil store present")
	}
}

func TestCloneSemantics(t *testing.T) {
	s, _ := newStore(t)
	d := "desc"
	o := obs("a", "/x", 1, time.Now(), "m")
	o.Models[0].Description = &d
	if err := s.Record(o); err != nil {
		t.Fatal(err)
	}
	o.Models[0].ID = "mutated"
	*o.Models[0].Description = "mutated"
	c, _ := s.Get("a", "/x")
	if c.Models[0].ID != "m" || *c.Models[0].Description != "desc" {
		t.Fatalf("input aliasing: %+v", c)
	}
	c.Models[0].ID = "x"
	*c.Models[0].Description = "x"
	c2, _ := s.Get("a", "/x")
	if c2.Models[0].ID != "m" || *c2.Models[0].Description != "desc" {
		t.Fatalf("output aliasing: %+v", c2)
	}
}

func TestWriteFailurePreservesMemory(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Record(obs("a", "/x", 1, time.Now(), "m1")); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s.path = filepath.Join(blocker, "c.json")
	if err := s.Record(obs("a", "/x", 2, time.Now(), "m2")); err == nil {
		t.Fatal("expected write error")
	}
	if c, _ := s.Get("a", "/x"); c.Models[0].ID != "m1" {
		t.Fatalf("memory changed on failure: %+v", c)
	}
}

func TestConcurrentRecords(t *testing.T) {
	s, path := newStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dir := "/d" + string(rune('a'+i))
			if err := s.Record(obs("a", dir, 1, time.Now(), "m")); err != nil {
				t.Error(err)
			}
			s.Get("a", dir)
		}(i)
	}
	wg.Wait()
	r, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		if _, ok := r.Get("a", "/d"+string(rune('a'+i))); !ok {
			t.Errorf("missing scope %d", i)
		}
	}
}
