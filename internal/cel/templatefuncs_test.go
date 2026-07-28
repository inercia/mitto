package cel

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
)

// =============================================================================
// Helpers
// =============================================================================

// evalCEL compiles and evaluates a CEL expression against ctx.
func evalCEL(t *testing.T, e *CELEvaluator, expr string, ctx *PromptEnabledContext) bool {
	t.Helper()
	return evaluate(t, e, compile(t, e, expr), ctx)
}

// RenderPromptTemplate is a test-local re-implementation of
// config.RenderPromptTemplate, kept here to avoid an internal/cel → internal/config
// dependency when the CEL sub-package was split out (mitto-b8k.3).
func RenderPromptTemplate(name, body string, data any, funcs template.FuncMap) (string, error) {
	if !strings.Contains(body, "{{") {
		return body, nil
	}
	t, err := template.New(name).Option("missingkey=zero").Funcs(funcs).Parse(body)
	if err != nil {
		return "", fmt.Errorf("prompt template %q: parse error: %w", name, err)
	}
	var buf strings.Builder
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("prompt template %q: render error: %w", name, err)
	}
	return buf.String(), nil
}

// newGitRepo initializes a temp git repository with one committed file
// ("tracked.txt") and returns its path. Skips the test when git is absent.
func newGitRepo(t *testing.T) string {
	t.Helper()
	if !commandExists("git") {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	// Disable commit signing for this repo so the test is hermetic: a
	// developer machine with a global commit.gpgsign=true (and no cached
	// gpg-agent passphrase) would otherwise fail "git commit" here with
	// "gpg failed to sign the data". CI runners don't have this configured,
	// but local machines might.
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "tracked.txt")
	run("commit", "-m", "init")
	return dir
}

// =============================================================================
// Parity tests: CEL binding result == pure-Go helper result for every input.
// =============================================================================

func TestParity_FileExists(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "file.txt")
	if err := os.WriteFile(testFile, []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(tmpDir, "sub")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	e := newTestEvaluator(t)
	ctx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: tmpDir}}

	cases := []struct{ path string }{
		{"file.txt"},          // existing file
		{"sub"},               // existing dir (should be false for fileExists)
		{"absent.txt"},        // non-existent
		{""},                  // empty path
		{testFile},            // absolute path to file
		{subDir},              // absolute path to dir
		{"/nonexistent/path"}, // absolute non-existent
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("path=%q", tc.path), func(t *testing.T) {
			goResult := fileExists(tmpDir, tc.path)
			celExpr := fmt.Sprintf("FileExists(%q)", tc.path)
			celResult := evalCEL(t, e, celExpr, ctx)
			if goResult != celResult {
				t.Errorf("parity failure: go=%v cel=%v for path %q", goResult, celResult, tc.path)
			}
		})
	}
}

func TestParity_DirExists(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "file.txt")
	if err := os.WriteFile(testFile, []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(tmpDir, "sub")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	e := newTestEvaluator(t)
	ctx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: tmpDir}}

	cases := []struct{ path string }{
		{"sub"},      // existing dir
		{"file.txt"}, // existing file (should be false for dirExists)
		{"absent"},   // non-existent
		{""},         // empty
		{subDir},     // absolute dir
		{testFile},   // absolute file
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("path=%q", tc.path), func(t *testing.T) {
			goResult := dirExists(tmpDir, tc.path)
			celExpr := fmt.Sprintf("DirExists(%q)", tc.path)
			celResult := evalCEL(t, e, celExpr, ctx)
			if goResult != celResult {
				t.Errorf("parity failure: go=%v cel=%v for path %q", goResult, celResult, tc.path)
			}
		})
	}
}

func TestParity_CommandExists(t *testing.T) {
	e := newTestEvaluator(t)
	ctx := &PromptEnabledContext{}

	cases := []struct {
		cmd  string
		want bool
	}{
		{"sh", true},                           // always present on Unix/macOS
		{"nonexistent_cmd_xyz_abc_999", false}, // absent
		{"", false},                            // empty
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("cmd=%q", tc.cmd), func(t *testing.T) {
			goResult := commandExists(tc.cmd)
			if goResult != tc.want {
				t.Errorf("commandExists(%q) = %v, want %v", tc.cmd, goResult, tc.want)
			}
			celExpr := fmt.Sprintf("CommandExists(%q)", tc.cmd)
			celResult := evalCEL(t, e, celExpr, ctx)
			if goResult != celResult {
				t.Errorf("parity failure: go=%v cel=%v for cmd %q", goResult, celResult, tc.cmd)
			}
		})
	}
}

// TestParity_GitHelpers walks a single git repo through a sequence of state
// mutations, asserting Go helper result == CEL eval result == expected bool
// at every step (mitto-d01).
func TestParity_GitHelpers(t *testing.T) {
	dir := newGitRepo(t)
	e := newTestEvaluator(t)
	ctx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: dir}}

	check := func(label string, goResult bool, celExpr string, want bool) {
		t.Helper()
		celResult := evalCEL(t, e, celExpr, ctx)
		if goResult != celResult {
			t.Errorf("%s: parity failure go=%v cel=%v", label, goResult, celResult)
		}
		if goResult != want {
			t.Errorf("%s: got=%v want=%v", label, goResult, want)
		}
	}

	// Step 1: freshly committed repo — everything clean.
	check("repo after setup", gitRepo(dir, ""), `GitRepo("")`, true)
	check("tracked after setup", gitFileTracked(dir, "tracked.txt"), `GitFileTracked("tracked.txt")`, true)
	check("fileModified after setup", gitFileModified(dir, "tracked.txt"), `GitFileModified("tracked.txt")`, false)
	check("deleted after setup", gitFileDeleted(dir, "tracked.txt"), `GitFileDeleted("tracked.txt")`, false)
	check("dirModified after setup", gitDirModified(dir, ""), `GitDirModified("")`, false)

	// Step 2: add an untracked file.
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	check("tracked untracked.txt", gitFileTracked(dir, "untracked.txt"), `GitFileTracked("untracked.txt")`, false)
	check("fileModified untracked.txt", gitFileModified(dir, "untracked.txt"), `GitFileModified("untracked.txt")`, false)
	check("dirModified after untracked add", gitDirModified(dir, ""), `GitDirModified("")`, true)

	// Step 3: modify the tracked file.
	f, err := os.OpenFile(filepath.Join(dir, "tracked.txt"), os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("more\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	check("fileModified after edit", gitFileModified(dir, "tracked.txt"), `GitFileModified("tracked.txt")`, true)
	check("dirModified after edit", gitDirModified(dir, ""), `GitDirModified("")`, true)

	// Step 4: remove the tracked file (unstaged deletion).
	if err := os.Remove(filepath.Join(dir, "tracked.txt")); err != nil {
		t.Fatal(err)
	}
	check("deleted after remove", gitFileDeleted(dir, "tracked.txt"), `GitFileDeleted("tracked.txt")`, true)
	check("fileModified after remove", gitFileModified(dir, "tracked.txt"), `GitFileModified("tracked.txt")`, true)
	check("tracked after remove", gitFileTracked(dir, "tracked.txt"), `GitFileTracked("tracked.txt")`, true)

	// Step 5: a path that never existed.
	check("tracked absent.txt", gitFileTracked(dir, "absent.txt"), `GitFileTracked("absent.txt")`, false)
	check("fileModified absent.txt", gitFileModified(dir, "absent.txt"), `GitFileModified("absent.txt")`, false)
	check("deleted absent.txt", gitFileDeleted(dir, "absent.txt"), `GitFileDeleted("absent.txt")`, false)

	// 0-arg GitDirModified() must equal the explicit "" form and GitDirModified(".").
	dirModified0 := evalCEL(t, e, `GitDirModified()`, ctx)
	dirModifiedEmpty := evalCEL(t, e, `GitDirModified("")`, ctx)
	dirModifiedDot := evalCEL(t, e, `GitDirModified(".")`, ctx)
	if dirModified0 != dirModifiedEmpty {
		t.Errorf("GitDirModified() = %v, GitDirModified(\"\") = %v", dirModified0, dirModifiedEmpty)
	}
	if dirModified0 != dirModifiedDot {
		t.Errorf("GitDirModified() = %v, GitDirModified(\".\") = %v", dirModified0, dirModifiedDot)
	}
	if dirModified0 != gitDirModified(dir, "") {
		t.Errorf("GitDirModified() = %v, gitDirModified(dir,\"\") = %v", dirModified0, gitDirModified(dir, ""))
	}

	// 0-arg GitRepo() must equal the explicit "" form.
	repo0 := evalCEL(t, e, `GitRepo()`, ctx)
	repoEmpty := evalCEL(t, e, `GitRepo("")`, ctx)
	if repo0 != repoEmpty {
		t.Errorf("GitRepo() = %v, GitRepo(\"\") = %v", repo0, repoEmpty)
	}
	if repo0 != gitRepo(dir, "") {
		t.Errorf("GitRepo() = %v, gitRepo(dir,\"\") = %v", repo0, gitRepo(dir, ""))
	}

	// A plain (non-git) directory must report false through both engines.
	plain := t.TempDir()
	plainCtx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: plain}}
	if gitRepo(plain, "") {
		t.Errorf("gitRepo(non-repo) = true, want false")
	}
	if evalCEL(t, e, `GitRepo()`, plainCtx) {
		t.Errorf("CEL GitRepo() on non-repo = true, want false")
	}
}

// TestBuildTemplateFuncMap_GitFuncsRenderSmoke verifies GitFileModified and the
// 0-arg GitDirModified form render correctly through RenderPromptTemplate.
func TestBuildTemplateFuncMap_GitFuncsRenderSmoke(t *testing.T) {
	dir := newGitRepo(t)
	f, err := os.OpenFile(filepath.Join(dir, "tracked.txt"), os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("more\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	ctx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: dir}}
	fm := BuildTemplateFuncMap(ctx)

	got, err := RenderPromptTemplate("test", `{{ if GitFileModified "tracked.txt" }}yes{{ else }}no{{ end }}`, ctx, fm)
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if got != "yes" {
		t.Errorf("GitFileModified render = %q, want %q", got, "yes")
	}

	got, err = RenderPromptTemplate("test", `{{ if GitDirModified }}yes{{ else }}no{{ end }}`, ctx, fm)
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if got != "yes" {
		t.Errorf("GitDirModified render = %q, want %q", got, "yes")
	}

	got, err = RenderPromptTemplate("test", `{{ if GitRepo }}yes{{ else }}no{{ end }}`, ctx, fm)
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if got != "yes" {
		t.Errorf("GitRepo render = %q, want %q", got, "yes")
	}
}

// TestBuildTemplateFuncMap_GitStatusFilesRenderSmoke verifies GitStatusFiles
// returns porcelain lines that render correctly through RenderPromptTemplate,
// and that it yields an empty slice on a clean repo / nil outside a repo.
func TestBuildTemplateFuncMap_GitStatusFilesRenderSmoke(t *testing.T) {
	dir := newGitRepo(t)

	// Clean tree: expect empty range output.
	cleanCtx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: dir}}
	cleanFM := BuildTemplateFuncMap(cleanCtx)
	got, err := RenderPromptTemplate("test", `[{{ range GitStatusFiles }}{{ . }}|{{ end }}]`, cleanCtx, cleanFM)
	if err != nil {
		t.Fatalf("render error (clean): %v", err)
	}
	if got != "[]" {
		t.Errorf("GitStatusFiles on clean repo = %q, want %q", got, "[]")
	}

	// Dirty tree: modify tracked file and add an untracked one.
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0644); err != nil {
		t.Fatal(err)
	}

	dirtyCtx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: dir}}
	dirtyFM := BuildTemplateFuncMap(dirtyCtx)
	got, err = RenderPromptTemplate("test", `{{ range GitStatusFiles }}{{ . }}
{{ end }}`, dirtyCtx, dirtyFM)
	if err != nil {
		t.Fatalf("render error (dirty): %v", err)
	}
	if !strings.Contains(got, "tracked.txt") {
		t.Errorf("GitStatusFiles output missing 'tracked.txt': %q", got)
	}
	if !strings.Contains(got, "new.txt") {
		t.Errorf("GitStatusFiles output missing 'new.txt': %q", got)
	}
	if !strings.Contains(got, "??") {
		t.Errorf("GitStatusFiles output missing '??' status code for untracked file: %q", got)
	}

	// Non-repo: expect nil slice → empty range output.
	plain := t.TempDir()
	plainCtx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: plain}}
	plainFM := BuildTemplateFuncMap(plainCtx)
	got, err = RenderPromptTemplate("test", `[{{ range GitStatusFiles }}{{ . }}|{{ end }}]`, plainCtx, plainFM)
	if err != nil {
		t.Fatalf("render error (non-repo): %v", err)
	}
	if got != "[]" {
		t.Errorf("GitStatusFiles on non-repo = %q, want %q", got, "[]")
	}
}

