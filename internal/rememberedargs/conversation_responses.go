package rememberedargs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConversationResponsesFileName is the session-local sidecar that holds the
// per-conversation "Remember responses" state for every prompt and workspace of
// one session. It lives in the session folder, so session deletion removes it.
const ConversationResponsesFileName = "remembered-prompt-responses.json"

const conversationResponsesVersion = 1

// Remember states for one (session, workspace, prompt) triple.
const (
	ResponsesUnset    = ""         // no explicit user decision yet
	ResponsesEnabled  = "enabled"  // snapshot of submitted responses is active
	ResponsesDisabled = "disabled" // user opted out; tombstone without values
)

// SessionSidecars is the narrow session-store capability needed to keep the
// responses file under the session write lock (see session.Store).
type SessionSidecars interface {
	UpdateSessionSidecarJSON(sessionID, name string, update func(current []byte) ([]byte, error)) error
}

// ConversationResponses persists tri-state conversation-local responses.
// Reads and writes go through SessionSidecars, so every read-modify-write is
// atomic with respect to other writers and to session deletion.
type ConversationResponses struct {
	sidecars  SessionSidecars
	legacyDir string // $MITTO_DIR/remembered-args-conversation; migration source only
}

// NewConversationResponses returns a ConversationResponses. legacyDir may be
// empty when no legacy conversation-scope data exists.
func NewConversationResponses(sidecars SessionSidecars, legacyDir string) *ConversationResponses {
	return &ConversationResponses{sidecars: sidecars, legacyDir: legacyDir}
}

// ResponsesState is the stored state for one (session, workspace, prompt).
// Legacy holds values migrated from the old conversation-scope file; they are
// only offered as prefill while State is ResponsesUnset and never imply consent.
type ResponsesState struct {
	State     string
	Arguments map[string]string
	Legacy    map[string]string
}

type responsesDocument struct {
	Version    int                                `json:"version"`
	Workspaces map[string]map[string]responseItem `json:"workspaces,omitempty"` // workspaceUUID → lower(prompt) → item
	Legacy     map[string]map[string]string       `json:"legacy,omitempty"`     // lower(prompt) → arg → value
}

