// Package fileutil provides common file I/O utilities for JSON operations.
package fileutil

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
)

// atomicTmpCounter provides a process-unique suffix for temp files created by
// WriteJSONAtomic, preventing rename collisions when multiple goroutines or
// processes write to the same target path concurrently.
var atomicTmpCounter uint64

// ReadJSON reads a JSON file and unmarshals it into the provided value.
// The value must be a pointer to the target type.
func ReadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}
	return nil
}

// WriteJSON writes a value to a JSON file with pretty-printing.
// It delegates to WriteJSONAtomic so concurrent writers are safe.
func WriteJSON(path string, v any, perm os.FileMode) error {
	return WriteJSONAtomic(path, v, perm)
}

// WriteJSONAtomic writes a value to a JSON file atomically with pretty-printing.
// It writes to a temporary file, syncs to disk, then renames to the target path.
// This ensures the file is either fully written or not modified at all.
// The temp filename includes the process PID and a per-process atomic counter so
// concurrent callers (goroutines or sibling processes) never collide on the same tmp path.
func WriteJSONAtomic(path string, v any, perm os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	// Write to temp file first; unique suffix prevents cross-goroutine/process collisions.
	tmpPath := fmt.Sprintf("%s.%d.%d.tmp", path, os.Getpid(), atomic.AddUint64(&atomicTmpCounter, 1))
	if err := os.WriteFile(tmpPath, data, perm); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Sync to ensure data is on disk before rename
	f, err := os.Open(tmpPath)
	if err == nil {
		_ = f.Sync()
		f.Close()
	}

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath) // Clean up temp file
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}
