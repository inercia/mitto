package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/prompts"
	"github.com/inercia/mitto/internal/session"
)

// QueueAddRequest represents a request to add a message to the queue.
type QueueAddRequest struct {
	Message       string            `json:"message"`
	ImageIDs      []string          `json:"image_ids,omitempty"`
	FileIDs       []string          `json:"file_ids,omitempty"`
	ScheduledTime *string           `json:"scheduled_time,omitempty"` // Optional: RFC 3339 timestamp or relative duration (e.g., "5m", "1h")
	Arguments     map[string]string `json:"arguments,omitempty"`      // Optional: values for Go-template .Args placeholders applied when sent
	PromptName    string            `json:"prompt_name,omitempty"`    // Optional: name of a workspace prompt to send by name (resolved at dispatch)
}

// QueueMoveRequest represents a request to move a message in the queue.
type QueueMoveRequest struct {
	Direction string `json:"direction"` // "up" or "down"
}

// QueueListResponse represents the response for listing queued messages.
type QueueListResponse struct {
	Messages []session.QueuedMessage `json:"messages"`
	Count    int                     `json:"count"`
}

// HandleSessionQueue handles queue operations for a session.
// Routes: GET/POST/DELETE {prefix}/api/sessions/{id}/queue
//
//	DELETE {prefix}/api/sessions/{id}/queue/{msg_id}
//	GET {prefix}/api/sessions/{id}/queue/{msg_id}
func (h *Handlers) HandleSessionQueue(w http.ResponseWriter, r *http.Request, sessionID, queuePath string) {
	store := h.deps.Store
	if store == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Session store not available")
		return
	}

	// Check if session exists
	if !store.Exists(sessionID) {
		writeErrorJSON(w, http.StatusNotFound, "", "Session not found")
		return
	}

	queue := store.Queue(sessionID)

	// Parse message ID and sub-action from path if present
	// queuePath is everything after "queue", e.g., "", "/{msg_id}", or "/{msg_id}/move"
	pathPart := strings.TrimPrefix(queuePath, "/")

	if pathPart != "" {
		// Check if there's a sub-action (e.g., /move)
		parts := strings.SplitN(pathPart, "/", 2)
		messageID := parts[0]
		subAction := ""
		if len(parts) > 1 {
			subAction = parts[1]
		}

		// Operations on a specific message
		h.handleQueueMessage(w, r, queue, sessionID, messageID, subAction)
		return
	}

	// Operations on the queue itself
	switch r.Method {
	case http.MethodGet:
		h.handleListQueue(w, queue)
	case http.MethodPost:
		h.handleAddToQueue(w, r, queue, sessionID)
	case http.MethodDelete:
		h.handleClearQueue(w, queue, sessionID)
	default:
		methodNotAllowed(w)
	}
}

// handleListQueue handles GET {prefix}/api/sessions/{id}/queue
func (h *Handlers) handleListQueue(w http.ResponseWriter, queue *session.Queue) {
	messages, err := queue.List()
	if err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to list queue", "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to list queue")
		return
	}

	writeJSONOK(w, QueueListResponse{
		Messages: messages,
		Count:    len(messages),
	})
}

