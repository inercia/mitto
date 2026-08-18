package beads

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/bdexec"
)

// ---------------------------------------------------------------------------
// Fake runner helpers
// ---------------------------------------------------------------------------

// recordingRunner is a fake Runner that captures calls and returns canned responses.
type recordingRunner struct {
	responses []runnerResp
	calls     []runnerCall
}

type runnerResp struct {
	stdout []byte
	stderr string
	err    error
}

type runnerCall struct {
	dir  string
	args []string
	env  []string
}

type deadlineRunner struct {
	deadline time.Time
	wait     bool
}

type opaqueTimeoutRunner struct {
	calls  atomic.Int32
	stderr string
}

type blockingRunner struct {
	entered chan struct{}
	release <-chan struct{}
	calls   atomic.Int32
}

func (r *blockingRunner) Run(ctx context.Context, _ string, _ ...string) ([]byte, string, error) {
	r.calls.Add(1)
	r.entered <- struct{}{}
	select {
	case <-r.release:
		return []byte("[]"), "", nil
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
}

func (r *blockingRunner) RunWithEnv(ctx context.Context, dir string, _ []string, args ...string) ([]byte, string, error) {
	return r.Run(ctx, dir, args...)
}

func (r *deadlineRunner) Run(ctx context.Context, _ string, _ ...string) ([]byte, string, error) {
	r.deadline, _ = ctx.Deadline()
	if r.wait {
		<-ctx.Done()
		return nil, "", ctx.Err()
	}
	return []byte("[]"), "", nil
}

func (r *deadlineRunner) RunWithEnv(ctx context.Context, dir string, _ []string, args ...string) ([]byte, string, error) {
	return r.Run(ctx, dir, args...)
}

func (r *opaqueTimeoutRunner) Run(ctx context.Context, _ string, _ ...string) ([]byte, string, error) {
	r.calls.Add(1)
	<-ctx.Done()
	return nil, r.stderr, errors.New("signal: killed")
}

func (r *opaqueTimeoutRunner) RunWithEnv(ctx context.Context, dir string, _ []string, args ...string) ([]byte, string, error) {
	return r.Run(ctx, dir, args...)
}

func (r *recordingRunner) Run(_ context.Context, dir string, args ...string) ([]byte, string, error) {
	r.calls = append(r.calls, runnerCall{dir: dir, args: args})
	if len(r.responses) == 0 {
		return nil, "", nil
	}
	resp := r.responses[0]
	r.responses = r.responses[1:]
	return resp.stdout, resp.stderr, resp.err
}

func (r *recordingRunner) RunWithEnv(_ context.Context, dir string, extraEnv []string, args ...string) ([]byte, string, error) {
	r.calls = append(r.calls, runnerCall{dir: dir, args: args, env: extraEnv})
	if len(r.responses) == 0 {
		return nil, "", nil
	}
	resp := r.responses[0]
	r.responses = r.responses[1:]
	return resp.stdout, resp.stderr, resp.err
}

func newClient(r *recordingRunner) *cliClient {
	return NewClientWithRunner(r).(*cliClient)
}

// initializedDir returns a temp dir that already contains .beads/config.yaml so
// isInitialized(dir) reports true and EnsureInitialized is a no-op. Use this for
// tests that exercise commands which auto-initialize (List, Create) but want to
// observe the underlying bd call rather than an init.
func initializedDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"), []byte("database: beads\n"), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}
	return dir
}

// ---------------------------------------------------------------------------
// CmdError / StderrOf
// ---------------------------------------------------------------------------

func TestCmdError_StderrOf(t *testing.T) {
	base := errors.New("bd exited with non-zero status")
	ce := &CmdError{Err: base, Stderr: "some stderr output"}

	if ce.Error() != base.Error() {
		t.Errorf("Error() = %q, want %q", ce.Error(), base.Error())
	}
	if ce.Unwrap() != base {
		t.Error("Unwrap() should return the underlying error")
	}
	if got := StderrOf(ce); got != "some stderr output" {
		t.Errorf("StderrOf = %q, want %q", got, "some stderr output")
	}
	// Non-CmdError returns empty string.
	if got := StderrOf(errors.New("plain")); got != "" {
		t.Errorf("StderrOf(plain) = %q, want empty", got)
	}
}