func TestParity_HasPattern(t *testing.T) {
	e := newTestEvaluator(t)
	reachable := NewReachableToolsContext([]string{"github_pr", "jira_create", "slack_post"}).Servers

	cases := []struct {
		name    string
		servers map[string]ServerToolInfo
		pattern string
		want    bool
	}{
		{"match", reachable, "github_*", true},
		{"unknown server fails open", reachable, "notion_*", true},
		{"no match on reachable server", reachable, "jira_other", false},
		{"cold start fails open", nil, "anything_*", true},
		{"exact match", reachable, "jira_create", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goResult := hasPattern(tc.servers, tc.pattern)
			if goResult != tc.want {
				t.Errorf("hasPattern(servers, %q) = %v, want %v", tc.pattern, goResult, tc.want)
			}
			ctx := &PromptEnabledContext{Tools: ToolsContext{Servers: tc.servers}}
			celExpr := fmt.Sprintf("Tools.HasPattern(%q)", tc.pattern)
			celResult := evalCEL(t, e, celExpr, ctx)
			if goResult != celResult {
				t.Errorf("parity failure: go=%v cel=%v for pattern %q", goResult, celResult, tc.pattern)
			}
		})
	}
}

func TestParity_HasAllPatterns(t *testing.T) {
	e := newTestEvaluator(t)
	reachable := NewReachableToolsContext([]string{"github_pr", "jira_create", "slack_post"}).Servers

	cases := []struct {
		name     string
		servers  map[string]ServerToolInfo
		patterns []string
		want     bool
	}{
		{"all satisfied", reachable, []string{"github_*", "jira_*"}, true},
		{"one unsatisfied on reachable server", reachable, []string{"github_*", "jira_other"}, false},
		{"unknown server fails open (does not fail the AND)", reachable, []string{"notion_*"}, true},
		{"cold start fails open", nil, []string{"notion_*"}, true},
		{"empty patterns", reachable, []string{}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goResult := hasAllPatterns(tc.servers, tc.patterns)
			if goResult != tc.want {
				t.Errorf("hasAllPatterns = %v, want %v", goResult, tc.want)
			}
			// Build CEL list literal for patterns
			ctx := &PromptEnabledContext{Tools: ToolsContext{Servers: tc.servers}}
			var celPatterns string
			for i, p := range tc.patterns {
				if i > 0 {
					celPatterns += ", "
				}
				celPatterns += fmt.Sprintf("%q", p)
			}
			celExpr := fmt.Sprintf("Tools.HasAllPatterns([%s])", celPatterns)
			celResult := evalCEL(t, e, celExpr, ctx)
			if goResult != celResult {
				t.Errorf("parity failure: go=%v cel=%v for patterns %v", goResult, celResult, tc.patterns)
			}
		})
	}
}

func TestParity_HasAnyPattern(t *testing.T) {
	e := newTestEvaluator(t)
	reachable := NewReachableToolsContext([]string{"github_pr", "jira_create"}).Servers

	cases := []struct {
		name     string
		servers  map[string]ServerToolInfo
		patterns []string
		want     bool
	}{
		{"one matches", reachable, []string{"github_*", "notion_*"}, true},
		{"none match on reachable servers", reachable, []string{"github_other", "jira_other"}, false},
		{"unknown server fails open (satisfies the OR)", reachable, []string{"notion_*"}, true},
		{"cold start fails open", nil, []string{"notion_*"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goResult := hasAnyPattern(tc.servers, tc.patterns)
			if goResult != tc.want {
				t.Errorf("hasAnyPattern = %v, want %v", goResult, tc.want)
			}
			ctx := &PromptEnabledContext{Tools: ToolsContext{Servers: tc.servers}}
			var celPatterns string
			for i, p := range tc.patterns {
				if i > 0 {
					celPatterns += ", "
				}
				celPatterns += fmt.Sprintf("%q", p)
			}
			celExpr := fmt.Sprintf("Tools.HasAnyPattern([%s])", celPatterns)
			celResult := evalCEL(t, e, celExpr, ctx)
			if goResult != celResult {
				t.Errorf("parity failure: go=%v cel=%v", goResult, celResult)
			}
		})
	}
}

// TestHasPattern_PerServerStates covers the three per-server MCP tool
// availability states (docs/devel/mcp-tool-discovery.md, Q3.2/Q4.1) and the
// prefix->server pattern resolution required by mitto-sys.1, keeping the Go
// template path (hasPattern) and the CEL path (Tools.HasPattern) in parity.
func TestHasPattern_PerServerStates(t *testing.T) {
	e := newTestEvaluator(t)

	servers := map[string]ServerToolInfo{
		"jira":   {State: ServerToolStateReachable, Names: []string{"jira_create_issue"}},
		"github": {State: ServerToolStateReachable, Names: []string{"github_list_prs"}},
		"slack":  {State: ServerToolStateUnknown},
		"notion": {State: ServerToolStateUnreachable},
	}

	cases := []struct {
		name    string
		servers map[string]ServerToolInfo
		pattern string
		want    bool
	}{
		{"unknown server fails open even with no matching tool", servers, "slack_post", true},
		{"configured-but-unreachable server fails open", servers, "notion_search", true},
		{"reachable server fails closed on match", servers, "jira_*", true},
		{"reachable server fails closed on no match", servers, "jira_other_thing", false},
		{"prefix mapping resolves to owning server, unaffected by a different reachable server", servers, "github_*", true},
		{"prefix mapping: no match on owning reachable server, unaffected by a different reachable server", servers, "github_nonexistent", false},
		{"cold start: nil server map globally fails open", nil, "jira_*", true},
		{"cold start: empty (non-nil) server map globally fails open", map[string]ServerToolInfo{}, "jira_*", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goResult := hasPattern(tc.servers, tc.pattern)
			if goResult != tc.want {
				t.Errorf("hasPattern(servers, %q) = %v, want %v", tc.pattern, goResult, tc.want)
			}
			ctx := &PromptEnabledContext{Tools: ToolsContext{Servers: tc.servers}}
			celExpr := fmt.Sprintf("Tools.HasPattern(%q)", tc.pattern)
			celResult := evalCEL(t, e, celExpr, ctx)
			if goResult != celResult {
				t.Errorf("parity failure: go=%v cel=%v for pattern %q", goResult, celResult, tc.pattern)
			}
		})
	}
}

func TestParity_MatchesServerType(t *testing.T) {
	e := newTestEvaluator(t)

	cases := []struct {
		name        string
		acpName     string
		acpType     string
		serverTypes []string
		want        bool
	}{
		{"type match", "Auggie", "augment", []string{"augment"}, true},
		{"case-insensitive", "Auggie", "augment", []string{"AUGMENT"}, true},
		{"no match", "Auggie", "augment", []string{"claude"}, false},
		{"fail-open empty name", "", "", []string{"anything"}, true},
		{"no server types", "Auggie", "augment", []string{}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goResult := matchesServerType(tc.acpName, tc.acpType, tc.serverTypes)
			if goResult != tc.want {
				t.Errorf("matchesServerType = %v, want %v", goResult, tc.want)
			}
			ctx := &PromptEnabledContext{ACP: ACPContext{Name: tc.acpName, Type: tc.acpType}}
			// CEL only supports single-arg form here; test with first type or empty list
			if len(tc.serverTypes) == 0 && tc.acpName != "" {
				// No easy way to test empty list in CEL matchesServerType macro; skip parity
				return
			}
			var celTypes string
			for i, st := range tc.serverTypes {
				if i > 0 {
					celTypes += ", "
				}
				celTypes += fmt.Sprintf("%q", st)
			}
			celExpr := fmt.Sprintf("ACP.MatchesServerType([%s])", celTypes)
			celResult := evalCEL(t, e, celExpr, ctx)
			if goResult != celResult {
				t.Errorf("parity failure: go=%v cel=%v", goResult, celResult)
			}
		})
	}
}

// =============================================================================
// arg / default tests
// =============================================================================

func TestArg(t *testing.T) {
	ctx := &PromptEnabledContext{
		Args: map[string]string{
			"BRANCH": "main",
			"EMPTY":  "",
		},
	}
	fm := BuildTemplateFuncMap(ctx)
	argFn := fm["Arg"].(func(string, ...string) string)

	// present and non-empty
	if got := argFn("BRANCH"); got != "main" {
		t.Errorf("arg(BRANCH) = %q, want %q", got, "main")
	}
	// present but empty → returns "" (no default given)
	if got := argFn("EMPTY"); got != "" {
		t.Errorf("arg(EMPTY) = %q, want %q", got, "")
	}
	// present but empty → returns default
	if got := argFn("EMPTY", "fallback"); got != "fallback" {
		t.Errorf("arg(EMPTY, fallback) = %q, want %q", got, "fallback")
	}
	// missing → returns ""
	if got := argFn("MISSING"); got != "" {
		t.Errorf("arg(MISSING) = %q, want %q", got, "")
	}
	// missing → returns default
	if got := argFn("MISSING", "def"); got != "def" {
		t.Errorf("arg(MISSING, def) = %q, want %q", got, "def")
	}
	// present non-empty → ignores default
	if got := argFn("BRANCH", "ignored"); got != "main" {
		t.Errorf("arg(BRANCH, ignored) = %q, want %q", got, "main")
	}
}

func TestDefault(t *testing.T) {
	ctx := &PromptEnabledContext{}
	fm := BuildTemplateFuncMap(ctx)
	defFn := fm["Default"].(func(string, string) string)

	if got := defFn("fallback", "value"); got != "value" {
		t.Errorf("default(fallback, value) = %q", got)
	}
	if got := defFn("fallback", ""); got != "fallback" {
		t.Errorf("default(fallback, ) = %q", got)
	}
	if got := defFn("", ""); got != "" {
		t.Errorf("default(, ) = %q", got)
	}
}

// TestBuildTemplateFuncMap_NilCtx verifies nil context safety.
func TestBuildTemplateFuncMap_NilCtx(t *testing.T) {
	fm := BuildTemplateFuncMap(nil)
	if fm == nil {
		t.Fatal("expected non-nil FuncMap")
	}
	// arg with nil ctx should return ""
	argFn := fm["Arg"].(func(string, ...string) string)
	if got := argFn("ANY"); got != "" {
		t.Errorf("nil ctx arg(ANY) = %q, want %q", got, "")
	}
	if got := argFn("ANY", "def"); got != "def" {
		t.Errorf("nil ctx arg(ANY, def) = %q, want %q", got, "def")
	}
}

