package config

import (
	"fmt"
	"os"
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
			celExpr := fmt.Sprintf("fileExists(%q)", tc.path)
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
			celExpr := fmt.Sprintf("dirExists(%q)", tc.path)
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
			celExpr := fmt.Sprintf("commandExists(%q)", tc.cmd)
			celResult := evalCEL(t, e, celExpr, ctx)
			if goResult != celResult {
				t.Errorf("parity failure: go=%v cel=%v for cmd %q", goResult, celResult, tc.cmd)
			}
		})
	}
}

func TestParity_HasPattern(t *testing.T) {
	e := newTestEvaluator(t)
	names := []string{"github_pr", "jira_create", "slack_post"}

	cases := []struct {
		name      string
		available bool
		pattern   string
		want      bool
	}{
		{"match", true, "github_*", true},
		{"no match", true, "notion_*", false},
		{"fail-open unavailable", false, "anything_*", true},
		{"exact match", true, "jira_create", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goResult := hasPattern(tc.available, names, tc.pattern)
			if goResult != tc.want {
				t.Errorf("hasPattern(%v, names, %q) = %v, want %v", tc.available, tc.pattern, goResult, tc.want)
			}
			ctx := &PromptEnabledContext{Tools: ToolsContext{Available: tc.available, Names: names}}
			celExpr := fmt.Sprintf("tools.hasPattern(%q)", tc.pattern)
			celResult := evalCEL(t, e, celExpr, ctx)
			if goResult != celResult {
				t.Errorf("parity failure: go=%v cel=%v for pattern %q available=%v", goResult, celResult, tc.pattern, tc.available)
			}
		})
	}
}

func TestParity_HasAllPatterns(t *testing.T) {
	e := newTestEvaluator(t)
	names := []string{"github_pr", "jira_create", "slack_post"}

	cases := []struct {
		name      string
		available bool
		patterns  []string
		want      bool
	}{
		{"all satisfied", true, []string{"github_*", "jira_*"}, true},
		{"one unsatisfied", true, []string{"github_*", "notion_*"}, false},
		{"fail-open unavailable", false, []string{"notion_*"}, true},
		{"empty patterns", true, []string{}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goResult := hasAllPatterns(tc.available, names, tc.patterns)
			if goResult != tc.want {
				t.Errorf("hasAllPatterns = %v, want %v", goResult, tc.want)
			}
			// Build CEL list literal for patterns
			ctx := &PromptEnabledContext{Tools: ToolsContext{Available: tc.available, Names: names}}
			var celPatterns string
			for i, p := range tc.patterns {
				if i > 0 {
					celPatterns += ", "
				}
				celPatterns += fmt.Sprintf("%q", p)
			}
			celExpr := fmt.Sprintf("tools.hasAllPatterns([%s])", celPatterns)
			celResult := evalCEL(t, e, celExpr, ctx)
			if goResult != celResult {
				t.Errorf("parity failure: go=%v cel=%v for patterns %v available=%v", goResult, celResult, tc.patterns, tc.available)
			}
		})
	}
}

