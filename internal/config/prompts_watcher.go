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

// DebounceDelay is the default delay for debouncing file system events.
const DebounceDelay = 100 * time.Millisecond

// PromptsChangeEvent represents a notification that prompts have changed.
type PromptsChangeEvent struct {
	// ChangedDirs contains the directories that had changes.
	ChangedDirs []string
	// Timestamp is when the change was detected.
	Timestamp time.Time
}

// PromptsSubscriber receives notifications when prompts change.
// Implementations must be safe for concurrent use.
type PromptsSubscriber interface {
	// OnPromptsChanged is called when any watched prompt directory changes.
	// The event contains information about which directories changed.
	OnPromptsChanged(event PromptsChangeEvent)
}

// PromptsWatcher monitors prompt directories for changes and notifies subscribers.
// It supports multiple directories with shared watching - when multiple conversations
// watch the same directory, only one fsnotify watch is used.
//
// Thread-safety: All public methods are safe for concurrent use.
type PromptsWatcher struct {
	mu sync.RWMutex

	// watcher is the underlying fsnotify watcher.
	watcher *fsnotify.Watcher

	// dirRefCounts tracks how many subscribers are interested in each directory.
	// When ref count reaches 0, the watch is removed.
	dirRefCounts map[string]int

	// subscriberDirs tracks which directories each subscriber is watching.
	subscriberDirs map[PromptsSubscriber]map[string]struct{}

	// subscribers is the set of all active subscribers.
	subscribers map[PromptsSubscriber]struct{}

	// debounceDelay is the delay before firing change events.
	debounceDelay time.Duration

	// pendingChanges accumulates directories with pending changes during debounce.
	pendingChanges map[string]struct{}
	debounceTimer  *time.Timer
	debounceMu     sync.Mutex

	// logger for debugging.
	logger *slog.Logger

	// done signals the event loop to stop.
	done chan struct{}
	// stopped is closed when the event loop has exited.
	stopped chan struct{}
}

// NewPromptsWatcher creates a new prompts watcher.
// Call Start() to begin watching and Close() when done.
func NewPromptsWatcher(logger *slog.Logger) (*PromptsWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	pw := &PromptsWatcher{
		watcher:        watcher,
		dirRefCounts:   make(map[string]int),
		subscriberDirs: make(map[PromptsSubscriber]map[string]struct{}),
		subscribers:    make(map[PromptsSubscriber]struct{}),
		debounceDelay:  DebounceDelay,
		pendingChanges: make(map[string]struct{}),
		logger:         logger,
		done:           make(chan struct{}),
		stopped:        make(chan struct{}),
	}

	return pw, nil
}

// SetDebounceDelay sets the debounce delay for batching rapid changes.
// Must be called before Start() or while no events are being processed.
func (pw *PromptsWatcher) SetDebounceDelay(d time.Duration) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	pw.debounceDelay = d
}

// Start begins the event processing loop.
// This should be called once after creating the watcher.
func (pw *PromptsWatcher) Start() {
	go pw.eventLoop()
}

// Close stops the watcher and releases resources.
// After Close returns, no more events will be delivered to subscribers.
func (pw *PromptsWatcher) Close() error {
	close(pw.done)
	err := pw.watcher.Close()
	<-pw.stopped // Wait for event loop to exit
	return err
}

// Subscribe registers a subscriber to receive notifications for the given directories.
// If a directory doesn't exist, it will attempt to watch the parent directory
// to detect when it's created.
//
// The same subscriber can be subscribed multiple times with different directories;
// directories are accumulated. Use Unsubscribe to remove all directories at once.
func (pw *PromptsWatcher) Subscribe(sub PromptsSubscriber, dirs []string) error {
	pw.mu.Lock()
	defer pw.mu.Unlock()

	// Initialize subscriber's directory set if needed
	if pw.subscriberDirs[sub] == nil {
		pw.subscriberDirs[sub] = make(map[string]struct{})
	}
	pw.subscribers[sub] = struct{}{}

	for _, dir := range dirs {
		// Normalize path
		absDir, err := filepath.Abs(dir)
		if err != nil {
			if pw.logger != nil {
				pw.logger.Warn("Failed to get absolute path for prompts dir",
					"dir", dir, "error", err)
			}
			continue
		}

		// Track this directory for this subscriber
		if _, exists := pw.subscriberDirs[sub][absDir]; exists {
			continue // Already watching this dir for this subscriber
		}
		pw.subscriberDirs[sub][absDir] = struct{}{}

		// Increment ref count and potentially add watch
		pw.dirRefCounts[absDir]++
		if pw.dirRefCounts[absDir] == 1 {
			// First subscriber for this directory - add watch
			if err := pw.addWatch(absDir); err != nil && pw.logger != nil {
				pw.logger.Warn("Failed to add watch for prompts dir",
					"dir", absDir, "error", err)
			}
		}
	}

	return nil
}

// Unsubscribe removes a subscriber and decrements ref counts for its directories.
// If this was the last subscriber for a directory, the watch is removed.
func (pw *PromptsWatcher) Unsubscribe(sub PromptsSubscriber) {
	pw.mu.Lock()
	defer pw.mu.Unlock()

	dirs, exists := pw.subscriberDirs[sub]
	if !exists {
		return
	}

	// Decrement ref counts for all directories this subscriber was watching
	for dir := range dirs {
		pw.dirRefCounts[dir]--
		if pw.dirRefCounts[dir] <= 0 {
			// No more subscribers for this directory - remove watch
			delete(pw.dirRefCounts, dir)
			if err := pw.watcher.Remove(dir); err != nil && pw.logger != nil {
				// Ignore errors for directories that don't exist
				if !os.IsNotExist(err) {
					pw.logger.Debug("Failed to remove watch",
						"dir", dir, "error", err)
				}
			}
		}
	}

	delete(pw.subscriberDirs, sub)
	delete(pw.subscribers, sub)
}

