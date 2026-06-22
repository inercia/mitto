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

// beadsCreateRequest is the JSON body for POST /api/beads/create.
type beadsCreateRequest struct {
	WorkingDir   string           `json:"working_dir"`
	Title        string           `json:"title"`
	Type         string           `json:"type,omitempty"`
	Priority     *int             `json:"priority,omitempty"` // pointer so 0 ("Critical") is distinguishable from absent
	Description  string           `json:"description,omitempty"`
	Parent       string           `json:"parent,omitempty"`
	Assignee     string           `json:"assignee,omitempty"`
	Notes        string           `json:"notes,omitempty"`
	Dependencies []beadsCreateDep `json:"dependencies,omitempty"`
}

// HandleBeadsCreate handles POST /api/beads/create.
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
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.WorkingDir == "" {
		http.Error(w, "working_dir is required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(req.WorkingDir) {
		http.Error(w, "working_dir must be an absolute path", http.StatusBadRequest)
		return
	}

	// Trim title and description before validation.
	title := strings.TrimSpace(req.Title)
	description := strings.TrimSpace(req.Description)

	if title == "" && description == "" {
		http.Error(w, "title or description is required", http.StatusBadRequest)
		return
	}

	if !h.isKnownWorkspaceDir(req.WorkingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	// Auto-generate title from description when the caller omitted it.
	if title == "" {
		ws := h.deps.SessionManager.GetWorkspace(req.WorkingDir)
		if ws == nil || ws.UUID == "" {
			http.Error(w, "unable to resolve workspace", http.StatusInternalServerError)
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
			http.Error(w, "invalid dependency id", http.StatusBadRequest)
			return
		}
		t := strings.TrimSpace(dep.Type)
		if t == "" {
			t = "blocks"
		}
		if !beads.IsValidDepType(t) {
			http.Error(w, "invalid dependency type", http.StatusBadRequest)
			return
		}
		deps = append(deps, t+":"+dep.ID)
	}

	out, err := h.beadsClient().Create(r.Context(), req.WorkingDir, beads.CreateParams{
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
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: beads.StderrOf(err)})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(out) //nolint:errcheck
}

// beadsCleanupRequest is the JSON body for POST /api/beads/cleanup.
type beadsCleanupRequest struct {
	WorkingDir string `json:"working_dir"`
}

// beadsCleanupResponse reports how many closed issues were deleted.
type beadsCleanupResponse struct {
	Deleted int `json:"deleted"`
}

// HandleBeadsCleanup handles POST /api/beads/cleanup.
// Deletes every closed issue in the workspace: it lists closed issues via
// "bd list --json --status closed -n 0", then runs "bd delete <ids...> --force".
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (h *Handlers) HandleBeadsCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var req beadsCleanupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.WorkingDir == "" {
		http.Error(w, "working_dir is required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(req.WorkingDir) {
		http.Error(w, "working_dir must be an absolute path", http.StatusBadRequest)
		return
	}
	if !h.isKnownWorkspaceDir(req.WorkingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	count, err := h.beadsClient().Cleanup(r.Context(), req.WorkingDir)
	if err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: beads.StderrOf(err)})
		return
	}

	writeJSONOK(w, beadsCleanupResponse{Deleted: count})
}

// beadsActionResponse is a minimal success body for delete/status actions.
type beadsActionResponse struct {
	OK bool `json:"ok"`
}

// beadsDeleteRequest is the JSON body for POST /api/beads/delete.
type beadsDeleteRequest struct {
	WorkingDir string `json:"working_dir"`
	ID         string `json:"id"`
}

// HandleBeadsDelete handles POST /api/beads/delete.
// Runs "bd delete <id> --force" in the workspace directory.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (h *Handlers) HandleBeadsDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var req beadsDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.WorkingDir == "" {
		http.Error(w, "working_dir is required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(req.WorkingDir) {
		http.Error(w, "working_dir must be an absolute path", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	if !h.isKnownWorkspaceDir(req.WorkingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	if err := h.beadsClient().Delete(r.Context(), req.WorkingDir, req.ID); err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: beads.StderrOf(err)})
		return
	}

	writeJSONOK(w, beadsActionResponse{OK: true})
}

// beadsStatusRequest is the JSON body for POST /api/beads/status.
// Action must be "close", "reopen", "defer" or "undefer".
type beadsStatusRequest struct {
	WorkingDir string `json:"working_dir"`
	ID         string `json:"id"`
	Action     string `json:"action"`
}

// HandleBeadsStatus handles POST /api/beads/status.
// Runs "bd close|reopen|defer|undefer <id>" in the workspace directory.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (h *Handlers) HandleBeadsStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var req beadsStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.WorkingDir == "" {
		http.Error(w, "working_dir is required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(req.WorkingDir) {
		http.Error(w, "working_dir must be an absolute path", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}

	var verb string
	switch req.Action {
	case "close", "reopen", "defer", "undefer":
		verb = req.Action
	default:
		http.Error(w, "action must be 'close', 'reopen', 'defer' or 'undefer'", http.StatusBadRequest)
		return
	}

	if !h.isKnownWorkspaceDir(req.WorkingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	if err := h.beadsClient().SetStatus(r.Context(), req.WorkingDir, req.ID, verb); err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: beads.StderrOf(err)})
		return
	}

	writeJSONOK(w, beadsActionResponse{OK: true})
}

