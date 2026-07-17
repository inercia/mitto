// Archived-session cleanup for *Store. Split out of store.go to keep
// core CRUD focused.
package session

import (
	"fmt"
	"os"
	"time"

	"github.com/inercia/mitto/internal/logging"
)

// CleanupArchivedSessions deletes archived sessions older than the specified retention period.
// Returns the number of sessions deleted and any error encountered.
// If retentionPeriod is "never" or empty, no cleanup is performed.
// Supported values: "1d" (1 day), "1w" (1 week), "1m" (1 month), "3m" (3 months).
func (s *Store) CleanupArchivedSessions(retentionPeriod string) (int, error) {
	log := logging.Session()

	// Parse retention period
	maxAge, err := parseRetentionPeriod(retentionPeriod)
	if err != nil {
		return 0, err
	}
	if maxAge == 0 {
		// "never" or empty - no cleanup
		return 0, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return 0, ErrStoreClosed
	}

	sessions, err := os.ReadDir(s.baseDir)
	if err != nil {
		return 0, fmt.Errorf("failed to read sessions directory: %w", err)
	}

	now := time.Now()
	var totalDeleted int
	var deleteErrors []error

	for _, sessionEntry := range sessions {
		if !sessionEntry.IsDir() {
			continue
		}

		sessionID := sessionEntry.Name()

		// Read session metadata
		meta, err := s.readMetadata(sessionID)
		if err != nil {
			continue // Skip sessions with invalid metadata
		}

		// Only process archived sessions
		if !meta.Archived {
			continue
		}

		// Check if archived_at is older than retention period. When
		// ArchivedAt is missing (older sessions predating the field) fall
		// back to UpdatedAt so the same timestamp drives both the deletion
		// decision and the log — otherwise the "age" field logs
		// now.Sub(zero time) ≈ two millennia and misleads operators.
		effectiveArchiveTime := meta.ArchivedAt
		if effectiveArchiveTime.IsZero() {
			effectiveArchiveTime = meta.UpdatedAt
		}
		if now.Sub(effectiveArchiveTime) <= maxAge {
			continue
		}

		// Delete the session
		sessionDir := s.sessionDir(sessionID)
		if err := os.RemoveAll(sessionDir); err != nil {
			deleteErrors = append(deleteErrors, fmt.Errorf("failed to delete session %s: %w", sessionID, err))
			continue
		}

		totalDeleted++
		log.Info("deleted archived session",
			"session_id", sessionID,
			"archived_at", effectiveArchiveTime,
			"age", now.Sub(effectiveArchiveTime))
	}

	if totalDeleted > 0 {
		log.Info("archived session cleanup completed",
			"deleted_count", totalDeleted,
			"retention_period", retentionPeriod)
	}

	if len(deleteErrors) > 0 {
		return totalDeleted, fmt.Errorf("encountered %d errors during cleanup", len(deleteErrors))
	}

	return totalDeleted, nil
}

// parseRetentionPeriod converts a retention period string to a duration.
// Returns 0 for "never" or empty string (no cleanup).
func parseRetentionPeriod(period string) (time.Duration, error) {
	switch period {
	case "", "never":
		return 0, nil
	case "1d":
		return 24 * time.Hour, nil
	case "1w":
		return 7 * 24 * time.Hour, nil
	case "1m":
		return 30 * 24 * time.Hour, nil
	case "3m":
		return 90 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid retention period: %s", period)
	}
}
