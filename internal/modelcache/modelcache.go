// Package modelcache persists the most recently advertised selectable models
// for each exact ACP server name.
package modelcache

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/fileutil"
)

// Model is the agent-advertised identity and display metadata for one model.
// The current selection is intentionally not part of this cache.
type Model struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// Catalog is the last non-empty model list advertised by one ACP server.
type Catalog struct {
	Models    []Model   `json:"models"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store is a concurrent-safe in-memory view of the atomic JSON cache.
type Store struct {
	mu       sync.RWMutex
	path     string
	catalogs map[string]Catalog
}

// New loads the catalog map from path. A missing file is an empty cache. If
// the file cannot be read or decoded, New returns a usable empty store together
// with the load error so callers can log it without preventing startup.
// An empty path creates a disabled, in-memory store.
func New(path string) (*Store, error) {
	store := &Store{
		path:     path,
		catalogs: make(map[string]Catalog),
	}
	if path == "" {
		return store, nil
	}
	if err := fileutil.ReadJSON(path, &store.catalogs); err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return store, fmt.Errorf("load model catalog cache: %w", err)
	}
	if store.catalogs == nil {
		store.catalogs = make(map[string]Catalog)
	}
	return store, nil
}

// Get returns an independent snapshot of the cached catalog for acpServer.
func (s *Store) Get(acpServer string) (Catalog, bool) {
	if s == nil {
		return Catalog{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	catalog, ok := s.catalogs[acpServer]
	if !ok || len(catalog.Models) == 0 {
		return Catalog{}, false
	}
	catalog.Models = cloneModels(catalog.Models)
	return catalog, true
}

// Record replaces the last-seen catalog for acpServer. Empty catalogs and an
// empty server name are ignored so they cannot erase or create an unscoped
// entry. File writes are atomic; the in-memory view changes only after a
// successful write.
func (s *Store) Record(acpServer string, models []Model) error {
	if len(models) == 0 || acpServer == "" {
		return nil
	}
	if s == nil {
		return fmt.Errorf("model catalog cache is unavailable")
	}
	if s.path == "" {
		return fmt.Errorf("model catalog cache path is unavailable")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	next := cloneCatalogs(s.catalogs)
	next[acpServer] = Catalog{
		Models:    cloneModels(models),
		UpdatedAt: time.Now().UTC(),
	}
	if err := fileutil.WriteJSONAtomic(s.path, next, 0o644); err != nil {
		return fmt.Errorf("write model catalog cache: %w", err)
	}
	s.catalogs = next
	return nil
}

func cloneCatalogs(catalogs map[string]Catalog) map[string]Catalog {
	cloned := make(map[string]Catalog, len(catalogs))
	for acpServer, catalog := range catalogs {
		catalog.Models = cloneModels(catalog.Models)
		cloned[acpServer] = catalog
	}
	return cloned
}

func cloneModels(models []Model) []Model {
	if models == nil {
		return nil
	}
	cloned := make([]Model, len(models))
	for i, model := range models {
		cloned[i] = model
		if model.Description != nil {
			description := *model.Description
			cloned[i].Description = &description
		}
	}
	return cloned
}
