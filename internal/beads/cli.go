package beads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/inercia/mitto/internal/bdexec"
)

const (
	defaultTimeout = 15 * time.Second
	initTimeout    = 60 * time.Second
	syncTimeout    = 120 * time.Second
	// ReadTimeout bounds read-only bd invocations. It is larger than
	// defaultTimeout because read-heavy queries (list, status, label list-all)
	// on a warm-cold dolt DB can occasionally exceed the write-path budget
	// without indicating a real failure. Exported (mitto-f8zx) so callers
	// with their own polling cadence around a runJSONRead-based call (e.g.
	// mcpserver's beads-wait loop) can size their poll interval strictly
	// greater than this deadline instead of hand-copying the value, which
	// previously let the two silently invert (readTimeout 45s > a 30s poll
	// interval elsewhere caused back-to-back subprocess spawns).
	ReadTimeout = 45 * time.Second
	// readTimeout is a package-local alias so existing call sites below need
	// no further changes.
	readTimeout = ReadTimeout
	// LabelsReadTimeout bounds label suggestions well below the generic read
	// budget. The endpoint is cosmetic and must not hold up all beads traffic
	// when bd is contended (mitto-i2ep).
	LabelsReadTimeout = 4 * time.Second
)

// Runner executes a bd subcommand in a directory. The returned error is the
// raw exec error; the caller is responsible for mapping it to a *CmdError.
type Runner interface {
	Run(ctx context.Context, dir string, args ...string) (stdout []byte, stderr string, err error)
	// RunWithEnv is like Run but also injects the given "KEY=VALUE" entries
	// into the subprocess environment (on top of the runner's default env).
	// Duplicate keys let the extra entries win. Used by the schema-migration
	// path to set BD_ALLOW_REMOTE_MIGRATE=1 for a single invocation without
	// affecting the parent process or other bd calls.
	RunWithEnv(ctx context.Context, dir string, extraEnv []string, args ...string) (stdout []byte, stderr string, err error)
}

// limitedRunner applies the process-wide bd concurrency bound around any
// Runner, including test doubles and web-layer wrappers (mitto-i2ep).
type limitedRunner struct {
	inner Runner
}

func (r limitedRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, string, error) {
	release, err := bdexec.Acquire(ctx)
	if err != nil {
		return nil, "", err
	}
	defer release()
	return r.inner.Run(ctx, dir, args...)
}

func (r limitedRunner) RunWithEnv(ctx context.Context, dir string, extraEnv []string, args ...string) ([]byte, string, error) {
	release, err := bdexec.Acquire(ctx)
	if err != nil {
		return nil, "", err
	}
	defer release()
	return r.inner.RunWithEnv(ctx, dir, extraEnv, args...)
}

// execRunner is the default Runner that invokes the real bd binary. When actor
// is non-empty it is exported to the bd subprocess as BEADS_ACTOR, which bd uses
// as the default --actor for its audit trail. An empty actor leaves the
// subprocess environment untouched (bd falls back to git user.name / $USER).
type execRunner struct {
	actor string
}

func (r execRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, string, error) {
	return r.RunWithEnv(ctx, dir, nil, args...)
}

func (r execRunner) RunWithEnv(ctx context.Context, dir string, extraEnv []string, args ...string) ([]byte, string, error) {
	cmd := exec.CommandContext(ctx, "bd", args...)
	cmd.Dir = dir
	// Bound Wait's post-cancellation process/pipe cleanup so a timed-out cache
	// fill returns within CacheFillMaxElapsed rather than consuming its caller's
	// remaining HTTP deadline (mitto-b4zs).
	cmd.WaitDelay = commandCleanupTimeout
	if r.actor != "" || len(extraEnv) > 0 {
		cmd.Env = envWithActor(r.actor)
		if len(extraEnv) > 0 {
			cmd.Env = append(cmd.Env, extraEnv...)
		}
	}

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
		// Preserve the original error (in particular *exec.ExitError) via %w so
		// callers using errors.As can recover the subprocess exit code.
		return stdout.Bytes(), stderr.String(), fmt.Errorf("%s: %w", msg, err)
	}

	return stdout.Bytes(), "", nil
}

