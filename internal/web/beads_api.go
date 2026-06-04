package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

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

	out, stderr, err := runBD(workingDir, "list", "--json", "--all", "-n", "0")
	if err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: stderr})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(out) //nolint:errcheck
}

// handleBeadsShow handles GET /api/beads/show?working_dir=...&id=...
// Runs "bd show <id> --json --include-comments --children" in the workspace directory.
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

	out, stderr, err := runBD(workingDir, "show", id, "--json", "--include-comments", "--children")
	if err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: stderr})
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
	if strings.TrimSpace(req.Title) == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}

	if !s.isKnownWorkspaceDir(req.WorkingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	args := []string{"create", req.Title, "--json"}
	if req.Type != "" {
		args = append(args, "--type", req.Type)
	}
	if req.Priority != nil {
		args = append(args, "--priority", strconv.Itoa(*req.Priority))
	}
	if req.Description != "" {
		args = append(args, "-d", req.Description)
	}

	out, stderr, err := runBD(req.WorkingDir, args...)
	if err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: stderr})
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

// beadsListItem is the minimal shape needed to collect issue IDs from bd list.
type beadsListItem struct {
	ID string `json:"id"`
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

	// Collect the IDs of all closed issues.
	out, stderr, err := runBD(req.WorkingDir, "list", "--json", "--status", "closed", "-n", "0")
	if err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: stderr})
		return
	}

	var items []beadsListItem
	if err := json.Unmarshal(out, &items); err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: "failed to parse closed issues"})
		return
	}

	ids := make([]string, 0, len(items))
	for _, it := range items {
		if it.ID != "" {
			ids = append(ids, it.ID)
		}
	}

	// Nothing to clean up — report success with a zero count.
	if len(ids) == 0 {
		writeJSONOK(w, beadsCleanupResponse{Deleted: 0})
		return
	}

	// "bd delete" emits plain text (not JSON), so use the raw runner. --force is
	// required to actually delete (and to orphan, rather than block on, any
	// dependents that live outside the closed set).
	args := make([]string, 0, len(ids)+2)
	args = append(args, "delete")
	args = append(args, ids...)
	args = append(args, "--force")

	if _, stderr, err := runBDRaw(req.WorkingDir, args...); err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: stderr})
		return
	}

	writeJSONOK(w, beadsCleanupResponse{Deleted: len(ids)})
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

	// "bd delete" emits plain text (not JSON), so use the raw runner. --force is
	// required to delete (and to orphan, rather than block on, any dependents).
	if _, stderr, err := runBDRaw(req.WorkingDir, "delete", req.ID, "--force"); err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: stderr})
		return
	}

	writeJSONOK(w, beadsActionResponse{OK: true})
}

// beadsStatusRequest is the JSON body for POST /api/beads/status.
// Action must be "close" or "reopen".
type beadsStatusRequest struct {
	WorkingDir string `json:"working_dir"`
	ID         string `json:"id"`
	Action     string `json:"action"`
}

// handleBeadsStatus handles POST /api/beads/status.
// Runs "bd close <id>" or "bd reopen <id>" in the workspace directory.
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
	case "close":
		verb = "close"
	case "reopen":
		verb = "reopen"
	default:
		http.Error(w, "action must be 'close' or 'reopen'", http.StatusBadRequest)
		return
	}

	if !s.isKnownWorkspaceDir(req.WorkingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	// "bd close"/"bd reopen" emit plain text (not JSON), so use the raw runner.
	if _, stderr, err := runBDRaw(req.WorkingDir, verb, req.ID); err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: stderr})
		return
	}

	writeJSONOK(w, beadsActionResponse{OK: true})
}

// isKnownWorkspaceDir returns true if workingDir matches any configured workspace.
func (s *Server) isKnownWorkspaceDir(workingDir string) bool {
	if s.sessionManager == nil {
		return false
	}
	for _, ws := range s.sessionManager.GetWorkspaces() {
		if ws.WorkingDir == workingDir {
			return true
		}
	}
	return false
}

// runBDRaw executes "bd <args>" with a 15-second timeout in dir, without
// validating that the output is JSON. Use this for commands such as "delete"
// that emit plain text. Returns stdout bytes, stderr string, and error.
func runBDRaw(dir string, args ...string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bd", args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		msg := err.Error()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			msg = "bd command timed out"
		} else if errors.As(err, &exitErr) {
			msg = "bd exited with non-zero status"
		}
		return nil, stderr.String(), errors.New(msg)
	}

	return stdout.Bytes(), "", nil
}

// runBD executes "bd <args>" like runBDRaw but additionally validates that the
// output is valid JSON before passing it through.
// Returns stdout bytes, stderr string, and error.
func runBD(dir string, args ...string) ([]byte, string, error) {
	out, stderr, err := runBDRaw(dir, args...)
	if err != nil {
		return nil, stderr, err
	}
	if !json.Valid(out) {
		return nil, stderr, errors.New("bd returned invalid JSON")
	}
	return out, "", nil
}
