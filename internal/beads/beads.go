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

// IsNotFound reports whether err represents a bd "issue not found" failure, as
// opposed to a genuine internal error. bd exits non-zero and prints a message
// like: no issue found matching "<id>" to stderr when the requested issue does
// not exist. Callers use this to map a missing issue to HTTP 404 instead of 500.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(StderrOf(err)), "no issue found matching")
}

// IsSchemaSkew reports whether err represents a bd schema-version skew
// failure: bd deliberately refuses to auto-apply pending migrations to a
// remote-backed database (only one designated clone may migrate it), so every
// read against that store fails until reconciled. This is distinct from a
// transient/startup failure and callers use it to map the failure to an
// actionable "needs migration" response instead of a bare 500.
func IsSchemaSkew(err error) bool {
	if err == nil {
		return false
	}
	stderr := strings.ToLower(StderrOf(err))
	if strings.Contains(stderr, "schema version mismatch") {
		return true
	}
	return strings.Contains(stderr, "schema migration") && strings.Contains(stderr, "remote-backed database")
}

// SchemaSkewDBPath best-effort parses the beads database path out of a schema
// skew error's stderr, e.g. from:
//
//	failed to open routed store at /Users/alvaro/.beads-planning: schema version mismatch
//
// it returns "/Users/alvaro/.beads-planning". Returns "" if the path cannot be
// parsed.
func SchemaSkewDBPath(err error) string {
	stderr := StderrOf(err)
	const marker = "store at "
	idx := strings.Index(stderr, marker)
	if idx < 0 {
		return ""
	}
	rest := stderr[idx+len(marker):]
	end := strings.Index(rest, ":")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

// CreateParams carries optional fields for Client.Create.
type CreateParams struct {
	Title       string
	Type        string
	Priority    *int
	Description string
	Parent      string
	Deps        []string // each entry is "type:id" or bare "id"
	Assignee    string
	Notes       string
}

// UpdateParams carries the fields for Client.Update. Pointer fields distinguish
// "not supplied" from an intentional zero/empty value.
type UpdateParams struct {
	ID          string
	Title       *string
	Type        *string
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

// LabelParams carries the fields for Client.Label.
type LabelParams struct {
	ID     string
	Label  string
	Action string // "add" or "remove"
}

// Client executes bd subcommands for a workspace directory.
// Each method accepts a context and an absolute workspace directory as its
// first two arguments.
type Client interface {
	List(ctx context.Context, dir string) ([]byte, error)
	Status(ctx context.Context, dir string) ([]byte, error)
	Show(ctx context.Context, dir, id string) ([]byte, error)
	Create(ctx context.Context, dir string, p CreateParams) ([]byte, error)
	Delete(ctx context.Context, dir, id string) error
	ListClosedIDs(ctx context.Context, dir string) ([]string, error)
	DeleteIDs(ctx context.Context, dir string, ids []string) error
	SetStatus(ctx context.Context, dir, id, action string) error
	Update(ctx context.Context, dir string, p UpdateParams) error
	Comment(ctx context.Context, dir, id, text string) error
	Dep(ctx context.Context, dir string, p DepParams) error
	Label(ctx context.Context, dir string, p LabelParams) error
	ListAllLabels(ctx context.Context, dir string) ([]byte, error)
	ConfigShow(ctx context.Context, dir string) (map[string]string, error)
	ConfigSet(ctx context.Context, dir, key, value string) error
	ConfigUnset(ctx context.Context, dir, key string) error
	EnsureInitialized(ctx context.Context, dir string) error
	Sync(ctx context.Context, dir, integration, action string) (string, error)
}

// webUIActor is the default BEADS_ACTOR for Tasks-view CRUD initiated through the
// web UI, where there is no single owning conversation. Stamping these writes
// mitto:webui distinguishes them from a human running bd directly and from a
// specific conversation's mitto:<sessionID>.
const webUIActor = "mitto:webui"

// NewClient returns a Client backed by the real bd binary. Writes it makes are
// stamped with the mitto:webui actor for audit attribution.
func NewClient() Client { return &cliClient{runner: execRunner{actor: webUIActor}} }

// NewExecRunner returns the default Runner that invokes the real bd binary,
// stamping writes with the mitto:webui actor. It is exported so callers can wrap
// it (e.g. to bracket each invocation with side effects) while preserving the
// production behavior of NewClient.
func NewExecRunner() Runner { return execRunner{actor: webUIActor} }

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
	case "none", "jira", "github", "gitlab", "linear", "prompts":
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

// IsValidLabel reports whether l is a safe bd label: non-empty after trimming
// and not flag-like (no leading '-'). Guarding against a leading dash prevents
// the label from being parsed as a flag in the bd argument list. bd itself
// enforces any further naming rules and reports a descriptive error otherwise.
func IsValidLabel(l string) bool {
	l = strings.TrimSpace(l)
	if l == "" || strings.HasPrefix(l, "-") {
		return false
	}
	return true
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
