// Package rememberedargs implements per-workspace and per-session persistence
// of prompt argument values so the most recent inputs can pre-fill the same
// prompt dialog next time it opens (mitto-x8v, mitto-47y.6.2).
//
// Storage layout: one JSON file per workspace UUID under baseDir (typically
// $MITTO_DIR/remembered-args, see appdir.RememberedArgsDir) for
// `remember: folder`, and one JSON file per session ID under
// conversationBaseDir (typically $MITTO_DIR/remembered-args-conversation, see
// appdir.RememberedArgsConversationDir) for `remember: conversation`. Each
// file holds a map[promptName]map[argName]value. Writes are atomic
// (fileutil.WriteJSONAtomic). An in-memory cache guarded by sync.RWMutex is
// populated lazily on the first Get/Set for a key.
//
// Only args declared with the matching `remember:` mode in their prompt
// frontmatter should be written here; that filtering is the caller's
// responsibility.
package rememberedargs

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/inercia/mitto/internal/fileutil"
)

// Store persists remembered prompt-argument values per workspace UUID
// (folder scope) and per session ID (conversation scope).
// The zero value is unusable; construct via NewStore.
type Store struct {
	baseDir             string // folder scope: per-workspace UUID
	conversationBaseDir string // conversation scope: per-session ID

	mu                sync.RWMutex
	cache             map[string]map[string]map[string]string // workspaceUUID → promptName → argName → value
	conversationCache map[string]map[string]map[string]string // sessionID → promptName → argName → value
}

// NewStore returns a Store that persists workspace-scoped snapshots under
// baseDir. When baseDir is empty the folder-scope Store is inert: Get returns
// empty maps and Set is a no-op. The conversation-scope namespace defaults to
// inert; call WithConversationBaseDir to enable it. Callers typically pass
// appdir.RememberedArgsDir().
func NewStore(baseDir string) *Store {
	return &Store{
		baseDir:           baseDir,
		cache:             make(map[string]map[string]map[string]string),
		conversationCache: make(map[string]map[string]map[string]string),
	}
}

// WithConversationBaseDir enables the per-session (conversation-scope)
// namespace by setting the base directory for its snapshots. Passing an empty
// string leaves that namespace inert. Returns the receiver for chaining.
// Callers typically pass appdir.RememberedArgsConversationDir() (mitto-47y.6.2).
func (s *Store) WithConversationBaseDir(dir string) *Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conversationBaseDir = dir
	return s
}

// pathFor returns the on-disk file for a workspace UUID, or "" when the Store
// is inert or the UUID is empty.
func (s *Store) pathFor(workspaceUUID string) string {
	if s.baseDir == "" || workspaceUUID == "" {
		return ""
	}
	return filepath.Join(s.baseDir, workspaceUUID+".json")
}

// conversationPathFor returns the on-disk file for a session ID, or "" when
// the conversation-scope namespace is inert or the sessionID is empty.
func (s *Store) conversationPathFor(sessionID string) string {
	if s.conversationBaseDir == "" || sessionID == "" {
		return ""
	}
	return filepath.Join(s.conversationBaseDir, sessionID+".json")
}

