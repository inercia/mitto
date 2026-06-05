package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/inercia/mitto/internal/config"
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

// beadsUpdateRequest is the JSON body for POST /api/beads/update.
// Description and Title are pointers so an omitted field (nil) is distinguishable
// from an intentional empty string (an empty description clears the field; an
// empty title is rejected).
type beadsUpdateRequest struct {
	WorkingDir  string  `json:"working_dir"`
	ID          string  `json:"id"`
	Description *string `json:"description,omitempty"`
	Title       *string `json:"title,omitempty"`
}

// handleBeadsUpdate handles POST /api/beads/update.
// Runs "bd update <id> [--title <title>] [-d <description>]" in the workspace
// directory. At least one of title or description must be supplied. When the
// description is an empty string, the --allow-empty-description flag is added so
// the description can be cleared; an empty title is rejected.
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
	if req.Description == nil && req.Title == nil {
		http.Error(w, "title or description is required", http.StatusBadRequest)
		return
	}
	if req.Title != nil && strings.TrimSpace(*req.Title) == "" {
		http.Error(w, "title must not be empty", http.StatusBadRequest)
		return
	}

	if !s.isKnownWorkspaceDir(req.WorkingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	// "bd update" emits plain text (not JSON), so use the raw runner. The title
	// and description are each passed as discrete argv elements (no shell), so
	// newlines and special characters are safe.
	args := []string{"update", req.ID}
	if req.Title != nil {
		args = append(args, "--title", *req.Title)
	}
	if req.Description != nil {
		args = append(args, "-d", *req.Description)
		if *req.Description == "" {
			args = append(args, "--allow-empty-description")
		}
	}
	if _, stderr, err := runBDRaw(req.WorkingDir, args...); err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: stderr})
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

// beadsDepTypes is the set of dependency edge kinds accepted by "bd dep add -t".
var beadsDepTypes = map[string]bool{
	"blocks":          true,
	"tracks":          true,
	"related":         true,
	"parent-child":    true,
	"discovered-from": true,
	"until":           true,
	"caused-by":       true,
	"validates":       true,
	"relates-to":      true,
	"supersedes":      true,
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

	var args []string
	switch req.Action {
	case "add":
		depType := req.Type
		if depType == "" {
			depType = "blocks"
		}
		if !beadsDepTypes[depType] {
			http.Error(w, "invalid dependency type", http.StatusBadRequest)
			return
		}
		args = []string{"dep", "add", req.ID, req.DependsOn, "-t", depType}
	case "remove":
		args = []string{"dep", "remove", req.ID, req.DependsOn}
	default:
		http.Error(w, "action must be 'add' or 'remove'", http.StatusBadRequest)
		return
	}

	// "bd dep" emits plain text (not JSON), so use the raw runner.
	if _, stderr, err := runBDRaw(req.WorkingDir, args...); err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: stderr})
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

// isValidBeadsConfigKey reports whether key is a safe bd config key: non-empty,
// not flag-like (no leading '-'), and composed only of letters, digits, '.',
// '-', and '_'. This prevents flag injection into the bd argument list.
func isValidBeadsConfigKey(key string) bool {
	if key == "" || strings.HasPrefix(key, "-") {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
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

// beadsConfigShowEntry is one item in the array returned by "bd config show
// --json". Each entry carries the effective value of a config key plus its
// provenance (database, config.yaml, env, git, metadata, default).
type beadsConfigShowEntry struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

// beadsConfigEditableSources is the set of provenance sources whose keys are
// user-set in this workspace and therefore safe to surface (and edit) in the
// UI. "default", "git", and "metadata" entries are excluded: they are derived
// or environment-implied values, not explicit per-folder configuration.
var beadsConfigEditableSources = map[string]bool{
	"database":    true,
	"config.yaml": true,
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

	out, stderr, err := runBD(workingDir, "config", "show", "--json")
	if err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: stderr})
		return
	}

	var entries []beadsConfigShowEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: "bd returned unexpected config format"})
		return
	}

	result := make(map[string]string, len(entries))
	for _, e := range entries {
		if beadsConfigEditableSources[e.Source] {
			result[e.Key] = e.Value
		}
	}

	writeJSONOK(w, result)
}

// handleBeadsConfigSet runs "bd config set <key> <value>" in the workspace directory.
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
	if !isValidBeadsConfigKey(req.Key) {
		http.Error(w, "invalid config key", http.StatusBadRequest)
		return
	}
	if !s.isKnownWorkspaceDir(req.WorkingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	// "bd config set" emits plain text (not JSON), so use the raw runner.
	if _, stderr, err := runBDRaw(req.WorkingDir, "config", "set", req.Key, req.Value); err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: stderr})
		return
	}

	// Best-effort: keep .beads/config.yaml (which beads uses to store secrets
	// such as github.token) out of version control. Failures here must not fail
	// the save — the value was already written successfully.
	_ = ensureBeadsConfigGitignored(req.WorkingDir)

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
	if !isValidBeadsConfigKey(key) {
		http.Error(w, "invalid config key", http.StatusBadRequest)
		return
	}
	if !s.isKnownWorkspaceDir(workingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	// "bd config unset" emits plain text (not JSON), so use the raw runner.
	if _, stderr, err := runBDRaw(workingDir, "config", "unset", key); err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: stderr})
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

