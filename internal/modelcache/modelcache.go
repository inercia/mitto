// Package modelcache persists the most recently advertised selectable models
// for each exact ACP server name and working directory.
package modelcache

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

// Version is the on-disk schema version. Files with a missing or different
// version (including the legacy server-only format) are invalidated, never
// guessed into a folder.
const Version = 2

var (
	// ErrInvalidScope is returned when the server or working directory is empty.
	ErrInvalidScope = errors.New("model catalog scope requires server and working directory")
	// ErrIncomplete is returned for incomplete, degraded or current-only observations.
	ErrIncomplete = errors.New("model catalog observation is incomplete")
	// ErrStale is returned when an observation is older than the stored record.
	ErrStale = errors.New("model catalog observation is stale")
)

// Catalog is the last complete model list observed for one server and folder.
// An empty Models list is a trusted explicit-empty record.
type Catalog struct {
	Models     []Model   `json:"models"`
	UpdatedAt  time.Time `json:"updated_at"`
	Generation uint64    `json:"generation"`
}

// Observation is one raw catalog observation for a server and folder.
type Observation struct {
	Server     string
	WorkingDir string
	Models     []Model
	// Complete must be true for the observation to be recorded.
	Complete   bool
	Generation uint64
	// ObservedAt defaults to now when zero.
	ObservedAt time.Time
}

type fileFormat struct {
	Version int                `json:"version"`
	Entries map[string]Catalog `json:"entries"`
}

// Store is a concurrent-safe in-memory view of the atomic JSON cache. It holds
// raw catalogs only; hidden-model policy is applied elsewhere.
type Store struct {
	mu       sync.RWMutex
	path     string
	catalogs map[string]Catalog
}

func scopeKey(server, workingDir string) (string, bool) {
	dir := strings.TrimSpace(workingDir)
	if dir != "" {
		dir = strings.TrimSpace(filepath.Clean(dir))
	}
	if server == "" || dir == "" {
		return "", false
	}
	return server + "\x00" + dir, true
}

// New loads the cache from path. A missing file is an empty cache. Files with
// a missing or old version are invalidated with a warning. If the file cannot
// be read or decoded, New returns a usable empty store together with the load
// error. An empty path creates a disabled, in-memory store.
func New(path string) (*Store, error) {
	store := &Store{path: path, catalogs: make(map[string]Catalog)}
	if path == "" {
		return store, nil
	}
	var f fileFormat
	if err := fileutil.ReadJSON(path, &f); err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			// A legacy server-only map has no version field and decodes badly.
			var legacy map[string]json.RawMessage
			if fileutil.ReadJSON(path, &legacy) == nil {
				slog.Warn("Invalidating legacy model catalog cache", "path", path)
				return store, nil
			}
		}
		return store, fmt.Errorf("load model catalog cache: %w", err)
	}
	if f.Version != Version {
		slog.Warn("Invalidating model catalog cache with unsupported version",
			"path", path, "version", f.Version, "want", Version)
		return store, nil
	}
	if f.Entries != nil {
		store.catalogs = f.Entries
	}
	return store, nil
}

// Get returns an independent snapshot of the catalog for the exact server and
// folder. present distinguishes an absent scope from a present-empty one.
func (s *Store) Get(server, workingDir string) (catalog Catalog, present bool) {
	if s == nil {
		return Catalog{}, false
	}
	key, ok := scopeKey(server, workingDir)
	if !ok {
		return Catalog{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	catalog, present = s.catalogs[key]
	if !present {
		return Catalog{}, false
	}
	catalog.Models = cloneModels(catalog.Models)
	return catalog, true
}

// Record replaces the full prior list for the observation's scope. Incomplete
// observations are rejected so last-known-good data is never erased. Older
// generations, or older observation times at the same generation, are rejected
// with ErrStale. The file is written atomically before the in-memory value
// changes.
func (s *Store) Record(obs Observation) error {
	key, ok := scopeKey(obs.Server, obs.WorkingDir)
	if !ok {
		return ErrInvalidScope
	}
	if !obs.Complete {
		return ErrIncomplete
	}
	if s == nil {
		return fmt.Errorf("model catalog cache is unavailable")
	}
	if s.path == "" {
		return fmt.Errorf("model catalog cache path is unavailable")
	}
	observedAt := obs.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	observedAt = observedAt.UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	if prior, exists := s.catalogs[key]; exists {
		if obs.Generation < prior.Generation ||
			(obs.Generation == prior.Generation && observedAt.Before(prior.UpdatedAt)) {
			return ErrStale
		}
	}
	next := cloneCatalogs(s.catalogs)
	models := cloneModels(obs.Models)
	if models == nil {
		models = []Model{}
	}
	next[key] = Catalog{Models: models, UpdatedAt: observedAt, Generation: obs.Generation}
	if err := fileutil.WriteJSONAtomic(s.path, fileFormat{Version: Version, Entries: next}, 0o644); err != nil {
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
