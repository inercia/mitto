// Package beads provides a typed Client for the bd (beads) command-line tool.
// All bd invocations are isolated here; callers in internal/web receive errors
// as *CmdError values and use StderrOf to extract captured stderr output.
package beads

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// CmdError wraps a bd command failure. It carries both the error message and
// the captured stderr so callers can surface both to the user. ExitCode is the
// bd subprocess exit status when the failure was a non-zero exit (0 otherwise
// — e.g. timeout or context cancellation, where no exit status is available).
type CmdError struct {
	Err      error
	Stderr   string
	ExitCode int
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

// ExitCodeOf returns the ExitCode field of the *CmdError wrapped in err, or 0
// if err is not (or does not wrap) a *CmdError. A returned 0 is ambiguous —
// it means either "not a *CmdError" or "no exit status recorded" (timeout,
// context cancellation, or exit code 0 which never surfaces as a failure).
func ExitCodeOf(err error) int {
	var ce *CmdError
	if errors.As(err, &ce) {
		return ce.ExitCode
	}
	return 0
}

// IsNotFound reports whether err represents a bd "issue not found" failure, as
// opposed to a genuine internal error. bd exits non-zero and prints a message
// like: no issue found matching "<id>" to stderr when the requested issue does
// not exist; newer versions emit a JSON error object on stdout of the form
// {"error":"no issues found matching the provided IDs", ...} instead. Callers
// use this to map a missing issue to HTTP 404 instead of 500.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	diag := StderrOf(err)
	if strings.Contains(strings.ToLower(diag), "no issue found matching") {
		return true
	}
	// Also match bd's JSON error object variant, which uses the plural
	// "no issues found matching" phrasing and may land on stdout (captured
	// into Stderr by diagnosticOutput when the real stderr is empty).
	trimmed := strings.TrimSpace(diag)
	if trimmed == "" {
		return false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return false
	}
	for k, raw := range obj {
		if !strings.EqualFold(k, "error") {
			continue
		}
		var msg string
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}
		m := strings.ToLower(msg)
		if strings.Contains(m, "no issue found matching") ||
			strings.Contains(m, "no issues found matching") {
			return true
		}
	}
	return false
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
	if strings.Contains(stderr, "remote_migrate_gate") {
		return true
	}
	return strings.Contains(stderr, "schema migration") && strings.Contains(stderr, "remote-backed database")
}

// SchemaSkewOption is one remediation path advertised by bd's structured
// remote_migrate_gate error blob (e.g. "migrate" or "adopt"). It carries a
// mode identifier plus a human-readable description and optional command
// hint so the UI can render actionable buttons instead of asking the user to
// hand-parse the diagnostic.
type SchemaSkewOption struct {
	Mode        string `json:"mode"`
	Description string `json:"description,omitempty"`
	Command     string `json:"command,omitempty"`
}

// SchemaSkewDetails is the structured view of a bd schema-skew failure.
// Fields are populated best-effort: DBPath falls back to legacy "store at ..."
// regex parsing when the modern JSON gate blob is absent; DBVersion /
// BinaryVersion / Options come from the JSON blob when bd emits one. Any
// field may be empty/zero when it cannot be parsed.
type SchemaSkewDetails struct {
	DBPath        string             `json:"db_path,omitempty"`
	DBVersion     int                `json:"db_version,omitempty"`
	BinaryVersion int                `json:"binary_version,omitempty"`
	Options       []SchemaSkewOption `json:"options,omitempty"`
}

// SchemaSkewInfo extracts the structured details of a schema-skew failure.
// It first tries to locate and decode bd's remote_migrate_gate JSON blob in
// stderr, then falls back to legacy regex parsing for the "failed to open
// routed store at ..." message. Returns a zero-value struct when nothing can
// be parsed; callers should still guard on IsSchemaSkew before calling.
func SchemaSkewInfo(err error) SchemaSkewDetails {
	var out SchemaSkewDetails
	stderr := StderrOf(err)
	if stderr == "" {
		return out
	}
	if blob, ok := findJSONBlob(stderr, "remote_migrate_gate"); ok {
		parseGateJSON(blob, &out)
	}
	if out.DBPath == "" {
		out.DBPath = legacySchemaSkewDBPath(stderr)
	}
	if out.DBVersion == 0 || out.BinaryVersion == 0 {
		if db, bin, ok := parseVersionsFromText(stderr); ok {
			if out.DBVersion == 0 {
				out.DBVersion = db
			}
			if out.BinaryVersion == 0 {
				out.BinaryVersion = bin
			}
		}
	}
	if out.DBVersion == 0 || out.BinaryVersion == 0 {
		if db, bin, ok := parseVersionsFromArrow(stderr); ok {
			if out.DBVersion == 0 {
				out.DBVersion = db
			}
			if out.BinaryVersion == 0 {
				out.BinaryVersion = bin
			}
		}
	}
	return out
}

// SchemaSkewDBPath is a compatibility wrapper around SchemaSkewInfo that
// returns only the parsed database path. Kept for existing callers.
func SchemaSkewDBPath(err error) string {
	return SchemaSkewInfo(err).DBPath
}

// legacySchemaSkewDBPath parses the DB path from the legacy stderr shape
// (e.g. "failed to open routed store at /path: schema version mismatch").
// Returns "" when the marker is absent.
func legacySchemaSkewDBPath(stderr string) string {
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

// findJSONBlob locates a JSON object in text that contains the given key,
// returning the raw bytes of the outermost object surrounding it. Brace
// tracking is intentionally naïve: bd's gate blobs are single-line JSON with
// no embedded string braces, so a stack-free depth counter is sufficient.
func findJSONBlob(text, key string) ([]byte, bool) {
	kidx := strings.Index(text, key)
	if kidx < 0 {
		return nil, false
	}
	start := strings.LastIndex(text[:kidx], "{")
	if start < 0 {
		return nil, false
	}
	depth := 0
	for i := start; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return []byte(text[start : i+1]), true
			}
		}
	}
	return nil, false
}

