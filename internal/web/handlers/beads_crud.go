package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/inercia/mitto/internal/beads"
	"github.com/inercia/mitto/internal/conversation"
)

// beadsCreateDep is a single dependency entry in a beadsCreateRequest.
type beadsCreateDep struct {
	ID   string `json:"id"`
	Type string `json:"type,omitempty"`
}

// beadsCreateRequest is the JSON body for POST /api/issues.
type beadsCreateRequest struct {
	Title        string           `json:"title"`
	Type         string           `json:"type,omitempty"`
	Priority     *int             `json:"priority,omitempty"` // pointer so 0 ("Critical") is distinguishable from absent
	Description  string           `json:"description,omitempty"`
	Parent       string           `json:"parent,omitempty"`
	Assignee     string           `json:"assignee,omitempty"`
	Notes        string           `json:"notes,omitempty"`
	Dependencies []beadsCreateDep `json:"dependencies,omitempty"`
}

// HandleBeadsCreate handles POST /api/issues?working_dir=...
// Runs "bd create <title> --json [--type T] [--priority N] [-d D]" in the workspace directory.
// When title is empty but description is non-empty, the title is auto-generated via the
// auxiliary session (with a 60s timeout) and falls back to conversation.GenerateQuickTitle, then "New Issue".
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (h *Handlers) HandleBeadsCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var req beadsCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
		return
	}

	workingDir := r.URL.Query().Get("working_dir")
	if workingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	if !filepath.IsAbs(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir must be an absolute path")
		return
	}

	// Trim title and description before validation.
	title := strings.TrimSpace(req.Title)
	description := strings.TrimSpace(req.Description)

	if title == "" && description == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "title or description is required")
		return
	}

	if !h.isKnownWorkspaceDir(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir does not match any known workspace")
		return
	}

	// Auto-generate title from description when the caller omitted it.
	if title == "" {
		ws := h.deps.SessionManager.GetWorkspace(workingDir)
		if ws == nil || ws.UUID == "" {
			writeErrorJSON(w, http.StatusInternalServerError, "", "unable to resolve workspace")
			return
		}

		if h.deps.GenerateAuxTitle != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
			defer cancel()
			if generated, err := h.deps.GenerateAuxTitle(ctx, ws.UUID, description); err == nil && strings.TrimSpace(generated) != "" {
				title = strings.TrimSpace(generated)
			} else if err != nil && h.deps.Logger != nil {
				h.deps.Logger.Warn("beads: title generation failed, using fallback", "error", err)
			}
		}

		// Fallback: derive a quick title from the description text.
		if title == "" {
			title = conversation.GenerateQuickTitle(description)
		}
		// Last resort.
		if title == "" {
			title = "New Issue"
		}
	}

	// Build dependency slice: validate each entry and resolve the edge type.
	var deps []string
	for _, dep := range req.Dependencies {
		if !isValidBeadsIssueRef(dep.ID) {
			writeErrorJSON(w, http.StatusBadRequest, "", "invalid dependency id")
			return
		}
		t := strings.TrimSpace(dep.Type)
		if t == "" {
			t = "blocks"
		}
		if !beads.IsValidDepType(t) {
			writeErrorJSON(w, http.StatusBadRequest, "", "invalid dependency type")
			return
		}
		deps = append(deps, t+":"+dep.ID)
	}

	out, err := h.beadsClient().Create(r.Context(), workingDir, beads.CreateParams{
		Title:       title,
		Type:        req.Type,
		Priority:    req.Priority,
		Description: req.Description,
		Parent:      strings.TrimSpace(req.Parent),
		Deps:        deps,
		Assignee:    strings.TrimSpace(req.Assignee),
		Notes:       strings.TrimSpace(req.Notes),
	})
	if err != nil {
		writeBeadsError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(out) //nolint:errcheck
}

