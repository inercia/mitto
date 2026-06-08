package beads

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// configShowEntry is one item in the array returned by "bd config show --json".
// Each entry carries the effective value of a config key and its provenance.
type configShowEntry struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

// configEditableSources is the set of provenance sources whose keys are
// user-set in this workspace and therefore safe to surface (and edit) in the
// UI. "default", "git", and "metadata" entries are excluded.
var configEditableSources = map[string]bool{
	"database":    true,
	"config.yaml": true,
}

// ConfigShow runs "bd config show --json" and returns a flat {key: value} map
// of user-set configuration (filtering to editable sources).
func (c *cliClient) ConfigShow(ctx context.Context, dir string) (map[string]string, error) {
	out, err := c.runJSON(ctx, dir, "config", "show", "--json")
	if err != nil {
		return nil, err
	}

	var entries []configShowEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, &CmdError{Err: errors.New("bd returned unexpected config format")}
	}

	result := make(map[string]string, len(entries))
	for _, e := range entries {
		if configEditableSources[e.Source] {
			result[e.Key] = e.Value
		}
	}
	return result, nil
}

// ConfigSet auto-initializes the workspace if needed, then runs
// "bd config set <key> <value>". On success it makes a best-effort attempt to
// add ".beads/config.yaml" to the workspace .gitignore.
func (c *cliClient) ConfigSet(ctx context.Context, dir, key, value string) error {
	// bd refuses "config set" until the folder has a .beads/config.yaml, so
	// auto-initialize on demand.
	if err := c.EnsureInitialized(ctx, dir); err != nil {
		return err
	}

	if _, err := c.runRaw(ctx, defaultTimeout, dir, "config", "set", key, value); err != nil {
		return err
	}

	// Best-effort: keep .beads/config.yaml (which stores secrets such as
	// github.token) out of version control. Failures are silently ignored.
	_ = ensureConfigGitignored(dir)
	return nil
}

// ConfigUnset runs "bd config unset <key>".
func (c *cliClient) ConfigUnset(ctx context.Context, dir, key string) error {
	_, err := c.runRaw(ctx, defaultTimeout, dir, "config", "unset", key)
	return err
}

// ensureConfigGitignored makes a best-effort attempt to keep
// ".beads/config.yaml" — which beads uses to store secrets such as
// github.token — out of version control by appending the pattern to the
// workspace root .gitignore.
func ensureConfigGitignored(workingDir string) error {
	gitignorePath := filepath.Join(workingDir, ".gitignore")
	return appendGitignorePattern(gitignorePath, ".beads/config.yaml")
}

// appendGitignorePattern appends pattern to the .gitignore-style file at path
// unless it is already present (idempotent). The parent directory and file are
// created if needed. A trailing newline is ensured before appending.
func appendGitignorePattern(path, pattern string) error {
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
