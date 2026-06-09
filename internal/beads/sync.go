package beads

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// isInitialized reports whether dir already has a beads database. It is
// detected by the presence of .beads/config.yaml — the exact file bd complains
// about when "run 'bd init' first" is required.
func isInitialized(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".beads", "config.yaml"))
	return err == nil
}

// EnsureInitialized makes bd commands usable in workingDir by running a minimal
// "bd init" when the folder has not yet been initialized. It is a no-op when the
// folder is already initialized.
//
// The init is intentionally conservative: --skip-agents and --skip-hooks avoid
// writing AGENTS.md or installing git hooks, while --non-interactive and --quiet
// keep it prompt-free and silent. A longer timeout is used because the first
// Dolt database creation can be slow.
func (c *cliClient) EnsureInitialized(ctx context.Context, dir string) error {
	if isInitialized(dir) {
		return nil // already initialized
	}

	_, err := c.runRaw(ctx, initTimeout, dir,
		"init", "--non-interactive", "--quiet", "--skip-agents", "--skip-hooks")
	return err
}

// Sync maps (integration, action) to the appropriate bd arguments and runs
// the command with an extended timeout to accommodate network round-trips.
// Returns the captured stdout on success.
func (c *cliClient) Sync(ctx context.Context, dir, integration, action string) (string, error) {
	args, ok := syncArgs(integration, action)
	if !ok {
		return "", &CmdError{Err: errors.New("unknown sync integration/action: " + integration + "/" + action)}
	}
	out, err := c.runRaw(ctx, syncTimeout, dir, args...)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// syncArgs maps an (integration, action) pair to the bd argument list.
// The flag spelling differs per integration: Jira and Linear use --pull/--push
// while GitHub and GitLab use --pull-only/--push-only. Returns false for unknown
// integration/action combinations.
func syncArgs(integration, action string) ([]string, bool) {
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
