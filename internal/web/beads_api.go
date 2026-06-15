package web

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/inercia/mitto/internal/beads"
	"github.com/inercia/mitto/internal/config"
)

// beadsClient returns the injectable beads Client. When the server was
// constructed without an explicit client (e.g. in tests via &Server{...}),
// it falls back to a default client backed by the real bd binary.
func (s *Server) beadsClient() beads.Client {
	if s.beads != nil {
		return s.beads
	}
	return beads.NewClient()
}

// beadsErrorResponse is returned when bd is missing or exits non-zero.
type beadsErrorResponse struct {
	Error  string `json:"error"`
	Stderr string `json:"stderr,omitempty"`
}

// handleBeadsList handles GET /api/beads/list?working_dir=...
// Runs "bd list --json --all -n 0" in the workspace directory.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (s *Server) handleBeadsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	workingDir := r.URL.Query().Get("working_dir")
	if workingDir == "" {
		http.Error(w, "working_dir is required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(workingDir) {
		http.Error(w, "working_dir must be an absolute path", http.StatusBadRequest)
		return
	}
	if !s.isKnownWorkspaceDir(workingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	out, err := s.beadsClient().List(r.Context(), workingDir)
	if err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: beads.StderrOf(err)})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(out) //nolint:errcheck
}

// handleBeadsShow handles GET /api/beads/show?working_dir=...&id=...
// Runs "bd show <id> --json --include-comments" in the workspace directory,
// returning the full issue including its comments and dependencies.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (s *Server) handleBeadsShow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	workingDir := r.URL.Query().Get("working_dir")
	id := r.URL.Query().Get("id")

	if workingDir == "" {
		http.Error(w, "working_dir is required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(workingDir) {
		http.Error(w, "working_dir must be an absolute path", http.StatusBadRequest)
		return
	}
	if id == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	if !s.isKnownWorkspaceDir(workingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	out, err := s.beadsClient().Show(r.Context(), workingDir, id)
	if err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: beads.StderrOf(err)})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(out) //nolint:errcheck
}

// beadsCreateRequest is the JSON body for POST /api/beads/create.
type beadsCreateRequest struct {
	WorkingDir  string `json:"working_dir"`
	Title       string `json:"title"`
	Type        string `json:"type,omitempty"`
	Priority    *int   `json:"priority,omitempty"` // pointer so 0 ("Critical") is distinguishable from absent
	Description string `json:"description,omitempty"`
}