func TestIsNotFound(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"missing issue", &CmdError{Err: errors.New("bd exited with non-zero status"),
			Stderr: `Error fetching mitto-cam: no issue found matching "mitto-cam"`}, true},
		{"mixed case", &CmdError{Stderr: `No Issue Found Matching "x"`}, true},
		{"other bd failure", &CmdError{Stderr: "database is locked"}, false},
		{"empty stderr", &CmdError{Stderr: ""}, false},
		{"plain error", errors.New("no issue found matching"), false},
		// Newer bd versions emit a JSON error object with the plural form on
		// stdout (captured into Stderr by diagnosticOutput when the real
		// stderr is empty). Both plural and singular variants must be treated
		// as not-found.
		{"plural JSON error object", &CmdError{Stderr: `{"error":"no issues found matching the provided IDs","schema_version":1}`}, true},
		{"singular JSON error object", &CmdError{Stderr: `{"error":"no issue found matching \"mitto-cam\""}`}, true},
		{"unrelated JSON error object", &CmdError{Stderr: `{"error":"database is locked"}`}, false},
		{"malformed JSON", &CmdError{Stderr: `{"error":`}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsNotFound(tc.err); got != tc.want {
				t.Errorf("IsNotFound = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExitCodeOf(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"non-CmdError", errors.New("plain"), 0},
		{"CmdError with exit code", &CmdError{Err: errors.New("bd exited with non-zero status"), ExitCode: 2}, 2},
		{"CmdError with zero exit code (timeout)", &CmdError{Err: errors.New("bd command timed out")}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExitCodeOf(tc.err); got != tc.want {
				t.Errorf("ExitCodeOf = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestIsSchemaSkew(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"real v49->v53 stderr", &CmdError{Err: errors.New("bd exited with non-zero status"),
			Stderr: "... refusing to auto-apply 4 pending schema migrations to a remote-backed database (v49 -> v53) ...\n" +
				"Error: failed to open routed store at /Users/alvaro/.beads-planning: schema version mismatch: database is at v49, binary expects v53 ..."}, true},
		{"schema version mismatch only", &CmdError{Stderr: "schema version mismatch: database is at v1, binary expects v2"}, true},
		{"not found", &CmdError{Stderr: `no issue found matching "mitto-cam"`}, false},
		{"schema migration without remote-backed", &CmdError{Stderr: "pending schema migrations detected"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsSchemaSkew(tc.err); got != tc.want {
				t.Errorf("IsSchemaSkew = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSchemaSkewDBPath(t *testing.T) {
	realStderr := "... refusing to auto-apply 4 pending schema migrations to a remote-backed database (v49 -> v53) ...\n" +
		"Error: failed to open routed store at /Users/alvaro/.beads-planning: schema version mismatch: database is at v49, binary expects v53 ..."
	err := &CmdError{Err: errors.New("bd exited with non-zero status"), Stderr: realStderr}
	if got, want := SchemaSkewDBPath(err), "/Users/alvaro/.beads-planning"; got != want {
		t.Errorf("SchemaSkewDBPath = %q, want %q", got, want)
	}

	noPath := &CmdError{Stderr: "schema version mismatch: database is at v1, binary expects v2"}
	if got := SchemaSkewDBPath(noPath); got != "" {
		t.Errorf("SchemaSkewDBPath(no path) = %q, want empty", got)
	}
}

// TestSchemaSkewInfo_LegacyStderr verifies that the details struct is
// populated from the legacy "failed to open routed store at ..." stderr shape:
// DBPath from the marker, DBVersion / BinaryVersion from the version phrase,
// and no options (that data lives only in the JSON blob variant).
func TestSchemaSkewInfo_LegacyStderr(t *testing.T) {
	realStderr := "... refusing to auto-apply 4 pending schema migrations to a remote-backed database (v49 -> v53) ...\n" +
		"Error: failed to open routed store at /Users/alvaro/.beads-planning: schema version mismatch: database is at v49, binary expects v53 ..."
	info := SchemaSkewInfo(&CmdError{Err: errors.New("bd exited with non-zero status"), Stderr: realStderr})
	if info.DBPath != "/Users/alvaro/.beads-planning" {
		t.Errorf("DBPath = %q, want /Users/alvaro/.beads-planning", info.DBPath)
	}
	if info.DBVersion != 49 {
		t.Errorf("DBVersion = %d, want 49", info.DBVersion)
	}
	if info.BinaryVersion != 53 {
		t.Errorf("BinaryVersion = %d, want 53", info.BinaryVersion)
	}
	if len(info.Options) != 0 {
		t.Errorf("Options = %+v, want none for legacy stderr", info.Options)
	}
}

// TestSchemaSkewInfo_JSONBlob verifies that a modern bd emitter carrying a
// remote_migrate_gate JSON blob populates DBPath, versions, AND options.
// The blob is embedded in surrounding stderr noise to match real-world
// behaviour where bd prefixes the JSON with human-readable log lines.
func TestSchemaSkewInfo_JSONBlob(t *testing.T) {
	stderr := "Error: bd cannot open remote-backed store: " +
		`{"db_path":"/opt/beads","db_version":49,"binary_version":53,` +
		`"remote_migrate_gate":{"options":[` +
		`{"mode":"migrate","description":"Apply pending migrations on this clone","command":"BD_ALLOW_REMOTE_MIGRATE=1 bd migrate schema"},` +
		`{"mode":"adopt","description":"Adopt an already-migrated schema from another clone","command":"bd bootstrap"}` +
		`]}}` +
		"\nsee docs for details"
	info := SchemaSkewInfo(&CmdError{Err: errors.New("bd exited with non-zero status"), Stderr: stderr})
	if info.DBPath != "/opt/beads" {
		t.Errorf("DBPath = %q, want /opt/beads", info.DBPath)
	}
	if info.DBVersion != 49 || info.BinaryVersion != 53 {
		t.Errorf("versions = (%d, %d), want (49, 53)", info.DBVersion, info.BinaryVersion)
	}
	if len(info.Options) != 2 {
		t.Fatalf("Options len = %d, want 2 (%+v)", len(info.Options), info.Options)
	}
	if info.Options[0].Mode != "migrate" || info.Options[0].Command == "" {
		t.Errorf("Options[0] = %+v, want mode=migrate with non-empty command", info.Options[0])
	}
	if info.Options[1].Mode != "adopt" {
		t.Errorf("Options[1] = %+v, want mode=adopt", info.Options[1])
	}
}

// TestSchemaSkewInfo_JSONBlobLegacyFallback verifies that when the JSON blob
// omits db_path but a legacy "failed to open routed store at ..." line is
// present alongside it, the parser falls back to legacy regex parsing for
// DBPath while still using JSON for versions/options.
func TestSchemaSkewInfo_JSONBlobLegacyFallback(t *testing.T) {
	stderr := "Error: failed to open routed store at /var/beads-x: schema version mismatch\n" +
		`{"remote_migrate_gate":{"options":[{"mode":"migrate"}]}}`
	info := SchemaSkewInfo(&CmdError{Err: errors.New("bd exited with non-zero status"), Stderr: stderr})
	if info.DBPath != "/var/beads-x" {
		t.Errorf("DBPath = %q, want /var/beads-x", info.DBPath)
	}
	if len(info.Options) != 1 || info.Options[0].Mode != "migrate" {
		t.Errorf("Options = %+v, want single migrate option", info.Options)
	}
}

// TestSchemaSkewInfo_FlatErrorHintShape verifies that bd 1.1.2's flat
// {"error","hint"} stderr shape (no remote_migrate_gate key, no legacy
// "database is at vN" / "binary expects vM" text; versions are embedded
// inline as "(v49 -> v53)") still yields the parsed DB/binary versions
// instead of the zeroed-out details reported in mitto-292. The exact stderr
// below is the one captured from the mitto-292 log line that showed
// db_version=0 binary_version=0 despite IsSchemaSkew correctly returning
// true (verified via the investigate-phase probe on this bead).
func TestSchemaSkewInfo_FlatErrorHintShape(t *testing.T) {
	stderr := `{"error": "refusing to auto-apply 4 pending schema migrations to a remote-backed database (v49 -> v53): migrating clones independently forks the schema (#4259)", "hint": "run BD_ALLOW_REMOTE_MIGRATE=1 bd migrate on the designated migrator clone"}`
	err := &CmdError{Err: errors.New("bd exited with non-zero status: exit status 1"), Stderr: stderr, ExitCode: 1}

	if !IsSchemaSkew(err) {
		t.Fatal("IsSchemaSkew = false, want true for bd 1.1.2's flat error/hint shape")
	}

	info := SchemaSkewInfo(err)
	if info.DBVersion != 49 {
		t.Errorf("DBVersion = %d, want 49 (mitto-292: currently parses as 0)", info.DBVersion)
	}
	if info.BinaryVersion != 53 {
		t.Errorf("BinaryVersion = %d, want 53 (mitto-292: currently parses as 0)", info.BinaryVersion)
	}
}

// TestSchemaSkewInfo_BD112GateBlob reproduces mitto-iwe1: the real bd 1.1.2
// remote_migrate_gate blob (captured verbatim from a mitto.log WARN "beads
// schema needs migration" line) currently yields DBVersion=0,
// BinaryVersion=0, and two all-empty Options entries instead of the parsed
// values, even though IsSchemaSkew correctly detects the failure and the
// outer JSON envelope decodes without error. Three independent shape
// mismatches (see Investigation comment on mitto-iwe1) cause this:
//  1. bd 1.1.2 emits current_version/latest_version, not
//     db_version/binary_version (or any other alias applyGateFields
//     recognizes) -> versions stay 0.
//  2. bd JSON-escapes ">" in the error string, so the arrow fallback regex
//     (which expects a literal "->") never matches "(v49 -\u003e v53)".
//  3. bd's option objects use {id, when, risk, commands}, not
//     {mode, description, command} -> json.Unmarshal succeeds but produces
//     len(Options)==2 with every field empty, which is worse than omitting
//     them (the UI would render two blank remediation buttons).
func TestSchemaSkewInfo_BD112GateBlob(t *testing.T) {
	// Verbatim stderr blob from bd 1.1.2 (mitto.log, 2026-08-10).
	stderr := `{
  "error": "refusing to auto-apply 4 pending schema migrations to a remote-backed database (v49 -\u003e v53): migrating clones independently forks the schema (#4259)",
  "hint": "Coordination decision required: only ONE clone may migrate a shared remote; a second clone migrating independently forks the schema unrecoverably (#4259). Do NOT auto-run a migration — surface remote_migrate_gate.options to the operator and let them choose.",
  "remote_migrate_gate": {
    "current_version": 49,
    "docs": "https://github.com/gastownhall/beads/blob/main/website/docs/getting-started/upgrading.md#remote-backed-databases-and-multiple-clones",
    "expected": "exactly one designated clone migrates and publishes; every other clone adopts the result",
    "human_decision_required": true,
    "latest_version": 53,
    "observed": "4 pending schema migration(s) and a configured remote",
    "options": [
      {
        "commands": [
          "BD_ALLOW_REMOTE_MIGRATE=1 bd migrate",
          "bd dolt push"
        ],
        "id": "migrate",
        "risk": "if another clone also migrates independently, the schema forks unrecoverably (#4259)",
        "when": "you are the single designated migrator (only ONE machine, confirmed with the operator) and no other clone has migrated yet"
      },
      {
        "commands": [
          "bd bootstrap"
        ],
        "id": "adopt",
        "risk": "re-clones and replaces the local database; push or export unpushed work first or it is lost",
        "when": "another machine has already migrated and pushed"
      }
    ],
    "pending": 4,
    "severity": "blocking"
  },
  "schema_version": 1
}`
	err := &CmdError{Err: errors.New("bd exited with non-zero status: exit status 1"), Stderr: stderr, ExitCode: 1}

	if !IsSchemaSkew(err) {
		t.Fatal("IsSchemaSkew = false, want true for the bd 1.1.2 remote_migrate_gate blob")
	}

	info := SchemaSkewInfo(err)
	if info.DBVersion != 49 {
		t.Errorf("DBVersion = %d, want 49 (mitto-iwe1: current_version/latest_version not recognized)", info.DBVersion)
	}
	if info.BinaryVersion != 53 {
		t.Errorf("BinaryVersion = %d, want 53 (mitto-iwe1: current_version/latest_version not recognized)", info.BinaryVersion)
	}
	if len(info.Options) != 2 {
		t.Fatalf("Options len = %d, want 2 (%+v)", len(info.Options), info.Options)
	}
	if info.Options[0].Mode != "migrate" {
		t.Errorf("Options[0].Mode = %q, want %q (mitto-iwe1: id/when/risk/commands not mapped)", info.Options[0].Mode, "migrate")
	}
	if info.Options[0].Description == "" {
		t.Errorf("Options[0].Description empty, want non-empty (mitto-iwe1: when/risk not mapped to Description)")
	}
	if info.Options[0].Command == "" {
		t.Errorf("Options[0].Command empty, want non-empty (mitto-iwe1: commands[] not mapped to Command)")
	}
	if info.Options[1].Mode != "adopt" {
		t.Errorf("Options[1].Mode = %q, want %q", info.Options[1].Mode, "adopt")
	}
}

// TestSchemaSkewInfo_Empty verifies that a nil error and a *CmdError with
// empty stderr both yield a zero-value SchemaSkewDetails.
func TestSchemaSkewInfo_Empty(t *testing.T) {
	isZero := func(d SchemaSkewDetails) bool {
		return d.DBPath == "" && d.DBVersion == 0 && d.BinaryVersion == 0 && len(d.Options) == 0
	}
	if got := SchemaSkewInfo(nil); !isZero(got) {
		t.Errorf("SchemaSkewInfo(nil) = %+v, want zero", got)
	}
	if got := SchemaSkewInfo(&CmdError{Stderr: ""}); !isZero(got) {
		t.Errorf("SchemaSkewInfo(empty stderr) = %+v, want zero", got)
	}
}

// TestIsSchemaSkew_JSONGate verifies that the modern JSON-blob variant is
// detected even when the legacy "schema version mismatch" phrase is absent.
func TestIsSchemaSkew_JSONGate(t *testing.T) {
	err := &CmdError{
		Err:    errors.New("bd exited with non-zero status"),
		Stderr: `Error: {"remote_migrate_gate":{"options":[{"mode":"migrate"}]}}`,
	}
	if !IsSchemaSkew(err) {
		t.Errorf("IsSchemaSkew(JSON gate blob) = false, want true")
	}
}

// ---------------------------------------------------------------------------
// Validators
// ---------------------------------------------------------------------------

func TestIsValidConfigKey(t *testing.T) {
	valid := []string{"jira.url", "github.repo", "custom.my_key", "issue_prefix", "a-b.c_d"}
	for _, k := range valid {
		if !IsValidConfigKey(k) {
			t.Errorf("IsValidConfigKey(%q) = false, want true", k)
		}
	}
	invalid := []string{"", "--force", "-x", "has space", "weird;key", "a/b"}
	for _, k := range invalid {
		if IsValidConfigKey(k) {
			t.Errorf("IsValidConfigKey(%q) = true, want false", k)
		}
	}
}

func TestIsValidUpstream(t *testing.T) {
	for _, u := range []string{"none", "jira", "github", "gitlab", "linear", "prompts"} {
		if !IsValidUpstream(u) {
			t.Errorf("IsValidUpstream(%q) = false, want true", u)
		}
	}
	for _, u := range []string{"", "trello", "asana", "JIRA"} {
		if IsValidUpstream(u) {
			t.Errorf("IsValidUpstream(%q) = true, want false", u)
		}
	}
}

func TestIsValidDepType(t *testing.T) {
	valid := []string{"blocks", "tracks", "related", "parent-child", "discovered-from",
		"until", "caused-by", "validates", "relates-to", "supersedes"}
	for _, tp := range valid {
		if !IsValidDepType(tp) {
			t.Errorf("IsValidDepType(%q) = false, want true", tp)
		}
	}
	for _, tp := range []string{"", "bogus", "BLOCKS", "dependency"} {
		if IsValidDepType(tp) {
			t.Errorf("IsValidDepType(%q) = true, want false", tp)
		}
	}
}

// ---------------------------------------------------------------------------
// Create arg construction
// ---------------------------------------------------------------------------

func TestClient_Create_ArgsMinimal(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{{stdout: []byte(`{}`)}}}
	c := newClient(r)
	_, _ = c.Create(context.Background(), initializedDir(t), CreateParams{Title: "My title"})
	if len(r.calls) == 0 {
		t.Fatal("expected a runner call")
	}
	args := r.calls[0].args
	if args[0] != "create" || args[1] != "My title" || args[2] != "--json" {
		t.Errorf("unexpected args: %v", args)
	}
	if len(args) != 3 {
		t.Errorf("expected 3 args, got %v", args)
	}
}

func TestClient_Create_ArgsWithTypeAndPriority(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{{stdout: []byte(`{}`)}}}
	c := newClient(r)
	prio := 2
	_, _ = c.Create(context.Background(), initializedDir(t), CreateParams{
		Title:       "T",
		Type:        "bug",
		Priority:    &prio,
		Description: "desc",
	})
	args := r.calls[0].args
	joined := strings.Join(args, " ")
	for _, want := range []string{"--type bug", "--priority 2", "-d desc"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %v missing %q", args, want)
		}
	}
}

func TestClient_Create_ArgsWithDepsAssigneeNotes(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{{stdout: []byte(`{}`)}}}
	c := newClient(r)
	_, _ = c.Create(context.Background(), initializedDir(t), CreateParams{
		Title:    "T",
		Deps:     []string{"blocks:mitto-1", "related:mitto-2"},
		Assignee: "alice",
		Notes:    "some notes",
	})
	args := r.calls[0].args
	joined := strings.Join(args, " ")
	for _, want := range []string{"--deps blocks:mitto-1,related:mitto-2", "-a alice", "--notes some notes"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %v missing %q", args, want)
		}
	}
}

func TestClient_Create_NoDepsArgsWhenEmpty(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{{stdout: []byte(`{}`)}}}
	c := newClient(r)
	_, _ = c.Create(context.Background(), initializedDir(t), CreateParams{Title: "T"})
	joined := strings.Join(r.calls[0].args, " ")
	for _, flag := range []string{"--deps", "-a", "--notes"} {
		if strings.Contains(joined, flag) {
			t.Errorf("args should not contain %q when fields are empty: %v", flag, r.calls[0].args)
		}
	}
}

// TestClient_List_NotInitialized_ReturnsEmpty verifies that listing an
// uninitialized folder returns an empty JSON array without invoking bd (so the
// Tasks view shows "No issues found" instead of an error, and viewing does not
// create a .beads database).
func TestClient_List_NotInitialized_ReturnsEmpty(t *testing.T) {
	r := &recordingRunner{}
	c := newClient(r)
	out, err := c.List(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if string(out) != "[]" {
		t.Errorf("List() = %q, want %q", out, "[]")
	}
	if len(r.calls) != 0 {
		t.Errorf("expected 0 runner calls (not initialized), got %d", len(r.calls))
	}
}

// TestClient_SameDatabaseSerializes reproduces mitto-i2ep's post-limiter
// recurrence: two globally-allowed bd processes still contend when they resolve
// to the same embedded Dolt database.
func TestClient_SameDatabaseSerializes(t *testing.T) {
	runner := &blockingRunner{
		entered: make(chan struct{}, 2),
		release: nil,
	}
	root := initializedDir(t)
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested workspace: %v", err)
	}

	holderCtx, cancelHolder := context.WithCancel(context.Background())
	defer cancelHolder()
	holderDone := make(chan struct{})
	go func() {
		_, _ = NewClientWithRunner(runner).Show(holderCtx, root, "mitto-held")
		close(holderDone)
	}()
	select {
	case <-runner.entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first bd invocation")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, err := NewClientWithRunner(runner).Show(ctx, nested, "mitto-queued")
		errCh <- err
	}()

	select {
	case <-runner.entered:
		t.Fatal("second bd invocation reached the same database while the first was active")
	case err := <-errCh:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("queued Show() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("same-database Show() did not respect its context deadline")
	}

	cancelHolder()
	select {
	case <-holderDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for holder call to exit")
	}
}

// TestClient_SharedConcurrencyLimit verifies the independent process-wide cap:
// once distinct databases occupy all global slots, another read expires in the
// limiter queue instead of spawning an additional process.
func TestClient_SharedConcurrencyLimit(t *testing.T) {
	release := make(chan struct{})
	runner := &blockingRunner{
		entered: make(chan struct{}, bdexec.MaxConcurrent+1),
		release: release,
	}
	holderCtx, cancelHolders := context.WithCancel(context.Background())
	defer cancelHolders()
	holdersDone := make(chan struct{}, bdexec.MaxConcurrent)

	for range bdexec.MaxConcurrent {
		client := NewClientWithRunner(runner)
		dir := t.TempDir()
		go func() {
			_, _ = client.Show(holderCtx, dir, "mitto-held")
			holdersDone <- struct{}{}
		}()
		select {
		case <-runner.entered:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for bd limiter capacity to fill")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	queuedDir := t.TempDir()
	errCh := make(chan error, 1)
	go func() {
		_, err := NewClientWithRunner(runner).Show(ctx, queuedDir, "mitto-queued")
		errCh <- err
	}()

	select {
	case <-runner.entered:
		t.Fatal("third bd invocation reached the runner while shared capacity was occupied")
	case err := <-errCh:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("queued Show() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued Show() did not respect its context deadline")
	}

	if got := runner.calls.Load(); got != bdexec.MaxConcurrent {
		t.Fatalf("runner calls = %d, want %d; queued call must not spawn bd", got, bdexec.MaxConcurrent)
	}
	cancelHolders()
	for range bdexec.MaxConcurrent {
		select {
		case <-holdersDone:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for holder call to exit")
		}
	}
}

// TestClient_List_DoltBackend_RunsBd verifies that a Dolt-backed database — which
// has .beads/metadata.json but no .beads/config.yaml — is recognized as
// initialized, so List invokes bd instead of short-circuiting to "[]".
func TestClient_List_DoltBackend_RunsBd(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(`{"backend":"dolt"}`), 0o644); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}

	r := &recordingRunner{responses: []runnerResp{{stdout: []byte("[]")}}}
	c := newClient(r)
	if _, err := c.List(context.Background(), dir); err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("expected 1 runner call (initialized via metadata.json), got %d", len(r.calls))
	}
	if got := strings.Join(r.calls[0].args[:2], " "); got != "--readonly list" {
		t.Errorf("expected read-only bd list call, got %q", got)
	}
}

// TestClient_ListAllLabels_DoesNotUseSlowSubcommand is a REPRODUCTION test for
// mitto-i2ep ("bd label list-all takes 30-37s and blocks ALL concurrent bd
// reads").
//
// Root cause under test: cliClient.ListAllLabels (cli.go) shells out to
// "bd label list-all --json", which was measured (investigation comment on
// mitto-i2ep) to take ~30s on a 374-issue Dolt-backed repo — CPU-bound, and
// for the full duration it holds bd's exclusive Dolt "noms/LOCK", blocking
// every other concurrent bd invocation (show/list/ready/status, and writes)
// in the same repo. The same investigation proved "bd list --all --json"
// (already used elsewhere in this file, ~0.8s) yields byte-identical
// {label,count} aggregates.
//
// Expected behavior (post-fix): ListAllLabels must NOT invoke the
// "label list-all" subcommand at all; it should derive the label/count
// aggregate from "bd list --all --json" instead, avoiding the long-held
// exclusive lock.
//
// This test is expected to FAIL against the current implementation (which
// calls "bd label list-all") and to PASS once ListAllLabels is switched to
// derive its result from "bd list --all --json".
func TestClient_ListAllLabels_DoesNotUseSlowSubcommand(t *testing.T) {
	dir := initializedDir(t)
	r := &recordingRunner{responses: []runnerResp{
		{stdout: []byte(`[{"id":"x-1","labels":["a","b"]},{"id":"x-2","labels":["a"]}]`)},
	}}
	c := newClient(r)

	if _, err := c.ListAllLabels(context.Background(), dir); err != nil {
		t.Fatalf("ListAllLabels() error: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("expected exactly 1 runner call, got %d: %+v", len(r.calls), r.calls)
	}
	args := r.calls[0].args
	if len(args) >= 2 && args[0] == "label" && args[1] == "list-all" {
		t.Errorf("ListAllLabels invoked the slow, lock-holding %q subcommand (args=%v); "+
			"it should derive labels from \"bd list --all --json\" instead (mitto-i2ep)", "label list-all", args)
	}
}

func TestClient_ListAllLabels_UsesBoundedDeadline(t *testing.T) {
	r := &deadlineRunner{}
	c := &cliClient{runner: r}

	if _, err := c.ListAllLabels(context.Background(), initializedDir(t)); err != nil {
		t.Fatalf("ListAllLabels() error: %v", err)
	}
	remaining := time.Until(r.deadline)
	if remaining <= 0 || remaining > LabelsReadTimeout {
		t.Fatalf("runner deadline remaining = %v, want within (0, %v]", remaining, LabelsReadTimeout)
	}
	if LabelsReadTimeout >= 5*time.Second {
		t.Fatalf("LabelsReadTimeout = %v, want below 5s endpoint budget", LabelsReadTimeout)
	}
}

func TestClient_ListAllLabels_DeadlineFailsOpen(t *testing.T) {
	r := &deadlineRunner{wait: true}
	c := &cliClient{runner: r}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	out, err := c.ListAllLabels(ctx, initializedDir(t))
	if err != nil {
		t.Fatalf("ListAllLabels() error: %v, want fail-open response", err)
	}
	if string(out) != "[]" {
		t.Fatalf("ListAllLabels() = %q, want empty label list", out)
	}
}

// TestClient_Create_NotInitialized_RunsInitThenCreate verifies that creating a
// task in an uninitialized folder first runs "bd init" and then "bd create".
func TestClient_Create_NotInitialized_RunsInitThenCreate(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{
		{stdout: []byte("")},   // init
		{stdout: []byte(`{}`)}, // create
	}}
	c := newClient(r)
	_, err := c.Create(context.Background(), t.TempDir(), CreateParams{Title: "T"})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("expected 2 runner calls (init + create), got %d: %v", len(r.calls), r.calls)
	}
	if r.calls[0].args[0] != "init" {
		t.Errorf("first call = %v, want init", r.calls[0].args)
	}
	if r.calls[1].args[0] != "create" {
		t.Errorf("second call = %v, want create", r.calls[1].args)
	}
}