// beadsCleanupResponse reports whether a background cleanup was started.
type beadsCleanupResponse struct {
	Started        bool `json:"started"`
	Total          int  `json:"total"`
	AlreadyRunning bool `json:"already_running,omitempty"`
}

// beadsCleanupBatchSize is how many closed issues are deleted per bd invocation.
const beadsCleanupBatchSize = 25

// HandleBeadsCleanup handles POST /api/issues/cleanup?working_dir=...
// It lists closed issues synchronously, then starts a background goroutine that
// deletes them in batches and reports progress over the global-events WebSocket.
// The HTTP response returns immediately so the 30 s middleware cap cannot fire.
func (h *Handlers) HandleBeadsCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	workingDir := r.URL.Query().Get("working_dir")
	if workingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	if !filepath.IsAbs(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir must be an absolute path")
		return
	}
	if !h.isKnownWorkspaceDir(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir does not match any known workspace")
		return
	}

	// Fast phase: list closed IDs using the request context.
	ids, err := h.beadsClient().ListClosedIDs(r.Context(), workingDir)
	if err != nil {
		writeBeadsError(w, err)
		return
	}
	total := len(ids)
	if total == 0 {
		writeJSONOK(w, beadsCleanupResponse{Started: false, Total: 0})
		return
	}

	// Guard against concurrent cleanups for the same working dir.
	if !h.tryStartBeadsCleanup(workingDir) {
		writeJSONOK(w, beadsCleanupResponse{Started: false, Total: total, AlreadyRunning: true})
		return
	}

	// Slow phase: delete in batches on a detached context so the 30s HTTP
	// timeout cannot cancel it. Progress is reported over the global-events WS.
	go h.runBeadsCleanup(workingDir, ids)

	writeJSONOK(w, beadsCleanupResponse{Started: true, Total: total})
}

// runBeadsCleanup deletes closed issues in batches and broadcasts progress.
func (h *Handlers) runBeadsCleanup(workingDir string, ids []string) {
	defer h.finishBeadsCleanup(workingDir)

	ctx := context.Background()
	client := h.beadsClient()
	total := len(ids)
	deleted := 0

	for start := 0; start < total; start += beadsCleanupBatchSize {
		end := start + beadsCleanupBatchSize
		if end > total {
			end = total
		}
		batch := ids[start:end]
		if err := client.DeleteIDs(ctx, workingDir, batch); err != nil {
			h.broadcastBeadsCleanupProgress(workingDir, deleted, total, true, err.Error())
			return
		}
		deleted += len(batch)
		h.broadcastBeadsCleanupProgress(workingDir, deleted, total, deleted >= total, "")
	}
}

func (h *Handlers) broadcastBeadsCleanupProgress(workingDir string, deleted, total int, done bool, errMsg string) {
	if h.deps.BroadcastBeadsCleanupProgress != nil {
		h.deps.BroadcastBeadsCleanupProgress(workingDir, deleted, total, done, errMsg)
	}
}

// tryStartBeadsCleanup marks a working dir as having an in-flight cleanup.
// It returns false if one is already running for that dir.
func (h *Handlers) tryStartBeadsCleanup(dir string) bool {
	h.beadsCleanupMu.Lock()
	defer h.beadsCleanupMu.Unlock()
	if h.beadsCleanupActive[dir] {
		return false
	}
	h.beadsCleanupActive[dir] = true
	return true
}

func (h *Handlers) finishBeadsCleanup(dir string) {
	h.beadsCleanupMu.Lock()
	defer h.beadsCleanupMu.Unlock()
	delete(h.beadsCleanupActive, dir)
}

// beadsActionResponse is a minimal success body for delete/status actions.
type beadsActionResponse struct {
	OK bool `json:"ok"`
}