// handleAddToQueue handles POST {prefix}/api/sessions/{id}/queue
func (h *Handlers) handleAddToQueue(w http.ResponseWriter, r *http.Request, queue *session.Queue, sessionID string) {
	var req QueueAddRequest
	if !parseJSONBody(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.Message) == "" && strings.TrimSpace(req.PromptName) == "" {
		writeErrorJSON(w, http.StatusBadRequest, "empty_message", "Message cannot be empty")
		return
	}

	// Defence-in-depth: when a workspace prompt is being enqueued by name,
	// reject the request if any `required: true` parameter is missing from
	// req.Arguments. Prevents MCP / integration-test / third-party callers
	// from tripping the same class of bug the frontend queue path had before
	// mitto-gtf. Fails open when SessionManager or GetWorkspacePromptsAll is
	// nil (preserves handler behavior under test harnesses that do not wire
	// those deps).
	if strings.TrimSpace(req.PromptName) != "" &&
		h.deps.SessionManager != nil && h.deps.GetWorkspacePromptsAll != nil {
		if bs := h.deps.SessionManager.GetSession(sessionID); bs != nil {
			if workingDir := bs.GetWorkingDir(); workingDir != "" {
				all := h.deps.GetWorkspacePromptsAll(workingDir)
				var target *config.WebPrompt
				for i := range all {
					if strings.EqualFold(all[i].Name, req.PromptName) {
						target = &all[i]
						break
					}
				}
				if target != nil {
					var missing []string
					for _, p := range target.Parameters {
						if p.Required == nil || !*p.Required {
							continue
						}
						if strings.TrimSpace(req.Arguments[p.Name]) == "" {
							missing = append(missing, p.Name)
						}
					}
					if len(missing) > 0 {
						writeErrorJSON(w, http.StatusBadRequest, "missing_required_arguments",
							fmt.Sprintf("Prompt %q is missing required arguments: %s",
								req.PromptName, strings.Join(missing, ", ")))
						return
					}
				}
			}
		}
	}

	// Get client ID from request context if available (e.g., from auth)
	clientID := ""

	// Get queue config from session (for max size and auto-generate titles)
	var queueConfig *config.QueueConfig
	if h.deps.SessionManager != nil {
		if bs := h.deps.SessionManager.GetSession(sessionID); bs != nil {
			queueConfig = bs.GetQueueConfig()
		}
	}

	// Get queue max size from config (or use default)
	maxSize := config.DefaultQueueMaxSize
	if queueConfig != nil {
		maxSize = queueConfig.GetMaxSize()
	}

	// Parse optional scheduled time (supports RFC 3339 or relative duration like "5m", "1h")
	var scheduledTime *time.Time
	if req.ScheduledTime != nil {
		t, err := session.ParseScheduleTime(*req.ScheduledTime)
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "invalid_scheduled_time", err.Error())
			return
		}
		scheduledTime = &t
	}

	msg, err := queue.Add(req.Message, req.ImageIDs, req.FileIDs, clientID, scheduledTime, maxSize, req.Arguments, req.PromptName)
	if err != nil {
		if errors.Is(err, session.ErrQueueFull) {
			writeErrorJSON(w, http.StatusConflict, "queue_full",
				fmt.Sprintf("Queue is full. Maximum %d messages allowed.", maxSize))
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to add message to queue", "error", err, "session_id", sessionID)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to add message to queue")
		return
	}

	// Notify observers about queue update
	if h.deps.NotifyQueueUpdate != nil {
		h.deps.NotifyQueueUpdate(sessionID, "added", msg.ID)
	}

	// Trigger conversation-level auto-title generation when the session has no
	// title yet. Mirrors the WebSocket prompt path (session_ws.handlePrompt):
	// without this, sessions whose first user prompt arrives via POST /queue
	// stay titled "Conversation" until the ACP turn eventually completes and
	// the post-turn safety net (retryTitleGenerationIfNeeded) fires — which
	// can be many minutes for long-running first turns. See mitto-58b.
	h.triggerTitleFromLoop(sessionID, req.Message, req.PromptName)

	// Persist per-argument remember:* values so the next open of the same
	// prompt dialog in this workspace/session pre-fills them (mitto-x8v,
	// mitto-47y.6.2). Best-effort: any failure is logged and does not affect
	// the enqueue.
	h.rememberScopedArgsForQueueAdd(sessionID, req.PromptName, req.Arguments)

	// Enqueue title generation if enabled (skip for named-prompt items — the prompt name is the label)
	if h.deps.QueueTitleWorker != nil && queueConfig.ShouldAutoGenerateTitles() && req.PromptName == "" {
		h.deps.QueueTitleWorker.Enqueue(conversation.QueueTitleRequest{
			SessionID: sessionID,
			MessageID: msg.ID,
			Message:   req.Message,
		})
	}

	// Try to process the queued message immediately if agent is idle
	// (skip for scheduled messages — the loop runner will deliver them when due)
	if scheduledTime == nil {
		if h.deps.SessionManager != nil {
			if bs := h.deps.SessionManager.GetSession(sessionID); bs != nil {
				go bs.TryProcessQueuedMessage()
			}
		}
	}

	writeJSONCreated(w, msg)
}