type responseItem struct {
	State     string            `json:"state"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

// Get returns the stored state for the triple. It performs the one-time legacy
// migration for sessions that have no session-local file yet.
func (c *ConversationResponses) Get(sessionID, workspaceUUID, promptName string) (ResponsesState, error) {
	prompt, err := promptKey(sessionID, workspaceUUID, promptName)
	if err != nil {
		return ResponsesState{}, err
	}
	var out ResponsesState
	err = c.mutate(sessionID, func(doc *responsesDocument) (bool, error) {
		item, ok := doc.Workspaces[workspaceUUID][prompt]
		if ok {
			out.State = item.State
			out.Arguments = cloneArgs(item.Arguments)
		} else {
			out.Legacy = cloneArgs(doc.Legacy[prompt])
		}
		return false, nil
	})
	if err != nil {
		return ResponsesState{}, err
	}
	return out, nil
}

// ReplaceEnabled stores args as the complete enabled snapshot for the triple,
// replacing any previous snapshot, and drops legacy values for the prompt.
func (c *ConversationResponses) ReplaceEnabled(sessionID, workspaceUUID, promptName string, args map[string]string) error {
	prompt, err := promptKey(sessionID, workspaceUUID, promptName)
	if err != nil {
		return err
	}
	return c.write(sessionID, func(doc *responsesDocument) {
		setItem(doc, workspaceUUID, prompt, responseItem{State: ResponsesEnabled, Arguments: cloneArgs(args)})
	})
}

// Disable stores the tombstone for the triple: no values are kept, and the
// legacy and folder fallbacks for this prompt in this conversation are
// suppressed. Shared folder values are never touched here.
func (c *ConversationResponses) Disable(sessionID, workspaceUUID, promptName string) error {
	prompt, err := promptKey(sessionID, workspaceUUID, promptName)
	if err != nil {
		return err
	}
	return c.write(sessionID, func(doc *responsesDocument) {
		setItem(doc, workspaceUUID, prompt, responseItem{State: ResponsesDisabled})
	})
}

func (c *ConversationResponses) write(sessionID string, apply func(doc *responsesDocument)) error {
	return c.mutate(sessionID, func(doc *responsesDocument) (bool, error) {
		apply(doc)
		return true, nil
	})
}

// mutate loads the session document under the session write lock, applies
// apply, and persists the result when apply reports a change. Legacy values are
// read only when no session-local file exists yet. The legacy file is removed
// only after the migrated document was durably written.
func (c *ConversationResponses) mutate(sessionID string, apply func(doc *responsesDocument) (bool, error)) error {
	if c == nil || c.sidecars == nil {
		return errors.New("conversation responses store is not configured")
	}
	migrated := false
	err := c.sidecars.UpdateSessionSidecarJSON(sessionID, ConversationResponsesFileName, func(current []byte) ([]byte, error) {
		doc := responsesDocument{Version: conversationResponsesVersion}
		if current != nil {
			if err := json.Unmarshal(current, &doc); err != nil {
				return nil, fmt.Errorf("decode %s: %w", ConversationResponsesFileName, err)
			}
			if doc.Version != conversationResponsesVersion {
				return nil, fmt.Errorf("unsupported %s version %d", ConversationResponsesFileName, doc.Version)
			}
		} else if legacy, ok := c.readLegacy(sessionID); ok {
			doc.Legacy = legacy
			migrated = true
		}
		changed, err := apply(&doc)
		if err != nil {
			return nil, err
		}
		if !changed && !migrated {
			return nil, nil
		}
		return json.Marshal(doc)
	})
	if err != nil {
		return err
	}
	if migrated {
		c.removeLegacy(sessionID)
	}
	return nil
}

// readLegacy reads the old external conversation-scope file. A missing or
// unreadable file yields ok=false and leaves the file on disk untouched.
func (c *ConversationResponses) readLegacy(sessionID string) (map[string]map[string]string, bool) {
	if c.legacyDir == "" || !safeSessionFileName(sessionID) {
		return nil, false
	}
	raw, err := os.ReadFile(filepath.Join(c.legacyDir, sessionID+".json"))
	if err != nil {
		return nil, false
	}
	var legacy map[string]map[string]string
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return nil, false
	}
	normalized := make(map[string]map[string]string, len(legacy))
	for prompt, args := range legacy {
		if len(args) == 0 {
			continue
		}
		key := strings.ToLower(prompt)
		if normalized[key] == nil {
			normalized[key] = map[string]string{}
		}
		for arg, value := range args {
			normalized[key][arg] = value
		}
	}
	if len(normalized) == 0 {
		return nil, false
	}
	return normalized, true
}

func (c *ConversationResponses) removeLegacy(sessionID string) {
	if c.legacyDir == "" || !safeSessionFileName(sessionID) {
		return
	}
	_ = os.Remove(filepath.Join(c.legacyDir, sessionID+".json"))
}

// promptKey returns the canonical (case-insensitive) prompt key for a triple.
func promptKey(sessionID, workspaceUUID, promptName string) (string, error) {
	if sessionID == "" || workspaceUUID == "" || strings.TrimSpace(promptName) == "" {
		return "", errors.New("session, workspace and prompt are required")
	}
	return strings.ToLower(promptName), nil
}

func setItem(doc *responsesDocument, workspaceUUID, promptKey string, item responseItem) {
	if doc.Workspaces == nil {
		doc.Workspaces = map[string]map[string]responseItem{}
	}
	if doc.Workspaces[workspaceUUID] == nil {
		doc.Workspaces[workspaceUUID] = map[string]responseItem{}
	}
	doc.Workspaces[workspaceUUID][promptKey] = item
	if doc.Legacy != nil {
		delete(doc.Legacy, promptKey)
	}
}

func cloneArgs(args map[string]string) map[string]string {
	if args == nil {
		return nil
	}
	out := make(map[string]string, len(args))
	for k, v := range args {
		out[k] = v
	}
	return out
}

func safeSessionFileName(sessionID string) bool {
	return sessionID != "" && sessionID != "." && sessionID != ".." && filepath.Base(sessionID) == sessionID
}
