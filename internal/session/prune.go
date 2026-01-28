package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/inercia/mitto/internal/logging"
)

// PruneConfig holds configuration for session pruning.
type PruneConfig struct {
	// MaxMessages is the maximum number of messages to retain per session.
	// If 0, no message limit is enforced.
	MaxMessages int
	// MaxSizeBytes is the maximum total size in bytes for a session's stored data.
	// If 0, no size limit is enforced.
	MaxSizeBytes int64
}

// IsEnabled returns true if any pruning limits are configured.
func (c *PruneConfig) IsEnabled() bool {
	return c.MaxMessages > 0 || c.MaxSizeBytes > 0
}

// PruneResult contains information about a pruning operation.
type PruneResult struct {
	// EventsRemoved is the number of events that were removed.
	EventsRemoved int
	// ImagesRemoved is the number of images that were cleaned up.
	ImagesRemoved int
	// BytesReclaimed is the approximate number of bytes freed.
	BytesReclaimed int64
}

// PruneIfNeeded checks if the session exceeds configured limits and prunes if necessary.
// It removes the oldest messages/events until the session is under both limits.
// Returns a PruneResult indicating what was pruned, or nil if no pruning was needed.
func (s *Store) PruneIfNeeded(sessionID string, config *PruneConfig) (*PruneResult, error) {
	if config == nil || !config.IsEnabled() {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrStoreClosed
	}

	return s.pruneInternal(sessionID, config)
}

// pruneInternal performs the actual pruning (must be called with lock held).
func (s *Store) pruneInternal(sessionID string, config *PruneConfig) (*PruneResult, error) {
	log := logging.Session()

	// Read all events to analyze what needs pruning
	events, err := s.readEventsInternal(sessionID)
	if err != nil {
		return nil, err
	}

	if len(events) == 0 {
		return nil, nil
	}

	// Calculate current size of events file
	eventsPath := s.eventsPath(sessionID)
	fileInfo, err := os.Stat(eventsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat events file: %w", err)
	}
	currentSize := fileInfo.Size()

	// Also account for images in size calculation
	_, imagesSize, err := s.listImagesInternal(sessionID)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}
	totalSize := currentSize + imagesSize

	// Determine how many events to remove
	eventsToRemove := 0

	// Check message count limit
	if config.MaxMessages > 0 && len(events) > config.MaxMessages {
		eventsToRemove = len(events) - config.MaxMessages
	}

	// Check size limit - estimate and iteratively remove until under limit
	if config.MaxSizeBytes > 0 && totalSize > config.MaxSizeBytes {
		// We need to remove events to get under the size limit
		// Start by removing enough to satisfy message count, then check size
		for eventsToRemove < len(events)-1 { // Keep at least 1 event
			estimatedRemovedSize := s.estimateEventsSize(events[:eventsToRemove])
			if totalSize-estimatedRemovedSize <= config.MaxSizeBytes {
				break
			}
			eventsToRemove++
		}
	}

	if eventsToRemove == 0 {
		return nil, nil
	}

	// Collect image references from events to be removed
	removedEvents := events[:eventsToRemove]
	remainingEvents := events[eventsToRemove:]
	imageRefsToCheck := s.extractImageRefs(removedEvents)
	activeImageRefs := s.extractImageRefs(remainingEvents)

	// Perform the pruning
	result, err := s.performPrune(sessionID, remainingEvents, imageRefsToCheck, activeImageRefs)
	if err != nil {
		return nil, err
	}

	result.EventsRemoved = eventsToRemove

	log.Info("session pruned",
		"session_id", sessionID,
		"events_removed", result.EventsRemoved,
		"images_removed", result.ImagesRemoved,
		"bytes_reclaimed", result.BytesReclaimed)

	return result, nil
}

// readEventsInternal reads events without locking (caller must hold lock).
func (s *Store) readEventsInternal(sessionID string) ([]Event, error) {
	f, err := os.Open(s.eventsPath(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("failed to open events file: %w", err)
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	const maxScannerBuffer = 10 * 1024 * 1024
	scanner.Buffer(make([]byte, 0, 64*1024), maxScannerBuffer)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("failed to unmarshal event: %w", err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read events: %w", err)
	}
	return events, nil
}

// estimateEventsSize estimates the size of serialized events.
func (s *Store) estimateEventsSize(events []Event) int64 {
	var size int64
	for _, event := range events {
		// Marshal each event and count bytes plus newline
		data, err := json.Marshal(event)
		if err == nil {
			size += int64(len(data)) + 1 // +1 for newline
		}
	}
	return size
}

// extractImageRefs extracts all image references from events.
func (s *Store) extractImageRefs(events []Event) map[string]struct{} {
	refs := make(map[string]struct{})
	for _, event := range events {
		if event.Type == EventTypeUserPrompt {
			if data, ok := event.Data.(map[string]interface{}); ok {
				if images, ok := data["images"].([]interface{}); ok {
					for _, img := range images {
						if imgMap, ok := img.(map[string]interface{}); ok {
							if id, ok := imgMap["id"].(string); ok && id != "" {
								refs[id] = struct{}{}
							}
						}
					}
				}
			}
		}
	}
	return refs
}

// performPrune rewrites the events file and cleans up orphaned images.
func (s *Store) performPrune(
	sessionID string,
	remainingEvents []Event,
	imageRefsToCheck map[string]struct{},
	activeImageRefs map[string]struct{},
) (*PruneResult, error) {
	result := &PruneResult{}

	// Get original file size for bytes reclaimed calculation
	eventsPath := s.eventsPath(sessionID)
	originalInfo, _ := os.Stat(eventsPath)
	var originalSize int64
	if originalInfo != nil {
		originalSize = originalInfo.Size()
	}

	// Rewrite events file with renumbered sequence numbers
	tmpPath := eventsPath + ".tmp"
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp events file: %w", err)
	}

	for i, event := range remainingEvents {
		// Renumber sequence to be continuous starting from 1
		event.Seq = int64(i + 1)
		data, err := json.Marshal(event)
		if err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			return nil, fmt.Errorf("failed to marshal event: %w", err)
		}
		if _, err := tmpFile.Write(append(data, '\n')); err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			return nil, fmt.Errorf("failed to write event: %w", err)
		}
	}

	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return nil, fmt.Errorf("failed to sync temp events file: %w", err)
	}
	tmpFile.Close()

	// Rename temp file to replace original
	if err := os.Rename(tmpPath, eventsPath); err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("failed to rename temp events file: %w", err)
	}

	// Calculate bytes reclaimed from events file
	newInfo, _ := os.Stat(eventsPath)
	if newInfo != nil {
		result.BytesReclaimed = originalSize - newInfo.Size()
	}

	// Clean up orphaned images
	for imageID := range imageRefsToCheck {
		if _, stillActive := activeImageRefs[imageID]; !stillActive {
			imagePath := s.imagesDir(sessionID) + "/" + imageID
			if info, err := os.Stat(imagePath); err == nil {
				result.BytesReclaimed += info.Size()
				if err := os.Remove(imagePath); err == nil {
					result.ImagesRemoved++
				}
			}
		}
	}

	// Update metadata
	meta, err := s.readMetadata(sessionID)
	if err != nil {
		return result, err
	}
	meta.EventCount = len(remainingEvents)
	if err := s.writeMetadata(meta); err != nil {
		return result, err
	}

	return result, nil
}