// rememberScopedArgsForQueueAdd filters the client-supplied arguments down to
// those declared `remember:*` on the resolved prompt definition and persists
// them via the scope-matched closure: `remember: folder` writes go to the
// RememberFolderArgs closure keyed by workspace UUID, and `remember:
// conversation` writes go to the RememberConversationArgs closure keyed by
// session ID (mitto-47y.6.2). It also persists inner prompts referenced
// through `type: prompts` picker parameters (carried on the wire as
// "<PickerName>_Args" JSON blobs, mitto-47y.2), keying each write under the
// INNER prompt name so different outer prompts picking the same inner prompt
// share remembered values (mitto-47y.3).
//
// Best-effort: nil deps, empty inputs, unresolved workspaces, or unknown
// prompts all no-op; I/O and unmarshal failures are logged at WARN and
// swallowed so the enqueue is never affected. Depth cap = 1: never
// recursively decode "<Inner>_Args" from within an inner map. See mitto-x8v.
func (h *Handlers) rememberScopedArgsForQueueAdd(sessionID, promptName string, args map[string]string) {
	if h.deps.RememberFolderArgs == nil && h.deps.RememberConversationArgs == nil {
		return
	}
	if promptName == "" || len(args) == 0 {
		return
	}
	if h.deps.SessionManager == nil {
		return
	}
	bs := h.deps.SessionManager.GetSession(sessionID)
	if bs == nil {
		return
	}
	workspaceUUID := bs.GetWorkspaceUUID()
	workingDir := bs.GetWorkingDir()
	if workingDir == "" {
		return
	}
	if h.deps.GetWorkspacePromptsAll == nil {
		return
	}
	all := h.deps.GetWorkspacePromptsAll(workingDir)
	var target *config.WebPrompt
	for i := range all {
		if strings.EqualFold(all[i].Name, promptName) {
			target = &all[i]
			break
		}
	}
	if target == nil {
		return
	}
	// Persist the OUTER prompt's remember:* values under the outer name,
	// dispatched to the right scope's writer.
	h.persistScopedArgs(sessionID, workspaceUUID, promptName, target.Parameters, args, promptName, "")
	// Persist any INNER prompt remember:* values carried through
	// `type: prompts` pickers. Each picker's inner map is written under the
	// INNER prompt's name so a different outer prompt picking the same inner
	// prompt shares the remembered values (mitto-47y.3 acceptance criterion).
	for _, p := range target.Parameters {
		if p.Type != "prompts" {
			continue
		}
		innerName := args[p.Name]
		if innerName == "" {
			continue
		}
		blob := args[p.Name+"_Args"]
		if blob == "" {
			continue
		}
		var innerArgs map[string]string
		if err := json.Unmarshal([]byte(blob), &innerArgs); err != nil {
			if h.deps.Logger != nil {
				h.deps.Logger.Warn("Failed to decode inner prompt args for remembered-args",
					"session_id", sessionID,
					"prompt_name", promptName,
					"picker", p.Name,
					"inner_prompt", innerName,
					"error", err)
			}
			continue
		}
		if len(innerArgs) == 0 {
			continue
		}
		var inner *config.WebPrompt
		for i := range all {
			if strings.EqualFold(all[i].Name, innerName) {
				inner = &all[i]
				break
			}
		}
		if inner == nil {
			continue
		}
		h.persistScopedArgs(sessionID, workspaceUUID, promptName, inner.Parameters, innerArgs, inner.Name, p.Name)
	}
}

// persistScopedArgs walks the given params, groups source values by their
// declared remember scope, and writes each non-empty group to its scope's
// closure keyed under storeKey. The outer prompt name and picker name are
// included in the log context so failures at either level are attributable.
// (mitto-47y.6.2)
func (h *Handlers) persistScopedArgs(sessionID, workspaceUUID, outerPromptName string, params []prompts.PromptParameter, src map[string]string, storeKey, pickerName string) {
	folderFiltered := map[string]string{}
	conversationFiltered := map[string]string{}
	for _, p := range params {
		v, ok := src[p.Name]
		if !ok || v == "" {
			continue
		}
		switch p.Remember {
		case prompts.RememberFolder:
			folderFiltered[p.Name] = v
		case prompts.RememberConversation:
			conversationFiltered[p.Name] = v
		}
	}
	if len(folderFiltered) > 0 && h.deps.RememberFolderArgs != nil && workspaceUUID != "" {
		if err := h.deps.RememberFolderArgs(workspaceUUID, storeKey, folderFiltered); err != nil {
			if h.deps.Logger != nil {
				h.deps.Logger.Warn("Failed to persist remembered folder args",
					"session_id", sessionID,
					"prompt_name", outerPromptName,
					"picker", pickerName,
					"store_key", storeKey,
					"error", err)
			}
		}
	}
	if len(conversationFiltered) > 0 && h.deps.RememberConversationArgs != nil && sessionID != "" {
		if err := h.deps.RememberConversationArgs(sessionID, storeKey, conversationFiltered); err != nil {
			if h.deps.Logger != nil {
				h.deps.Logger.Warn("Failed to persist remembered conversation args",
					"session_id", sessionID,
					"prompt_name", outerPromptName,
					"picker", pickerName,
					"store_key", storeKey,
					"error", err)
			}
		}
	}
}

// handleClearQueue handles DELETE {prefix}/api/sessions/{id}/queue
func (h *Handlers) handleClearQueue(w http.ResponseWriter, queue *session.Queue, sessionID string) {
	if err := queue.Clear(); err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to clear queue", "error", err, "session_id", sessionID)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to clear queue")
		return
	}

	// Notify observers about queue update
	if h.deps.NotifyQueueUpdate != nil {
		h.deps.NotifyQueueUpdate(sessionID, "cleared", "")
	}

	writeNoContent(w)
}
