// Package hooks provides lifecycle hook execution for the Mitto web server.

package hooks

import (
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/logging"
)

// ShutdownFunc is a function that performs cleanup during shutdown.
// It receives a reason string describing why shutdown was triggered.
type ShutdownFunc func(reason string)

// ShutdownManager coordinates graceful shutdown across the application.
// It ensures cleanup functions run exactly once, handles signals, and
// provides a way for external code (like WebView) to trigger shutdown.
//
// It is safe for concurrent use.
type ShutdownManager struct {
	mu       sync.Mutex
	once     sync.Once
	done     chan struct{}
	reason   string
	cleanups []ShutdownFunc

	// Hook configuration
	upHook   *Process
	downHook config.WebHook
	port     int

	// Optional callback to terminate UI event loop (e.g., WebView.Terminate)
	onTerminateUI func()
}

// NewShutdownManager creates a new shutdown manager.
// It does not start signal handling until Start() is called.
func NewShutdownManager() *ShutdownManager {
	return &ShutdownManager{
		done: make(chan struct{}),
	}
}

// SetHooks configures the up and down hooks for the shutdown manager.
// The up hook process will be stopped during shutdown, and the down hook
// command will be executed synchronously.
func (sm *ShutdownManager) SetHooks(upHook *Process, downHook config.WebHook, port int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.upHook = upHook
	sm.downHook = downHook
	sm.port = port
}

// SetTerminateUI sets a callback that will be called during shutdown to
// terminate the UI event loop (e.g., WebView.Terminate). This is called
// after all cleanup functions have run.
func (sm *ShutdownManager) SetTerminateUI(fn func()) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.onTerminateUI = fn
}

// AddCleanup adds a cleanup function to be called during shutdown.
// Cleanup functions are called in the order they were added.
func (sm *ShutdownManager) AddCleanup(fn ShutdownFunc) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.cleanups = append(sm.cleanups, fn)
}

// Start begins listening for shutdown signals (SIGINT, SIGTERM).
// When a signal is received, Shutdown() is called automatically.
// This should be called after all cleanup functions have been registered.
func (sm *ShutdownManager) Start() {
	logger := logging.Shutdown()
	logger.Debug("Shutdown manager started, listening for signals")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.Info("Signal received, initiating shutdown",
			"signal", sig.String(),
		)
		sm.Shutdown("signal:" + sig.String())
	}()
}

// Shutdown triggers graceful shutdown with the given reason.
// It is safe to call multiple times; only the first call will execute cleanup.
// This method blocks until all cleanup is complete.
func (sm *ShutdownManager) Shutdown(reason string) {
	sm.once.Do(func() {
		sm.doShutdown(reason)
	})
}

// doShutdown performs the actual shutdown sequence.
func (sm *ShutdownManager) doShutdown(reason string) {
	logger := logging.Shutdown()
	logger.Info("Starting shutdown sequence",
		"reason", reason,
	)

	sm.mu.Lock()
	sm.reason = reason
	upHook := sm.upHook
	downHook := sm.downHook
	port := sm.port
	cleanups := make([]ShutdownFunc, len(sm.cleanups))
	copy(cleanups, sm.cleanups)
	terminateUI := sm.onTerminateUI
	sm.mu.Unlock()

	// Step 1: Stop the up hook process (if running)
	if upHook != nil {
		logger.Debug("Stopping up hook process")
		upHook.Stop()
	}

	// Step 2: Run the down hook synchronously
	if downHook.Command != "" {
		logger.Debug("Running down hook",
			"command", downHook.Command,
			"port", port,
		)
		RunDown(downHook, port)
	} else {
		logger.Debug("No down hook configured")
	}

	// Step 3: Run registered cleanup functions in order
	for i, fn := range cleanups {
		logger.Debug("Running cleanup function",
			"index", i,
			"total", len(cleanups),
		)
		fn(reason)
	}

	// Step 4: Terminate UI event loop if configured
	if terminateUI != nil {
		logger.Debug("Terminating UI event loop")
		terminateUI()
	}

	logger.Info("Shutdown sequence complete",
		"reason", reason,
	)

	// Signal that shutdown is complete
	close(sm.done)
}

// Done returns a channel that is closed when shutdown is complete.
func (sm *ShutdownManager) Done() <-chan struct{} {
	return sm.done
}

// Reason returns the reason for shutdown, or empty string if not yet shut down.
func (sm *ShutdownManager) Reason() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.reason
}
