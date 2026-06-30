package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// BeadsDebounceDelay is the trailing debounce delay for beads file-system events.
// It is intentionally larger than the shared DebounceDelay because the embedded
// Dolt database under .beads/ writes in noisy bursts (last-touched, backup/*.jsonl,
// embeddeddolt/). A longer quiet window coalesces consecutive write waves from a
// single logical operation into one notification.
const BeadsDebounceDelay = 750 * time.Millisecond

// BeadsMaxWait caps how long accumulated changes may wait before firing, even if
// new events keep arriving (which would otherwise keep resetting the trailing
// timer). It guarantees that, under sustained activity, subscribers are notified
// at most once per this window instead of being starved or woken too often.
const BeadsMaxWait = 3 * time.Second

// BeadsChangeEvent represents a notification that beads issues have changed on disk.
type BeadsChangeEvent struct {
	// ChangedDirs contains the .beads/ directories that had changes.
	ChangedDirs []string
	// WorkingDirs contains the workspace root directories (parent of each changed .beads/ dir).
	WorkingDirs []string
	// Timestamp is when the change was detected.
	Timestamp time.Time
}

// BeadsSubscriber receives notifications when beads issues change.
// Implementations must be safe for concurrent use.
type BeadsSubscriber interface {
	// OnBeadsChanged is called when any watched .beads/ directory changes.
	OnBeadsChanged(event BeadsChangeEvent)
}

// BeadsWatcher monitors .beads/ directories for changes and notifies subscribers.
// It mirrors PromptsWatcher: shared fsnotify watches, parent-dir fallback when the
// target does not yet exist, reference-counted subscriptions, and debounced fan-out.
//
// Thread-safety: All public methods are safe for concurrent use.
type BeadsWatcher struct {
	mu sync.RWMutex

	watcher            *fsnotify.Watcher
	dirRefCounts       map[string]int
	actualWatchedPaths map[string]string
	subscriberDirs     map[BeadsSubscriber]map[string]struct{}
	subscribers        map[BeadsSubscriber]struct{}

	debounceDelay  time.Duration
	maxWait        time.Duration
	pendingChanges map[string]struct{}
	firstPendingAt time.Time
	debounceTimer  *time.Timer
	debounceMu     sync.Mutex

	logger  *slog.Logger
	done    chan struct{}
	stopped chan struct{}
}

// NewBeadsWatcher creates a new beads watcher.
// Call Start() to begin watching and Close() when done.
func NewBeadsWatcher(logger *slog.Logger) (*BeadsWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &BeadsWatcher{
		watcher:            watcher,
		dirRefCounts:       make(map[string]int),
		actualWatchedPaths: make(map[string]string),
		subscriberDirs:     make(map[BeadsSubscriber]map[string]struct{}),
		subscribers:        make(map[BeadsSubscriber]struct{}),
		debounceDelay:      BeadsDebounceDelay,
		maxWait:            BeadsMaxWait,
		pendingChanges:     make(map[string]struct{}),
		logger:             logger,
		done:               make(chan struct{}),
		stopped:            make(chan struct{}),
	}, nil
}

// SetDebounceDelay sets the trailing debounce delay. Must be called before Start().
func (bw *BeadsWatcher) SetDebounceDelay(d time.Duration) {
	bw.debounceMu.Lock()
	defer bw.debounceMu.Unlock()
	bw.debounceDelay = d
}

// SetMaxWait sets the maximum time accumulated changes may wait before firing,
// even while new events keep arriving. A value <= 0 disables the cap, restoring
// pure trailing-debounce behavior. Must be called before Start().
func (bw *BeadsWatcher) SetMaxWait(d time.Duration) {
	bw.debounceMu.Lock()
	defer bw.debounceMu.Unlock()
	bw.maxWait = d
}

// Start begins the event processing loop.
func (bw *BeadsWatcher) Start() { go bw.eventLoop() }

// Close stops the watcher and releases resources.
func (bw *BeadsWatcher) Close() error {
	close(bw.done)
	err := bw.watcher.Close()
	<-bw.stopped
	return err
}