// exitCodeFromErr extracts the subprocess exit code from an error chain if it
// wraps an *exec.ExitError, returning 0 when no exit status is available (e.g.
// context cancellation, timeout, or a non-exec error).
func exitCodeFromErr(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 0
}

// maxDiagnosticLen bounds captured bd output logged on failure so a large
// stdout cannot flood the logs.
const maxDiagnosticLen = 2000

// diagnosticOutput returns the best available diagnostic text for a failed bd
// invocation: stderr when bd wrote to it, otherwise stdout (bd sometimes emits
// its error there, or exits non-zero with no stderr during dolt warm-up). The
// result is trimmed and rune-safe length-bounded.
func diagnosticOutput(stderr, stdout string) string {
	diag := strings.TrimSpace(stderr)
	if diag == "" {
		diag = strings.TrimSpace(stdout)
	}
	runes := []rune(diag)
	if len(runes) > maxDiagnosticLen {
		diag = string(runes[:maxDiagnosticLen]) + "… (truncated)"
	}
	return diag
}

// recoverableJSON returns the first candidate that is valid JSON and is not a
// bd machine-readable error object, or nil if none qualifies. Candidates are
// checked in order (stdout preferred, then stderr).
func recoverableJSON(candidates ...[]byte) []byte {
	for _, cand := range candidates {
		trimmed := bytes.TrimSpace(cand)
		if len(trimmed) == 0 || !json.Valid(trimmed) {
			continue
		}
		if isJSONErrorObject(trimmed) {
			continue
		}
		return trimmed
	}
	return nil
}

// isJSONErrorObject reports whether b is a JSON object carrying a top-level
// "error" key, i.e. a bd machine-readable failure rather than a success
// payload. A JSON array (list output) or an object without an "error" key is
// treated as a success payload.
func isJSONErrorObject(b []byte) bool {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil {
		return false
	}
	for k := range obj {
		if strings.EqualFold(k, "error") {
			return true
		}
	}
	return false
}

// isTransientLock reports whether err is a transient dolt/database lock or
// contention failure that is safe to retry for a read-only command.
func isTransientLock(err error) bool {
	s := strings.ToLower(StderrOf(err))
	if s == "" {
		return false
	}
	if strings.Contains(s, "another dolt process") ||
		strings.Contains(s, "database is locked") ||
		strings.Contains(s, "database table is locked") ||
		strings.Contains(s, "resource temporarily unavailable") {
		return true
	}
	return strings.Contains(s, "lock") && (strings.Contains(s, "could not acquire") || strings.Contains(s, "failed to acquire"))
}