// handleBeadsCreate handles POST /api/beads/create.
// Runs "bd create <title> --json [--type T] [--priority N] [-d D]" in the workspace directory.
// When title is empty but description is non-empty, the title is auto-generated via the
// auxiliary session (with a 60s timeout) and falls back to GenerateQuickTitle, then "New Issue".
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (s *Server) handleBeadsCreate(w http.ResponseWriter, r *http.Request) {
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

	if !s.isKnownWorkspaceDir(req.WorkingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	// Auto-generate title from description when the caller omitted it.
	if title == "" {
		ws := s.sessionManager.GetWorkspace(req.WorkingDir)
		if ws == nil || ws.UUID == "" {
			http.Error(w, "unable to resolve workspace", http.StatusInternalServerError)
			return
		}

		if s.auxiliaryManager != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
			defer cancel()
			if generated, err := s.auxiliaryManager.GenerateTitle(ctx, ws.UUID, description); err == nil && strings.TrimSpace(generated) != "" {
				title = strings.TrimSpace(generated)
			} else if err != nil {
				s.logger.Warn("beads: title generation failed, using fallback", "error", err)
			}
		}

		// Fallback: derive a quick title from the description text.
		if title == "" {
			title = GenerateQuickTitle(description)
		}
		// Last resort.
		if title == "" {
			title = "New Issue"
		}
	}

	out, err := s.beadsClient().Create(r.Context(), req.WorkingDir, beads.CreateParams{
		Title:       title,
		Type:        req.Type,
		Priority:    req.Priority,
		Description: req.Description,
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

// handleBeadsCleanup handles POST /api/beads/cleanup.
// Deletes every closed issue in the workspace: it lists closed issues via
// "bd list --json --status closed -n 0", then runs "bd delete <ids...> --force".
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (s *Server) handleBeadsCleanup(w http.ResponseWriter, r *http.Request) {
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
	if !s.isKnownWorkspaceDir(req.WorkingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	count, err := s.beadsClient().Cleanup(r.Context(), req.WorkingDir)
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

// handleBeadsDelete handles POST /api/beads/delete.
// Runs "bd delete <id> --force" in the workspace directory.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (s *Server) handleBeadsDelete(w http.ResponseWriter, r *http.Request) {
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
	if !s.isKnownWorkspaceDir(req.WorkingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	if err := s.beadsClient().Delete(r.Context(), req.WorkingDir, req.ID); err != nil {
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

// handleBeadsStatus handles POST /api/beads/status.
// Runs "bd close|reopen|defer|undefer <id>" in the workspace directory.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (s *Server) handleBeadsStatus(w http.ResponseWriter, r *http.Request) {
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

	if !s.isKnownWorkspaceDir(req.WorkingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	if err := s.beadsClient().SetStatus(r.Context(), req.WorkingDir, req.ID, verb); err != nil {
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
	Priority    *int    `json:"priority,omitempty"` // pointer so 0 ("Critical") is distinguishable from absent
	Assignee    *string `json:"assignee,omitempty"` // pointer so an empty string (clear assignee) is distinguishable from absent
	Notes       *string `json:"notes,omitempty"`    // pointer so an empty string (clear notes) is distinguishable from absent
}

// handleBeadsUpdate handles POST /api/beads/update.
// Runs "bd update <id> [--title <title>] [-d <description>] [--priority N] [-a <assignee>] [--notes <notes>]"
// in the workspace directory. At least one of title, description, priority,
// assignee or notes must be supplied. When the description is an empty string,
// the --allow-empty-description flag is added so the description can be cleared;
// an empty title is rejected; an empty assignee clears the assignee; an empty
// notes value clears the notes.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (s *Server) handleBeadsUpdate(w http.ResponseWriter, r *http.Request) {
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
	if req.Description == nil && req.Title == nil && req.Priority == nil && req.Assignee == nil && req.Notes == nil {
		http.Error(w, "title, description, priority, assignee or notes is required", http.StatusBadRequest)
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
	if !s.isKnownWorkspaceDir(req.WorkingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	if err := s.beadsClient().Update(r.Context(), req.WorkingDir, beads.UpdateParams{
		ID:          req.ID,
		Title:       req.Title,
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

// handleBeadsComment handles POST /api/beads/comment.
// Runs "bd comment <id> -- <text>" in the workspace directory, adding a comment
// to the issue. The text must be non-empty.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (s *Server) handleBeadsComment(w http.ResponseWriter, r *http.Request) {
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
	if !s.isKnownWorkspaceDir(req.WorkingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	if err := s.beadsClient().Comment(r.Context(), req.WorkingDir, req.ID, req.Text); err != nil {
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

// isValidBeadsIssueRef reports whether s is a safe issue reference: non-empty,
// not flag-like (no leading '-'), and composed only of letters, digits, '.',
// '-', '_', and ':'. The colon permits external references of the form
// external:<project>:<capability>. This prevents flag injection into the bd
// argument list.
func isValidBeadsIssueRef(s string) bool {
	if s == "" || strings.HasPrefix(s, "-") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_' || r == ':':
		default:
			return false
		}
	}
	return true
}

// handleBeadsDep handles POST /api/beads/dep.
// For action "add" it runs "bd dep add <id> <depends_on> -t <type>"; for
// "remove" it runs "bd dep remove <id> <depends_on>". Both emit plain text.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (s *Server) handleBeadsDep(w http.ResponseWriter, r *http.Request) {
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
	if !s.isKnownWorkspaceDir(req.WorkingDir) {
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

	if err := s.beadsClient().Dep(r.Context(), req.WorkingDir, beads.DepParams{
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

// beadsConfigSetRequest is the JSON body for PUT /api/beads/config.
type beadsConfigSetRequest struct {
	WorkingDir string `json:"working_dir"`
	Key        string `json:"key"`
	Value      string `json:"value"`
}

// handleBeadsConfig handles the per-folder beads config store:
//   - GET    /api/beads/config?working_dir=...            -> "bd config show --json"
//   - PUT    /api/beads/config (body: working_dir,key,value) -> "bd config set <key> <value>"
//   - DELETE /api/beads/config?working_dir=...&key=...     -> "bd config unset <key>"
//
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (s *Server) handleBeadsConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleBeadsConfigGet(w, r)
	case http.MethodPut:
		s.handleBeadsConfigSet(w, r)
	case http.MethodDelete:
		s.handleBeadsConfigUnset(w, r)
	default:
		methodNotAllowed(w)
	}
}

// handleBeadsConfigGet runs "bd config show --json" in the workspace directory
// and returns a flat {key: value} map of user-set configuration.
//
// We use "show" rather than "list" because "list" only reports keys stored in
// the beads database, omitting integration keys (e.g. github.token) that live
// in .beads/config.yaml. "show" reports all effective config with provenance;
// we filter to user-set sources and flatten the array into the flat-map shape
// the frontend expects.
func (s *Server) handleBeadsConfigGet(w http.ResponseWriter, r *http.Request) {
	workingDir := r.URL.Query().Get("working_dir")
	if workingDir == "" {
		http.Error(w, "working_dir is required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(workingDir) {
		http.Error(w, "working_dir must be an absolute path", http.StatusBadRequest)
		return
	}
	if !s.isKnownWorkspaceDir(workingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	result, err := s.beadsClient().ConfigShow(r.Context(), workingDir)
	if err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: beads.StderrOf(err)})
		return
	}

	writeJSONOK(w, result)
}

// handleBeadsConfigSet runs "bd config set <key> <value>" in the workspace
// directory. The folder is auto-initialized first when needed so configuring
// an integration in a fresh folder "just works" rather than failing with
// "run 'bd init' first".
func (s *Server) handleBeadsConfigSet(w http.ResponseWriter, r *http.Request) {
	var req beadsConfigSetRequest
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
	if !beads.IsValidConfigKey(req.Key) {
		http.Error(w, "invalid config key", http.StatusBadRequest)
		return
	}
	if !s.isKnownWorkspaceDir(req.WorkingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	if err := s.beadsClient().ConfigSet(r.Context(), req.WorkingDir, req.Key, req.Value); err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: beads.StderrOf(err)})
		return
	}

	writeJSONOK(w, beadsActionResponse{OK: true})
}

// handleBeadsConfigUnset runs "bd config unset <key>" in the workspace directory.
func (s *Server) handleBeadsConfigUnset(w http.ResponseWriter, r *http.Request) {
	workingDir := r.URL.Query().Get("working_dir")
	key := r.URL.Query().Get("key")

	if workingDir == "" {
		http.Error(w, "working_dir is required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(workingDir) {
		http.Error(w, "working_dir must be an absolute path", http.StatusBadRequest)
		return
	}
	if !beads.IsValidConfigKey(key) {
		http.Error(w, "invalid config key", http.StatusBadRequest)
		return
	}
	if !s.isKnownWorkspaceDir(workingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	if err := s.beadsClient().ConfigUnset(r.Context(), workingDir, key); err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: beads.StderrOf(err)})
		return
	}

	writeJSONOK(w, beadsActionResponse{OK: true})
}

// beadsUpstreamRequest is the JSON body for PUT /api/beads/upstream.
type beadsUpstreamRequest struct {
	WorkingDir string `json:"working_dir"`
	Upstream   string `json:"upstream"`
}

// beadsUpstreamResponse reports the configured upstream task system for a folder.
type beadsUpstreamResponse struct {
	Upstream string `json:"upstream"`
}

// handleBeadsUpstream manages the per-folder beads upstream task system stored
// in folders.json (folder-native, not a bd config value):
//   - GET /api/beads/upstream?working_dir=...        -> {"upstream": "none|jira|github|gitlab|linear"}
//   - PUT /api/beads/upstream (body: working_dir,upstream) -> persists the choice
//
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (s *Server) handleBeadsUpstream(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleBeadsUpstreamGet(w, r)
	case http.MethodPut:
		s.handleBeadsUpstreamSet(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) handleBeadsUpstreamGet(w http.ResponseWriter, r *http.Request) {
	workingDir := r.URL.Query().Get("working_dir")
	if workingDir == "" {
		http.Error(w, "working_dir is required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(workingDir) {
		http.Error(w, "working_dir must be an absolute path", http.StatusBadRequest)
		return
	}
	if !s.isKnownWorkspaceDir(workingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	upstream := config.FolderBeadsUpstream(workingDir)
	if upstream == "" {
		upstream = "none"
	}
	writeJSONOK(w, beadsUpstreamResponse{Upstream: upstream})
}

func (s *Server) handleBeadsUpstreamSet(w http.ResponseWriter, r *http.Request) {
	var req beadsUpstreamRequest
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
	if !beads.IsValidUpstream(req.Upstream) {
		http.Error(w, "upstream must be one of: none, jira, github, gitlab, linear", http.StatusBadRequest)
		return
	}
	if !s.isKnownWorkspaceDir(req.WorkingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	if err := config.SetFolderBeadsUpstream(req.WorkingDir, req.Upstream); err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error()})
		return
	}

	upstream := req.Upstream
	if upstream == "" {
		upstream = "none"
	}
	writeJSONOK(w, beadsUpstreamResponse{Upstream: upstream})
}

// beadsSyncRequest is the JSON body for POST /api/beads/sync.
// Action must be "pull", "push", "sync", or "status".
type beadsSyncRequest struct {
	WorkingDir string `json:"working_dir"`
	Action     string `json:"action"`
}

// beadsSyncResponse carries the captured bd output on success.
type beadsSyncResponse struct {
	OK     bool   `json:"ok"`
	Output string `json:"output,omitempty"`
}

// handleBeadsSync handles POST /api/beads/sync. It runs the configured
// upstream's pull/push/sync/status command for the folder. The integration is
// read authoritatively from folders.json — the client only chooses the action.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (s *Server) handleBeadsSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var req beadsSyncRequest
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
	if !s.isKnownWorkspaceDir(req.WorkingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	// The integration is read from folders.json, never trusted from the client.
	upstream := config.FolderBeadsUpstream(req.WorkingDir)
	if upstream == "" || upstream == "none" {
		writeJSONOK(w, beadsErrorResponse{Error: "no upstream task system is configured for this folder"})
		return
	}

	// Validate the action before invoking bd (keeps HTTP 400 for invalid actions).
	switch req.Action {
	case "pull", "push", "sync", "status":
		// valid
	default:
		http.Error(w, "action must be one of: pull, push, sync, status", http.StatusBadRequest)
		return
	}

	out, err := s.beadsClient().Sync(r.Context(), req.WorkingDir, upstream, req.Action)
	if err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: beads.StderrOf(err)})
		return
	}

	writeJSONOK(w, beadsSyncResponse{OK: true, Output: out})
}

// isKnownWorkspaceDir returns true if workingDir matches any configured
// workspace or the working directory of any active session. The latter covers
// conversations running in an isolated git worktree (under
// <repo>/.mitto/worktrees/<sid>): that directory is not a registered workspace,
// but it has its own checked-out .beads/ and is a valid bd working directory.
// This mirrors FileServer.isValidWorkspace.
func (s *Server) isKnownWorkspaceDir(workingDir string) bool {
	if s.sessionManager == nil {
		return false
	}
	for _, ws := range s.sessionManager.GetWorkspaces() {
		if ws.WorkingDir == workingDir {
			return true
		}
	}
	for _, dir := range s.sessionManager.GetActiveWorkingDirs() {
		if dir == workingDir {
			return true
		}
	}
	return false
}
