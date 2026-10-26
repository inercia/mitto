// Package fileutil provides common file I/O utilities for JSON operations.
package fileutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
)

// atomicTmpCounter provides a process-unique suffix for temp files created by
// WriteJSONAtomic, preventing rename collisions when multiple goroutines or
// processes write to the same target path concurrently.
var atomicTmpCounter uint64

// ErrParentDirMissing is returned by WriteJSONAtomicIfDirExists when the
// target file's parent directory does not exist. Callers writing
// session-scoped sidecars (queue.json, processor_state.json, ...) should
// treat this as a benign no-op: it means the owning directory was removed
// concurrently (e.g. the session was deleted), so there is nothing
// meaningful left to persist into (mitto-32ef).
var ErrParentDirMissing = errors.New("fileutil: parent directory does not exist")

// WriteJSONAtomicDurableIfDirExists is an opt-in checked write. It never creates
// directories. Success includes file sync/close, rename and directory sync/close.
// An error after rename can leave the new value visible but unacknowledged;
// callers must not infer that every error means the old value is still present.
func WriteJSONAtomicDurableIfDirExists(path string, value any, perm os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal durable JSON: %w", err)
	}
	return writeAtomicDurable(path, data, perm, durableFileOps{
		create: func(name string, mode os.FileMode) (durableFile, error) {
			return os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		},
		openDir: func(name string) (durableFile, error) { return os.Open(name) },
		rename:  os.Rename, remove: os.Remove,
	})
}

type durableFile interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

// Per-call operations keep failure injection deterministic without global hooks.
type durableFileOps struct {
	create  func(string, os.FileMode) (durableFile, error)
	openDir func(string) (durableFile, error)
	rename  func(string, string) error
	remove  func(string) error
}

func writeAtomicDurable(path string, data []byte, perm os.FileMode, ops durableFileOps) error {
	tmp := fmt.Sprintf("%s.%d.%d.tmp", path, os.Getpid(), atomic.AddUint64(&atomicTmpCounter, 1))
	f, err := ops.create(tmp, perm)
	if err != nil {
		return fmt.Errorf("create durable temporary file: %w", err)
	}
	defer ops.remove(tmp)
	n, writeErr := f.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("write/sync/close durable file: %w", err)
	}
	if err := ops.rename(tmp, path); err != nil {
		return fmt.Errorf("rename durable file: %w", err)
	}
	dir, err := ops.openDir(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open durable directory (rename completed): %w", err)
	}
	syncErr := dir.Sync()
	closeErr = dir.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return fmt.Errorf("sync/close durable directory (rename completed): %w", err)
	}
	return nil
}

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
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}
	return writeJSONAtomicNoMkdir(path, v, perm)
}

// WriteJSONAtomicIfDirExists behaves like WriteJSONAtomic except it never
// creates the parent directory. If the parent directory does not already
// exist, the write is skipped and ErrParentDirMissing is returned.
//
// This exists so session-scoped sidecar writers (queue.json,
// processor_state.json, action_buttons.json, loop.json, ...) cannot
// resurrect a session directory that Store.Delete concurrently removed via
// os.RemoveAll: without this guard, WriteJSONAtomic's own MkdirAll would
// recreate the directory containing only that one sidecar file, leaking an
// orphan directory with no metadata.json/events.jsonl (mitto-32ef).
func WriteJSONAtomicIfDirExists(path string, v any, perm os.FileMode) error {
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		if os.IsNotExist(err) {
			return ErrParentDirMissing
		}
		return err
	}
	return writeJSONAtomicNoMkdir(path, v, perm)
}

// writeJSONAtomicNoMkdir marshals v and writes it to path via the
// write-temp-then-rename sequence shared by WriteJSONAtomic and
// WriteJSONAtomicIfDirExists. The caller is responsible for ensuring the
// parent directory exists (or intentionally skipping the write if not).
func writeJSONAtomicNoMkdir(path string, v any, perm os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
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