// envWithActor returns a copy of the current process environment with any
// existing BEADS_ACTOR entry removed and a single BEADS_ACTOR=actor appended, so
// the bd subprocess is stamped with the given actor regardless of what the
// parent process inherited.
func envWithActor(actor string) []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+1)
	for _, kv := range base {
		if strings.HasPrefix(kv, "BEADS_ACTOR=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "BEADS_ACTOR="+actor)
}

// cliClient implements Client using a Runner.
type cliClient struct {
	runner Runner
}

// runRaw executes bd with the given timeout, wrapping any runner error in *CmdError.
func (c *cliClient) runRaw(ctx context.Context, timeout time.Duration, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	out, stderr, err := c.runner.Run(ctx, dir, args...)
	if err != nil {
		return nil, &CmdError{Err: err, Stderr: diagnosticOutput(stderr, string(out)), ExitCode: exitCodeFromErr(err)}
	}
	return out, nil
}

// runJSONOnceWithTimeout executes bd once with the given timeout and validates
// JSON output. On a non-zero exit it applies JSON recovery: bd can exit
// non-zero while still emitting the intended JSON payload (observed:
// created-issue JSON printed to stderr with a non-zero exit right after a
// restart). If the raw stdout or stderr already contains valid, non-error
// JSON, the call is treated as a success rather than a hard failure.
func (c *cliClient) runJSONOnceWithTimeout(ctx context.Context, dir string, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	out, stderr, err := c.runner.Run(ctx, dir, args...)
	if err != nil {
		if j := recoverableJSON(out, []byte(stderr)); j != nil {
			return j, nil
		}
		return nil, &CmdError{Err: err, Stderr: diagnosticOutput(stderr, string(out)), ExitCode: exitCodeFromErr(err)}
	}
	if !json.Valid(out) {
		return nil, &CmdError{Err: errors.New("bd returned invalid JSON")}
	}
	return out, nil
}

// runJSONOnce is the write-path wrapper that uses defaultTimeout.
func (c *cliClient) runJSONOnce(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return c.runJSONOnceWithTimeout(ctx, dir, defaultTimeout, args...)
}

// runJSON runs a JSON bd command with recovery but NO retry. Some callers
// (Create) are non-idempotent, so a blind retry could duplicate a write; the
// recovery in runJSONOnce already handles the observed non-zero-but-valid-JSON
// case safely.
func (c *cliClient) runJSON(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return c.runJSONOnce(ctx, dir, args...)
}

// runJSONRead is like runJSON but retries ONCE on a transient dolt-lock
// failure. It is safe only for read-only commands (no risk of a duplicate
// write). Reads use readTimeout (larger than defaultTimeout) to absorb
// warm-cold dolt DB latency without SIGKILLing bd. Pass bd's global
// --readonly flag before the subcommand so opening a workspace for polling can
// never auto-apply schema migrations (notably the accidental bd v1.2.1 v65
// migration, which ran even for ordinary list/status commands).
func (c *cliClient) runJSONRead(ctx context.Context, dir string, args ...string) ([]byte, error) {
	readArgs := make([]string, 0, len(args)+1)
	readArgs = append(readArgs, "--readonly")
	readArgs = append(readArgs, args...)
	out, err := c.runJSONOnceWithTimeout(ctx, dir, readTimeout, readArgs...)
	if err != nil && isTransientLock(err) {
		out, err = c.runJSONOnceWithTimeout(ctx, dir, readTimeout, readArgs...)
	}
	return out, err
}

func (c *cliClient) List(ctx context.Context, dir string) ([]byte, error) {
	// An uninitialized folder has no issues yet. Return an empty list rather
	// than letting bd fail, so simply opening the Tasks view does not surface an
	// error (and does not create a .beads database just by viewing).
	if !isInitialized(dir) {
		return []byte("[]"), nil
	}
	return c.runJSONRead(ctx, dir, "list", "--json", "--all", "-n", "0")
}

func (c *cliClient) Ready(ctx context.Context, dir string) ([]byte, error) {
	// An uninitialized folder has no issue database. Return an empty array
	// rather than letting bd fail, so aggregating callers (e.g. the dashboard
	// endpoint) can skip such folders silently instead of surfacing an error.
	if !isInitialized(dir) {
		return []byte("[]"), nil
	}
	return c.runJSONRead(ctx, dir, "ready", "--json", "-n", "0")
}

func (c *cliClient) Status(ctx context.Context, dir string) ([]byte, error) {
	// An uninitialized folder has no issue database. Return an empty summary
	// rather than letting bd fail, so the sidebar stats line renders nothing
	// (and viewing does not create a .beads database just by querying).
	if !isInitialized(dir) {
		return []byte(`{"summary":{}}`), nil
	}
	return c.runJSONRead(ctx, dir, "status", "--json", "--no-activity")
}

func (c *cliClient) Show(ctx context.Context, dir, id string) ([]byte, error) {
	return c.runJSONRead(ctx, dir, "show", id, "--json", "--include-comments")
}

func (c *cliClient) Create(ctx context.Context, dir string, p CreateParams) ([]byte, error) {
	// Transparently initialize the beads database on first task creation so the
	// user does not have to run "bd init" manually for a new folder.
	if err := c.EnsureInitialized(ctx, dir); err != nil {
		return nil, err
	}

	args := []string{"create", p.Title, "--json"}
	if p.Type != "" {
		args = append(args, "--type", p.Type)
	}
	if p.Priority != nil {
		args = append(args, "--priority", strconv.Itoa(*p.Priority))
	}
	if p.Description != "" {
		args = append(args, "-d", p.Description)
	}
	if p.Parent != "" {
		args = append(args, "--parent", p.Parent)
	}
	if len(p.Deps) > 0 {
		args = append(args, "--deps", strings.Join(p.Deps, ","))
	}
	if p.Assignee != "" {
		args = append(args, "-a", p.Assignee)
	}
	if p.Notes != "" {
		args = append(args, "--notes", p.Notes)
	}
	return c.runJSON(ctx, dir, args...)
}

func (c *cliClient) Delete(ctx context.Context, dir, id string) error {
	_, err := c.runRaw(ctx, defaultTimeout, dir, "delete", id, "--force")
	return err
}

// listItem is the minimal shape needed to collect issue IDs from bd list.
type listItem struct {
	ID string `json:"id"`
}

// cleanupTimeout scales the bulk-delete timeout with the number of closed
// issues being removed. On the Dolt backend each delete rewrites dependency
// links, updates text references in connected issues, and commits, so large
// closed-issue sets routinely take far longer than defaultTimeout. We budget a
// generous per-issue allowance on top of a high floor.
func cleanupTimeout(n int) time.Duration {
	const perIssue = 750 * time.Millisecond
	d := time.Duration(n) * perIssue
	if d < syncTimeout {
		return syncTimeout
	}
	return d
}

func (c *cliClient) ListClosedIDs(ctx context.Context, dir string) ([]string, error) {
	out, err := c.runJSONRead(ctx, dir, "list", "--json", "--status", "closed", "-n", "0")
	if err != nil {
		return nil, err
	}
	var items []listItem
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, &CmdError{Err: errors.New("failed to parse closed issues")}
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		if it.ID != "" {
			ids = append(ids, it.ID)
		}
	}
	return ids, nil
}

