package runner

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
)

// VariableResolver handles variable substitution in paths.
//
// Supported variables:
//   - $MITTO_WORKING_DIR or ${MITTO_WORKING_DIR} - Current workspace directory
//   - $WORKSPACE or ${WORKSPACE} - Legacy alias for $MITTO_WORKING_DIR (backward compatible)
//   - $MITTO_WORKTREES_DIR or ${MITTO_WORKTREES_DIR} - Mitto out-of-tree worktrees directory
//   - $HOME or ${HOME} - User's home directory
//   - $MITTO_DIR or ${MITTO_DIR} - Mitto data directory
//   - $USER or ${USER} - Current username
//   - $TMPDIR or ${TMPDIR} - System temp directory
//
// Variables are resolved at runtime when the runner is created.
type VariableResolver struct {
	workspace    string
	home         string
	mittoDir     string
	worktreesDir string
	user         string
	tmpDir       string
}

// NewVariableResolver creates a resolver with runtime values.
func NewVariableResolver(workspace string) (*VariableResolver, error) {
	home, _ := os.UserHomeDir()
	mittoDir, _ := appdir.Dir()
	worktreesDir, _ := appdir.WorktreesDir()
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME") // Windows fallback
	}
	tmpDir := os.TempDir()

	return &VariableResolver{
		workspace:    workspace,
		home:         home,
		mittoDir:     mittoDir,
		worktreesDir: worktreesDir,
		user:         user,
		tmpDir:       tmpDir,
	}, nil
}

// Resolve replaces variables in a path.
//
// Supports both $VAR and ${VAR} syntax.
// Also expands ~ to home directory.
func (vr *VariableResolver) Resolve(path string) string {
	// Replace variables (both $VAR and ${VAR} syntax)
	// $MITTO_WORKING_DIR or ${MITTO_WORKING_DIR} - Current workspace directory
	path = strings.ReplaceAll(path, "$MITTO_WORKING_DIR", vr.workspace)
	path = strings.ReplaceAll(path, "${MITTO_WORKING_DIR}", vr.workspace)
	// Legacy: keep $WORKSPACE support for backward compatibility
	path = strings.ReplaceAll(path, "$WORKSPACE", vr.workspace)
	path = strings.ReplaceAll(path, "${WORKSPACE}", vr.workspace)
	path = strings.ReplaceAll(path, "$HOME", vr.home)
	path = strings.ReplaceAll(path, "${HOME}", vr.home)
	path = strings.ReplaceAll(path, "$MITTO_WORKTREES_DIR", vr.worktreesDir)
	path = strings.ReplaceAll(path, "${MITTO_WORKTREES_DIR}", vr.worktreesDir)
	path = strings.ReplaceAll(path, "$MITTO_DIR", vr.mittoDir)
	path = strings.ReplaceAll(path, "${MITTO_DIR}", vr.mittoDir)
	path = strings.ReplaceAll(path, "$USER", vr.user)
	path = strings.ReplaceAll(path, "${USER}", vr.user)
	path = strings.ReplaceAll(path, "$TMPDIR", vr.tmpDir)
	path = strings.ReplaceAll(path, "${TMPDIR}", vr.tmpDir)

	// Expand ~ to home directory
	if strings.HasPrefix(path, "~/") {
		path = filepath.Join(vr.home, path[2:])
	}

	return path
}

// ResolvePaths resolves variables in a list of paths.
//
// Paths that resolve to an empty string (e.g. an unset variable like
// $MITTO_WORKTREES_DIR when the data directory cannot be determined) are
// dropped, since an empty allow-list entry would otherwise widen the sandbox
// to every path.
func (vr *VariableResolver) ResolvePaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}

	resolved := make([]string, 0, len(paths))
	for _, path := range paths {
		r := vr.Resolve(path)
		if r == "" {
			continue
		}
		resolved = append(resolved, r)
	}
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}

// resolveVariables resolves all variables in restrictions.
func resolveVariables(restrictions *config.RunnerRestrictions, resolver *VariableResolver) *config.RunnerRestrictions {
	if restrictions == nil {
		return nil
	}

	resolved := &config.RunnerRestrictions{
		AllowNetworking:   restrictions.AllowNetworking,
		MergeWithDefaults: restrictions.MergeWithDefaults,
		Docker:            restrictions.Docker,
	}

	resolved.AllowReadFolders = resolver.ResolvePaths(restrictions.AllowReadFolders)
	resolved.AllowWriteFolders = resolver.ResolvePaths(restrictions.AllowWriteFolders)

	return resolved
}