// ---------------------------------------------------------------------------
// Update arg construction
// ---------------------------------------------------------------------------

func TestClient_Update_AllowEmptyDescription(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{{stdout: []byte("")}}}
	c := newClient(r)
	emptyDesc := ""
	_ = c.Update(context.Background(), "/dir", UpdateParams{
		ID:          "abc-1",
		Description: &emptyDesc,
	})
	args := r.calls[0].args
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--allow-empty-description") {
		t.Errorf("args %v missing --allow-empty-description", args)
	}
	if !strings.Contains(joined, "-d") {
		t.Errorf("args %v missing -d", args)
	}
}

func TestClient_Update_NonEmptyDescription_NoAllowEmpty(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{{stdout: []byte("")}}}
	c := newClient(r)
	desc := "some desc"
	_ = c.Update(context.Background(), "/dir", UpdateParams{ID: "abc-1", Description: &desc})
	args := r.calls[0].args
	for _, a := range args {
		if a == "--allow-empty-description" {
			t.Errorf("args should not contain --allow-empty-description: %v", args)
		}
	}
}

// ---------------------------------------------------------------------------
// Dep arg construction
// ---------------------------------------------------------------------------

func TestClient_Dep_Add(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{{stdout: []byte("")}}}
	c := newClient(r)
	_ = c.Dep(context.Background(), "/dir", DepParams{
		ID: "abc-1", DependsOn: "abc-2", Type: "tracks", Action: "add",
	})
	args := r.calls[0].args
	joined := strings.Join(args, " ")
	if joined != "dep add abc-1 abc-2 -t tracks" {
		t.Errorf("unexpected args: %q", joined)
	}
}

