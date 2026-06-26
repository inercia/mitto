package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// SessionCreateRequest represents a request to create a new session.
type SessionCreateRequest struct {
	Name              string            `json:"name,omitempty"`
	WorkingDir        string            `json:"working_dir,omitempty"`
	ACPServer         string            `json:"acp_server,omitempty"`          // Optional: specify ACP server for the session
	BeadsIssue        string            `json:"beads_issue,omitempty"`         // Optional: link conversation to a beads issue ID at creation
	InitialPromptName string            `json:"initial_prompt_name,omitempty"` // Optional: seed the queue with a named prompt atomically on creation
	Arguments         map[string]string `json:"arguments,omitempty"`           // Optional: ${VAR} substitution arguments for the initial prompt
}

// HandleCreateSession handles POST /api/sessions
func (h *Handlers) HandleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req SessionCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Allow empty body for default session creation
		req = SessionCreateRequest{}
	}

	// Note: Empty names are allowed - they will be auto-generated after first message
	// The frontend displays "New Conversation" as a placeholder for empty names

	// Determine workspace to use
	// Use sessionManager.GetWorkspaces() as the source of truth - it maintains the live
	// workspace data that can be dynamically updated via the settings UI.
	// s.config.GetWorkspaces() may be stale if workspaces were added/removed at runtime.
	var workspace *configPkg.WorkspaceSettings
	workspaces := h.deps.SessionManager.GetWorkspaces()

	if req.WorkingDir != "" {
		// User specified a working directory - find matching workspace.
		// If acp_server is also specified, match both (for duplicate workspaces with
		// same dir). If only the directory is known and multiple workspaces share it,
		// prefer the one marked IsDefault so folder-only launches (e.g. from the beads
		// menu) are deterministic.
		for i := range workspaces {
			if workspaces[i].WorkingDir == req.WorkingDir {
				// If ACP server is specified, only match if it also matches
				if req.ACPServer != "" && workspaces[i].ACPServer != req.ACPServer {
					continue
				}
				if req.ACPServer == "" && workspaces[i].IsDefault {
					workspace = &workspaces[i]
					break
				}
				if workspace == nil {
					workspace = &workspaces[i]
					if req.ACPServer != "" {
						break
					}
				}
			}
		}
		// No exact workspace match — check whether a registered workspace OWNS the
		// requested directory (it is a subdirectory of that workspace). If so, reuse
		// that workspace so its shared ACP process serves this session while
		// req.WorkingDir continues to flow as the per-session cwd.
		if workspace == nil {
			if owningWs := ResolveOwningWorkspace(req.WorkingDir, workspaces); owningWs != nil && owningWs.UUID != "" {
				workspace = owningWs
			}
		}
		// If not found in workspaces but working dir provided, create ad-hoc workspace
		if workspace == nil {
			// Use default workspace's ACP server with the requested directory.
			// Command/cwd/env are resolved from global config at runtime — not cached here.
			defaultWs := h.deps.SessionManager.GetDefaultWorkspace()
			if defaultWs != nil {
				workspace = &configPkg.WorkspaceSettings{
					ACPServer:          defaultWs.ACPServer,
					ACPCommandOverride: defaultWs.ACPCommandOverride,
					WorkingDir:         req.WorkingDir,
				}
				// Ensure the ad-hoc workspace has a UUID for auxiliary sessions
				workspace.EnsureUUID()
			}
		}
	} else if len(workspaces) == 1 {
		// Single workspace configured - use it
		workspace = &workspaces[0]
		req.WorkingDir = workspace.WorkingDir
	} else {
		// Multiple workspaces - use default
		workspace = h.deps.SessionManager.GetDefaultWorkspace()
		if workspace != nil {
			req.WorkingDir = workspace.WorkingDir
		}
	}

	// Fall back to current directory if still no working dir
	if req.WorkingDir == "" {
		req.WorkingDir, _ = os.Getwd()
	}

	// Validate that we have a valid ACP configuration
	if workspace == nil || workspace.ACPServer == "" {
		writeErrorJSON(w, http.StatusBadRequest, "no_workspace_configured",
			"No workspace configured. Please configure a workspace in Settings first.")
		return
	}

	// Note: The session manager already has the store set by the server at startup.
	// No need to create a new store here.

	// Create the background session with workspace configuration.
	// The session/new ACP RPC is no longer performed here — it is deferred to the
	// first prompt (see ensureSharedACPSession) so creating a conversation never
	// blocks on a busy agent. r.Context() is still passed for the create call.
	bs, err := h.deps.SessionManager.CreateSessionWithWorkspace(r.Context(), req.Name, req.WorkingDir, workspace)
	if err != nil {
		if err == conversation.ErrTooManySessions {
			writeErrorJSON(w, http.StatusServiceUnavailable, "too_many_sessions", "Maximum number of sessions reached (32)")
			return
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			if h.deps.Logger != nil {
				h.deps.Logger.Warn("Session creation timed out or was cancelled", "error", err)
			}
			writeErrorJSON(w, http.StatusServiceUnavailable, "session_creation_timeout",
				"Agent is busy — please try again in a moment")
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to create session", "error", err)
		}
		// Broadcast ACP start failure to all clients (use empty session_id since session wasn't created)
		if h.deps.BroadcastACPStartFailed != nil {
			h.deps.BroadcastACPStartFailed("", req.Name, err, workspace.ACPServer)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to create session")
		return
	}

	// Invalidate negative session cache in case this session ID was previously cached as not found
	if h.deps.RemoveNegativeCache != nil {
		h.deps.RemoveNegativeCache(bs.GetSessionID())
	}

	// Persist the linked beads issue (if provided) on the freshly created session.
	if req.BeadsIssue != "" {
		if store := h.deps.Store; store != nil {
			if err := store.UpdateMetadata(bs.GetSessionID(), func(meta *session.Metadata) {
				meta.BeadsIssue = req.BeadsIssue
			}); err != nil && h.deps.Logger != nil {
				h.deps.Logger.Warn("Failed to set beads_issue on new session", "error", err, "session_id", bs.GetSessionID())
			}
		}
	}

	// Determine the ACP server name for the response
	acpServerName := h.deps.DefaultACPServer
	if workspace != nil && workspace.ACPServer != "" {
		acpServerName = workspace.ACPServer
	}

	// Seed the queue with the named prompt if provided (atomic create+seed).
	// This uses the same queue plumbing as POST /api/sessions/{id}/queue so
	// dispatch happens via the normal TryProcessQueuedMessage path.
	if req.InitialPromptName != "" {
		h.seedQueueWithNamedPrompt(bs, bs.GetSessionID(), req.InitialPromptName, req.Arguments)
	}

	// Broadcast session creation to all global events clients
	sessionData := map[string]interface{}{
		"session_id":     bs.GetSessionID(),
		"acp_session_id": bs.GetACPID(),
		"name":           req.Name,
		"acp_server":     acpServerName,
		"working_dir":    req.WorkingDir,
		"status":         "active",
		"beads_issue":    req.BeadsIssue,
	}
	if h.deps.BroadcastSessionCreated != nil {
		h.deps.BroadcastSessionCreated(sessionData)
	}

	// Return session info
	writeJSONCreated(w, sessionData)
}

