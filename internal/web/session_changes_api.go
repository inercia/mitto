package web

import (
	"context"
	"net/http"
	"os/exec"
	"strconv"
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

// handleSessionChanges handles GET /api/sessions/{id}/changes
// Returns the list of files changed in the session's workspace (git status + numstat).
func (s *Server) handleSessionChanges(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	// Get session's working directory from metadata or background session
	workDir := s.resolveSessionWorkingDir(sessionID)
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
	statusCmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
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
		filePath := strings.TrimSpace(line[3:])

		// Handle renames: "R  old -> new"
		var oldPath string
		if strings.Contains(filePath, " -> ") {
			parts := strings.SplitN(filePath, " -> ", 2)
			oldPath = parts[0]
			filePath = parts[1]
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
func (s *Server) resolveSessionWorkingDir(sessionID string) string {
	store := s.Store()
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
	bs := s.sessionManager.GetSession(sessionID)
	if bs != nil {
		return bs.GetWorkingDir()
	}
	return ""
}

// classifyGitStatus determines the single-letter status from porcelain format.
func classifyGitStatus(indexStatus, workTreeStatus byte) string {
	switch {
	case indexStatus == '?' && workTreeStatus == '?':
		return "?"
	case indexStatus == 'A' || (indexStatus == ' ' && workTreeStatus == 'A'):
		return "A"
	case indexStatus == 'D' || workTreeStatus == 'D':
		return "D"
	case indexStatus == 'R':
		return "R"
	case indexStatus == 'C':
		return "C"
	default:
		return "M"
	}
}

// mergeNumstat runs git diff with numstat and merges additions/deletions into the file map.
func mergeNumstat(ctx context.Context, workDir string, fileMap map[string]*ChangedFile, ref, flag string) {
	args := []string{"diff", "--no-ext-diff", "--no-color", ref, flag}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		filePath := parts[2]
		if len(parts) > 3 {
			filePath = strings.Join(parts[2:], " ")
		}
		if strings.Contains(filePath, " => ") {
			for mapPath := range fileMap {
				if strings.HasSuffix(filePath, mapPath) || mapPath == filePath {
					filePath = mapPath
					break
				}
			}
		}
		if cf, ok := fileMap[filePath]; ok {
			if adds, err := strconv.Atoi(parts[0]); err == nil {
				cf.Additions += adds
			}
			if dels, err := strconv.Atoi(parts[1]); err == nil {
				cf.Deletions += dels
			}
		}
	}
}