func TestClient_Dep_AddDefaultType(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{{stdout: []byte("")}}}
	c := newClient(r)
	_ = c.Dep(context.Background(), "/dir", DepParams{
		ID: "abc-1", DependsOn: "abc-2", Action: "add",
	})
	joined := strings.Join(r.calls[0].args, " ")
	if !strings.Contains(joined, "-t blocks") {
		t.Errorf("expected default type blocks in %q", joined)
	}
}

func TestClient_Dep_Remove(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{{stdout: []byte("")}}}
	c := newClient(r)
	_ = c.Dep(context.Background(), "/dir", DepParams{
		ID: "abc-1", DependsOn: "abc-2", Action: "remove",
	})
	joined := strings.Join(r.calls[0].args, " ")
	if joined != "dep remove abc-1 abc-2" {
		t.Errorf("unexpected args: %q", joined)
	}
}

// ---------------------------------------------------------------------------
// SetStatus
// ---------------------------------------------------------------------------

func TestClient_SetStatus_PassesVerb(t *testing.T) {
	for _, action := range []string{"close", "reopen", "defer", "undefer"} {
		r := &recordingRunner{responses: []runnerResp{{stdout: []byte("")}}}
		c := newClient(r)
		_ = c.SetStatus(context.Background(), "/dir", "abc-1", action)
		if r.calls[0].args[0] != action {
			t.Errorf("action %q: first arg = %q, want %q", action, r.calls[0].args[0], action)
		}
	}
}