// statusItem is the minimal shape needed to read id -> status pairs from
// "bd list --id <csv> --all --json".
type statusItem struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func (c *cliClient) Statuses(ctx context.Context, dir string, ids []string) (map[string]string, error) {
	if len(ids) == 0 {
		return map[string]string{}, nil
	}
	// Batch: one subprocess for all ids via "bd list --id <csv> --all --json".
	csv := strings.Join(ids, ",")
	out, err := c.runJSONRead(ctx, dir, "list", "--id", csv, "--all", "--json")
	if err != nil {
		return nil, err
	}
	var items []statusItem
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, &CmdError{Err: errors.New("failed to parse bd list output")}
	}
	result := make(map[string]string, len(items))
	for _, it := range items {
		if it.ID != "" {
			result[it.ID] = it.Status
		}
	}
	return result, nil
}

func (c *cliClient) DeleteIDs(ctx context.Context, dir string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	args := make([]string, 0, len(ids)+2)
	args = append(args, "delete")
	args = append(args, ids...)
	args = append(args, "--force")
	_, err := c.runRaw(ctx, cleanupTimeout(len(ids)), dir, args...)
	return err
}

func (c *cliClient) SetStatus(ctx context.Context, dir, id, action string) error {
	_, err := c.runRaw(ctx, defaultTimeout, dir, action, id)
	return err
}

func (c *cliClient) Update(ctx context.Context, dir string, p UpdateParams) error {
	args := []string{"update", p.ID}
	if p.Title != nil {
		args = append(args, "--title", *p.Title)
	}
	if p.Type != nil {
		args = append(args, "--type", *p.Type)
	}
	if p.Description != nil {
		args = append(args, "-d", *p.Description)
		if *p.Description == "" {
			args = append(args, "--allow-empty-description")
		}
	}
	if p.Priority != nil {
		args = append(args, "--priority", strconv.Itoa(*p.Priority))
	}
	if p.Assignee != nil {
		args = append(args, "-a", *p.Assignee)
	}
	if p.Notes != nil {
		args = append(args, "--notes", *p.Notes)
	}
	for _, key := range p.UnsetMetadata {
		args = append(args, "--unset-metadata", key)
	}
	_, err := c.runRaw(ctx, defaultTimeout, dir, args...)
	return err
}

