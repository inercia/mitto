package handlers

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/inercia/mitto/internal/beads"
)

// beadsClient returns the injectable beads Client. When the handlers were
// constructed without an explicit client (e.g. in tests), it falls back to a
// default client backed by the real bd binary.
func (h *Handlers) beadsClient() beads.Client {
	if h.deps.BeadsClient != nil {
		return h.deps.BeadsClient
	}
	return beads.NewClient()
}

// isKnownWorkspaceDir returns true if workingDir matches any configured workspace.
func (h *Handlers) isKnownWorkspaceDir(workingDir string) bool {
	if h.deps.SessionManager == nil {
		return false
	}
	for _, ws := range h.deps.SessionManager.GetWorkspaces() {
		if ws.WorkingDir == workingDir {
			return true
		}
	}
	return false
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

// beadsErrorResponse is returned when bd is missing or exits non-zero.
type beadsErrorResponse struct {
	Error  string `json:"error"`
	Stderr string `json:"stderr,omitempty"`
}

// HandleBeadsList handles GET /api/beads/list?working_dir=...
// Runs "bd list --json --all -n 0" in the workspace directory.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (h *Handlers) HandleBeadsList(w http.ResponseWriter, r *http.Request) {
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
	if !h.isKnownWorkspaceDir(workingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	out, err := h.beadsClient().List(r.Context(), workingDir)
	if err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: beads.StderrOf(err)})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(out) //nolint:errcheck
}

// HandleBeadsStats handles GET /api/beads/stats?working_dir=...
// Runs "bd status --json --no-activity" in the workspace directory, returning an
// aggregate summary of issue counts by state (open, in_progress, ready, blocked,
// closed, ...). Used by the sidebar to render a per-folder Tasks stats line.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (h *Handlers) HandleBeadsStats(w http.ResponseWriter, r *http.Request) {
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
	if !h.isKnownWorkspaceDir(workingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	out, err := h.beadsClient().Status(r.Context(), workingDir)
	if err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: beads.StderrOf(err)})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(out) //nolint:errcheck
}

// HandleBeadsShow handles GET /api/beads/show?working_dir=...&id=...
// Runs "bd show <id> --json --include-comments" in the workspace directory,
// returning the full issue including its comments and dependencies.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (h *Handlers) HandleBeadsShow(w http.ResponseWriter, r *http.Request) {
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
	if !h.isKnownWorkspaceDir(workingDir) {
		http.Error(w, "working_dir does not match any known workspace", http.StatusBadRequest)
		return
	}

	out, err := h.beadsClient().Show(r.Context(), workingDir, id)
	if err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: beads.StderrOf(err)})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(out) //nolint:errcheck
}