// ---------------------------------------------------------------------------
// ListClosedIDs / DeleteIDs
// ---------------------------------------------------------------------------

func TestClient_ListClosedIDs_Empty(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{
		{stdout: []byte(`[]`)}, // empty list
	}}
	c := newClient(r)
	ids, err := c.ListClosedIDs(context.Background(), "/dir")
	if err != nil {
		t.Fatalf("ListClosedIDs() error: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("ids = %v, want empty", ids)
	}
	if len(r.calls) != 1 {
		t.Errorf("expected 1 runner call (list only), got %d", len(r.calls))
	}
}

func TestClient_ListClosedIDs_ReturnIDs(t *testing.T) {
	listJSON := `[{"id":"abc-1"},{"id":"abc-2"}]`
	r := &recordingRunner{responses: []runnerResp{
		{stdout: []byte(listJSON)},
	}}
	c := newClient(r)
	ids, err := c.ListClosedIDs(context.Background(), "/dir")
	if err != nil {
		t.Fatalf("ListClosedIDs() error: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %d", len(ids))
	}
	if ids[0] != "abc-1" || ids[1] != "abc-2" {
		t.Errorf("ids = %v, want [abc-1, abc-2]", ids)
	}
}

func TestClient_DeleteIDs_NoOp_WhenEmpty(t *testing.T) {
	r := &recordingRunner{}
	c := newClient(r)
	if err := c.DeleteIDs(context.Background(), "/dir", nil); err != nil {
		t.Fatalf("DeleteIDs(nil) error: %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("expected 0 runner calls, got %d", len(r.calls))
	}
}

func TestClient_DeleteIDs_DeletesWithForce(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{
		{stdout: []byte("")}, // delete call
	}}
	c := newClient(r)
	ids := []string{"abc-1", "abc-2"}
	if err := c.DeleteIDs(context.Background(), "/dir", ids); err != nil {
		t.Fatalf("DeleteIDs() error: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(r.calls))
	}
	joined := strings.Join(r.calls[0].args, " ")
	if !strings.Contains(joined, "--force") {
		t.Errorf("delete args missing --force: %v", r.calls[0].args)
	}
	if !strings.Contains(joined, "abc-1") || !strings.Contains(joined, "abc-2") {
		t.Errorf("delete args missing IDs: %v", r.calls[0].args)
	}
}

func TestCleanupTimeout_ScalesWithCount(t *testing.T) {
	// Small counts use the high floor (syncTimeout), not the old 15s default.
	if got := cleanupTimeout(0); got != syncTimeout {
		t.Errorf("cleanupTimeout(0) = %v, want floor %v", got, syncTimeout)
	}
	if got := cleanupTimeout(10); got != syncTimeout {
		t.Errorf("cleanupTimeout(10) = %v, want floor %v", got, syncTimeout)
	}
	// The floor must comfortably exceed the previous defaultTimeout.
	if syncTimeout <= defaultTimeout {
		t.Errorf("floor %v must exceed old defaultTimeout %v", syncTimeout, defaultTimeout)
	}
	// Large counts scale above the floor and grow monotonically.
	big := cleanupTimeout(1000)
	if big <= syncTimeout {
		t.Errorf("cleanupTimeout(1000) = %v, want > floor %v", big, syncTimeout)
	}
	if cleanupTimeout(2000) <= big {
		t.Errorf("cleanupTimeout must increase with count: 2000 (%v) <= 1000 (%v)", cleanupTimeout(2000), big)
	}
	// 363 closed issues (the case that exceeded the old 15s timeout) must get a
	// budget well beyond the measured bulk-delete duration.
	if got, want := cleanupTimeout(363), 363*750*time.Millisecond; got != want {
		t.Errorf("cleanupTimeout(363) = %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// ConfigShow filtering
// ---------------------------------------------------------------------------

func TestClient_ConfigShow_FiltersToEditableSources(t *testing.T) {
	jsonResp := `[
		{"key":"jira.url","value":"https://j","source":"config.yaml"},
		{"key":"issue_prefix","value":"PROJ","source":"database"},
		{"key":"some.default","value":"v","source":"default"},
		{"key":"meta","value":"x","source":"metadata"}
	]`
	r := &recordingRunner{responses: []runnerResp{{stdout: []byte(jsonResp)}}}
	c := newClient(r)
	result, err := c.ConfigShow(context.Background(), "/dir")
	if err != nil {
		t.Fatalf("ConfigShow() error: %v", err)
	}
	if result["jira.url"] != "https://j" {
		t.Errorf("jira.url missing or wrong: %v", result)
	}
	if result["issue_prefix"] != "PROJ" {
		t.Errorf("issue_prefix missing or wrong: %v", result)
	}
	if _, ok := result["some.default"]; ok {
		t.Errorf("default-source key should be excluded: %v", result)
	}
	if _, ok := result["meta"]; ok {
		t.Errorf("metadata-source key should be excluded: %v", result)
	}
}

// TestClient_ConfigShow_HidesKVNamespace pins the mitto-xdqx fix: kv.* keys
// (populated by `bd remember`, sometimes several KB each) share provenance
// with editable database config but are internal beads state and must not be
// surfaced in the editable-config UI, where rendering them as <input> rows
// froze the Tasks tab.
func TestClient_ConfigShow_HidesKVNamespace(t *testing.T) {
	jsonResp := `[
		{"key":"issue_prefix","value":"PROJ","source":"database"},
		{"key":"kv.memory.some-note","value":"a very long blob","source":"database"},
		{"key":"kv.other.thing","value":"x","source":"database"}
	]`
	r := &recordingRunner{responses: []runnerResp{{stdout: []byte(jsonResp)}}}
	c := newClient(r)
	result, err := c.ConfigShow(context.Background(), "/dir")
	if err != nil {
		t.Fatalf("ConfigShow() error: %v", err)
	}
	if result["issue_prefix"] != "PROJ" {
		t.Errorf("issue_prefix missing or wrong: %v", result)
	}
	if _, ok := result["kv.memory.some-note"]; ok {
		t.Errorf("kv.memory.* key should be excluded: %v", result)
	}
	if _, ok := result["kv.other.thing"]; ok {
		t.Errorf("kv.* key should be excluded: %v", result)
	}
}

// ---------------------------------------------------------------------------
// syncArgs (via Sync)
// ---------------------------------------------------------------------------

func TestSyncArgs(t *testing.T) {
	cases := []struct {
		integration, action string
		want                []string
	}{
		{"jira", "pull", []string{"jira", "sync", "--pull"}},
		{"jira", "push", []string{"jira", "sync", "--push"}},
		{"jira", "sync", []string{"jira", "sync"}},
		{"jira", "status", []string{"jira", "status"}},
		{"github", "pull", []string{"github", "sync", "--pull-only"}},
		{"github", "push", []string{"github", "sync", "--push-only"}},
		{"gitlab", "pull", []string{"gitlab", "sync", "--pull-only"}},
		{"gitlab", "push", []string{"gitlab", "sync", "--push-only"}},
		{"linear", "pull", []string{"linear", "sync", "--pull"}},
		{"linear", "push", []string{"linear", "sync", "--push"}},
		{"linear", "sync", []string{"linear", "sync"}},
		{"linear", "status", []string{"linear", "status"}},
	}
	for _, tc := range cases {
		got, ok := syncArgs(tc.integration, tc.action)
		if !ok {
			t.Errorf("syncArgs(%q,%q) ok=false, want true", tc.integration, tc.action)
			continue
		}
		if strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("syncArgs(%q,%q) = %v, want %v", tc.integration, tc.action, got, tc.want)
		}
	}
	if _, ok := syncArgs("trello", "pull"); ok {
		t.Error("syncArgs(trello,pull) ok=true, want false")
	}
	if _, ok := syncArgs("jira", "frobnicate"); ok {
		t.Error("syncArgs(jira,frobnicate) ok=true, want false")
	}
}

func TestClient_Sync_UnknownIntegrationReturnsError(t *testing.T) {
	r := &recordingRunner{}
	c := newClient(r)
	_, err := c.Sync(context.Background(), "/dir", "trello", "pull")
	if err == nil {
		t.Error("expected error for unknown integration")
	}
}

// ---------------------------------------------------------------------------
// Runner error → *CmdError propagation
// ---------------------------------------------------------------------------

func TestClient_RunnerError_WrappedAsCmdError(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{
		{stderr: "some stderr", err: errors.New("bd exited with non-zero status")},
	}}
	c := newClient(r)
	_, err := c.List(context.Background(), initializedDir(t))
	if err == nil {
		t.Fatal("expected error")
	}
	var ce *CmdError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T, want *CmdError", err)
	}
	if ce.Stderr != "some stderr" {
		t.Errorf("Stderr = %q, want %q", ce.Stderr, "some stderr")
	}
	if StderrOf(err) != "some stderr" {
		t.Errorf("StderrOf = %q, want %q", StderrOf(err), "some stderr")
	}
}

// ---------------------------------------------------------------------------
// runJSON recovery + read retry (mitto-xl0)
// ---------------------------------------------------------------------------

// TestRunJSON_RecoversJSONFromStderr covers the bug that motivated mitto-xl0: bd
// can exit non-zero while still emitting the intended JSON payload (observed on
// Create: the created-issue JSON printed to stderr right after a dolt restart).
// Create must treat that response as a success rather than logging a hard
// "beads command failed".
func TestRunJSON_RecoversJSONFromStderr(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{
		{
			stderr: `[{"id":"mitto-54k.5"}]`,
			err:    errors.New("bd exited with non-zero status"),
		},
	}}
	c := newClient(r)
	out, err := c.Create(context.Background(), initializedDir(t), CreateParams{Title: "T"})
	if err != nil {
		t.Fatalf("Create() error = %v, want nil (JSON on stderr should be recovered)", err)
	}
	if !strings.Contains(string(out), "mitto-54k.5") {
		t.Errorf("recovered bytes = %q, want to contain %q", out, "mitto-54k.5")
	}
}