// TestBuildTemplateFuncMap_StringUtils exercises the string utility functions
// via RenderPromptTemplate and direct invocation.
func TestBuildTemplateFuncMap_StringUtils(t *testing.T) {
	ctx := &PromptEnabledContext{}
	fm := BuildTemplateFuncMap(ctx)

	// Direct invocation for join (no slice builtin available in the template).
	joinFn := fm["Join"].(func(string, []string) string)
	if got := joinFn(", ", []string{"a", "b", "c"}); got != "a, b, c" {
		t.Errorf("join = %q, want %q", got, "a, b, c")
	}
	if got := joinFn("-", []string{}); got != "" {
		t.Errorf("join empty = %q, want %q", got, "")
	}

	// Template-rendered cases.
	cases := []struct {
		body string
		want string
	}{
		{`{{ Upper "hello" }}`, "HELLO"},
		{`{{ Lower "WORLD" }}`, "world"},
		{`{{ Trim "  hi  " }}`, "hi"},
		{`{{ Contains "foobar" "bar" }}`, "true"},
		{`{{ HasPrefix "foobar" "foo" }}`, "true"},
		{`{{ HasSuffix "foobar" "baz" }}`, "false"},
	}
	for _, tc := range cases {
		got, err := RenderPromptTemplate("test", tc.body, nil, fm)
		if err != nil {
			t.Errorf("render %q: %v", tc.body, err)
			continue
		}
		if got != tc.want {
			t.Errorf("render %q = %q, want %q", tc.body, got, tc.want)
		}
	}
}

// TestBuildTemplateFuncMap_DirEdgeCases pins the behaviour of Dir on path
// shapes a prompt author can realistically hit (absolute, backslash, trailing
// slash, ".." segments) and — where relevant — how the derived path composes
// with FileExists / ReadFile. These are regression pins for mitto-qv2: no
// behavioural change is intended, but a future refactor of Dir, readFile's
// containment check, or the FileExists jail must not silently weaken these
// interactions.
func TestBuildTemplateFuncMap_DirEdgeCases(t *testing.T) {
	// Shared workspace with a small on-disk fixture: <folder>/a/b/x.md
	dir := t.TempDir()
	subdir := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "x.md"), []byte("x body"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: dir}}
	fm := BuildTemplateFuncMap(ctx)
	dirFn := fm["Dir"].(func(string) string)

	// --- Direct Dir() assertions: pin path.Dir semantics on each shape. ---
	t.Run("direct", func(t *testing.T) {
		cases := []struct {
			name string
			in   string
			want string
		}{
			// Absolute path: Dir returns the parent as-is; the jail is enforced
			// downstream by ReadFile (see composition subtests below).
			{"absolute", "/etc/passwd", "/etc"},
			// Backslash path: path.Dir treats "\" as a literal on all platforms
			// (workspace paths are documented as forward-slash), so a
			// Windows-authored path silently degrades to ".". This is a
			// decision, not an accident — pin it.
			{"backslash", `a\b\x.md`, "."},
			// Trailing-slash shapes that composed printf paths rely on.
			{"trailing_slash_nested", "a/b/", "a/b"},
			{"trailing_slash_shallow", "a/", "a"},
			{"root", "/", "/"},
			// ".." segment: Dir preserves it; the jail rejection happens in
			// ReadFile (see composition subtests below).
			{"parent_segment", "../x.md", ".."},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := dirFn(tc.in); got != tc.want {
					t.Errorf("Dir(%q) = %q, want %q", tc.in, got, tc.want)
				}
			})
		}
	})

	// --- Composition assertions: ReadFile must reject unsafe derived paths. ---
	// FileExists (via evaluator.statResolved) does NOT reject absolute paths,
	// so we do not assert on its output here — the security-relevant contract
	// is that ReadFile's jail holds, so a runaway "%s/leaf.md" template cannot
	// exfiltrate arbitrary files even when Dir happily returns "/etc" or "..".
	t.Run("readfile_rejects_absolute_derived", func(t *testing.T) {
		body := `{{ ReadFile (printf "%s/passwd" (Dir .Args.Test)) }}`
		ctx2 := &PromptEnabledContext{
			Workspace: WorkspaceContext{Folder: dir},
			Args:      map[string]string{"Test": "/etc/passwd"},
		}
		got, err := RenderPromptTemplate("t", body, ctx2, BuildTemplateFuncMap(ctx2))
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if got != "" {
			t.Errorf("ReadFile of absolute-derived path = %q, want %q", got, "")
		}
	})

	t.Run("readfile_rejects_parent_escape_derived", func(t *testing.T) {
		body := `{{ ReadFile (printf "%s/cleanup.md" (Dir .Args.Test)) }}`
		ctx2 := &PromptEnabledContext{
			Workspace: WorkspaceContext{Folder: dir},
			Args:      map[string]string{"Test": "../x.md"},
		}
		got, err := RenderPromptTemplate("t", body, ctx2, BuildTemplateFuncMap(ctx2))
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if got != "" {
			t.Errorf("ReadFile of ..-derived path = %q, want %q", got, "")
		}
	})

	// Trailing-slash composition: Dir("a/b/") = "a/b", so the derived leaf is
	// a normal in-workspace path and ReadFile should return the fixture body.
	t.Run("readfile_trailing_slash_composes", func(t *testing.T) {
		body := `{{ ReadFile (printf "%s/x.md" (Dir .Args.Test)) }}`
		ctx2 := &PromptEnabledContext{
			Workspace: WorkspaceContext{Folder: dir},
			Args:      map[string]string{"Test": "a/b/"},
		}
		got, err := RenderPromptTemplate("t", body, ctx2, BuildTemplateFuncMap(ctx2))
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if want := "x body"; got != want {
			t.Errorf("ReadFile of trailing-slash-derived path = %q, want %q", got, want)
		}
	})
}

// TestUserData verifies the UserData template function.
func TestUserData(t *testing.T) {
	ctx := &PromptEnabledContext{
		UserData: map[string]string{
			"JIRA Ticket": "PROJ-42",
			"env":         "prod",
		},
	}
	fm := BuildTemplateFuncMap(ctx)
	udFn := fm["UserData"].(func(string) string)

	// present key
	if got := udFn("JIRA Ticket"); got != "PROJ-42" {
		t.Errorf(`UserData("JIRA Ticket") = %q, want "PROJ-42"`, got)
	}
	// another present key
	if got := udFn("env"); got != "prod" {
		t.Errorf(`UserData("env") = %q, want "prod"`, got)
	}
	// absent key → ""
	if got := udFn("missing"); got != "" {
		t.Errorf(`UserData("missing") = %q, want ""`, got)
	}

	// nil UserData (menu-time context) must not panic and return "".
	nilCtx := &PromptEnabledContext{}
	fm2 := BuildTemplateFuncMap(nilCtx)
	udFn2 := fm2["UserData"].(func(string) string)
	if got := udFn2("any"); got != "" {
		t.Errorf(`UserData nil map = %q, want ""`, got)
	}
}

// TestModel verifies the Model(tag) template func resolves current-model capability tags
// case-insensitively and degrades to false for an empty / unknown-model tag set (mitto-i5sr).
func TestModel(t *testing.T) {
	ctx := &PromptEnabledContext{
		Session: SessionContext{ModelTags: []string{"Smart", "Expensive"}},
	}
	fm := BuildTemplateFuncMap(ctx)
	modelFn := fm["Model"].(func(string) bool)

	if !modelFn("Smart") {
		t.Errorf(`Model("Smart") = false, want true`)
	}
	if !modelFn("smart") {
		t.Errorf(`Model("smart") = false, want true (case-insensitive)`)
	}
	if modelFn("cheap") {
		t.Errorf(`Model("cheap") = true, want false`)
	}

	// nil tags (cold start / unknown model) must not panic and return false.
	nilCtx := &PromptEnabledContext{}
	fm2 := BuildTemplateFuncMap(nilCtx)
	modelFn2 := fm2["Model"].(func(string) bool)
	if modelFn2("smart") {
		t.Errorf(`Model nil tags = true, want false`)
	}

	// Renders correctly through RenderPromptTemplate ({{ if Model "smart" }}).
	got, err := RenderPromptTemplate("test", `{{ if Model "smart" }}SMART{{ else }}PLAIN{{ end }}`, ctx, fm)
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if got != "SMART" {
		t.Errorf("render got %q, want %q", got, "SMART")
	}
}

// TestBuildTemplateFuncMap_AllKeysPresent verifies all expected keys exist.
func TestBuildTemplateFuncMap_AllKeysPresent(t *testing.T) {
	fm := BuildTemplateFuncMap(nil)
	expected := []string{
		"Arg", "Default", "UserData",
		"FileExists", "DirExists", "ReadFile", "CommandExists", "HasPattern", "Model",
		"GitFileModified", "GitDirModified", "GitStatusFiles", "GitFileTracked", "GitFileDeleted",
		"BeadsCount", "HasBeads", "BeadHasLabels", "BeadIsOpen", "BeadMetadata",
		"PromptText",
		"Trim", "Lower", "Upper", "Contains", "HasPrefix", "HasSuffix", "Join",
	}
	for _, key := range expected {
		if fm[key] == nil {
			t.Errorf("FuncMap missing key %q", key)
		}
	}
}

// TestBuildTemplateFuncMap_FuncMapPlugsIntoRender verifies BuildTemplateFuncMap
// integrates with RenderPromptTemplate correctly.
func TestBuildTemplateFuncMap_FuncMapPlugsIntoRender(t *testing.T) {
	ctx := &PromptEnabledContext{
		Args: map[string]string{"NAME": "Alice"},
	}
	fm := BuildTemplateFuncMap(ctx)

	got, err := RenderPromptTemplate("test", `Hello {{ Upper (Arg "NAME") }}!`, ctx, fm)
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if got != "Hello ALICE!" {
		t.Errorf("got %q, want %q", got, "Hello ALICE!")
	}
}

// TestBuildTemplateFuncMap_FileExistsParity verifies template fileExists matches pure-Go.
func TestBuildTemplateFuncMap_FileExistsParity(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "present.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: tmpDir}}
	fm := BuildTemplateFuncMap(ctx)

	for _, path := range []string{"present.txt", "absent.txt"} {
		body := fmt.Sprintf(`{{ FileExists %q }}`, path)
		got, err := RenderPromptTemplate("test", body, ctx, fm)
		if err != nil {
			t.Fatalf("render error for %q: %v", path, err)
		}
		wantGo := fmt.Sprintf("%v", fileExists(tmpDir, path))
		if got != wantGo {
			t.Errorf("template fileExists(%q) = %q, pure-Go = %q", path, got, wantGo)
		}
	}
}