// Unsubscribe removes a subscriber and decrements ref counts for its directories.
func (bw *BeadsWatcher) Unsubscribe(sub BeadsSubscriber) {
	bw.mu.Lock()
	defer bw.mu.Unlock()

	dirs, exists := bw.subscriberDirs[sub]
	if !exists {
		return
	}
	for dir := range dirs {
		bw.dirRefCounts[dir]--
		if bw.dirRefCounts[dir] <= 0 {
			delete(bw.dirRefCounts, dir)
			actualPath := bw.actualWatchedPaths[dir]
			if actualPath == "" {
				actualPath = dir
			}
			delete(bw.actualWatchedPaths, dir)
			if err := bw.watcher.Remove(actualPath); err != nil && bw.logger != nil {
				if !os.IsNotExist(err) {
					bw.logger.Debug("Failed to remove beads watch",
						"dir", dir, "actual_path", actualPath, "error", err)
				}
			}
		}
	}
	delete(bw.subscriberDirs, sub)
	delete(bw.subscribers, sub)
}

// SubscriberCount returns the number of active subscribers.
func (bw *BeadsWatcher) SubscriberCount() int {
	bw.mu.RLock()
	defer bw.mu.RUnlock()
	return len(bw.subscribers)
}

// WatchedDirCount returns the number of directories being watched.
func (bw *BeadsWatcher) WatchedDirCount() int {
	bw.mu.RLock()
	defer bw.mu.RUnlock()
	return len(bw.dirRefCounts)
}

// Subscribe registers a subscriber for the given .beads/ directories.
// Directories that do not yet exist are handled by watching the parent.
func (bw *BeadsWatcher) Subscribe(sub BeadsSubscriber, dirs []string) error {
	bw.mu.Lock()
	defer bw.mu.Unlock()

	if bw.subscriberDirs[sub] == nil {
		bw.subscriberDirs[sub] = make(map[string]struct{})
	}
	bw.subscribers[sub] = struct{}{}

	for _, dir := range dirs {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			if bw.logger != nil {
				bw.logger.Warn("Failed to get absolute path for beads dir", "dir", dir, "error", err)
			}
			continue
		}
		if _, exists := bw.subscriberDirs[sub][absDir]; exists {
			continue
		}
		bw.subscriberDirs[sub][absDir] = struct{}{}
		bw.dirRefCounts[absDir]++
		if bw.dirRefCounts[absDir] == 1 {
			if err := bw.addWatch(absDir); err != nil && bw.logger != nil {
				bw.logger.Warn("Failed to add watch for beads dir", "dir", absDir, "error", err)
			}
		}
	}
	return nil
}

// addWatch watches dir, or its parent if dir does not yet exist.
// Must be called with bw.mu held.
func (bw *BeadsWatcher) addWatch(dir string) error {
	info, err := os.Stat(dir)
	if err == nil && info.IsDir() {
		bw.actualWatchedPaths[dir] = dir
		return bw.watcher.Add(dir)
	}

	parent := filepath.Dir(dir)
	if parent == dir {
		return err
	}
	if _, err := os.Stat(parent); err != nil {
		if bw.logger != nil {
			bw.logger.Debug("Parent directory doesn't exist, cannot watch beads dir",
				"dir", dir, "parent", parent)
		}
		return err
	}
	if bw.logger != nil {
		bw.logger.Debug("Watching parent directory for beads dir creation",
			"target", dir, "parent", parent)
	}
	bw.actualWatchedPaths[dir] = parent
	return bw.watcher.Add(parent)
}

// eventLoop processes fsnotify events and debounces notifications.
func (bw *BeadsWatcher) eventLoop() {
	defer close(bw.stopped)

	for {
		select {
		case <-bw.done:
			return
		case event, ok := <-bw.watcher.Events:
			if !ok {
				return
			}
			bw.handleEvent(event)
		case err, ok := <-bw.watcher.Errors:
			if !ok {
				return
			}
			if bw.logger != nil {
				bw.logger.Warn("Beads watcher error", "error", err)
			}
		}
	}
}

// isRelevantBeadsPath reports whether path should trigger a beads change event.
// Relevant: last-touched, backup/*.jsonl, anything under embeddeddolt/.
func isRelevantBeadsPath(path string) bool {
	base := filepath.Base(path)
	if base == "last-touched" {
		return true
	}
	// backup/*.jsonl
	dir := filepath.Dir(path)
	if filepath.Base(dir) == "backup" && strings.HasSuffix(base, ".jsonl") {
		return true
	}
	// anything inside embeddeddolt/
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if part == "embeddeddolt" {
			return true
		}
	}
	return false
}