// loadLocked reads the workspace snapshot from disk into the cache. Must be
// called with s.mu held for writing. A missing file yields an empty map.
func (s *Store) loadLocked(workspaceUUID string) map[string]map[string]string {
	if existing, ok := s.cache[workspaceUUID]; ok {
		return existing
	}
	path := s.pathFor(workspaceUUID)
	loaded := make(map[string]map[string]string)
	if path != "" {
		var raw map[string]map[string]string
		if err := fileutil.ReadJSON(path, &raw); err == nil {
			for prompt, args := range raw {
				if len(args) == 0 {
					continue
				}
				m := make(map[string]string, len(args))
				for k, v := range args {
					m[k] = v
				}
				loaded[prompt] = m
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			// Corrupt / unreadable file: fall through with the empty map.
			// A subsequent Set will overwrite it atomically.
			_ = err
		}
	}
	s.cache[workspaceUUID] = loaded
	return loaded
}

// Get returns a copy of the remembered arguments for (workspaceUUID, promptName).
// An empty map is returned when nothing is remembered, when the Store is inert,
// or when either identifier is empty. The returned map is safe for the caller
// to mutate.
func (s *Store) Get(workspaceUUID, promptName string) (map[string]string, error) {
	out := make(map[string]string)
	if workspaceUUID == "" || promptName == "" || s.baseDir == "" {
		return out, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	byPrompt := s.loadLocked(workspaceUUID)
	if args, ok := byPrompt[promptName]; ok {
		for k, v := range args {
			out[k] = v
		}
	}
	return out, nil
}

// Set merges args into the remembered snapshot for (workspaceUUID, promptName)
// and writes the workspace file atomically. Empty workspaceUUID, promptName,
// args, or an inert Store make Set a no-op that returns nil. Existing values
// for arg names not present in args are preserved; existing values for arg
// names present in args are overwritten (including with the empty string).
func (s *Store) Set(workspaceUUID, promptName string, args map[string]string) error {
	if workspaceUUID == "" || promptName == "" || len(args) == 0 || s.baseDir == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	byPrompt := s.loadLocked(workspaceUUID)
	existing := byPrompt[promptName]
	if existing == nil {
		existing = make(map[string]string, len(args))
	}
	for k, v := range args {
		existing[k] = v
	}
	byPrompt[promptName] = existing
	s.cache[workspaceUUID] = byPrompt

	path := s.pathFor(workspaceUUID)
	if path == "" {
		return nil
	}
	return fileutil.WriteJSONAtomic(path, byPrompt, 0o644)
}

// loadConversationLocked reads the session snapshot from disk into the
// conversation cache. Must be called with s.mu held for writing. A missing
// file yields an empty map. Mirrors loadLocked but targets the per-session
// namespace (mitto-47y.6.2).
func (s *Store) loadConversationLocked(sessionID string) map[string]map[string]string {
	if existing, ok := s.conversationCache[sessionID]; ok {
		return existing
	}
	path := s.conversationPathFor(sessionID)
	loaded := make(map[string]map[string]string)
	if path != "" {
		var raw map[string]map[string]string
		if err := fileutil.ReadJSON(path, &raw); err == nil {
			for prompt, args := range raw {
				if len(args) == 0 {
					continue
				}
				m := make(map[string]string, len(args))
				for k, v := range args {
					m[k] = v
				}
				loaded[prompt] = m
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			// Corrupt / unreadable file: fall through with the empty map.
			_ = err
		}
	}
	s.conversationCache[sessionID] = loaded
	return loaded
}

// GetConversation returns a copy of the remembered arguments for
// (sessionID, promptName) in the conversation-scope namespace. An empty map
// is returned when nothing is remembered, when the namespace is inert, or
// when either identifier is empty. The returned map is safe for the caller
// to mutate (mitto-47y.6.2).
func (s *Store) GetConversation(sessionID, promptName string) (map[string]string, error) {
	out := make(map[string]string)
	if sessionID == "" || promptName == "" || s.conversationBaseDir == "" {
		return out, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	byPrompt := s.loadConversationLocked(sessionID)
	if args, ok := byPrompt[promptName]; ok {
		for k, v := range args {
			out[k] = v
		}
	}
	return out, nil
}

// SetConversation merges args into the remembered snapshot for
// (sessionID, promptName) in the conversation-scope namespace and writes the
// session file atomically. Empty sessionID, promptName, args, or an inert
// namespace make SetConversation a no-op that returns nil. Merge semantics
// mirror Set (mitto-47y.6.2).
func (s *Store) SetConversation(sessionID, promptName string, args map[string]string) error {
	if sessionID == "" || promptName == "" || len(args) == 0 || s.conversationBaseDir == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	byPrompt := s.loadConversationLocked(sessionID)
	existing := byPrompt[promptName]
	if existing == nil {
		existing = make(map[string]string, len(args))
	}
	for k, v := range args {
		existing[k] = v
	}
	byPrompt[promptName] = existing
	s.conversationCache[sessionID] = byPrompt

	path := s.conversationPathFor(sessionID)
	if path == "" {
		return nil
	}
	return fileutil.WriteJSONAtomic(path, byPrompt, 0o644)
}