// TestReadFile_Basic covers the happy path, missing-file, and directory cases.
func TestReadFile_Basic(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "hello.md"), []byte("hi\nworld"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tmpDir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		path string
		want string
	}{
		{"present", "hello.md", "hi\nworld"},
		{"missing", "absent.md", ""},
		{"empty_path", "", ""},
		{"is_dir", "sub", ""},
		{"path_escape_dotdot", "../etc/passwd", ""},
		{"absolute_path_rejected", "/etc/passwd", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := readFile(tmpDir, tc.path)
			if got != tc.want {
				t.Errorf("readFile(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// TestReadFile_SizeCap verifies content beyond readFileMaxBytes is truncated
// rather than returned in full or aborted.
func TestReadFile_SizeCap(t *testing.T) {
	tmpDir := t.TempDir()
	// Write cap+1024 bytes; expect exactly cap bytes back.
	big := make([]byte, readFileMaxBytes+1024)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "big.md"), big, 0644); err != nil {
		t.Fatal(err)
	}
	got := readFile(tmpDir, "big.md")
	if len(got) != readFileMaxBytes {
		t.Errorf("readFile(big.md) len = %d, want %d (cap)", len(got), readFileMaxBytes)
	}
}

// TestReadFile_TemplateInlining verifies ReadFile plugged into
// RenderPromptTemplate inlines contents at render time.
func TestReadFile_TemplateInlining(t *testing.T) {
	tmpDir := t.TempDir()
	fragDir := filepath.Join(tmpDir, ".mitto", "support", "C0TEST")
	if err := os.MkdirAll(fragDir, 0755); err != nil {
		t.Fatal(err)
	}
	body := "**Scope:** channel-specific triage rules go here."
	if err := os.WriteFile(filepath.Join(fragDir, "scope.md"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: tmpDir}}
	fm := BuildTemplateFuncMap(ctx)

	// Present: inline body.
	tmplPresent := `{{- if FileExists ".mitto/support/C0TEST/scope.md" -}}` +
		`{{ ReadFile ".mitto/support/C0TEST/scope.md" }}` +
		`{{- else -}}FALLBACK{{- end -}}`
	got, err := RenderPromptTemplate("t", tmplPresent, ctx, fm)
	if err != nil {
		t.Fatalf("render (present): %v", err)
	}
	if got != body {
		t.Errorf("present: got %q, want %q", got, body)
	}

	// Missing: fallback branch.
	tmplMissing := `{{- if FileExists ".mitto/support/C0TEST/tone.md" -}}` +
		`{{ ReadFile ".mitto/support/C0TEST/tone.md" }}` +
		`{{- else -}}FALLBACK{{- end -}}`
	got, err = RenderPromptTemplate("t", tmplMissing, ctx, fm)
	if err != nil {
		t.Fatalf("render (missing): %v", err)
	}
	if got != "FALLBACK" {
		t.Errorf("missing: got %q, want FALLBACK", got)
	}
}

// TestReadFile_NilCtxSafe verifies ReadFile is safe when BuildTemplateFuncMap
// is called with a nil ctx (folder == ""): every call returns "".
func TestReadFile_NilCtxSafe(t *testing.T) {
	fm := BuildTemplateFuncMap(nil)
	got, err := RenderPromptTemplate("t", `[{{ ReadFile "anything.md" }}]`, nil, fm)
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if got != "[]" {
		t.Errorf("nil-ctx ReadFile got %q, want []", got)
	}
}

// Compile-time check: template.FuncMap is the declared return type.
var _ template.FuncMap = BuildTemplateFuncMap(nil)

// =============================================================================
// FormatACPServers tests
// =============================================================================

func TestFormatACPServers(t *testing.T) {
	cases := []struct {
		name    string
		servers []ACPServerInfo
		want    string
	}{
		{"nil", nil, ""},
		{"empty", []ACPServerInfo{}, ""},
		{
			"single no-tags not-current",
			[]ACPServerInfo{{Name: "claude-code"}},
			"claude-code",
		},
		{
			"single with tags current",
			[]ACPServerInfo{{Name: "auggie", Tags: []string{"coding", "ai-assistant"}, Current: true}},
			"auggie [coding, ai-assistant] (current)",
		},
		{
			"multi: one current, one not",
			[]ACPServerInfo{
				{Name: "auggie", Tags: []string{"coding"}, Current: false},
				{Name: "claude-code", Tags: []string{"coding", "fast"}, Current: true},
			},
			"auggie [coding], claude-code [coding, fast] (current)",
		},
		{
			"server with type — type not in output, name is",
			[]ACPServerInfo{{Name: "claude-fast", Type: "claude-code", Tags: []string{"fast"}, Current: true}},
			"claude-fast [fast] (current)",
		},
		{
			"no tags no current",
			[]ACPServerInfo{{Name: "bare"}},
			"bare",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatACPServers(tc.servers); got != tc.want {
				t.Errorf("FormatACPServers() = %q, want %q", got, tc.want)
			}
		})
	}
}

// =============================================================================
// FormatChildren tests
// =============================================================================

func TestFormatChildren(t *testing.T) {
	cases := []struct {
		name     string
		children []ChildInfo
		want     string
	}{
		{"nil", nil, ""},
		{"empty", []ChildInfo{}, ""},
		{
			"single with name and acp",
			[]ChildInfo{{ID: "sess-1", Name: "Research", ACPServer: "claude-code"}},
			"sess-1 (Research) [claude-code]",
		},
		{
			"single no-name",
			[]ChildInfo{{ID: "sess-1", ACPServer: "auggie"}},
			"sess-1 [auggie]",
		},
		{
			"single no-acp",
			[]ChildInfo{{ID: "sess-1", Name: "Test"}},
			"sess-1 (Test)",
		},
		{
			"bare id only",
			[]ChildInfo{{ID: "sess-1"}},
			"sess-1",
		},
		{
			"multi",
			[]ChildInfo{
				{ID: "sess-1", Name: "Research", ACPServer: "claude-code"},
				{ID: "sess-2", Name: "Tests", ACPServer: "auggie"},
			},
			"sess-1 (Research) [claude-code], sess-2 (Tests) [auggie]",
		},
		{
			"single with beads issue",
			[]ChildInfo{{ID: "sess-1", Name: "Research", ACPServer: "claude-code", BeadsIssue: "mitto-59b"}},
			"sess-1 (Research) [claude-code] {mitto-59b}",
		},
		{
			"bare id with beads issue",
			[]ChildInfo{{ID: "sess-1", BeadsIssue: "mitto-123"}},
			"sess-1 {mitto-123}",
		},
		{
			"beads issue without name",
			[]ChildInfo{{ID: "sess-1", ACPServer: "auggie", BeadsIssue: "mitto-abc"}},
			"sess-1 [auggie] {mitto-abc}",
		},
		{
			"multi mixed beads issue",
			[]ChildInfo{
				{ID: "sess-1", Name: "Research", ACPServer: "claude-code", BeadsIssue: "mitto-59b"},
				{ID: "sess-2", Name: "Tests", ACPServer: "auggie"},
			},
			"sess-1 (Research) [claude-code] {mitto-59b}, sess-2 (Tests) [auggie]",
		},
		// mitto-p9r: QueuedCount is a per-child struct field for template consumers
		// ({{ range .Children.All }} … {{ .QueuedCount }}); it must NOT alter the
		// default FormatChildren rendered string (byte-identical goldens preserved).
		{
			"queued-count does not affect rendering",
			[]ChildInfo{{ID: "sess-1", Name: "Research", ACPServer: "claude-code", QueuedCount: 5}},
			"sess-1 (Research) [claude-code]",
		},
		{
			"queued-count with beads issue does not affect rendering",
			[]ChildInfo{{ID: "sess-1", Name: "Research", ACPServer: "claude-code", BeadsIssue: "mitto-59b", QueuedCount: 3}},
			"sess-1 (Research) [claude-code] {mitto-59b}",
		},
		{
			"queued-count on bare id does not affect rendering",
			[]ChildInfo{{ID: "sess-1", QueuedCount: 2}},
			"sess-1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatChildren(tc.children); got != tc.want {
				t.Errorf("FormatChildren() = %q, want %q", got, tc.want)
			}
		})
	}
}

// =============================================================================
// ACP.AvailableText / Children.AllText / Children.MCPText template accessor tests
// =============================================================================

// TestTemplateFuncs_ACPServersChildrenMCPChildren verifies that the three struct-method
// template accessors render correctly from a populated PromptEnabledContext.
func TestTemplateFuncs_ACPServersChildrenMCPChildren(t *testing.T) {
	ctx := &PromptEnabledContext{
		ACP: ACPContext{
			Available: []ACPServerInfo{
				{Name: "auggie", Tags: []string{"coding"}, Current: true},
				{Name: "claude-code", Tags: []string{"fast"}},
			},
		},
		Children: ChildrenContext{
			All: []ChildInfo{
				{ID: "s1", Name: "Worker", ACPServer: "auggie", Origin: "mcp"},
				{ID: "s2", Name: "Helper", ACPServer: "claude-code", Origin: "auto"},
			},
			MCP: []ChildInfo{
				{ID: "s1", Name: "Worker", ACPServer: "auggie", Origin: "mcp"},
			},
		},
	}
	fm := BuildTemplateFuncMap(ctx)

	// ACP.AvailableText renders all available ACP servers.
	got, err := RenderPromptTemplate("t", `{{ .ACP.AvailableText }}`, ctx, fm)
	if err != nil {
		t.Fatalf("ACP.AvailableText render error: %v", err)
	}
	if want := "auggie [coding] (current), claude-code [fast]"; got != want {
		t.Errorf("ACP.AvailableText: got %q, want %q", got, want)
	}

	// Children.AllText renders all children (All slice).
	got, err = RenderPromptTemplate("t", `{{ .Children.AllText }}`, ctx, fm)
	if err != nil {
		t.Fatalf("Children.AllText render error: %v", err)
	}
	if want := "s1 (Worker) [auggie], s2 (Helper) [claude-code]"; got != want {
		t.Errorf("Children.AllText: got %q, want %q", got, want)
	}

	// Children.MCPText renders only MCP-origin children (MCP slice).
	got, err = RenderPromptTemplate("t", `{{ .Children.MCPText }}`, ctx, fm)
	if err != nil {
		t.Fatalf("Children.MCPText render error: %v", err)
	}
	if want := "s1 (Worker) [auggie]"; got != want {
		t.Errorf("Children.MCPText: got %q, want %q", got, want)
	}
}

// TestTemplateFuncs_ZeroValueCtxACPServersChildren verifies that ACP.AvailableText,
// Children.AllText, and Children.MCPText return "" when the context is zero-valued (no data).
func TestTemplateFuncs_ZeroValueCtxACPServersChildren(t *testing.T) {
	ctx := &PromptEnabledContext{}
	fm := BuildTemplateFuncMap(ctx)
	for _, body := range []string{"{{ .ACP.AvailableText }}", "{{ .Children.AllText }}", "{{ .Children.MCPText }}"} {
		got, err := RenderPromptTemplate("t", body, ctx, fm)
		if err != nil {
			t.Errorf("zero-value ctx %q: unexpected error: %v", body, err)
		}
		if got != "" {
			t.Errorf("zero-value ctx %q: expected empty string, got %q", body, got)
		}
	}
}

// TestTemplateFuncs_EmptySlicesACPServersChildren verifies that ACP.AvailableText,
// Children.AllText, and Children.MCPText return "" when the slices are empty (non-nil ctx, no data).
func TestTemplateFuncs_EmptySlicesACPServersChildren(t *testing.T) {
	ctx := &PromptEnabledContext{}
	fm := BuildTemplateFuncMap(ctx)
	for _, body := range []string{"{{ .ACP.AvailableText }}", "{{ .Children.AllText }}", "{{ .Children.MCPText }}"} {
		got, err := RenderPromptTemplate("t", body, ctx, fm)
		if err != nil {
			t.Errorf("empty ctx %q: unexpected error: %v", body, err)
		}
		if got != "" {
			t.Errorf("empty ctx %q: expected empty string, got %q", body, got)
		}
	}
}

