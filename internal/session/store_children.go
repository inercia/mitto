// Child-session traversal and enumeration methods on *Store. Grouped here
// (separate from core CRUD) because they form a self-contained sub-domain.
package session

import (
	"fmt"
	"os"

	"github.com/inercia/mitto/internal/logging"
)

// FindAutoChildrenRecursive returns all auto-child session IDs recursively.
//
// Deprecated: Use FindAllChildrenRecursive instead, which finds children of all origins.
func (s *Store) FindAutoChildrenRecursive(sessionID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, ErrStoreClosed
	}

	return s.findAutoChildrenRecursive(sessionID, make(map[string]bool))
}

func (s *Store) findAutoChildrenRecursive(sessionID string, visited map[string]bool) ([]string, error) {
	if visited[sessionID] {
		return nil, nil // Prevent cycles
	}
	visited[sessionID] = true

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return nil, err
	}

	var result []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		childID := entry.Name()
		meta, err := s.readMetadata(childID)
		if err != nil {
			continue
		}
		if meta.ParentSessionID == sessionID && meta.IsAutoChild {
			result = append(result, childID)
			// Recurse to find grandchildren
			grandchildren, _ := s.findAutoChildrenRecursive(childID, visited)
			result = append(result, grandchildren...)
		}
	}
	return result, nil
}

// FindAllChildrenRecursive returns all child session IDs recursively, regardless of origin.
// This includes auto-children, MCP-children, and human-created children.
// Used by the web layer to close ACP processes before cascade deletion.
func (s *Store) FindAllChildrenRecursive(sessionID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, ErrStoreClosed
	}

	return s.findAllChildrenRecursive(sessionID, make(map[string]bool))
}

func (s *Store) findAllChildrenRecursive(sessionID string, visited map[string]bool) ([]string, error) {
	if visited[sessionID] {
		return nil, nil // Prevent cycles
	}
	visited[sessionID] = true

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return nil, err
	}

	var result []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		childID := entry.Name()
		meta, err := s.readMetadata(childID)
		if err != nil {
			continue
		}
		if meta.ParentSessionID == sessionID {
			result = append(result, childID)
			// Recurse to find grandchildren
			grandchildren, _ := s.findAllChildrenRecursive(childID, visited)
			result = append(result, grandchildren...)
		}
	}
	return result, nil
}

// ListChildSessions returns all sessions that have the given parentID as their ParentSessionID.
// Returns direct children only (not grandchildren).
// Returns empty slice if no children exist.
func (s *Store) ListChildSessions(parentID string) ([]Metadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, ErrStoreClosed
	}

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return nil, err
	}

	children := []Metadata{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionID := entry.Name()
		meta, err := s.readMetadata(sessionID)
		if err != nil {
			continue // Skip sessions with unreadable metadata
		}
		if meta.ParentSessionID == parentID {
			children = append(children, meta)
		}
	}
	return children, nil
}

// CountChildSessions returns the count of direct child sessions.
// More efficient than ListChildSessions when only the count is needed.
func (s *Store) CountChildSessions(parentID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return 0, ErrStoreClosed
	}

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionID := entry.Name()
		meta, err := s.readMetadata(sessionID)
		if err != nil {
			continue
		}
		if meta.ParentSessionID == parentID {
			count++
		}
	}
	return count, nil
}

// CountMCPChildSessions returns the count of direct non-archived child sessions that were
// created via MCP (ChildOriginMCP) or by a human (ChildOriginHuman).
// Auto-children (ChildOriginAuto) and archived children are excluded from the count.
// This is used for enforcing the max_child_conversations limit.
func (s *Store) CountMCPChildSessions(parentID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return 0, ErrStoreClosed
	}

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionID := entry.Name()
		meta, err := s.readMetadata(sessionID)
		if err != nil {
			continue
		}
		if meta.ParentSessionID == parentID {
			meta.MigrateChildOrigin()
			// Exclude auto-children and archived children from the count
			if meta.ChildOrigin != ChildOriginAuto && !meta.Archived {
				count++
			}
		}
	}
	return count, nil
}

// HasChildSessions returns true if the session has at least one child.
// More efficient than CountChildSessions when only existence check is needed.
func (s *Store) HasChildSessions(parentID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return false, ErrStoreClosed
	}

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return false, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionID := entry.Name()
		meta, err := s.readMetadata(sessionID)
		if err != nil {
			continue
		}
		if meta.ParentSessionID == parentID {
			return true, nil
		}
	}
	return false, nil
}

// handleChildSessionsOnParentDelete cascade-deletes ALL child sessions when parent is deleted.
// All children (auto, MCP, and human-created) are recursively deleted along with their parent.
// Returns the list of child IDs that were deleted.
// Note: This method assumes the caller holds s.mu.Lock().
func (s *Store) handleChildSessionsOnParentDelete(parentSessionID string, visited map[string]bool) ([]string, error) {
	log := logging.Session()

	if visited == nil {
		visited = make(map[string]bool)
	}
	if visited[parentSessionID] {
		log.Warn("Circular reference detected in session hierarchy", "session_id", parentSessionID)
		return nil, nil
	}
	visited[parentSessionID] = true

	// Read all session directories
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read sessions directory: %w", err)
	}

	var deletedIDs []string
	var deleteErrors []error

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		sessionID := entry.Name()
		if sessionID == parentSessionID {
			continue
		}

		meta, err := s.readMetadata(sessionID)
		if err != nil {
			continue
		}

		// Check if this session has the parent we're deleting
		if meta.ParentSessionID == parentSessionID {
			// CASCADE DELETE: Recursively delete this child and all its descendants
			// First, handle this child's own children
			grandchildDeleted, _ := s.handleChildSessionsOnParentDelete(sessionID, visited)
			deletedIDs = append(deletedIDs, grandchildDeleted...)

			// Now delete this child
			sessionDir := s.sessionDir(sessionID)
			if err := os.RemoveAll(sessionDir); err != nil {
				deleteErrors = append(deleteErrors, fmt.Errorf("failed to delete child %s: %w", sessionID, err))
				continue
			}
			deletedIDs = append(deletedIDs, sessionID)

			// Migrate for logging purposes
			meta.MigrateChildOrigin()
			log.Info("Cascade deleted child session",
				"parent_session_id", parentSessionID,
				"deleted_session_id", sessionID,
				"session_name", meta.Name,
				"child_origin", string(meta.ChildOrigin))
		}
	}

	if len(deleteErrors) > 0 {
		log.Error("Errors during child session cascade deletion",
			"parent_session_id", parentSessionID,
			"error_count", len(deleteErrors),
			"errors", deleteErrors)
	}

	log.Debug("Cascade deleted child sessions on parent delete",
		"parent_session_id", parentSessionID,
		"children_deleted", len(deletedIDs))

	return deletedIDs, nil
}