// handleEvent processes a single fsnotify event.
func (bw *BeadsWatcher) handleEvent(event fsnotify.Event) {
	path := event.Name
	isRelevant := false

	// Check for relevant beads data files.
	if isRelevantBeadsPath(path) {
		isRelevant = event.Has(fsnotify.Create) ||
			event.Has(fsnotify.Write) ||
			event.Has(fsnotify.Remove) ||
			event.Has(fsnotify.Rename)
	}

	// Directory creation: check if it's a .beads/ dir we've been waiting for.
	if !isRelevant && (event.Has(fsnotify.Create) || event.Has(fsnotify.Write)) {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			bw.mu.Lock()
			if _, tracked := bw.dirRefCounts[path]; tracked {
				if err := bw.watcher.Add(path); err == nil {
					isRelevant = true
					if bw.logger != nil {
						bw.logger.Debug("Started watching newly created .beads dir", "dir", path)
					}
				}
			}
			bw.mu.Unlock()
		}
	}

	if !isRelevant {
		return
	}

	// Find which watched .beads/ dir this change belongs to.
	// Walk up from path until we find a tracked dir.
	bw.mu.RLock()
	var beadsDir string
	for candidate := filepath.Dir(path); candidate != filepath.Dir(candidate); candidate = filepath.Dir(candidate) {
		if _, ok := bw.dirRefCounts[candidate]; ok {
			beadsDir = candidate
			break
		}
	}
	// Also check the path itself (in case the .beads/ dir was just created).
	if beadsDir == "" {
		if _, ok := bw.dirRefCounts[path]; ok {
			beadsDir = path
		}
	}
	bw.mu.RUnlock()

	if beadsDir == "" {
		return
	}

	if bw.logger != nil {
		bw.logger.Debug("Beads directory changed",
			"path", path, "beads_dir", beadsDir, "op", event.Op.String())
	}

	bw.debounceMu.Lock()
	bw.pendingChanges[beadsDir] = struct{}{}
	now := time.Now()
	if bw.firstPendingAt.IsZero() {
		bw.firstPendingAt = now
	}
	// Trailing debounce: fire debounceDelay after the most recent event so a
	// burst of writes collapses into one notification. The maxWait cap bounds
	// the total wait from the first pending change, so sustained activity that
	// keeps resetting the trailing timer still fires at most once per window
	// instead of waking subscribers repeatedly (or being starved indefinitely).
	delay := bw.debounceDelay
	if bw.maxWait > 0 {
		if remaining := bw.maxWait - now.Sub(bw.firstPendingAt); remaining < delay {
			delay = remaining
		}
		if delay < 0 {
			delay = 0
		}
	}
	if bw.debounceTimer != nil {
		bw.debounceTimer.Stop()
	}
	bw.debounceTimer = time.AfterFunc(delay, bw.firePendingChanges)
	bw.debounceMu.Unlock()
}

// firePendingChanges notifies subscribers about accumulated changes.
func (bw *BeadsWatcher) firePendingChanges() {
	bw.debounceMu.Lock()
	changes := bw.pendingChanges
	bw.pendingChanges = make(map[string]struct{})
	bw.debounceTimer = nil
	bw.firstPendingAt = time.Time{}
	bw.debounceMu.Unlock()

	if len(changes) == 0 {
		return
	}

	changedDirs := make([]string, 0, len(changes))
	for dir := range changes {
		changedDirs = append(changedDirs, dir)
	}

	// Build de-duped WorkingDirs (parent of each .beads/ dir).
	seenWorkingDirs := make(map[string]struct{})
	workingDirs := make([]string, 0, len(changedDirs))
	for _, d := range changedDirs {
		wd := filepath.Dir(d)
		if _, seen := seenWorkingDirs[wd]; !seen {
			seenWorkingDirs[wd] = struct{}{}
			workingDirs = append(workingDirs, wd)
		}
	}

	event := BeadsChangeEvent{
		ChangedDirs: changedDirs,
		WorkingDirs: workingDirs,
		Timestamp:   time.Now(),
	}

	// Fan out to matching subscribers.
	bw.mu.RLock()
	subscriberSet := make(map[BeadsSubscriber]struct{})
	for sub, dirs := range bw.subscriberDirs {
	changedLoop:
		for _, changedDir := range changedDirs {
			for watchedDir := range dirs {
				if changedDir == watchedDir ||
					strings.HasPrefix(changedDir, watchedDir+string(filepath.Separator)) {
					subscriberSet[sub] = struct{}{}
					break changedLoop
				}
			}
		}
	}
	bw.mu.RUnlock()

	toNotify := make([]BeadsSubscriber, 0, len(subscriberSet))
	for sub := range subscriberSet {
		toNotify = append(toNotify, sub)
	}

	if bw.logger != nil {
		bw.logger.Debug("Notifying subscribers of beads changes",
			"changed_dirs", changedDirs, "subscriber_count", len(toNotify))
	}

	for _, sub := range toNotify {
		sub.OnBeadsChanged(event)
	}
}