// beadsUpdateRequest is the JSON body for POST /api/beads/update.
// Description, Title, Priority, Assignee and Notes are pointers so an omitted
// field (nil) is distinguishable from an intentional value (an empty
// description, assignee or notes clears the field; an empty title is rejected;
// priority 0 is a valid "Critical" value).
type beadsUpdateRequest struct {
	WorkingDir  string  `json:"working_dir"`
	ID          string  `json:"id"`
	Description *string `json:"description,omitempty"`
	Title       *string `json:"title,omitempty"`
	Type        *string `json:"type,omitempty"`
	Priority    *int    `json:"priority,omitempty"` // pointer so 0 ("Critical") is distinguishable from absent
	Assignee    *string `json:"assignee,omitempty"` // pointer so an empty string (clear assignee) is distinguishable from absent
	Notes       *string `json:"notes,omitempty"`    // pointer so an empty string (clear notes) is distinguishable from absent
}

// HandleBeadsUpdate handles POST /api/beads/update.
// Runs "bd update <id> [--title <title>] [-d <description>] [--priority N] [-a <assignee>] [--notes <notes>]"
// in the workspace directory. At least one of title, description, priority,
// assignee or notes must be supplied. When the description is an empty string,
// the --allow-empty-description flag is added so the description can be cleared;
// an empty title is rejected; an empty assignee clears the assignee; an empty
// notes value clears the notes.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (h *Handlers) HandleBeadsUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var req beadsUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.WorkingDir == "" {
		http.Error(w, "working_dir is required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(req.WorkingDir) {
		http.Error(w, "working_dir must be an absolute path", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	if req.Description == nil && req.Title == nil && req.Type == nil && req.Priority == nil && req.Assignee == nil && req.Notes == nil {
		http.Error(w, "title, description, type, priority, assignee or notes is required", http.StatusBadRequest)
		return
	}
	if req.Title != nil && strings.TrimSpace(*req.Title) == "" {
		http.Error(w, "title must not be empty", http.StatusBadRequest)
		return
	}
	if req.Priority != nil && (*req.Priority < 0 || *req.Priority > 4) {
		http.Error(w, "priority must be between 0 and 4", http.StatusBadRequest)
		return
	}
	if !h.isKnownWorkspaceDir(req.WorkingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	if err := h.beadsClient().Update(r.Context(), req.WorkingDir, beads.UpdateParams{
		ID:          req.ID,
		Title:       req.Title,
		Type:        req.Type,
		Description: req.Description,
		Priority:    req.Priority,
		Assignee:    req.Assignee,
		Notes:       req.Notes,
	}); err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: beads.StderrOf(err)})
		return
	}

	writeJSONOK(w, beadsActionResponse{OK: true})
}

// beadsCommentRequest is the JSON body for POST /api/beads/comment.
type beadsCommentRequest struct {
	WorkingDir string `json:"working_dir"`
	ID         string `json:"id"`
	Text       string `json:"text"`
}

// HandleBeadsComment handles POST /api/beads/comment.
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
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.WorkingDir == "" {
		http.Error(w, "working_dir is required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(req.WorkingDir) {
		http.Error(w, "working_dir must be an absolute path", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		http.Error(w, "text must not be empty", http.StatusBadRequest)
		return
	}
	if !h.isKnownWorkspaceDir(req.WorkingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	if err := h.beadsClient().Comment(r.Context(), req.WorkingDir, req.ID, req.Text); err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: beads.StderrOf(err)})
		return
	}

	writeJSONOK(w, beadsActionResponse{OK: true})
}

// beadsDepRequest is the JSON body for POST /api/beads/dep.
// Action must be "add" or "remove". For "add", Type selects the dependency
// edge kind (default "blocks"). DependsOn is the issue that ID depends on; it
// may be a local issue id or an external reference (external:<project>:<cap>).
type beadsDepRequest struct {
	WorkingDir string `json:"working_dir"`
	ID         string `json:"id"`
	DependsOn  string `json:"depends_on"`
	Type       string `json:"type,omitempty"`
	Action     string `json:"action"`
}

// HandleBeadsDep handles POST /api/beads/dep.
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
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.WorkingDir == "" {
		http.Error(w, "working_dir is required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(req.WorkingDir) {
		http.Error(w, "working_dir must be an absolute path", http.StatusBadRequest)
		return
	}
	if !isValidBeadsIssueRef(req.ID) {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	if !isValidBeadsIssueRef(req.DependsOn) {
		http.Error(w, "depends_on is required", http.StatusBadRequest)
		return
	}
	if !h.isKnownWorkspaceDir(req.WorkingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	switch req.Action {
	case "add":
		depType := req.Type
		if depType == "" {
			depType = "blocks"
		}
		if !beads.IsValidDepType(depType) {
			http.Error(w, "invalid dependency type", http.StatusBadRequest)
			return
		}
	case "remove":
		// no extra validation needed
	default:
		http.Error(w, "action must be 'add' or 'remove'", http.StatusBadRequest)
		return
	}

	if err := h.beadsClient().Dep(r.Context(), req.WorkingDir, beads.DepParams{
		ID:        req.ID,
		DependsOn: req.DependsOn,
		Type:      req.Type,
		Action:    req.Action,
	}); err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: beads.StderrOf(err)})
		return
	}

	writeJSONOK(w, beadsActionResponse{OK: true})
}
