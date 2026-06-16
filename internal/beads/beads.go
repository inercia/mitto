// Package beads provides a typed Client for the bd (beads) command-line tool.
// All bd invocations are isolated here; callers in internal/web receive errors
// as *CmdError values and use StderrOf to extract captured stderr output.
package beads

import (
	"context"
	"errors"
	"strings"
)

// CmdError wraps a bd command failure. It carries both the error message and
// the captured stderr so callers can surface both to the user.
type CmdError struct {
	Err    error
	Stderr string
}

// Error implements the error interface.
func (e *CmdError) Error() string { return e.Err.Error() }

// Unwrap allows errors.Is/As to inspect the underlying error.
func (e *CmdError) Unwrap() error { return e.Err }

// StderrOf returns the Stderr field of the *CmdError wrapped in err, or "" if
// err is not (or does not wrap) a *CmdError.
func StderrOf(err error) string {
	var ce *CmdError
	if errors.As(err, &ce) {
		return ce.Stderr
	}
	return ""
}

// CreateParams carries optional fields for Client.Create.
type CreateParams struct {
	Title       string
	Type        string
	Priority    *int
	Description string
	Parent      string
}

// UpdateParams carries the fields for Client.Update. Pointer fields distinguish
// "not supplied" from an intentional zero/empty value.
type UpdateParams struct {
	ID          string
	Title       *string
	Description *string
	Priority    *int
	Assignee    *string
	Notes       *string
}

// DepParams carries the fields for Client.Dep.
type DepParams struct {
	ID        string
	DependsOn string
	Type      string // "add" action only; defaults to "blocks"
	Action    string // "add" or "remove"
}

// Client executes bd subcommands for a workspace directory.
// Each method accepts a context and an absolute workspace directory as its
// first two arguments.
type Client interface {
	List(ctx context.Context, dir string) ([]byte, error)
	Show(ctx context.Context, dir, id string) ([]byte, error)
	Create(ctx context.Context, dir string, p CreateParams) ([]byte, error)
	Delete(ctx context.Context, dir, id string) error
	Cleanup(ctx context.Context, dir string) (int, error)
	SetStatus(ctx context.Context, dir, id, action string) error
	Update(ctx context.Context, dir string, p UpdateParams) error
	Comment(ctx context.Context, dir, id, text string) error
	Dep(ctx context.Context, dir string, p DepParams) error
	ConfigShow(ctx context.Context, dir string) (map[string]string, error)
	ConfigSet(ctx context.Context, dir, key, value string) error
	ConfigUnset(ctx context.Context, dir, key string) error
	EnsureInitialized(ctx context.Context, dir string) error
	Sync(ctx context.Context, dir, integration, action string) (string, error)
}

// NewClient returns a Client backed by the real bd binary.
func NewClient() Client { return &cliClient{runner: execRunner{}} }

// NewClientWithRunner returns a Client backed by a custom Runner (for testing).
func NewClientWithRunner(r Runner) Client { return &cliClient{runner: r} }

// IsValidConfigKey reports whether key is a safe bd config key: non-empty, not
// flag-like (no leading '-'), and composed only of letters, digits, '.', '-',
// and '_'. This prevents flag injection into the bd argument list.
func IsValidConfigKey(key string) bool {
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

// IsValidUpstream reports whether u is a recognised upstream task system.
func IsValidUpstream(u string) bool {
	switch u {
	case "none", "jira", "github", "gitlab", "linear":
		return true
	default:
		return false
	}
}

// IsValidDepType reports whether t is a recognised dependency edge kind
// accepted by "bd dep add -t".
func IsValidDepType(t string) bool {
	_, ok := validDepTypes[t]
	return ok
}

// validDepTypes is the set of dependency edge kinds accepted by "bd dep add -t".
var validDepTypes = map[string]bool{
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