// HandleBeadsDelete handles DELETE /api/issues/{id}?working_dir=...
// Runs "bd delete <id> --force" in the workspace directory.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (h *Handlers) HandleBeadsDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		methodNotAllowed(w)
		return
	}

	workingDir := r.URL.Query().Get("working_dir")
	id := r.PathValue("id")
	if workingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	if !filepath.IsAbs(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir must be an absolute path")
		return
	}
	if strings.TrimSpace(id) == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "id is required")
		return
	}
	if !h.isKnownWorkspaceDir(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir does not match any known workspace")
		return
	}

	if err := h.beadsClient().Delete(r.Context(), workingDir, id); err != nil {
		writeBeadsError(w, err)
		return
	}

	writeJSONOK(w, beadsActionResponse{OK: true})
}

// beadsStatusRequest is the JSON body for POST /api/issues/{id}/status.
// Action must be "close", "reopen", "defer" or "undefer".
type beadsStatusRequest struct {
	Action string `json:"action"`
}

// HandleBeadsStatus handles POST /api/issues/{id}/status?working_dir=...
// Runs "bd close|reopen|defer|undefer <id>" in the workspace directory.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (h *Handlers) HandleBeadsStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	workingDir := r.URL.Query().Get("working_dir")
	id := r.PathValue("id")
	if workingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	if !filepath.IsAbs(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir must be an absolute path")
		return
	}
	if !h.isKnownWorkspaceDir(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir does not match any known workspace")
		return
	}
	if !isValidBeadsIssueRef(id) {
		writeErrorJSON(w, http.StatusBadRequest, "", "id is required")
		return
	}

	var req beadsStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
		return
	}

	var verb string
	switch req.Action {
	case "close", "reopen", "defer", "undefer":
		verb = req.Action
	default:
		writeErrorJSON(w, http.StatusBadRequest, "", "action must be 'close', 'reopen', 'defer' or 'undefer'")
		return
	}

	if err := h.beadsClient().SetStatus(r.Context(), workingDir, id, verb); err != nil {
		writeBeadsError(w, err)
		return
	}

	writeJSONOK(w, beadsActionResponse{OK: true})
}

// beadsUpdateRequest is the JSON body for PATCH /api/issues/{id}.
// Description, Title, Priority, Assignee and Notes are pointers so an omitted
// field (nil) is distinguishable from an intentional value (an empty
// description, assignee or notes clears the field; an empty title is rejected;
// priority 0 is a valid "Critical" value).
type beadsUpdateRequest struct {
	Description *string `json:"description,omitempty"`
	Title       *string `json:"title,omitempty"`
	Type        *string `json:"type,omitempty"`
	Priority    *int    `json:"priority,omitempty"` // pointer so 0 ("Critical") is distinguishable from absent
	Assignee    *string `json:"assignee,omitempty"` // pointer so an empty string (clear assignee) is distinguishable from absent
	Notes       *string `json:"notes,omitempty"`    // pointer so an empty string (clear notes) is distinguishable from absent
}

// HandleBeadsUpdate handles PATCH /api/issues/{id}?working_dir=...
// Runs "bd update <id> [--title <title>] [-d <description>] [--priority N] [-a <assignee>] [--notes <notes>]"
// in the workspace directory. At least one of title, description, priority,
// assignee or notes must be supplied. When the description is an empty string,
// the --allow-empty-description flag is added so the description can be cleared;
// an empty title is rejected; an empty assignee clears the assignee; an empty
// notes value clears the notes.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (h *Handlers) HandleBeadsUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		methodNotAllowed(w)
		return
	}

	var req beadsUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
		return
	}

	workingDir := r.URL.Query().Get("working_dir")
	id := r.PathValue("id")
	if workingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	if !filepath.IsAbs(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir must be an absolute path")
		return
	}
	if strings.TrimSpace(id) == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "id is required")
		return
	}
	if req.Description == nil && req.Title == nil && req.Type == nil && req.Priority == nil && req.Assignee == nil && req.Notes == nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "title, description, type, priority, assignee or notes is required")
		return
	}
	if req.Title != nil && strings.TrimSpace(*req.Title) == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "title must not be empty")
		return
	}
	if req.Priority != nil && (*req.Priority < 0 || *req.Priority > 4) {
		writeErrorJSON(w, http.StatusBadRequest, "", "priority must be between 0 and 4")
		return
	}
	if !h.isKnownWorkspaceDir(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir does not match any known workspace")
		return
	}

	if err := h.beadsClient().Update(r.Context(), workingDir, beads.UpdateParams{
		ID:          id,
		Title:       req.Title,
		Type:        req.Type,
		Description: req.Description,
		Priority:    req.Priority,
		Assignee:    req.Assignee,
		Notes:       req.Notes,
	}); err != nil {
		writeBeadsError(w, err)
		return
	}

	writeJSONOK(w, beadsActionResponse{OK: true})
}

