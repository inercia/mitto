package handlers

import (
	"context"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// ChangedFile represents a file changed in the workspace.
type ChangedFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"`             // "A", "M", "D", "R", "?"
	Additions int    `json:"additions"`          // lines added
	Deletions int    `json:"deletions"`          // lines deleted
	OldPath   string `json:"old_path,omitempty"` // for renames
}

// ChangesResponse is the JSON response for the changes endpoint.
type ChangesResponse struct {
	Files     []ChangedFile `json:"files"` // Must be empty array, not nil — ACP validates this
	IsGitRepo bool          `json:"is_git_repo"`
	Branch    string        `json:"branch"`
	Error     string        `json:"error,omitempty"`
}

const gitChangesTimeout = 15 * time.Second

// HandleSessionChanges handles GET /api/sessions/{id}/changes
// Returns the list of files changed in the session's workspace (git status + numstat).
func (h *Handlers) HandleSessionChanges(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	// Get session's working directory from metadata or background session
	workDir := h.resolveSessionWorkingDir(sessionID)
	if workDir == "" {
		writeJSONOK(w, ChangesResponse{Files: []ChangedFile{}})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), gitChangesTimeout)
	defer cancel()

	// Check if git repo
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--git-dir")
	cmd.Dir = workDir
	if err := cmd.Run(); err != nil {
		writeJSONOK(w, ChangesResponse{Files: []ChangedFile{}})
		return
	}

	// Get branch name
	branchCmd := exec.CommandContext(ctx, "git", "branch", "--show-current")
	branchCmd.Dir = workDir
	branchOut, _ := branchCmd.Output()
	branch := strings.TrimSpace(string(branchOut))

	// Get file statuses using git status --porcelain
	statusCmd := exec.CommandContext(ctx, "git", "status", "--porcelain", "-uall")
	statusCmd.Dir = workDir
	statusOut, err := statusCmd.Output()
	if err != nil {
		writeJSONOK(w, ChangesResponse{Files: []ChangedFile{}, IsGitRepo: true, Branch: branch, Error: "Failed to get git status"})
		return
	}

	// Parse status output into ordered map
	fileMap := make(map[string]*ChangedFile)
	fileOrder := []string{} // Must be empty array, not nil — ACP validates this
	for _, line := range strings.Split(strings.TrimSpace(string(statusOut)), "\n") {
		if len(line) < 3 {
			continue
		}
		indexStatus := line[0]
		workTreeStatus := line[1]
		filePath := strings.TrimSpace(line[2:])
		if filePath == "" {
			continue
		}

		// Handle renames: "R  old -> new"
		var oldPath string
		if strings.Contains(filePath, " -> ") {
			parts := strings.SplitN(filePath, " -> ", 2)
			oldPath = parts[0]
			filePath = parts[1]
		}

		// Skip directory entries (untracked directories appear with trailing slash)
		if strings.HasSuffix(filePath, "/") {
			continue
		}

		status := classifyGitStatus(indexStatus, workTreeStatus)
		cf := &ChangedFile{Path: filePath, Status: status, OldPath: oldPath}
		fileMap[filePath] = cf
		fileOrder = append(fileOrder, filePath)
	}

	// Get additions/deletions using git diff HEAD --numstat (covers both staged and unstaged)
	mergeNumstat(ctx, workDir, fileMap, "HEAD", "--numstat")

	// Build ordered result
	files := make([]ChangedFile, 0, len(fileOrder))
	for _, path := range fileOrder {
		if cf, ok := fileMap[path]; ok {
			files = append(files, *cf)
		}
	}

	writeJSONOK(w, ChangesResponse{Files: files, IsGitRepo: true, Branch: branch})
}

// resolveSessionWorkingDir gets the working directory for a session from metadata or active session.
func (h *Handlers) resolveSessionWorkingDir(sessionID string) string {
	store := h.deps.Store
	if store != nil {
		meta, err := store.GetMetadata(sessionID)
		if err == nil && meta.WorkingDir != "" {
			return meta.WorkingDir
		}
		if err != nil && err != session.ErrSessionNotFound {
			return ""
		}
	}
	// Try getting from active background session
	if h.deps.SessionManager == nil {
		return ""
	}
	bs := h.deps.SessionManager.GetSession(sessionID)
	if bs != nil {
		return bs.GetWorkingDir()
	}
	return ""
}