// isValidBeadsUpstream reports whether u is a recognised upstream task system.
func isValidBeadsUpstream(u string) bool {
	switch u {
	case "none", "jira", "github", "gitlab", "linear":
		return true
	default:
		return false
	}
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
	if !isValidBeadsUpstream(req.Upstream) {
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

// beadsSyncArgs maps an (integration, action) pair to the bd argument list.
// The flag spelling differs per integration: Jira and Linear use --pull/--push
// while GitHub and GitLab use --pull-only/--push-only. Returns false for unknown
// integration/action combinations.
func beadsSyncArgs(integration, action string) ([]string, bool) {
	switch integration {
	case "jira":
		switch action {
		case "pull":
			return []string{"jira", "sync", "--pull"}, true
		case "push":
			return []string{"jira", "sync", "--push"}, true
		case "sync":
			return []string{"jira", "sync"}, true
		case "status":
			return []string{"jira", "status"}, true
		}
	case "github":
		switch action {
		case "pull":
			return []string{"github", "sync", "--pull-only"}, true
		case "push":
			return []string{"github", "sync", "--push-only"}, true
		case "sync":
			return []string{"github", "sync"}, true
		case "status":
			return []string{"github", "status"}, true
		}
	case "gitlab":
		switch action {
		case "pull":
			return []string{"gitlab", "sync", "--pull-only"}, true
		case "push":
			return []string{"gitlab", "sync", "--push-only"}, true
		case "sync":
			return []string{"gitlab", "sync"}, true
		case "status":
			return []string{"gitlab", "status"}, true
		}
	case "linear":
		switch action {
		case "pull":
			return []string{"linear", "sync", "--pull"}, true
		case "push":
			return []string{"linear", "sync", "--push"}, true
		case "sync":
			return []string{"linear", "sync"}, true
		case "status":
			return []string{"linear", "status"}, true
		}
	}
	return nil, false
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

	args, ok := beadsSyncArgs(upstream, req.Action)
	if !ok {
		http.Error(w, "action must be one of: pull, push, sync, status", http.StatusBadRequest)
		return
	}

	// Sync can be slow (network round-trips to the upstream), so allow a longer
	// budget than the default raw runner timeout.
	out, stderr, err := runBDRawWithTimeout(req.WorkingDir, 120*time.Second, args...)
	if err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: stderr})
		return
	}

	writeJSONOK(w, beadsSyncResponse{OK: true, Output: string(out)})
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
	return runBDRawWithTimeout(dir, 15*time.Second, args...)
}

// runBDRawWithTimeout is runBDRaw with a caller-supplied timeout. Used for
// long-running commands such as upstream sync (pull/push) which can exceed the
// default 15-second budget.
func runBDRawWithTimeout(dir string, timeout time.Duration, args ...string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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

// ensureBeadsConfigGitignored makes a best-effort attempt to keep
// ".beads/config.yaml" — which beads uses to store secret keys such as
// github.token — out of version control. If workingDir is inside a git
// repository and the file is not already ignored, its repo-relative path is
// appended to the local ".git/info/exclude" file. That exclude is per-clone and
// never committed, so it cannot create noise in tracked files (and matches the
// mechanism beads itself references for fork protection).
//
// Every step is best-effort: if git is unavailable, the directory is not a git
// repository, or the file is already ignored, the function returns nil without
// modifying anything.
func ensureBeadsConfigGitignored(workingDir string) error {
	configPath := filepath.Join(workingDir, ".beads", "config.yaml")

	// Already ignored? (exit 0 = ignored). For a non-git directory this exits
	// non-zero, and the rev-parse below then short-circuits to a no-op.
	if runGitQuiet(workingDir, "check-ignore", "-q", "--", configPath) == nil {
		return nil
	}

	// Repository top-level. Fails (non-zero) when workingDir is not a git repo.
	repoRoot, err := runGitOutput(workingDir, "rev-parse", "--show-toplevel")
	if err != nil || repoRoot == "" {
		return nil //nolint:nilerr // not a git repository: nothing to do
	}

	// Locate the exclude file in a worktree/submodule-safe way.
	excludePath, err := runGitOutput(workingDir, "rev-parse", "--git-path", "info/exclude")
	if err != nil || excludePath == "" {
		return err
	}
	if !filepath.IsAbs(excludePath) {
		excludePath = filepath.Join(repoRoot, excludePath)
	}

	// Pattern relative to the repo root (handles workingDir being a subdirectory
	// of the repository). Fall back to the conventional path if it escapes root.
	pattern, relErr := filepath.Rel(repoRoot, configPath)
	if relErr != nil || pattern == "" || strings.HasPrefix(pattern, "..") {
		pattern = filepath.Join(".beads", "config.yaml")
	}
	pattern = filepath.ToSlash(pattern)

	return appendGitExcludePattern(excludePath, pattern)
}

// runGitQuiet runs "git <args>" in dir, discarding output, returning the run
// error. Exit code 0 yields a nil error.
func runGitQuiet(dir string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	return cmd.Run()
}

// runGitOutput runs "git <args>" in dir and returns trimmed stdout.
func runGitOutput(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// appendGitExcludePattern appends pattern to the git exclude file at path unless
// it is already present (idempotent). The parent directory and file are created
// if needed. A trailing newline is ensured before appending.
func appendGitExcludePattern(path, pattern string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil // already present
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck

	var b strings.Builder
	// Start on a fresh line if the file is non-empty without a trailing newline.
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		b.WriteString("\n")
	}
	b.WriteString("# Added by Mitto: keep beads secrets out of version control\n")
	b.WriteString(pattern + "\n")

	_, err = f.WriteString(b.String())
	return err
}