// parseGateJSON decodes bd's remote_migrate_gate blob into the details
// struct. The blob's exact shape is not tightly specified by bd, so parsing
// is defensive: unknown/missing fields silently leave the corresponding
// details entries empty. Recognised shapes (all optional):
//
//	{"db_path":"...","db_version":49,"binary_version":53,
//	 "remote_migrate_gate":{"options":[{"mode":"migrate","description":"...","command":"..."}]}}
//
// Some emitters put the fields at the top level; others nest them under
// remote_migrate_gate. Both are accepted.
func parseGateJSON(blob []byte, out *SchemaSkewDetails) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(blob, &raw); err != nil {
		return
	}
	applyGateFields(raw, out)
	if gate, ok := raw["remote_migrate_gate"]; ok {
		var nested map[string]json.RawMessage
		if json.Unmarshal(gate, &nested) == nil {
			applyGateFields(nested, out)
		}
	}
}

// applyGateFields folds a decoded JSON object's known keys into out. It is
// called for both the outer envelope and the inner remote_migrate_gate slot
// so either layout populates the same details struct.
func applyGateFields(obj map[string]json.RawMessage, out *SchemaSkewDetails) {
	for k, raw := range obj {
		switch strings.ToLower(k) {
		case "db_path", "database_path", "path":
			if out.DBPath == "" {
				var s string
				if json.Unmarshal(raw, &s) == nil {
					out.DBPath = strings.TrimSpace(s)
				}
			}
		case "db_version", "database_version", "from_version":
			if out.DBVersion == 0 {
				var n int
				if json.Unmarshal(raw, &n) == nil {
					out.DBVersion = n
				}
			}
		case "binary_version", "to_version", "expected_version":
			if out.BinaryVersion == 0 {
				var n int
				if json.Unmarshal(raw, &n) == nil {
					out.BinaryVersion = n
				}
			}
		case "options":
			if len(out.Options) == 0 {
				var opts []SchemaSkewOption
				if json.Unmarshal(raw, &opts) == nil {
					out.Options = opts
				}
			}
		}
	}
}

// parseVersionsFromText extracts "database is at vN, binary expects vM" from
// legacy stderr text, returning (db, binary, true) on match.
func parseVersionsFromText(stderr string) (int, int, bool) {
	const dbMarker = "database is at v"
	const binMarker = "binary expects v"
	dbIdx := strings.Index(stderr, dbMarker)
	binIdx := strings.Index(stderr, binMarker)
	if dbIdx < 0 || binIdx < 0 {
		return 0, 0, false
	}
	db := parseTrailingInt(stderr[dbIdx+len(dbMarker):])
	bin := parseTrailingInt(stderr[binIdx+len(binMarker):])
	if db == 0 || bin == 0 {
		return 0, 0, false
	}
	return db, bin, true
}

// schemaSkewArrowVersionsRe matches bd 1.1.2's inline version shorthand, e.g.
// "... a remote-backed database (v49 -> v53): ...", where no separate
// "database is at vN" / "binary expects vM" text is emitted.
var schemaSkewArrowVersionsRe = regexp.MustCompile(`\(v(\d+)\s*->\s*v(\d+)\)`)

// parseVersionsFromArrow extracts "(vN -> vM)" from bd 1.1.2's flat stderr
// shape (see schemaSkewArrowVersionsRe), returning (db, binary, true) on
// match.
func parseVersionsFromArrow(stderr string) (int, int, bool) {
	m := schemaSkewArrowVersionsRe.FindStringSubmatch(stderr)
	if m == nil {
		return 0, 0, false
	}
	db := parseTrailingInt(m[1])
	bin := parseTrailingInt(m[2])
	if db == 0 || bin == 0 {
		return 0, 0, false
	}
	return db, bin, true
}

// parseTrailingInt reads a leading run of ASCII digits from s and returns
// the integer, or 0 when the input does not start with a digit.
func parseTrailingInt(s string) int {
	n := 0
	found := false
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
		found = true
	}
	if !found {
		return 0
	}
	return n
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
	Ready(ctx context.Context, dir string) ([]byte, error)
	Status(ctx context.Context, dir string) ([]byte, error)
	Show(ctx context.Context, dir, id string) ([]byte, error)
	// Statuses returns a map of id -> current bd status for each of the given
	// ids. Missing ids are simply absent from the result (not an error). It is
	// used by mitto_conversation_wait's beads_issues_reached_state branch as a
	// batched fast-path check that avoids one bd invocation per id.
	Statuses(ctx context.Context, dir string, ids []string) (map[string]string, error)
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
	// MigrateRemote applies pending schema migrations to a remote-backed
	// database, bypassing bd's remote-migrate safety gate for this invocation
	// (BD_ALLOW_REMOTE_MIGRATE=1). It runs "bd migrate schema" and then
	// "bd dolt push" to publish the reconciled schema. The returned bytes are
	// bd's stdout (bd migrate --json), included in the API response so the
	// caller can surface concrete migration details.
	MigrateRemote(ctx context.Context, dir string) ([]byte, error)
	// Bootstrap runs "bd bootstrap --non-interactive" on this clone, adopting
	// a schema that another clone has already migrated. Used when this clone
	// is NOT the designated migrator and must catch up without forking.
	Bootstrap(ctx context.Context, dir string) ([]byte, error)
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