// TestRunJSON_RecoversJSONFromStdout covers the symmetric case where the JSON
// payload landed on stdout but bd still exited non-zero (e.g. a stderr advisory
// during dolt warm-up).
func TestRunJSON_RecoversJSONFromStdout(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{
		{
			stdout: []byte(`{"id":"mitto-1"}`),
			stderr: "warning: dolt sync advisory",
			err:    errors.New("bd exited with non-zero status"),
		},
	}}
	c := newClient(r)
	out, err := c.Create(context.Background(), initializedDir(t), CreateParams{Title: "T"})
	if err != nil {
		t.Fatalf("Create() error = %v, want nil (JSON on stdout should be recovered)", err)
	}
	if !strings.Contains(string(out), "mitto-1") {
		t.Errorf("recovered bytes = %q, want to contain %q", out, "mitto-1")
	}
}

// TestRunJSON_ErrorObjectNotRecovered ensures that a bd machine-readable error
// JSON payload (top-level "error" key) is NOT treated as a success, even
// though it happens to be valid JSON.
func TestRunJSON_ErrorObjectNotRecovered(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{
		{
			stdout: []byte(`{"error":"boom"}`),
			err:    errors.New("bd exited with non-zero status"),
		},
	}}
	c := newClient(r)
	_, err := c.Create(context.Background(), initializedDir(t), CreateParams{Title: "T"})
	if err == nil {
		t.Fatal("expected error, got nil (JSON error object must not be recovered)")
	}
	var ce *CmdError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T, want *CmdError", err)
	}
}

