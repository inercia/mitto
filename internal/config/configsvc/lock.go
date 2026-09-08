package configsvc

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/instancefile"
)

// offlineLockFileName is the advisory lock file coordinating concurrent
// offline read-modify-write settings mutations (CLI processes, a
// concurrent startup, etc.) with each other. It is NOT settings.json.lock
// (which is left alone; unrelated to config.SaveSettings's .bak scheme).
const offlineLockFileName = "settings.lock"

// lockAcquireTimeout bounds how long Mutate waits for a contended advisory
// lock before giving up with ErrKindLocked, rather than blocking forever.
const lockAcquireTimeout = 5 * time.Second

// lockPollInterval is how often a blocked acquire attempt is retried.
const lockPollInterval = 50 * time.Millisecond

// acquireOfflineLock serializes configsvc writers against each other (an
// advisory flock on unix; see lock_unix.go/lock_other.go) and additionally
// refuses to proceed if a running Mitto server/app is currently up
// (detected via instance.json + PID liveness, mirroring
// internal/instancefile's own staleness check) — PID checks alone are not
// sufficient for the flock case, but combined with flock they cover both
// "another offline writer" and "a live running server" concurrently.
// Returns an unlock func to call (via defer) once the caller is done.
func acquireOfflineLock() (func(), error) {
	return acquireLock(lockAcquireTimeout, false)
}

// acquireOfflineLockWithTimeout is acquireOfflineLock with an explicit
// contention timeout, split out so tests can use a short timeout instead
// of waiting the full production default.
func acquireOfflineLockWithTimeout(timeout time.Duration) (func(), error) {
	return acquireLock(timeout, false)
}

// acquireInProcessLock is acquireOfflineLock's live-safe counterpart
// (mitto-4rz.3): it still takes the advisory flock below — serializing this
// call against any other configsvc writer, including another goroutine in
// THIS SAME process, since lock_unix.go's flock(2) is scoped to the open
// file description created by tryFlock's os.OpenFile, not the process, so
// two concurrent callers here still correctly contend/block each other —
// but it SKIPS the runningServerDetected refusal below. That refusal exists
// to stop an OFFLINE (separate-process) mutation from racing a live
// server's own settings.json; it must not also stop the live server itself
// from mutating its own settings, which is exactly what a live web handler
// (the caller here) needs to do.
func acquireInProcessLock() (func(), error) {
	return acquireLock(lockAcquireTimeout, true)
}

// acquireLock implements the shared body of the three lock entry points
// above: skipRunningCheck selects whether the runningServerDetected refusal
// applies (false for offline callers, true for in-process/live callers).
func acquireLock(timeout time.Duration, skipRunningCheck bool) (func(), error) {
	if !skipRunningCheck {
		if running, err := runningServerDetected(); err != nil {
			return nil, err
		} else if running {
			return nil, newErr(ErrKindLocked, "", "a running Mitto server/app owns settings.json; stop it before mutating settings offline")
		}
	}

	dir, err := appdir.Dir()
	if err != nil {
		return nil, wrapErr(ErrKindIO, "", "failed to resolve Mitto directory", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, wrapErr(ErrKindIO, "", "failed to create Mitto directory", err)
	}
	lockPath := filepath.Join(dir, offlineLockFileName)

	deadline := time.Now().Add(timeout)
	for {
		unlock, err := tryFlock(lockPath)
		if err == nil {
			return unlock, nil
		}
		if !isLockContended(err) {
			return nil, wrapErr(ErrKindIO, "", "failed to acquire settings lock", err)
		}
		if time.Now().After(deadline) {
			return nil, newErr(ErrKindLocked, "", "another process is currently writing settings.json")
		}
		time.Sleep(lockPollInterval)
	}
}

// runningServerDetected reports whether instance.json names a still-live
// process. A missing or stale (dead-PID) instance.json means "no running
// server", not an error.
func runningServerDetected() (bool, error) {
	inst, err := instancefile.Read()
	if err != nil {
		if err == instancefile.ErrNotFound {
			return false, nil
		}
		if isStaleInstanceErr(err) {
			return false, nil
		}
		// Corrupt instance.json: fail open (don't block offline mutation
		// on an unreadable discovery file) but surface nothing — this is
		// intentionally lenient, mirroring instancefile's own tolerance.
		return false, nil
	}
	return inst != nil, nil
}

func isStaleInstanceErr(err error) bool {
	return err == instancefile.ErrStale
}

// lockConflictError marks an error returned by a platform lock backend as
// "someone else holds this lock right now" (as opposed to a genuine I/O
// failure), so acquireOfflineLock knows to retry/poll instead of aborting.
type lockConflictError struct{ err error }

func (e *lockConflictError) Error() string {
	return fmt.Sprintf("lock held by another process: %v", e.err)
}
func (e *lockConflictError) Unwrap() error { return e.err }

func isLockContended(err error) bool {
	_, ok := err.(*lockConflictError)
	return ok
}