// SubscriberCount returns the number of active subscribers.
func (pw *PromptsWatcher) SubscriberCount() int {
	pw.mu.RLock()
	defer pw.mu.RUnlock()
	return len(pw.subscribers)
}

// WatchedDirCount returns the number of directories being watched.
func (pw *PromptsWatcher) WatchedDirCount() int {
	pw.mu.RLock()
	defer pw.mu.RUnlock()
	return len(pw.dirRefCounts)
}

// addWatch adds a watch for a directory.
// If the directory doesn't exist, watches the parent to detect creation.
// Must be called with pw.mu held.
func (pw *PromptsWatcher) addWatch(dir string) error {
	// Check if directory exists
	info, err := os.Stat(dir)
	if err == nil && info.IsDir() {
		// Directory exists - watch it directly
		return pw.watcher.Add(dir)
	}

	// Directory doesn't exist - watch parent to detect creation
	parent := filepath.Dir(dir)
	if parent == dir {
		// Reached filesystem root
		return err
	}

	// Check if parent exists
	if _, err := os.Stat(parent); err != nil {
		if pw.logger != nil {
			pw.logger.Debug("Parent directory doesn't exist, cannot watch",
				"dir", dir, "parent", parent)
		}
		return err
	}

	if pw.logger != nil {
		pw.logger.Debug("Watching parent directory for creation",
			"target", dir, "parent", parent)
	}

	return pw.watcher.Add(parent)
}

// eventLoop processes fsnotify events and debounces notifications.
func (pw *PromptsWatcher) eventLoop() {
	defer close(pw.stopped)

	for {
		select {
		case <-pw.done:
			return

		case event, ok := <-pw.watcher.Events:
			if !ok {
				return
			}
			pw.handleEvent(event)

		case err, ok := <-pw.watcher.Errors:
			if !ok {
				return
			}
			if pw.logger != nil {
				pw.logger.Warn("Prompts watcher error", "error", err)
			}
		}
	}
}

// handleEvent processes a single fsnotify event.
func (pw *PromptsWatcher) handleEvent(event fsnotify.Event) {
	// Only care about .md files and directory changes
	path := event.Name
	ext := strings.ToLower(filepath.Ext(path))

	// Check if this is a relevant event
	isRelevant := false

	// Check for .md file changes
	if ext == ".md" {
		isRelevant = event.Has(fsnotify.Create) ||
			event.Has(fsnotify.Write) ||
			event.Has(fsnotify.Remove) ||
			event.Has(fsnotify.Rename)
	}

	// Check for directory creation (might be a watched dir being created)
	if !isRelevant && (event.Has(fsnotify.Create) || event.Has(fsnotify.Write)) {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			// A directory was created - check if it's one we want to watch
			pw.mu.Lock()
			if _, tracked := pw.dirRefCounts[path]; tracked {
				// This is a directory we're interested in - add a direct watch
				if err := pw.watcher.Add(path); err == nil {
					isRelevant = true
					if pw.logger != nil {
						pw.logger.Debug("Started watching newly created directory",
							"dir", path)
					}
				}
			}
			pw.mu.Unlock()
		}
	}

	if !isRelevant {
		return
	}

	// Determine which directory this change belongs to
	dir := filepath.Dir(path)

	if pw.logger != nil {
		pw.logger.Debug("Prompts directory changed",
			"path", path,
			"dir", dir,
			"op", event.Op.String())
	}

	// Add to pending changes and reset debounce timer
	pw.debounceMu.Lock()
	pw.pendingChanges[dir] = struct{}{}

	if pw.debounceTimer != nil {
		pw.debounceTimer.Stop()
	}
	pw.debounceTimer = time.AfterFunc(pw.debounceDelay, pw.firePendingChanges)
	pw.debounceMu.Unlock()
}

// firePendingChanges notifies subscribers about accumulated changes.
func (pw *PromptsWatcher) firePendingChanges() {
	pw.debounceMu.Lock()
	changes := pw.pendingChanges
	pw.pendingChanges = make(map[string]struct{})
	pw.debounceTimer = nil
	pw.debounceMu.Unlock()

	if len(changes) == 0 {
		return
	}

	// Build list of changed directories
	changedDirs := make([]string, 0, len(changes))
	for dir := range changes {
		changedDirs = append(changedDirs, dir)
	}

	event := PromptsChangeEvent{
		ChangedDirs: changedDirs,
		Timestamp:   time.Now(),
	}

	// Get subscribers interested in these directories
	pw.mu.RLock()
	subscribersToNotify := make([]PromptsSubscriber, 0)
	for sub, dirs := range pw.subscriberDirs {
		for _, changedDir := range changedDirs {
			// Check if subscriber is watching this directory or a parent
			for watchedDir := range dirs {
				if changedDir == watchedDir || strings.HasPrefix(changedDir, watchedDir+string(filepath.Separator)) {
					subscribersToNotify = append(subscribersToNotify, sub)
					break
				}
			}
		}
	}
	pw.mu.RUnlock()

	if pw.logger != nil {
		pw.logger.Debug("Notifying subscribers of prompts changes",
			"changed_dirs", changedDirs,
			"subscriber_count", len(subscribersToNotify))
	}

	// Notify subscribers (outside of lock)
	for _, sub := range subscribersToNotify {
		sub.OnPromptsChanged(event)
	}
}