// TestRunJSONRead_RetriesOnceOnTransientLock verifies that read-only commands
// retry once when the first invocation fails with a transient dolt lock error.
func TestRunJSONRead_RetriesOnceOnTransientLock(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{
		{stderr: "another dolt process is using the database", err: errors.New("bd exited with non-zero status")},
		{stdout: []byte("[]")},
	}}
	c := newClient(r)
	out, err := c.List(context.Background(), initializedDir(t))
	if err != nil {
		t.Fatalf("List() error after retry = %v, want nil", err)
	}
	if string(out) != "[]" {
		t.Errorf("List() = %q, want %q", out, "[]")
	}
	if len(r.calls) != 2 {
		t.Errorf("runner call count = %d, want 2 (initial + one retry)", len(r.calls))
	}
}

func TestRunJSONOnceWithTimeout_PreservesDeadlineForOpaqueRunnerError(t *testing.T) {
	r := &opaqueTimeoutRunner{}
	c := &cliClient{runner: r}

	_, err := c.runJSONOnceWithTimeout(context.Background(), "/dir", 10*time.Millisecond, "status", "--json")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runJSONOnceWithTimeout() error = %v, want context deadline exceeded", err)
	}
	if got := r.calls.Load(); got != 1 {
		t.Fatalf("runner calls = %d, want 1", got)
	}
}

func TestRunJSONRead_DoesNotRetryTimedOutTransientLock(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{
		{
			stderr: "another dolt process is using the database",
			err:    errors.Join(context.DeadlineExceeded, errors.New("signal: killed")),
		},
		{stdout: []byte("[]")},
	}}
	c := &cliClient{runner: r}

	_, err := c.runJSONRead(context.Background(), "/dir", "status", "--json")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runJSONRead() error = %v, want context deadline exceeded", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1; timed-out reads must not retry", len(r.calls))
	}
}

// TestCreate_NotRetriedOnTransientLock verifies that Create — which is
// non-idempotent — is NOT retried even on a transient lock error, to avoid
// duplicating a write if the first attempt actually committed.
func TestCreate_NotRetriedOnTransientLock(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{
		{stderr: "another dolt process is using the database", err: errors.New("bd exited with non-zero status")},
	}}
	c := newClient(r)
	_, err := c.Create(context.Background(), initializedDir(t), CreateParams{Title: "T"})
	if err == nil {
		t.Fatal("expected error (Create must not retry)")
	}
	if len(r.calls) != 1 {
		t.Errorf("runner call count = %d, want 1 (Create must not retry)", len(r.calls))
	}
}

// ---------------------------------------------------------------------------
// EnsureInitialized
// ---------------------------------------------------------------------------

