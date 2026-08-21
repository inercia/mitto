// Package bdexec coordinates bd subprocess execution across Mitto packages.
package bdexec

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// MaxConcurrent bounds process-wide bd subprocess concurrency. Embedded Dolt
// serializes many operations internally, so allowing a large spawn fan-out only
// compounds lock contention without increasing useful throughput (mitto-i2ep).
const MaxConcurrent = 2

var slots = make(chan struct{}, MaxConcurrent)

type databaseSlot struct {
	token chan struct{}
	refs  int
}

var databases = struct {
	sync.Mutex
	slots map[string]*databaseSlot
}{slots: make(map[string]*databaseSlot)}

// Acquire serializes bd commands resolving to the same .beads database, then
// waits for a process-wide execution slot. The optional binary argument supports
// processors that invoke bd by absolute path.
func Acquire(ctx context.Context, dir string, binaries ...string) (release func(), err error) {
	waitStarted := time.Now()
	database := databaseKey(dir)
	releaseDatabase, err := acquireDatabase(ctx, database)
	if err != nil {
		logAcquireFailure(waitStarted, database, "database", err)
		return nil, err
	}

	databaseWaitDuration := time.Since(waitStarted)
	globalWaitStarted := time.Now()
	select {
	case slots <- struct{}{}:
		waitDuration := time.Since(waitStarted)
		globalWaitDuration := time.Since(globalWaitStarted)
		binary := "bd"
		if len(binaries) > 0 && binaries[0] != "" {
			binary = binaries[0]
		}
		if err := ensureCompatible(ctx, binary); err != nil {
			<-slots
			releaseDatabase()
			slog.Warn("bd compatibility check blocked execution", "binary", binary, "error", err)
			return nil, err
		}
		executionStarted := time.Now()
		return func() {
			executionDuration := time.Since(executionStarted)
			<-slots
			releaseDatabase()
			slog.Debug("bd limiter slot released",
				"bd_database", database,
				"bd_limiter_wait_ms", waitDuration.Milliseconds(),
				"bd_database_wait_ms", databaseWaitDuration.Milliseconds(),
				"bd_global_wait_ms", globalWaitDuration.Milliseconds(),
				"bd_execution_ms", executionDuration.Milliseconds(),
				"slots_in_use", len(slots),
				"max_concurrent", MaxConcurrent)
		}, nil
	case <-ctx.Done():
		releaseDatabase()
		logAcquireFailure(waitStarted, database, "global", ctx.Err())
		return nil, ctx.Err()
	}
}

func acquireDatabase(ctx context.Context, key string) (func(), error) {
	databases.Lock()
	slot := databases.slots[key]
	if slot == nil {
		slot = &databaseSlot{token: make(chan struct{}, 1)}
		databases.slots[key] = slot
	}
	slot.refs++
	databases.Unlock()

	select {
	case slot.token <- struct{}{}:
		return func() {
			<-slot.token
			releaseDatabaseRef(key, slot)
		}, nil
	case <-ctx.Done():
		releaseDatabaseRef(key, slot)
		return nil, ctx.Err()
	}
}

func releaseDatabaseRef(key string, slot *databaseSlot) {
	databases.Lock()
	defer databases.Unlock()
	slot.refs--
	if slot.refs == 0 {
		delete(databases.slots, key)
	}
}

func databaseKey(dir string) string {
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		} else {
			dir = "."
		}
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = filepath.Clean(dir)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absDir); resolveErr == nil {
		absDir = resolved
	}
	for candidate := absDir; ; candidate = filepath.Dir(candidate) {
		if _, statErr := os.Stat(filepath.Join(candidate, ".beads")); statErr == nil {
			return candidate
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return absDir
		}
	}
}

func logAcquireFailure(started time.Time, database, stage string, err error) {
	slog.Debug("bd limiter acquire failed",
		"bd_database", database,
		"bd_limiter_stage", stage,
		"bd_limiter_wait_ms", time.Since(started).Milliseconds(),
		"error", err)
}