// Comment adds a comment to an issue: "bd comment <id> -- <text>". The "--"
// terminator stops flag parsing so comment text beginning with a dash is
// treated as positional text rather than a flag.
func (c *cliClient) Comment(ctx context.Context, dir, id, text string) error {
	_, err := c.runRaw(ctx, defaultTimeout, dir, "comment", id, "--", text)
	return err
}

func (c *cliClient) Dep(ctx context.Context, dir string, p DepParams) error {
	var args []string
	switch p.Action {
	case "add":
		depType := p.Type
		if depType == "" {
			depType = "blocks"
		}
		args = []string{"dep", "add", p.ID, p.DependsOn, "-t", depType}
	case "remove":
		args = []string{"dep", "remove", p.ID, p.DependsOn}
	default:
		return &CmdError{Err: errors.New("invalid dep action: " + p.Action)}
	}
	_, err := c.runRaw(ctx, defaultTimeout, dir, args...)
	return err
}

func (c *cliClient) Label(ctx context.Context, dir string, p LabelParams) error {
	var args []string
	switch p.Action {
	case "add":
		args = []string{"label", "add", p.ID, p.Label}
	case "remove":
		args = []string{"label", "remove", p.ID, p.Label}
	default:
		return &CmdError{Err: errors.New("invalid label action: " + p.Action)}
	}
	_, err := c.runRaw(ctx, defaultTimeout, dir, args...)
	return err
}

// listLabelsItem is the minimal shape needed to aggregate per-label counts
// from "bd list --all --json".
type listLabelsItem struct {
	Labels []string `json:"labels"`
}

// labelCount mirrors the {"label","count"} shape "bd label list-all --json"
// produces, so ListAllLabels's callers (HandleBeadsLabelsAll, the frontend's
// label-suggestion datalist) see an unchanged response format.
type labelCount struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// ListAllLabels returns a list of {"label","count"} objects for every unique
// label in the database. An uninitialized folder has no issue database, so an
// empty JSON array is returned rather than letting bd fail.
//
// This deliberately derives the aggregate from "bd list --all --json" instead
// of running "bd label list-all --json": the latter was measured (mitto-i2ep)
// to take 30-37s on a real-world repo and to hold bd's exclusive Dolt
// noms/LOCK for the full duration, blocking every other concurrent bd
// invocation (show/list/ready/status, and writes) in the same repo. "bd list
// --all --json" returns the same per-issue label data in well under a second
// and was verified to produce byte-identical label/count aggregates.
func (c *cliClient) ListAllLabels(ctx context.Context, dir string) ([]byte, error) {
	if !isInitialized(dir) {
		return []byte("[]"), nil
	}
	labelsCtx, cancel := context.WithTimeout(ctx, LabelsReadTimeout)
	defer cancel()
	out, err := c.runJSONRead(labelsCtx, dir, "list", "--json", "--all", "-n", "0")
	if err != nil {
		if errors.Is(labelsCtx.Err(), context.DeadlineExceeded) {
			return []byte("[]"), nil
		}
		return nil, err
	}
	var items []listLabelsItem
	if jsonErr := json.Unmarshal(out, &items); jsonErr != nil {
		return nil, &CmdError{Err: errors.New("failed to parse bd list output")}
	}
	counts := make(map[string]int)
	order := make([]string, 0)
	for _, it := range items {
		for _, label := range it.Labels {
			if _, seen := counts[label]; !seen {
				order = append(order, label)
			}
			counts[label]++
		}
	}
	sort.Strings(order)
	result := make([]labelCount, 0, len(order))
	for _, label := range order {
		result = append(result, labelCount{Label: label, Count: counts[label]})
	}
	return json.Marshal(result)
}
