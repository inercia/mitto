package session

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// isPIDRunning checks if a process with the given PID is still running.
func isPIDRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds, so we need to send signal 0 to check
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// lockRegistry keeps track of all active locks for cleanup on exit.
var (
	registryMu     sync.Mutex
	activeLocks    = make(map[string]*Lock) // instanceID -> Lock
	cleanupStarted bool
)

// registerLock adds a lock to the cleanup registry.
func registerLock(lock *Lock) {
	registryMu.Lock()
	defer registryMu.Unlock()

	activeLocks[lock.instanceID] = lock

	// Start cleanup handler on first lock registration
	if !cleanupStarted {
		cleanupStarted = true
		go cleanupOnSignal()
	}
}

// unregisterLock removes a lock from the cleanup registry.
func unregisterLock(lock *Lock) {
	registryMu.Lock()
	defer registryMu.Unlock()

	delete(activeLocks, lock.instanceID)
}

// cleanupOnSignal handles cleanup when the process receives termination signals.
func cleanupOnSignal() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	<-sigCh

	// Release all active locks
	releaseAllLocks()

	// Re-raise the signal to allow normal termination
	signal.Stop(sigCh)
}

// releaseAllLocks releases all registered locks.
// This is called on signal or can be called manually for cleanup.
func releaseAllLocks() {
	registryMu.Lock()
	locks := make([]*Lock, 0, len(activeLocks))
	for _, lock := range activeLocks {
		locks = append(locks, lock)
	}
	registryMu.Unlock()

	// Release locks outside the registry lock to avoid deadlock
	for _, lock := range locks {
		lock.Release()
	}
}