func TestEnsureInitialized_AlreadyInitialized(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"), []byte("database: beads\n"), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}

	r := &recordingRunner{}
	c := newClient(r)
	if err := c.EnsureInitialized(context.Background(), dir); err != nil {
		t.Fatalf("EnsureInitialized() error: %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("expected 0 runner calls (already initialized), got %d", len(r.calls))
	}
}

// ---------------------------------------------------------------------------
// appendGitignorePattern / ensureConfigGitignored
// ---------------------------------------------------------------------------

func skipIfNoGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v\n%s", err, out)
	}
}

func countPatternLines(t *testing.T, path, pattern string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read %s: %v", path, err)
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == pattern {
			n++
		}
	}
	return n
}

func TestEnsureBeadsConfigGitignored_GitRepo(t *testing.T) {
	skipIfNoGit(t)
	dir := t.TempDir()
	gitInit(t, dir)

	if err := ensureConfigGitignored(dir); err != nil {
		t.Fatalf("ensureConfigGitignored() error: %v", err)
	}

	gitignorePath := filepath.Join(dir, ".gitignore")
	if got := countPatternLines(t, gitignorePath, ".beads/config.yaml"); got != 1 {
		t.Fatalf("gitignore pattern count = %d, want 1", got)
	}

	cmd := exec.Command("git", "check-ignore", "-q", "--", filepath.Join(dir, ".beads", "config.yaml"))
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("expected config.yaml to be ignored, check-ignore says not-ignored: %v", err)
	}
}

func TestEnsureBeadsConfigGitignored_NotGitRepo(t *testing.T) {
	dir := t.TempDir()

	if err := ensureConfigGitignored(dir); err != nil {
		t.Fatalf("ensureConfigGitignored() error: %v", err)
	}

	gitignorePath := filepath.Join(dir, ".gitignore")
	if got := countPatternLines(t, gitignorePath, ".beads/config.yaml"); got != 1 {
		t.Fatalf("gitignore pattern count = %d, want 1", got)
	}

	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("expected no .git directory, stat err = %v", err)
	}
}

func TestEnsureBeadsConfigGitignored_Idempotent(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := ensureConfigGitignored(dir); err != nil {
			t.Fatalf("call %d error: %v", i, err)
		}
	}
	gitignorePath := filepath.Join(dir, ".gitignore")
	if got := countPatternLines(t, gitignorePath, ".beads/config.yaml"); got != 1 {
		t.Fatalf("pattern count after repeated calls = %d, want 1", got)
	}
}

func TestEnsureBeadsConfigGitignored_ExistingGitignorePreserved(t *testing.T) {
	dir := t.TempDir()
	gitignorePath := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	if err := ensureConfigGitignored(dir); err != nil {
		t.Fatalf("ensureConfigGitignored() error: %v", err)
	}

	if got := countPatternLines(t, gitignorePath, "node_modules/"); got != 1 {
		t.Fatalf("pre-existing pattern count = %d, want 1 (must be preserved)", got)
	}
	if got := countPatternLines(t, gitignorePath, ".beads/config.yaml"); got != 1 {
		t.Fatalf("config.yaml pattern count = %d, want 1", got)
	}
}

func TestAppendGitignorePattern_NewFileAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")

	if err := appendGitignorePattern(path, "x/y.yaml"); err != nil {
		t.Fatalf("first append error: %v", err)
	}
	if err := appendGitignorePattern(path, "x/y.yaml"); err != nil {
		t.Fatalf("second append error: %v", err)
	}
	if got := countPatternLines(t, path, "x/y.yaml"); got != 1 {
		t.Fatalf("pattern count = %d, want 1", got)
	}
}

func TestAppendGitignorePattern_AppendsNewlineToTruncatedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte("existing-pattern"), 0o644); err != nil {
		t.Fatalf("seed gitignore: %v", err)
	}

	if err := appendGitignorePattern(path, "new-pattern"); err != nil {
		t.Fatalf("append error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read gitignore: %v", err)
	}
	if got := countPatternLines(t, path, "existing-pattern"); got != 1 {
		t.Fatalf("existing-pattern count = %d, want 1 (content: %q)", got, data)
	}
	if got := countPatternLines(t, path, "new-pattern"); got != 1 {
		t.Fatalf("new-pattern count = %d, want 1 (content: %q)", got, data)
	}
}

func TestEnvWithActor_OverridesAndDedupes(t *testing.T) {
	// A stale BEADS_ACTOR inherited from the parent process must be replaced,
	// not duplicated, so the bd subprocess sees exactly our actor.
	t.Setenv("BEADS_ACTOR", "stale:value")

	env := envWithActor("mitto:webui")

	var actors []string
	for _, kv := range env {
		if strings.HasPrefix(kv, "BEADS_ACTOR=") {
			actors = append(actors, strings.TrimPrefix(kv, "BEADS_ACTOR="))
		}
	}
	if len(actors) != 1 {
		t.Fatalf("BEADS_ACTOR entries = %d (%v), want exactly 1", len(actors), actors)
	}
	if actors[0] != "mitto:webui" {
		t.Errorf("BEADS_ACTOR = %q, want %q", actors[0], "mitto:webui")
	}
}

func TestDiagnosticOutput(t *testing.T) {
	longInput := strings.Repeat("x", maxDiagnosticLen+500)
	wantTruncatedLen := maxDiagnosticLen + len([]rune("… (truncated)"))

	tests := []struct {
		name   string
		stderr string
		stdout string
		want   string
	}{
		{
			name:   "stderr non-empty is returned as-is, trimmed",
			stderr: "  boom\n",
			stdout: "ignored",
			want:   "boom",
		},
		{
			name:   "stderr empty falls back to stdout",
			stderr: "",
			stdout: "  warming up\n",
			want:   "warming up",
		},
		{
			name:   "both empty",
			stderr: "",
			stdout: "",
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := diagnosticOutput(tt.stderr, tt.stdout); got != tt.want {
				t.Errorf("diagnosticOutput(%q, %q) = %q, want %q", tt.stderr, tt.stdout, got, tt.want)
			}
		})
	}

	t.Run("over-length input is truncated", func(t *testing.T) {
		got := diagnosticOutput(longInput, "")
		if !strings.HasSuffix(got, "… (truncated)") {
			t.Fatalf("diagnosticOutput() = %q, want suffix %q", got, "… (truncated)")
		}
		if gotLen := len([]rune(got)); gotLen != wantTruncatedLen {
			t.Errorf("len([]rune(diagnosticOutput())) = %d, want %d", gotLen, wantTruncatedLen)
		}
	})
}

func TestNewClient_DefaultsWebUIActor(t *testing.T) {
	c, ok := NewClient().(*cliClient)
	if !ok {
		t.Fatalf("NewClient did not return *cliClient")
	}
	lr, ok := c.runner.(limitedRunner)
	if !ok {
		t.Fatalf("NewClient runner is %T, want limitedRunner", c.runner)
	}
	r, ok := lr.inner.(execRunner)
	if !ok {
		t.Fatalf("limitedRunner inner is %T, want execRunner", lr.inner)
	}
	if r.actor != webUIActor {
		t.Errorf("execRunner.actor = %q, want %q", r.actor, webUIActor)
	}
}