// TestTemplateFuncs_MCPChildrenFiltersCorrectly verifies that Children.MCPText only
// renders the MCP slice even when All contains additional non-MCP entries.
func TestTemplateFuncs_MCPChildrenFiltersCorrectly(t *testing.T) {
	ctx := &PromptEnabledContext{
		Children: ChildrenContext{
			All: []ChildInfo{
				{ID: "m1", Name: "MCP child", ACPServer: "auggie", Origin: "mcp"},
				{ID: "a1", Name: "Auto child", ACPServer: "auggie", Origin: "auto"},
			},
			MCP: []ChildInfo{
				{ID: "m1", Name: "MCP child", ACPServer: "auggie", Origin: "mcp"},
			},
		},
	}
	fm := BuildTemplateFuncMap(ctx)

	allGot, _ := RenderPromptTemplate("t", `{{ .Children.AllText }}`, ctx, fm)
	mcpGot, _ := RenderPromptTemplate("t", `{{ .Children.MCPText }}`, ctx, fm)

	if want := "m1 (MCP child) [auggie], a1 (Auto child) [auggie]"; allGot != want {
		t.Errorf("Children.AllText: got %q, want %q", allGot, want)
	}
	if want := "m1 (MCP child) [auggie]"; mcpGot != want {
		t.Errorf("Children.MCPText: got %q, want %q", mcpGot, want)
	}
}

// TestChildrenContext_Get_DirectMethodCall exercises the Get lookup accessor
// directly (independent of template rendering) to pin the pure Go contract:
// found → non-nil pointer to the matching ChildInfo, unknown/empty id → nil,
// searches .All so any origin (mcp/auto/human) matches.
func TestChildrenContext_Get_DirectMethodCall(t *testing.T) {
	ctx := ChildrenContext{
		All: []ChildInfo{
			{ID: "s1", Name: "Worker", ACPServer: "auggie", Origin: "mcp", IsPrompting: true, BeadsIssue: "mitto-1"},
			{ID: "s2", Name: "Helper", ACPServer: "claude-code", Origin: "auto"},
			{ID: "s3", Name: "Human child", ACPServer: "auggie", Origin: "human"},
		},
		MCP: []ChildInfo{
			{ID: "s1", Name: "Worker", ACPServer: "auggie", Origin: "mcp", IsPrompting: true, BeadsIssue: "mitto-1"},
		},
	}

	// Found: mcp-origin child.
	if got := ctx.Get("s1"); got == nil {
		t.Fatal("Get(\"s1\"): got nil, want non-nil pointer to Worker")
	} else if got.Name != "Worker" || got.ACPServer != "auggie" || !got.IsPrompting || got.BeadsIssue != "mitto-1" {
		t.Errorf("Get(\"s1\"): fields mismatch: %+v", *got)
	}

	// Found: auto-origin child (Get searches .All, not .MCP).
	if got := ctx.Get("s2"); got == nil {
		t.Fatal("Get(\"s2\"): got nil, want non-nil pointer to Helper")
	} else if got.Name != "Helper" || got.Origin != "auto" {
		t.Errorf("Get(\"s2\"): fields mismatch: %+v", *got)
	}

	// Found: human-origin child.
	if got := ctx.Get("s3"); got == nil || got.Origin != "human" {
		t.Errorf("Get(\"s3\"): want human-origin child, got %+v", got)
	}

	// Not found → nil.
	if got := ctx.Get("does-not-exist"); got != nil {
		t.Errorf("Get(unknown): want nil, got %+v", *got)
	}

	// Empty id short-circuits to nil so an unset .Args placeholder is safe.
	if got := ctx.Get(""); got != nil {
		t.Errorf("Get(\"\"): want nil, got %+v", *got)
	}

	// Zero-valued ChildrenContext (no children at all) → nil for any id.
	var zero ChildrenContext
	if got := zero.Get("s1"); got != nil {
		t.Errorf("zero ChildrenContext.Get: want nil, got %+v", *got)
	}
}

// TestTemplateFuncs_ChildrenGet_TemplateRender verifies the accessor works
// end-to-end through the prompt template renderer — the canonical usage pattern
// documented in docs/config/prompts.md ({{ with .Children.Get "id" }}...{{ else }}...{{ end }}).
func TestTemplateFuncs_ChildrenGet_TemplateRender(t *testing.T) {
	ctx := &PromptEnabledContext{
		Children: ChildrenContext{
			All: []ChildInfo{
				{ID: "s1", Name: "Worker", ACPServer: "auggie", Origin: "mcp", IsPrompting: true},
				{ID: "s2", Name: "Helper", ACPServer: "claude-code", Origin: "auto", IsPrompting: false},
			},
		},
	}
	fm := BuildTemplateFuncMap(ctx)

	// Found + IsPrompting=true → "running" branch, inlines all four fields.
	body := `{{ with .Children.Get "s1" }}{{ .Name }} ({{ .ID }}) on {{ .ACPServer }} — {{ if .IsPrompting }}running{{ else }}idle{{ end }}{{ else }}not found{{ end }}`
	got, err := RenderPromptTemplate("t", body, ctx, fm)
	if err != nil {
		t.Fatalf("Children.Get render error: %v", err)
	}
	if want := "Worker (s1) on auggie — running"; got != want {
		t.Errorf("Children.Get found+prompting: got %q, want %q", got, want)
	}

	// Found + IsPrompting=false → "idle" branch.
	body = `{{ with .Children.Get "s2" }}{{ .Name }} — {{ if .IsPrompting }}running{{ else }}idle{{ end }}{{ else }}not found{{ end }}`
	got, err = RenderPromptTemplate("t", body, ctx, fm)
	if err != nil {
		t.Fatalf("Children.Get render error: %v", err)
	}
	if want := "Helper — idle"; got != want {
		t.Errorf("Children.Get found+idle: got %q, want %q", got, want)
	}

	// Not found → {{ else }} branch fires (nil pointer is falsy for `with`).
	body = `{{ with .Children.Get "missing" }}{{ .Name }}{{ else }}not found{{ end }}`
	got, err = RenderPromptTemplate("t", body, ctx, fm)
	if err != nil {
		t.Fatalf("Children.Get render error: %v", err)
	}
	if want := "not found"; got != want {
		t.Errorf("Children.Get missing: got %q, want %q", got, want)
	}

	// Empty id → {{ else }} branch (unset .Args placeholder should be safe).
	body = `{{ with .Children.Get "" }}{{ .Name }}{{ else }}empty-id{{ end }}`
	got, err = RenderPromptTemplate("t", body, ctx, fm)
	if err != nil {
		t.Fatalf("Children.Get render error: %v", err)
	}
	if want := "empty-id"; got != want {
		t.Errorf("Children.Get empty id: got %q, want %q", got, want)
	}
}

// TestTemplateFuncs_ChildrenGet_ZeroValueCtx verifies the accessor is safe on
// a zero-valued PromptEnabledContext — nil-safe, no panic, {{ else }} fires.
func TestTemplateFuncs_ChildrenGet_ZeroValueCtx(t *testing.T) {
	ctx := &PromptEnabledContext{}
	fm := BuildTemplateFuncMap(ctx)

	body := `{{ with .Children.Get "s1" }}{{ .Name }}{{ else }}no-children{{ end }}`
	got, err := RenderPromptTemplate("t", body, ctx, fm)
	if err != nil {
		t.Fatalf("zero-value ctx Children.Get: unexpected error: %v", err)
	}
	if got != "no-children" {
		t.Errorf("zero-value ctx Children.Get: got %q, want %q", got, "no-children")
	}
}

// =============================================================================
// FormatPeers tests (mitto-4d6)
// =============================================================================

