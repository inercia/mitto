package modelcache

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStoreRecordReloadsCatalogPerACPServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model-cache.json")
	store, err := New(path)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	description := "Fast model"
	models := []Model{{ID: "model-a", Name: "Model A", Description: &description}}
	if err := store.Record("server-a", models); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if err := store.Record("server-b", []Model{{ID: "model-b", Name: "Model B"}}); err != nil {
		t.Fatalf("Record(server-b) error = %v", err)
	}

	reloaded, err := New(path)
	if err != nil {
		t.Fatalf("New(reload) error = %v", err)
	}
	catalog, ok := reloaded.Get("server-a")
	if !ok {
		t.Fatal("Get(server-a) did not return persisted catalog")
	}
	if len(catalog.Models) != 1 || catalog.Models[0].ID != "model-a" ||
		catalog.Models[0].Name != "Model A" || catalog.Models[0].Description == nil ||
		*catalog.Models[0].Description != description {
		t.Fatalf("Get(server-a) = %+v", catalog)
	}
	if catalog.UpdatedAt.IsZero() || catalog.UpdatedAt.Location() != time.UTC {
		t.Errorf("UpdatedAt = %v; want non-zero UTC timestamp", catalog.UpdatedAt)
	}
	if _, ok := reloaded.Get("server-b"); !ok {
		t.Fatal("server-b catalog was lost while recording server-a")
	}
}

func TestNewMissingOrCorruptCacheIsSafe(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		store, err := New(filepath.Join(t.TempDir(), "missing.json"))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if _, ok := store.Get("server-a"); ok {
			t.Fatal("missing cache unexpectedly returned an entry")
		}
	})

	t.Run("corrupt", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "corrupt.json")
		if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		store, err := New(path)
		if err == nil {
			t.Fatal("New() error = nil for corrupt JSON")
		}
		if store == nil {
			t.Fatal("New() returned nil store for corrupt JSON")
		}
		if _, ok := store.Get("server-a"); ok {
			t.Fatal("corrupt cache unexpectedly returned an entry")
		}
		if err := store.Record("server-a", []Model{{ID: "model-a", Name: "Model A"}}); err != nil {
			t.Fatalf("Record() did not recover the corrupt cache: %v", err)
		}
	})
}

func TestRecordEmptyCatalogPreservesLastValidEntry(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "model-cache.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Record("server-a", []Model{{ID: "model-a", Name: "Model A"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Record("server-a", nil); err != nil {
		t.Fatalf("empty Record() error = %v", err)
	}
	catalog, ok := store.Get("server-a")
	if !ok || len(catalog.Models) != 1 || catalog.Models[0].ID != "model-a" {
		t.Fatalf("empty catalog clobbered last valid entry: %+v, ok=%v", catalog, ok)
	}
}

func TestRecordWriteFailurePreservesInMemoryCatalog(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "model-cache.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Record("server-a", []Model{{ID: "model-a", Name: "Model A"}}); err != nil {
		t.Fatal(err)
	}

	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	store.path = filepath.Join(blocker, "model-cache.json")
	if err := store.Record("server-a", []Model{{ID: "model-b", Name: "Model B"}}); err == nil {
		t.Fatal("Record() error = nil when parent path is a file")
	}
	catalog, ok := store.Get("server-a")
	if !ok || catalog.Models[0].ID != "model-a" {
		t.Fatalf("failed write changed in-memory catalog: %+v, ok=%v", catalog, ok)
	}
}

func TestStoreConcurrentRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model-cache.json")
	store, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	const count = 16
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			server := string(rune('a' + i))
			if err := store.Record(server, []Model{{ID: server, Name: server}}); err != nil {
				t.Errorf("Record(%q) error = %v", server, err)
			}
		}(i)
	}
	wg.Wait()

	reloaded, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		server := string(rune('a' + i))
		catalog, ok := reloaded.Get(server)
		if !ok || len(catalog.Models) != 1 || catalog.Models[0].ID != server {
			t.Errorf("Get(%q) = %+v, ok=%v", server, catalog, ok)
		}
	}
}