// beadsCommentRequest is the JSON body for POST /api/issues/{id}/comments.
type beadsCommentRequest struct {
	Text string `json:"text"`
}

// HandleBeadsComment handles POST /api/issues/{id}/comments?working_dir=...
// Runs "bd comment <id> -- <text>" in the workspace directory, adding a comment
// to the issue. The text must be non-empty.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (h *Handlers) HandleBeadsComment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var req beadsCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
		return
	}

	workingDir := r.URL.Query().Get("working_dir")
	id := r.PathValue("id")
	if workingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	if !filepath.IsAbs(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir must be an absolute path")
		return
	}
	if strings.TrimSpace(id) == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "id is required")
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "text must not be empty")
		return
	}
	if !h.isKnownWorkspaceDir(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir does not match any known workspace")
		return
	}

	if err := h.beadsClient().Comment(r.Context(), workingDir, id, req.Text); err != nil {
		writeBeadsError(w, err)
		return
	}

	writeJSONOK(w, beadsActionResponse{OK: true})
}

// beadsDepRequest is the JSON body for POST /api/issues/{id}/dependencies.
// Action must be "add" or "remove". For "add", Type selects the dependency
// edge kind (default "blocks"). DependsOn is the issue that ID depends on; it
// may be a local issue id or an external reference (external:<project>:<cap>).
type beadsDepRequest struct {
	DependsOn string `json:"depends_on"`
	Type      string `json:"type,omitempty"`
	Action    string `json:"action"`
}

// HandleBeadsDep handles POST /api/issues/{id}/dependencies?working_dir=...
// For action "add" it runs "bd dep add <id> <depends_on> -t <type>"; for
// "remove" it runs "bd dep remove <id> <depends_on>". Both emit plain text.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (h *Handlers) HandleBeadsDep(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var req beadsDepRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
		return
	}

	workingDir := r.URL.Query().Get("working_dir")
	id := r.PathValue("id")
	if workingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	if !filepath.IsAbs(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir must be an absolute path")
		return
	}
	if !isValidBeadsIssueRef(id) {
		writeErrorJSON(w, http.StatusBadRequest, "", "id is required")
		return
	}
	if !isValidBeadsIssueRef(req.DependsOn) {
		writeErrorJSON(w, http.StatusBadRequest, "", "depends_on is required")
		return
	}
	if !h.isKnownWorkspaceDir(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir does not match any known workspace")
		return
	}

	switch req.Action {
	case "add":
		depType := req.Type
		if depType == "" {
			depType = "blocks"
		}
		if !beads.IsValidDepType(depType) {
			writeErrorJSON(w, http.StatusBadRequest, "", "invalid dependency type")
			return
		}
	case "remove":
		// no extra validation needed
	default:
		writeErrorJSON(w, http.StatusBadRequest, "", "action must be 'add' or 'remove'")
		return
	}

	if err := h.beadsClient().Dep(r.Context(), workingDir, beads.DepParams{
		ID:        id,
		DependsOn: req.DependsOn,
		Type:      req.Type,
		Action:    req.Action,
	}); err != nil {
		writeBeadsError(w, err)
		return
	}

	writeJSONOK(w, beadsActionResponse{OK: true})
}