func TestFormatPeers(t *testing.T) {
	cases := []struct {
		name  string
		peers []PeerInfo
		want  string
	}{
		{"nil", nil, ""},
		{"empty", []PeerInfo{}, ""},
		{
			"single with name and acp",
			[]PeerInfo{{ID: "sess-1", Name: "Driver", ACPServer: "auggie"}},
			"sess-1 (Driver) [auggie]",
		},
		{
			"single no-name",
			[]PeerInfo{{ID: "sess-1", ACPServer: "claude-code"}},
			"sess-1 [claude-code]",
		},
		{
			"single no-acp",
			[]PeerInfo{{ID: "sess-1", Name: "Fixer"}},
			"sess-1 (Fixer)",
		},
		{
			"bare id only",
			[]PeerInfo{{ID: "sess-1"}},
			"sess-1",
		},
		{
			"single with beads issue",
			[]PeerInfo{{ID: "sess-1", Name: "Driver", ACPServer: "auggie", BeadsIssue: "mitto-4d6"}},
			"sess-1 (Driver) [auggie] {mitto-4d6}",
		},
		{
			"bare id with beads issue",
			[]PeerInfo{{ID: "sess-1", BeadsIssue: "mitto-123"}},
			"sess-1 {mitto-123}",
		},
		{
			"multi mixed beads issue",
			[]PeerInfo{
				{ID: "sess-1", Name: "Fix mitto-4d6", ACPServer: "auggie", BeadsIssue: "mitto-4d6"},
				{ID: "sess-2", Name: "Implement mitto-abc", ACPServer: "claude-code"},
			},
			"sess-1 (Fix mitto-4d6) [auggie] {mitto-4d6}, sess-2 (Implement mitto-abc) [claude-code]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatPeers(tc.peers); got != tc.want {
				t.Errorf("FormatPeers() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPeersContext_AllText verifies that PeersContext.AllText() delegates to
// FormatPeers and produces the same byte-identical output as the template
// accessor { .Workspace.Peers.AllText }.
func TestPeersContext_AllText(t *testing.T) {
	pc := PeersContext{
		All: []PeerInfo{
			{ID: "s1", Name: "Peer A", ACPServer: "auggie", BeadsIssue: "mitto-1"},
			{ID: "s2", ACPServer: "claude-code"},
		},
	}
	want := "s1 (Peer A) [auggie] {mitto-1}, s2 [claude-code]"
	if got := pc.AllText(); got != want {
		t.Errorf("PeersContext.AllText() = %q, want %q", got, want)
	}

	// Zero value returns empty string.
	if got := (PeersContext{}).AllText(); got != "" {
		t.Errorf("zero PeersContext.AllText() = %q, want \"\"", got)
	}
}

// TestTemplateFuncs_WorkspacePeersAllText verifies the { .Workspace.Peers.AllText }
// template accessor renders correctly from a populated PromptEnabledContext.
func TestTemplateFuncs_WorkspacePeersAllText(t *testing.T) {
	ctx := &PromptEnabledContext{
		Workspace: WorkspaceContext{
			Peers: PeersContext{
				Count:  2,
				Exists: true,
				All: []PeerInfo{
					{ID: "s1", Name: "Driver", ACPServer: "auggie", BeadsIssue: "mitto-4d6"},
					{ID: "s2", Name: "Helper", ACPServer: "claude-code"},
				},
			},
		},
	}
	fm := BuildTemplateFuncMap(ctx)
	got, err := RenderPromptTemplate("t", `{{ .Workspace.Peers.AllText }}`, ctx, fm)
	if err != nil {
		t.Fatalf("Workspace.Peers.AllText render error: %v", err)
	}
	if want := "s1 (Driver) [auggie] {mitto-4d6}, s2 (Helper) [claude-code]"; got != want {
		t.Errorf("Workspace.Peers.AllText: got %q, want %q", got, want)
	}
}

// TestTemplateFuncs_WorkspacePeersEmpty verifies that { .Workspace.Peers.AllText }
// returns "" when the peers slice is nil or empty (zero-value safety).
func TestTemplateFuncs_WorkspacePeersEmpty(t *testing.T) {
	ctx := &PromptEnabledContext{}
	fm := BuildTemplateFuncMap(ctx)
	got, err := RenderPromptTemplate("t", `{{ .Workspace.Peers.AllText }}`, ctx, fm)
	if err != nil {
		t.Fatalf("zero-value Workspace.Peers.AllText render error: %v", err)
	}
	if got != "" {
		t.Errorf("zero-value Workspace.Peers.AllText: got %q, want \"\"", got)
	}
}

// =============================================================================
// cond/when tests (mitto-m7sb.12)
// =============================================================================

// TestCond_Parity asserts that direct CEL evaluation and {{ cond "expr" }} in a
// template produce the SAME bool for the same context.
func TestCond_Parity(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "present.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := &PromptEnabledContext{
		ACP:       ACPContext{Name: "auggie", Type: "augment"},
		Session:   SessionContext{IsChild: true},
		Workspace: WorkspaceContext{Folder: tmpDir},
		Tools:     NewReachableToolsContext([]string{"mitto_list", "jira_create"}),
	}

	e := newTestEvaluator(t)

	exprs := []string{
		"Session.IsChild",
		"!Session.IsChild",
		`ACP.MatchesServerType("augment")`,
		`ACP.MatchesServerType("claude")`,
		`FileExists("present.txt")`,
		`FileExists("absent.txt")`,
		`Tools.HasPattern("mitto_*")`,
		`Tools.HasPattern("notion_*")`,
	}

	for _, expr := range exprs {
		t.Run(expr, func(t *testing.T) {
			// Direct CEL evaluation.
			celResult := evalCEL(t, e, expr, ctx)

			// Template cond evaluation.
			body := fmt.Sprintf(`{{ if Cond %q }}yes{{ else }}no{{ end }}`, expr)
			got, err := RenderPromptTemplate("test", body, ctx, BuildTemplateFuncMap(ctx))
			if err != nil {
				t.Fatalf("render error: %v", err)
			}
			tmplResult := got == "yes"

			if celResult != tmplResult {
				t.Errorf("parity failure: CEL=%v template=%v for expr %q", celResult, tmplResult, expr)
			}
		})
	}
}

// TestCond_ArgsBranching verifies that the args CEL variable is accessible from
// cond expressions and that ctx.Args values flow through correctly.
func TestCond_ArgsBranching(t *testing.T) {
	// Use `"KEY" in Args && Args["KEY"] == "val"` — CEL map access throws on missing
	// keys (unlike Go's zero-value return), so the `in` guard prevents the error.

	// 1. Template branching via args.
	ctx := &PromptEnabledContext{
		Args: map[string]string{"MODE": "fast"},
	}
	fm := BuildTemplateFuncMap(ctx)

	// true branch: MODE == "fast" (key present and matches)
	body := `{{ if Cond "\"MODE\" in Args && Args[\"MODE\"] == \"fast\"" }}fast{{ else }}slow{{ end }}`
	got, err := RenderPromptTemplate("test", body, ctx, fm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "fast" {
		t.Errorf("expected %q, got %q", "fast", got)
	}

	// false branch: different MODE value (key present, value doesn't match)
	ctx2 := &PromptEnabledContext{Args: map[string]string{"MODE": "slow"}}
	got2, err := RenderPromptTemplate("test", body, ctx2, BuildTemplateFuncMap(ctx2))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got2 != "slow" {
		t.Errorf("expected %q, got %q", "slow", got2)
	}

	// false branch: empty Args map (key absent — short-circuit prevents subscript)
	ctx3 := &PromptEnabledContext{Args: map[string]string{}}
	got3, err := RenderPromptTemplate("test", body, ctx3, BuildTemplateFuncMap(ctx3))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got3 != "slow" {
		t.Errorf("expected %q, got %q", "slow", got3)
	}

	// 2. Direct CEL evaluation of "MODE" in Args (via newTestEvaluator).
	e := newTestEvaluator(t)
	ctxWithMode := &PromptEnabledContext{Args: map[string]string{"MODE": "fast"}}
	if !evalCEL(t, e, `"MODE" in Args`, ctxWithMode) {
		t.Error(`"MODE" in Args should be true when Args has MODE`)
	}
	ctxNoMode := &PromptEnabledContext{Args: map[string]string{}}
	if evalCEL(t, e, `"MODE" in Args`, ctxNoMode) {
		t.Error(`"MODE" in Args should be false when Args is empty`)
	}
	// nil Args normalizes to empty map — no panic.
	ctxNilArgs := &PromptEnabledContext{Args: nil}
	if evalCEL(t, e, `"MODE" in Args`, ctxNilArgs) {
		t.Error(`"MODE" in Args should be false when Args is nil`)
	}
}

// TestCond_ErrorPropagation verifies fail-closed: invalid CEL → non-nil render error.
func TestCond_ErrorPropagation(t *testing.T) {
	ctx := &PromptEnabledContext{}
	fm := BuildTemplateFuncMap(ctx)
	_, err := RenderPromptTemplate("t", `{{ Cond "this is ::: not valid CEL" }}`, ctx, fm)
	if err == nil {
		t.Fatal("expected non-nil error for invalid CEL expression, got nil")
	}
}

// TestCond_WhenAlias verifies that when is identical to cond.
func TestCond_WhenAlias(t *testing.T) {
	ctx := &PromptEnabledContext{}
	fm := BuildTemplateFuncMap(ctx)
	got, err := RenderPromptTemplate("test", `{{ if When "true" }}yes{{ else }}no{{ end }}`, ctx, fm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "yes" {
		t.Errorf("when alias: got %q, want %q", got, "yes")
	}
}

// TestCond_NilCtx verifies cond works when ctx is nil (Evaluate returns true,nil).
func TestCond_NilCtx(t *testing.T) {
	fm := BuildTemplateFuncMap(nil)
	got, err := RenderPromptTemplate("test", `{{ if Cond "true" }}ok{{ end }}`, nil, fm)
	if err != nil {
		t.Fatalf("unexpected error with nil ctx: %v", err)
	}
	if got != "ok" {
		t.Errorf("nil ctx cond: got %q, want %q", got, "ok")
	}
}

// TestBuildTemplateFuncMap_CondWhenKeysPresent verifies Cond and When are registered.
func TestBuildTemplateFuncMap_CondWhenKeysPresent(t *testing.T) {
	fm := BuildTemplateFuncMap(nil)
	if fm["Cond"] == nil {
		t.Error("FuncMap missing 'Cond'")
	}
	if fm["When"] == nil {
		t.Error("FuncMap missing 'When'")
	}
}

// installFakeBd writes a fake `bd` shell script to a fresh temp dir, prepends
// that dir to PATH for the duration of the test, and clears the beadsCache so
// results from other tests don't leak. The script's stdout comes from `stdout`
// and its exit code from `exitCode` (0 = success). Returns the temp dir.
func installFakeBd(t *testing.T, stdout string, exitCode int) string {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\ncat <<'MITTO_BD_EOF'\n%s\nMITTO_BD_EOF\nexit %d\n", stdout, exitCode)
	bdPath := filepath.Join(dir, "bd")
	if err := os.WriteFile(bdPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)
	// Clear the cache so a previous test's result doesn't shadow this one.
	beadsCacheMu.Lock()
	beadsCache = map[string]beadsCacheEntry{}
	beadsCacheMu.Unlock()
	return dir
}

// TestBeadsCount_EmptyResult verifies that a legitimate empty result (bd exit
// 0, `[]`) returns 0 — NOT the fail-open sentinel.
func TestBeadsCount_EmptyResult(t *testing.T) {
	installFakeBd(t, "[]", 0)
	tmp := t.TempDir()

	got := beadsCount(tmp, "support-question", "open,in_progress")
	if got != 0 {
		t.Errorf("beadsCount empty = %d, want 0", got)
	}
	if hasBeads(tmp, "support-question", "open,in_progress") {
		t.Errorf("hasBeads empty = true, want false")
	}
}

// TestBeadsCount_JSONParse verifies that a well-formed array is counted correctly.
func TestBeadsCount_JSONParse(t *testing.T) {
	installFakeBd(t, `[{"id":"mitto-1"},{"id":"mitto-2"},{"id":"mitto-3"}]`, 0)
	tmp := t.TempDir()

	got := beadsCount(tmp, "support-question", "open,in_progress")
	if got != 3 {
		t.Errorf("beadsCount = %d, want 3", got)
	}
	if !hasBeads(tmp, "support-question", "open,in_progress") {
		t.Errorf("hasBeads = false, want true")
	}
}

// TestBeadsCount_FailOpenOnNonZeroExit verifies that a non-zero exit code
// (e.g. not a beads repo) returns the positive sentinel so HasBeads is truthy.
func TestBeadsCount_FailOpenOnNonZeroExit(t *testing.T) {
	installFakeBd(t, "error: not a beads repo", 1)
	tmp := t.TempDir()

	got := beadsCount(tmp, "support-question", "open,in_progress")
	if got != beadsCountFailOpen {
		t.Errorf("beadsCount fail-open = %d, want %d", got, beadsCountFailOpen)
	}
	if !hasBeads(tmp, "support-question", "open,in_progress") {
		t.Errorf("hasBeads fail-open = false, want true")
	}
}

// TestBeadsCount_FailOpenOnBadJSON verifies that unparseable stdout returns
// the positive sentinel.
func TestBeadsCount_FailOpenOnBadJSON(t *testing.T) {
	installFakeBd(t, "not json at all {{{", 0)
	tmp := t.TempDir()

	got := beadsCount(tmp, "support-question", "open,in_progress")
	if got != beadsCountFailOpen {
		t.Errorf("beadsCount fail-open on bad json = %d, want %d", got, beadsCountFailOpen)
	}
}

// TestBeadsCount_FailOpenWhenMissing verifies that bd absent from PATH returns
// the positive sentinel (fail-open).
func TestBeadsCount_FailOpenWhenMissing(t *testing.T) {
	// Force an isolated PATH with no bd.
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)
	beadsCacheMu.Lock()
	beadsCache = map[string]beadsCacheEntry{}
	beadsCacheMu.Unlock()

	got := beadsCount(emptyDir, "support-question", "open,in_progress")
	if got != beadsCountFailOpen {
		t.Errorf("beadsCount missing bd = %d, want %d", got, beadsCountFailOpen)
	}
	if !hasBeads(emptyDir, "support-question", "open,in_progress") {
		t.Errorf("hasBeads missing bd = false, want true (fail-open)")
	}
}

// TestBeadsCount_Cache verifies that repeated calls within beadsCacheTTL hit
// the cache and don't re-exec bd. We swap the fake bd's script mid-test: the
// second call must still return the first (cached) value.
func TestBeadsCount_Cache(t *testing.T) {
	dir := installFakeBd(t, `[{"id":"a"},{"id":"b"}]`, 0)
	tmp := t.TempDir()

	first := beadsCount(tmp, "support-question", "open,in_progress")
	if first != 2 {
		t.Fatalf("first beadsCount = %d, want 2", first)
	}
	// Overwrite the fake bd to return a different count; the cache must mask this.
	bdPath := filepath.Join(dir, "bd")
	newScript := "#!/bin/sh\necho '[{\"id\":\"a\"},{\"id\":\"b\"},{\"id\":\"c\"},{\"id\":\"d\"}]'\n"
	if err := os.WriteFile(bdPath, []byte(newScript), 0755); err != nil {
		t.Fatal(err)
	}
	second := beadsCount(tmp, "support-question", "open,in_progress")
	if second != first {
		t.Errorf("second beadsCount = %d, want cached %d", second, first)
	}
}

// TestBeadsCount_CELParity verifies that HasBeads and BeadsCount evaluated
// through CEL produce the same result as the pure-Go helpers — mirrors the
// git-func parity tests (mitto-d01 pattern).
func TestBeadsCount_CELParity(t *testing.T) {
	installFakeBd(t, `[{"id":"mitto-1"},{"id":"mitto-2"}]`, 0)
	tmp := t.TempDir()

	e := newTestEvaluator(t)
	ctx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: tmp}}

	// BeadsCount(...) -> int; evaluate raw via cel.Program to compare Int values.
	ce, err := e.Compile(`BeadsCount("support-question", "open,in_progress")`)
	if err != nil {
		t.Fatalf("compile BeadsCount: %v", err)
	}
	out, _, err := ce.prog.Eval(buildActivation(ctx))
	if err != nil {
		t.Fatalf("eval BeadsCount: %v", err)
	}
	i, ok := out.Value().(int64)
	if !ok {
		t.Fatalf("BeadsCount result type = %T, want int64", out.Value())
	}
	goCount := beadsCount(tmp, "support-question", "open,in_progress")
	if int64(goCount) != i {
		t.Errorf("CEL BeadsCount = %d, go beadsCount = %d", i, goCount)
	}

	// HasBeads(...) -> bool through evalCEL.
	got := evalCEL(t, e, `HasBeads("support-question", "open,in_progress")`, ctx)
	if got != hasBeads(tmp, "support-question", "open,in_progress") {
		t.Errorf("CEL HasBeads = %v, go hasBeads mismatch", got)
	}
	if !got {
		t.Errorf("CEL HasBeads = false, want true (2 beads returned)")
	}

	// Combined expression: mirrors the real support-housekeeping gate.
	combined := evalCEL(t, e, `CommandExists("bd") && HasBeads("support-question", "open,in_progress")`, ctx)
	if !combined {
		t.Errorf("combined gate = false, want true")
	}
}

// TestBeadHasLabels_Match verifies that a bead whose labels contain ALL the
// requested labels returns true, and that a missing label returns false.
func TestBeadHasLabels_Match(t *testing.T) {
	installFakeBd(t, `{"id":"mitto-1","labels":["support","support-question","state:drafting"]}`, 0)
	tmp := t.TempDir()

	if !beadHasLabels(tmp, "mitto-1", "support-question,state:drafting") {
		t.Errorf("beadHasLabels all-present = false, want true")
	}
	if beadHasLabels(tmp, "mitto-1", "support-question,state:resolved") {
		t.Errorf("beadHasLabels missing-label = true, want false")
	}
}

// TestBeadHasLabels_EmptyIDOrLabels verifies fail-open on an empty id and
// "no requirement" on an empty labels list (both return true).
func TestBeadHasLabels_EmptyIDOrLabels(t *testing.T) {
	installFakeBd(t, `{"id":"mitto-1","labels":["support-question"]}`, 0)
	tmp := t.TempDir()

	if !beadHasLabels(tmp, "", "support-question") {
		t.Errorf("beadHasLabels empty id = false, want true (fail-open)")
	}
	if !beadHasLabels(tmp, "mitto-1", "") {
		t.Errorf("beadHasLabels empty labels = false, want true (no requirement)")
	}
}

// TestBeadHasLabels_FailOpenWhenMissing verifies that bd absent from PATH
// returns true (fail-open), so a gate never wrongly hides a prompt.
func TestBeadHasLabels_FailOpenWhenMissing(t *testing.T) {
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)
	beadsCacheMu.Lock()
	beadsCache = map[string]beadsCacheEntry{}
	beadsCacheMu.Unlock()

	if !beadHasLabels(emptyDir, "mitto-1", "support-question,state:drafting") {
		t.Errorf("beadHasLabels missing bd = false, want true (fail-open)")
	}
}

// TestBeadHasLabels_FailOpenOnBadJSON verifies fail-open (true) when bd emits
// unparseable JSON.
func TestBeadHasLabels_FailOpenOnBadJSON(t *testing.T) {
	installFakeBd(t, "not json at all {{{", 0)
	tmp := t.TempDir()

	if !beadHasLabels(tmp, "mitto-1", "support-question") {
		t.Errorf("beadHasLabels bad JSON = false, want true (fail-open)")
	}
}

// TestBeadHasLabels_CELParity verifies BeadHasLabels evaluated through CEL
// produces the same result as the pure-Go helper, exercising the macro +
// Session.BeadsIssue rewrite path (mirrors TestBeadsCount_CELParity).
func TestBeadHasLabels_CELParity(t *testing.T) {
	installFakeBd(t, `{"id":"mitto-1","labels":["support-question","state:drafting"]}`, 0)
	tmp := t.TempDir()

	e := newTestEvaluator(t)
	ctx := &PromptEnabledContext{
		Workspace: WorkspaceContext{Folder: tmp},
		Session:   SessionContext{HasBeadsIssue: true, BeadsIssue: "mitto-1"},
	}

	got := evalCEL(t, e, `BeadHasLabels(Session.BeadsIssue, "support-question,state:drafting")`, ctx)
	if got != beadHasLabels(tmp, "mitto-1", "support-question,state:drafting") {
		t.Errorf("CEL BeadHasLabels mismatch with go beadHasLabels")
	}
	if !got {
		t.Errorf("CEL BeadHasLabels = false, want true")
	}

	// Combined expression mirrors the real conversation/prompts gate branch.
	combined := evalCEL(t, e,
		`CommandExists("bd") && Session.HasBeadsIssue && BeadHasLabels(Session.BeadsIssue, "support-question,state:drafting")`,
		ctx)
	if !combined {
		t.Errorf("combined gate = false, want true")
	}
}

// TestBeadHasLabels_ArrayShape verifies parsing of the current `bd show --json`
// shape, a single-element ARRAY ([{...}]), not a bare object. Regression guard:
// an earlier version unmarshalled into a struct and would fail-open (return
// true) on the array, defeating the gate.
func TestBeadHasLabels_ArrayShape(t *testing.T) {
	installFakeBd(t, `[{"id":"mitto-1","status":"open","labels":["support-question","state:drafting"]}]`, 0)
	tmp := t.TempDir()

	if !beadHasLabels(tmp, "mitto-1", "support-question,state:drafting") {
		t.Errorf("beadHasLabels array-shape all-present = false, want true")
	}
	if beadHasLabels(tmp, "mitto-1", "state:resolved") {
		t.Errorf("beadHasLabels array-shape missing-label = true, want false")
	}
}

// TestBeadIsOpen_OpenAndClosed verifies beadIsOpen returns true for a non-closed
// bead and false for a closed one, across both the array and bare-object shapes.
func TestBeadIsOpen_OpenAndClosed(t *testing.T) {
	installFakeBd(t, `[{"id":"mitto-1","status":"open"}]`, 0)
	tmp := t.TempDir()
	if !beadIsOpen(tmp, "mitto-1") {
		t.Errorf("beadIsOpen open (array) = false, want true")
	}

	installFakeBd(t, `[{"id":"mitto-1","status":"closed"}]`, 0)
	tmp2 := t.TempDir()
	if beadIsOpen(tmp2, "mitto-1") {
		t.Errorf("beadIsOpen closed (array) = true, want false")
	}

	installFakeBd(t, `{"id":"mitto-1","status":"in_progress"}`, 0)
	tmp3 := t.TempDir()
	if !beadIsOpen(tmp3, "mitto-1") {
		t.Errorf("beadIsOpen in_progress (object) = false, want true")
	}
}

// TestBeadIsOpen_FailOpen verifies fail-open (true) on empty id, missing bd, and
// unparseable JSON.
func TestBeadIsOpen_FailOpen(t *testing.T) {
	installFakeBd(t, `[{"id":"mitto-1","status":"closed"}]`, 0)
	tmp := t.TempDir()
	if !beadIsOpen(tmp, "") {
		t.Errorf("beadIsOpen empty id = false, want true (fail-open)")
	}

	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)
	beadsCacheMu.Lock()
	beadsCache = map[string]beadsCacheEntry{}
	beadsCacheMu.Unlock()
	if !beadIsOpen(emptyDir, "mitto-1") {
		t.Errorf("beadIsOpen missing bd = false, want true (fail-open)")
	}

	installFakeBd(t, "not json {{{", 0)
	tmp2 := t.TempDir()
	if !beadIsOpen(tmp2, "mitto-1") {
		t.Errorf("beadIsOpen bad JSON = false, want true (fail-open)")
	}
}

