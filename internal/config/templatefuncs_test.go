package config

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
		"FileExists", "DirExists", "CommandExists", "HasPattern", "Model",
		"GitFileModified", "GitDirModified", "GitFileTracked", "GitFileDeleted",
		"BeadsCount", "HasBeads",
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

// =============================================================================
// PrecompileTemplateConds tests
// =============================================================================

// TestPrecompileTemplateConds_Valid returns nil for valid literal Cond args.
func TestPrecompileTemplateConds_Valid(t *testing.T) {
	body := `{{ if Cond "Session.IsChild" }}child{{ end }}`
	if err := PrecompileTemplateConds("my-prompt", body); err != nil {
		t.Errorf("expected nil for valid cond, got: %v", err)
	}
}

// TestPrecompileTemplateConds_Invalid returns non-nil error for invalid CEL.
func TestPrecompileTemplateConds_Invalid(t *testing.T) {
	body := `{{ if Cond "this is ::: not valid CEL" }}x{{ end }}`
	err := PrecompileTemplateConds("my-prompt", body)
	if err == nil {
		t.Fatal("expected non-nil error for invalid CEL literal, got nil")
	}
	// Error message must include prompt name and "cond precompile".
	if !strings.Contains(err.Error(), "my-prompt") {
		t.Errorf("error missing prompt name: %v", err)
	}
	if !strings.Contains(err.Error(), "cond precompile") {
		t.Errorf("error missing 'cond precompile': %v", err)
	}
}

// TestPrecompileTemplateConds_NoTemplate returns nil for bodies without {{}}.
func TestPrecompileTemplateConds_NoTemplate(t *testing.T) {
	if err := PrecompileTemplateConds("p", "plain text ${VAR} @mitto:x"); err != nil {
		t.Errorf("expected nil for no-template body, got: %v", err)
	}
}

// TestPrecompileTemplateConds_ValidWhen returns nil when using the When alias.
func TestPrecompileTemplateConds_ValidWhen(t *testing.T) {
	body := `{{ if When "!Session.IsChild" }}root{{ end }}`
	if err := PrecompileTemplateConds("p", body); err != nil {
		t.Errorf("expected nil for valid when alias, got: %v", err)
	}
}

// TestPrecompileTemplateConds_ParseError returns an error for template parse failures.
func TestPrecompileTemplateConds_ParseError(t *testing.T) {
	body := `{{ if Cond "true" }}no end`
	err := PrecompileTemplateConds("p", body)
	if err == nil {
		t.Fatal("expected parse error, got nil")
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
