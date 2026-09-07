//go:build unix

package configsvc

import (
	"os"
	"syscall"
)

// tryFlock takes a non-blocking exclusive advisory lock on path (created if
// needed) using flock(2). The returned unlock func releases the lock and
// closes the file handle; it is safe to call at most once.
func tryFlock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, &lockConflictError{err: err}
		}
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