// TestBeadIsOpen_CELParity verifies BeadIsOpen evaluated through CEL matches the
// pure-Go helper, exercising the macro + Session.BeadsIssue rewrite path.
func TestBeadIsOpen_CELParity(t *testing.T) {
	installFakeBd(t, `[{"id":"mitto-1","status":"closed","labels":["support-question"]}]`, 0)
	tmp := t.TempDir()

	e := newTestEvaluator(t)
	ctx := &PromptEnabledContext{
		Workspace: WorkspaceContext{Folder: tmp},
		Session:   SessionContext{HasBeadsIssue: true, BeadsIssue: "mitto-1"},
	}

	got := evalCEL(t, e, `BeadIsOpen(Session.BeadsIssue)`, ctx)
	if got != beadIsOpen(tmp, "mitto-1") {
		t.Errorf("CEL BeadIsOpen mismatch with go beadIsOpen")
	}
	if got {
		t.Errorf("CEL BeadIsOpen = true for closed bead, want false")
	}

	// Combined gate branch mirrors the check-status/investigate conversation menu.
	combined := evalCEL(t, e,
		`Session.HasBeadsIssue && BeadIsOpen(Session.BeadsIssue) && BeadHasLabels(Session.BeadsIssue, "support-question")`,
		ctx)
	if combined {
		t.Errorf("combined open+label gate = true for closed bead, want false")
	}
}

// TestBeadMetadata_PresentArrayShape verifies happy-path retrieval from the
// current `bd show --json` shape, a single-element ARRAY ([{...}]).
func TestBeadMetadata_PresentArrayShape(t *testing.T) {
	installFakeBd(t, `[{"id":"mitto-1","metadata":{"slack_channel":"C0TEST","other":"x"}}]`, 0)
	tmp := t.TempDir()

	if got := beadMetadata(tmp, "mitto-1", "slack_channel"); got != "C0TEST" {
		t.Errorf("beadMetadata array-shape slack_channel = %q, want %q", got, "C0TEST")
	}
}

// TestBeadMetadata_PresentObjectShape verifies parsing of the legacy bare
// object shape (`{...}`, older bd) — the extended bdBead struct piggybacks on
// parseBdShow's dual-shape tolerance.
func TestBeadMetadata_PresentObjectShape(t *testing.T) {
	installFakeBd(t, `{"id":"mitto-1","metadata":{"slack_channel":"C0LEG"}}`, 0)
	tmp := t.TempDir()

	if got := beadMetadata(tmp, "mitto-1", "slack_channel"); got != "C0LEG" {
		t.Errorf("beadMetadata object-shape slack_channel = %q, want %q", got, "C0LEG")
	}
}

// TestBeadMetadata_MissingKey verifies that a present metadata map without the
// requested key returns "" (fail-open / natural nil-map indexing semantics).
func TestBeadMetadata_MissingKey(t *testing.T) {
	installFakeBd(t, `[{"id":"mitto-1","metadata":{"other":"x"}}]`, 0)
	tmp := t.TempDir()

	if got := beadMetadata(tmp, "mitto-1", "slack_channel"); got != "" {
		t.Errorf("beadMetadata missing key = %q, want %q", got, "")
	}
}

