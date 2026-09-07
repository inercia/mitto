//go:build !unix

package configsvc

import (
	"os"
)

// tryFlock provides a best-effort advisory lock on non-unix platforms
// (no flock(2)/x/sys/unix dependency) using exclusive file creation
// (O_CREATE|O_EXCL). Unlike the unix flock backend, a stale lock file left
// behind by a crashed process is NOT automatically reclaimed here; callers
// are limited to the process-liveness (instance.json) exclusion in
// acquireOfflineLock plus this same-process serialization.
func tryFlock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, &lockConflictError{err: err}
		}
		return nil, err
	}
	return func() {
		_ = f.Close()
		_ = os.Remove(path)
	}, nil
}
