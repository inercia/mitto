package beads

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

func newClient(r *recordingRunner) *cliClient { return &cliClient{runner: r} }

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
	for _, u := range []string{"none", "jira", "github", "gitlab", "linear"} {
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
	if got := r.calls[0].args[0]; got != "list" {
		t.Errorf("expected bd \"list\" call, got %q", got)
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
// Cleanup
// ---------------------------------------------------------------------------

func TestClient_Cleanup_ZeroClosed_NoDeleteCall(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{
		{stdout: []byte(`[]`)}, // empty list
	}}
	c := newClient(r)
	count, err := c.Cleanup(context.Background(), "/dir")
	if err != nil {
		t.Fatalf("Cleanup() error: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
	if len(r.calls) != 1 {
		t.Errorf("expected 1 runner call (list only), got %d", len(r.calls))
	}
}

func TestClient_Cleanup_DeletesWithForce(t *testing.T) {
	listJSON := `[{"id":"abc-1"},{"id":"abc-2"}]`
	r := &recordingRunner{responses: []runnerResp{
		{stdout: []byte(listJSON)}, // list call
		{stdout: []byte("")},       // delete call
	}}
	c := newClient(r)
	count, err := c.Cleanup(context.Background(), "/dir")
	if err != nil {
		t.Fatalf("Cleanup() error: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
	if len(r.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(r.calls))
	}
	deleteArgs := r.calls[1].args
	joined := strings.Join(deleteArgs, " ")
	if !strings.Contains(joined, "--force") {
		t.Errorf("delete args missing --force: %v", deleteArgs)
	}
	if !strings.Contains(joined, "abc-1") || !strings.Contains(joined, "abc-2") {
		t.Errorf("delete args missing IDs: %v", deleteArgs)
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