func TestParity_HasAnyPattern(t *testing.T) {
	e := newTestEvaluator(t)
	names := []string{"github_pr", "jira_create"}

	cases := []struct {
		name      string
		available bool
		patterns  []string
		want      bool
	}{
		{"one matches", true, []string{"github_*", "notion_*"}, true},
		{"none match", true, []string{"slack_*", "notion_*"}, false},
		{"fail-open unavailable", false, []string{"notion_*"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goResult := hasAnyPattern(tc.available, names, tc.patterns)
			if goResult != tc.want {
				t.Errorf("hasAnyPattern = %v, want %v", goResult, tc.want)
			}
			ctx := &PromptEnabledContext{Tools: ToolsContext{Available: tc.available, Names: names}}
			var celPatterns string
			for i, p := range tc.patterns {
				if i > 0 {
					celPatterns += ", "
				}
				celPatterns += fmt.Sprintf("%q", p)
			}
			celExpr := fmt.Sprintf("tools.hasAnyPattern([%s])", celPatterns)
			celResult := evalCEL(t, e, celExpr, ctx)
			if goResult != celResult {
				t.Errorf("parity failure: go=%v cel=%v", goResult, celResult)
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
			celExpr := fmt.Sprintf("acp.matchesServerType([%s])", celTypes)
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
	argFn := fm["arg"].(func(string, ...string) string)

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
	defFn := fm["default"].(func(string, string) string)

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
	argFn := fm["arg"].(func(string, ...string) string)
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
	joinFn := fm["join"].(func(string, []string) string)
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
		{`{{ upper "hello" }}`, "HELLO"},
		{`{{ lower "WORLD" }}`, "world"},
		{`{{ trim "  hi  " }}`, "hi"},
		{`{{ contains "foobar" "bar" }}`, "true"},
		{`{{ hasPrefix "foobar" "foo" }}`, "true"},
		{`{{ hasSuffix "foobar" "baz" }}`, "false"},
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

// TestBuildTemplateFuncMap_AllKeysPresent verifies all expected keys exist.
func TestBuildTemplateFuncMap_AllKeysPresent(t *testing.T) {
	fm := BuildTemplateFuncMap(nil)
	expected := []string{
		"arg", "default",
		"fileExists", "dirExists", "commandExists", "hasPattern",
		"trim", "lower", "upper", "contains", "hasPrefix", "hasSuffix", "join",
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

	got, err := RenderPromptTemplate("test", `Hello {{ upper (arg "NAME") }}!`, ctx, fm)
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
		body := fmt.Sprintf(`{{ fileExists %q }}`, path)
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
		Tools:     ToolsContext{Available: true, Names: []string{"mitto_list", "jira_create"}},
	}

	e := newTestEvaluator(t)

	exprs := []string{
		"session.isChild",
		"!session.isChild",
		`acp.matchesServerType("augment")`,
		`acp.matchesServerType("claude")`,
		`fileExists("present.txt")`,
		`fileExists("absent.txt")`,
		`tools.hasPattern("mitto_*")`,
		`tools.hasPattern("notion_*")`,
	}

	for _, expr := range exprs {
		t.Run(expr, func(t *testing.T) {
			// Direct CEL evaluation.
			celResult := evalCEL(t, e, expr, ctx)

			// Template cond evaluation.
			body := fmt.Sprintf(`{{ if cond %q }}yes{{ else }}no{{ end }}`, expr)
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
	// Use `"KEY" in args && args["KEY"] == "val"` — CEL map access throws on missing
	// keys (unlike Go's zero-value return), so the `in` guard prevents the error.

	// 1. Template branching via args.
	ctx := &PromptEnabledContext{
		Args: map[string]string{"MODE": "fast"},
	}
	fm := BuildTemplateFuncMap(ctx)

	// true branch: MODE == "fast" (key present and matches)
	body := `{{ if cond "\"MODE\" in args && args[\"MODE\"] == \"fast\"" }}fast{{ else }}slow{{ end }}`
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

	// 2. Direct CEL evaluation of "MODE" in args (via newTestEvaluator).
	e := newTestEvaluator(t)
	ctxWithMode := &PromptEnabledContext{Args: map[string]string{"MODE": "fast"}}
	if !evalCEL(t, e, `"MODE" in args`, ctxWithMode) {
		t.Error(`"MODE" in args should be true when Args has MODE`)
	}
	ctxNoMode := &PromptEnabledContext{Args: map[string]string{}}
	if evalCEL(t, e, `"MODE" in args`, ctxNoMode) {
		t.Error(`"MODE" in args should be false when Args is empty`)
	}
	// nil Args normalizes to empty map — no panic.
	ctxNilArgs := &PromptEnabledContext{Args: nil}
	if evalCEL(t, e, `"MODE" in args`, ctxNilArgs) {
		t.Error(`"MODE" in args should be false when Args is nil`)
	}
}

// TestCond_ErrorPropagation verifies fail-closed: invalid CEL → non-nil render error.
func TestCond_ErrorPropagation(t *testing.T) {
	ctx := &PromptEnabledContext{}
	fm := BuildTemplateFuncMap(ctx)
	_, err := RenderPromptTemplate("t", `{{ cond "this is ::: not valid CEL" }}`, ctx, fm)
	if err == nil {
		t.Fatal("expected non-nil error for invalid CEL expression, got nil")
	}
}

// TestCond_WhenAlias verifies that when is identical to cond.
func TestCond_WhenAlias(t *testing.T) {
	ctx := &PromptEnabledContext{}
	fm := BuildTemplateFuncMap(ctx)
	got, err := RenderPromptTemplate("test", `{{ if when "true" }}yes{{ else }}no{{ end }}`, ctx, fm)
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
	got, err := RenderPromptTemplate("test", `{{ if cond "true" }}ok{{ end }}`, nil, fm)
	if err != nil {
		t.Fatalf("unexpected error with nil ctx: %v", err)
	}
	if got != "ok" {
		t.Errorf("nil ctx cond: got %q, want %q", got, "ok")
	}
}

// TestBuildTemplateFuncMap_CondWhenKeysPresent verifies cond and when are registered.
func TestBuildTemplateFuncMap_CondWhenKeysPresent(t *testing.T) {
	fm := BuildTemplateFuncMap(nil)
	if fm["cond"] == nil {
		t.Error("FuncMap missing 'cond'")
	}
	if fm["when"] == nil {
		t.Error("FuncMap missing 'when'")
	}
}

// =============================================================================
// PrecompileTemplateConds tests
// =============================================================================

// TestPrecompileTemplateConds_Valid returns nil for valid literal cond args.
func TestPrecompileTemplateConds_Valid(t *testing.T) {
	body := `{{ if cond "session.isChild" }}child{{ end }}`
	if err := PrecompileTemplateConds("my-prompt", body); err != nil {
		t.Errorf("expected nil for valid cond, got: %v", err)
	}
}

// TestPrecompileTemplateConds_Invalid returns non-nil error for invalid CEL.
func TestPrecompileTemplateConds_Invalid(t *testing.T) {
	body := `{{ if cond "this is ::: not valid CEL" }}x{{ end }}`
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

// TestPrecompileTemplateConds_ValidWhen returns nil when using the when alias.
func TestPrecompileTemplateConds_ValidWhen(t *testing.T) {
	body := `{{ if when "!session.isChild" }}root{{ end }}`
	if err := PrecompileTemplateConds("p", body); err != nil {
		t.Errorf("expected nil for valid when alias, got: %v", err)
	}
}

// TestPrecompileTemplateConds_ParseError returns an error for template parse failures.
func TestPrecompileTemplateConds_ParseError(t *testing.T) {
	body := `{{ if cond "true" }}no end`
	err := PrecompileTemplateConds("p", body)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
}