// TestBeadMetadata_NullMetadata verifies that a null metadata field decodes to
// a nil map and returns "" (nil-map indexing is safe).
func TestBeadMetadata_NullMetadata(t *testing.T) {
	installFakeBd(t, `[{"id":"mitto-1","metadata":null}]`, 0)
	tmp := t.TempDir()

	if got := beadMetadata(tmp, "mitto-1", "slack_channel"); got != "" {
		t.Errorf("beadMetadata null metadata = %q, want %q", got, "")
	}
}

// TestBeadMetadata_NoMetadataField verifies that a bead JSON with no metadata
// field at all (as real bd currently emits — see AGENTS.md observations) still
// returns "" without erroring.
func TestBeadMetadata_NoMetadataField(t *testing.T) {
	installFakeBd(t, `[{"id":"mitto-1","status":"open","labels":["support-question"]}]`, 0)
	tmp := t.TempDir()

	if got := beadMetadata(tmp, "mitto-1", "slack_channel"); got != "" {
		t.Errorf("beadMetadata absent metadata field = %q, want %q", got, "")
	}
}

// TestBeadMetadata_EmptyID verifies fail-open on an empty id (skips exec).
func TestBeadMetadata_EmptyID(t *testing.T) {
	installFakeBd(t, `[{"id":"mitto-1","metadata":{"slack_channel":"C0TEST"}}]`, 0)
	tmp := t.TempDir()

	if got := beadMetadata(tmp, "", "slack_channel"); got != "" {
		t.Errorf("beadMetadata empty id = %q, want %q (fail-open)", got, "")
	}
	if got := beadMetadata(tmp, "   ", "slack_channel"); got != "" {
		t.Errorf("beadMetadata whitespace id = %q, want %q (fail-open)", got, "")
	}
}

// TestBeadMetadata_FailOpenWhenBdMissing verifies fail-open ("") when bd is
// absent from PATH — mirrors TestBeadHasLabels_FailOpenWhenMissing.
func TestBeadMetadata_FailOpenWhenBdMissing(t *testing.T) {
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)
	beadsCacheMu.Lock()
	beadsCache = map[string]beadsCacheEntry{}
	beadsStrCache = map[string]beadsStrCacheEntry{}
	beadsCacheMu.Unlock()

	if got := beadMetadata(emptyDir, "mitto-1", "slack_channel"); got != "" {
		t.Errorf("beadMetadata missing bd = %q, want %q (fail-open)", got, "")
	}
}

// TestBeadMetadata_FailOpenOnBadJSON verifies fail-open ("") when bd emits
// unparseable stdout.
func TestBeadMetadata_FailOpenOnBadJSON(t *testing.T) {
	installFakeBd(t, "not json at all {{{", 0)
	tmp := t.TempDir()

	if got := beadMetadata(tmp, "mitto-1", "slack_channel"); got != "" {
		t.Errorf("beadMetadata bad JSON = %q, want %q (fail-open)", got, "")
	}
}

// TestBeadMetadata_Caching verifies repeated in-window calls return the same
// value without re-execing bd. Mirrors TestBeadsCount_Cache: install one fake
// stdout, take the first value, swap the fake mid-test, verify the second
// call still returns the cached first value.
func TestBeadMetadata_Caching(t *testing.T) {
	dir := installFakeBd(t, `[{"id":"mitto-1","metadata":{"slack_channel":"C0FIRST"}}]`, 0)
	tmp := t.TempDir()

	first := beadMetadata(tmp, "mitto-1", "slack_channel")
	if first != "C0FIRST" {
		t.Fatalf("first beadMetadata = %q, want %q", first, "C0FIRST")
	}

	// Swap the fake bd's stdout in place.
	bdPath := filepath.Join(dir, "bd")
	newScript := "#!/bin/sh\ncat <<'MITTO_BD_EOF'\n[{\"id\":\"mitto-1\",\"metadata\":{\"slack_channel\":\"C0SECOND\"}}]\nMITTO_BD_EOF\nexit 0\n"
	if err := os.WriteFile(bdPath, []byte(newScript), 0755); err != nil {
		t.Fatal(err)
	}

	second := beadMetadata(tmp, "mitto-1", "slack_channel")
	if second != first {
		t.Errorf("cached beadMetadata second call = %q, want %q (cache miss?)", second, first)
	}
}

// TestBeadMetadata_TemplateFuncRender verifies BeadMetadata renders through
// RenderPromptTemplate — the actual production usage path (a support prompt
// falls back to it when SlackChannelID was not passed at spawn time).
func TestBeadMetadata_TemplateFuncRender(t *testing.T) {
	installFakeBd(t, `[{"id":"mitto-1","metadata":{"slack_channel":"C0TMPL"}}]`, 0)
	tmp := t.TempDir()

	ctx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: tmp}}
	fm := BuildTemplateFuncMap(ctx)

	body := `{{- $c := "" -}}{{- if eq $c "" -}}{{- $c = BeadMetadata "mitto-1" "slack_channel" -}}{{- end -}}channel={{ $c }}`
	got, err := RenderPromptTemplate("test", body, ctx, fm)
	if err != nil {
		t.Fatalf("render BeadMetadata: %v", err)
	}
	if got != "channel=C0TMPL" {
		t.Errorf("BeadMetadata render = %q, want %q", got, "channel=C0TMPL")
	}
}

// TestBeadsCount_TemplateFuncRender verifies BeadsCount/HasBeads render through
// RenderPromptTemplate (mirrors TestBuildTemplateFuncMap_GitFuncsRenderSmoke).
func TestBeadsCount_TemplateFuncRender(t *testing.T) {
	installFakeBd(t, `[{"id":"mitto-1"}]`, 0)
	tmp := t.TempDir()

	ctx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: tmp}}
	fm := BuildTemplateFuncMap(ctx)

	got, err := RenderPromptTemplate("test", `{{ if HasBeads "support-question" "open,in_progress" }}yes{{ else }}no{{ end }}`, ctx, fm)
	if err != nil {
		t.Fatalf("render HasBeads: %v", err)
	}
	if got != "yes" {
		t.Errorf("HasBeads render = %q, want %q", got, "yes")
	}

	got, err = RenderPromptTemplate("test", `count={{ BeadsCount "support-question" "open,in_progress" }}`, ctx, fm)
	if err != nil {
		t.Fatalf("render BeadsCount: %v", err)
	}
	if got != "count=1" {
		t.Errorf("BeadsCount render = %q, want %q", got, "count=1")
	}
}

// TestBuildTemplateFuncMap_PromptText verifies the PromptText template function
// (mitto-85y.3): resolves a prompt NAME to its full body via an injected
// resolver, fails-closed on nil resolver / empty name / unknown prompt, and
// strips trailing newlines from the returned body.
func TestBuildTemplateFuncMap_PromptText(t *testing.T) {
	// Fake resolver returns a fixed body for "known" and errors for anything else.
	resolver := func(name string) (string, error) {
		if name == "known" {
			return "body-A", nil
		}
		return "", fmt.Errorf("prompt %q not found", name)
	}

	t.Run("resolves known prompt", func(t *testing.T) {
		ctx := &PromptEnabledContext{PromptTextResolver: resolver}
		fm := BuildTemplateFuncMap(ctx)
		got, err := RenderPromptTemplate("test", `{{ PromptText "known" }}`, ctx, fm)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if got != "body-A" {
			t.Errorf("got %q, want %q", got, "body-A")
		}
	})

	t.Run("unknown prompt fails render", func(t *testing.T) {
		ctx := &PromptEnabledContext{PromptTextResolver: resolver}
		fm := BuildTemplateFuncMap(ctx)
		_, err := RenderPromptTemplate("test", `{{ PromptText "unknown" }}`, ctx, fm)
		if err == nil {
			t.Fatalf("expected error for unknown prompt, got nil")
		}
		if !strings.Contains(err.Error(), "unknown") {
			t.Errorf("error should mention the prompt name; got %v", err)
		}
	})

	t.Run("trailing newline is stripped", func(t *testing.T) {
		trailingResolver := func(name string) (string, error) {
			return "body-with-newline\n", nil
		}
		ctx := &PromptEnabledContext{PromptTextResolver: trailingResolver}
		fm := BuildTemplateFuncMap(ctx)
		got, err := RenderPromptTemplate("test", `{{ PromptText "x" }}`, ctx, fm)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if got != "body-with-newline" {
			t.Errorf("got %q, want %q", got, "body-with-newline")
		}
	})

	t.Run("nil resolver fails-closed", func(t *testing.T) {
		ctx := &PromptEnabledContext{PromptTextResolver: nil}
		fm := BuildTemplateFuncMap(ctx)
		_, err := RenderPromptTemplate("test", `{{ PromptText "x" }}`, ctx, fm)
		if err == nil {
			t.Fatalf("expected error for nil resolver, got nil")
		}
		if !strings.Contains(err.Error(), "no resolver") {
			t.Errorf("error should mention 'no resolver'; got %v", err)
		}
	})

	t.Run("empty name fails-closed", func(t *testing.T) {
		ctx := &PromptEnabledContext{PromptTextResolver: resolver}
		fm := BuildTemplateFuncMap(ctx)
		_, err := RenderPromptTemplate("test", `{{ PromptText "" }}`, ctx, fm)
		if err == nil {
			t.Fatalf("expected error for empty name, got nil")
		}
		if !strings.Contains(err.Error(), "empty prompt name") {
			t.Errorf("error should mention 'empty prompt name'; got %v", err)
		}
	})
}

// TestBuildTemplateFuncMap_Dict verifies the `dict` helper builds a
// map[string]any from alternating key/value pairs (the well-known Sprig
// signature) and rejects odd-arity/bad-key calls. Shared support fragments
// rely on this to pass structured arguments through the single-value
// `{{ template "name" X }}` call syntax.
func TestBuildTemplateFuncMap_Dict(t *testing.T) {
	ctx := &PromptEnabledContext{}
	fm := BuildTemplateFuncMap(ctx)

	// Happy path: even number of args, string keys, mixed value types.
	got, err := RenderPromptTemplate("dict-happy",
		`{{ $d := dict "Name" "alice" "N" 3 }}{{ $d.Name }}={{ $d.N }}`, nil, fm)
	if err != nil {
		t.Fatalf("dict happy path: %v", err)
	}
	if got != "alice=3" {
		t.Errorf("dict happy path = %q, want %q", got, "alice=3")
	}

	// Empty dict.
	got, err = RenderPromptTemplate("dict-empty", `{{ len (dict) }}`, nil, fm)
	if err != nil {
		t.Fatalf("dict empty: %v", err)
	}
	if got != "0" {
		t.Errorf("dict empty len = %q, want %q", got, "0")
	}

	// Odd arity must surface as an execute error.
	_, err = RenderPromptTemplate("dict-odd", `{{ dict "K" }}`, nil, fm)
	if err == nil {
		t.Fatalf("dict odd arity: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "odd number of arguments") {
		t.Errorf("dict odd arity error should mention 'odd number of arguments'; got %v", err)
	}

	// Non-string key must surface as an execute error.
	_, err = RenderPromptTemplate("dict-badkey", `{{ dict 1 "v" }}`, nil, fm)
	if err == nil {
		t.Fatalf("dict bad key: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "want string") {
		t.Errorf("dict bad key error should mention 'want string'; got %v", err)
	}
}