// seedQueueWithNamedPrompt enqueues a named prompt on a freshly created session,
// reusing the same queue plumbing as the queue API (Add + notifyQueueUpdate +
// TryProcessQueuedMessage). Title generation is skipped for named-prompt items.
func (h *Handlers) seedQueueWithNamedPrompt(bs *conversation.BackgroundSession, sessionID, promptName string, arguments map[string]string) {
	store := h.deps.Store
	if store == nil {
		return
	}
	queue := store.Queue(sessionID)
	maxSize := configPkg.DefaultQueueMaxSize
	if qc := bs.GetQueueConfig(); qc != nil {
		maxSize = qc.GetMaxSize()
	}
	msg, err := queue.Add("", nil, nil, "", nil, maxSize, arguments, promptName)
	if err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Warn("Failed to seed new session with named prompt",
				"error", err,
				"session_id", sessionID,
				"prompt_name", promptName)
		}
		return
	}
	if h.deps.NotifyQueueUpdate != nil {
		h.deps.NotifyQueueUpdate(sessionID, "added", msg.ID)
	}
	// Dispatch immediately if the agent is idle — same path as the queue API.
	go bs.TryProcessQueuedMessage()
}

// ResolveOwningWorkspace returns the registered workspace that OWNS reqDir, so
// its shared ACP process can be reused for a session whose per-session cwd lives
// inside (or is) that workspace's directory. Returns nil when no workspace owns
// reqDir, in which case the caller falls back to ad-hoc workspace creation.
//
// Ownership is decided by directory containment: a workspace owns reqDir when
// reqDir equals or is strictly inside the workspace dir. When several match, the
// deepest (longest WorkingDir) wins.
func ResolveOwningWorkspace(reqDir string, workspaces []configPkg.WorkspaceSettings) *configPkg.WorkspaceSettings {
	if reqDir == "" {
		return nil
	}
	return ownerByContainment(normalizeDir(reqDir), workspaces)
}

// normalizeDir cleans a directory path and resolves symlinks best-effort,
// keeping the cleaned path when the path does not exist or cannot be resolved.
func normalizeDir(dir string) string {
	cleaned := filepath.Clean(dir)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return resolved
	}
	return cleaned
}

// ownerByContainment returns the deepest workspace whose directory contains (or
// equals) normReq, or nil. normReq must already be normalized via normalizeDir.
func ownerByContainment(normReq string, workspaces []configPkg.WorkspaceSettings) *configPkg.WorkspaceSettings {
	var best *configPkg.WorkspaceSettings
	var bestLen int
	for i := range workspaces {
		ws := &workspaces[i]
		if ws.WorkingDir == "" || ws.UUID == "" {
			continue
		}
		wsDir := normalizeDir(ws.WorkingDir)
		rel, err := filepath.Rel(wsDir, normReq)
		if err != nil {
			continue
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
			continue
		}
		if len(wsDir) > bestLen {
			best = ws
			bestLen = len(wsDir)
		}
	}
	return best
}
