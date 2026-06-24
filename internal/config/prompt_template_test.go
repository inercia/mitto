package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
	"time"
)

// TestHasTemplateSyntax verifies the fast-path predicate.
func TestHasTemplateSyntax(t *testing.T) {
	tests := []struct {
		body string
		want bool
	}{
		{"plain text", false},
		{"${VAR} @mitto:session_id", false},
		{"has {{ .Name }} inside", true},
		{"{{- trim -}}", true},
		{"", false},
	}
	for _, tc := range tests {
		if got := HasTemplateSyntax(tc.body); got != tc.want {
			t.Errorf("HasTemplateSyntax(%q) = %v, want %v", tc.body, got, tc.want)
		}
	}
}

// TestRenderPromptTemplate covers all required cases.
func TestRenderPromptTemplate(t *testing.T) {
	type item struct{ ID string }
	type ctx struct {
		Name  string
		Flag  bool
		M     map[string]string
		Items []item
	}

	tests := []struct {
		name    string
		body    string
		data    any
		funcs   template.FuncMap
		want    string
		wantErr string // non-empty: expect an error whose message contains this substring
	}{
		// 1. No-template passthrough — body without {{ returned byte-for-byte unchanged.
		{
			name: "passthrough-plain",
			body: "Hello world",
			data: ctx{Name: "Alice"},
			want: "Hello world",
		},
		{
			name: "passthrough-dollar-var",
			body: "Value is ${VAR}",
			data: ctx{},
			want: "Value is ${VAR}",
		},
		{
			name: "passthrough-mitto",
			body: "Session: @mitto:session_id",
			data: ctx{},
			want: "Session: @mitto:session_id",
		},

		// 2. Simple struct field.
		{
			name: "struct-field",
			body: "Hello {{ .Name }}",
			data: ctx{Name: "Alice"},
			want: "Hello Alice",
		},

		// 3. Map field access.
		{
			name: "map-field",
			body: "Branch: {{ .M.branch }}",
			data: ctx{M: map[string]string{"branch": "main"}},
			want: "Branch: main",
		},

		// 4a. if branch true.
		{
			name: "if-true",
			body: "{{ if .Flag }}A{{ else }}B{{ end }}",
			data: ctx{Flag: true},
			want: "A",
		},
		// 4b. if branch false.
		{
			name: "if-false",
			body: "{{ if .Flag }}A{{ else }}B{{ end }}",
			data: ctx{Flag: false},
			want: "B",
		},

		// 5. Range over a slice.
		{
			name: "range-slice",
			body: "{{ range .Items }}{{ .ID }} {{ end }}",
			data: ctx{Items: []item{{"x"}, {"y"}, {"z"}}},
			want: "x y z ",
		},

		// 6. Whitespace trimming with {{- and -}}.
		{
			name: "whitespace-trim",
			body: "before\n{{- \" mid \" -}}\nafter",
			data: nil,
			want: "before mid after",
		},

		// 7. Literal double-brace escaping via {{ "{{" }} and {{ "}}" }}.
		{
			name: "literal-double-brace",
			body: `{{ "{{" }} x {{ "}}" }}`,
			data: nil,
			want: "{{ x }}",
		},

		// 8. Parse error: missing {{ end }}.
		{
			name:    "parse-error-missing-end",
			body:    "{{ if .Flag }}oops",
			data:    ctx{Flag: true},
			wantErr: "parse error",
		},
		// 8b. Parse error: {{ fi }} is not valid Go template syntax.
		{
			name:    "parse-error-fi",
			body:    "{{ if .Flag }}A{{ fi }}",
			data:    ctx{Flag: true},
			wantErr: "parse error",
		},

		// 9. Exec error: func that returns an error.
		{
			name: "exec-error-func",
			body: "{{ boom . }}",
			data: ctx{Name: "x"},
			funcs: template.FuncMap{
				"boom": func(_ any) (string, error) { return "", errBoom },
			},
			wantErr: "render error",
		},

		// 10. missingkey=zero: absent map key renders as "" not "<no value>".
		{
			name: "missingkey-zero",
			body: "val=|{{ .M.absent }}|",
			data: ctx{M: map[string]string{"other": "x"}},
			want: "val=||",
		},

		// 11a. Custom func invocation.
		{
			name:  "custom-func",
			body:  "{{ upper .Name }}",
			data:  ctx{Name: "hello"},
			funcs: template.FuncMap{"upper": strings.ToUpper},
			want:  "HELLO",
		},
		// 11b. nil funcs is safe for a no-func template.
		{
			name:  "nil-funcs-safe",
			body:  "{{ .Name }}",
			data:  ctx{Name: "ok"},
			funcs: nil,
			want:  "ok",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RenderPromptTemplate("test-prompt", tc.body, tc.data, tc.funcs)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (output=%q)", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				if got != "" {
					t.Errorf("on error want empty output, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// errBoom is a sentinel error for test case 9.
var errBoom = fmt.Errorf("boom")

// TestDeprecatedMittoVars covers DeprecatedMittoVars detection logic.
func TestDeprecatedMittoVars(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string // nil means expect nil/empty
	}{
		{
			name: "fast path no @mitto",
			body: "plain text",
			want: nil,
		},
		{
			name: "session_id is migratable",
			body: "id @mitto:session_id",
			want: []string{"session_id"},
		},
		{
			name: "keep-list excluded — children",
			body: "@mitto:children",
			want: nil,
		},
		{
			name: "keep-list excluded — available_acp_servers",
			body: "@mitto:available_acp_servers",
			want: nil,
		},
		{
			name: "keep-list excluded — mcp_children",
			body: "@mitto:mcp_children",
			want: nil,
		},
		{
			name: "keep-list excluded — user_data",
			body: "@mitto:user_data @mitto:user_data_schema",
			want: nil,
		},
		{
			name: "mixed — migratable and keep-list",
			body: "@mitto:session_id and @mitto:children",
			want: []string{"session_id"},
		},
		{
			name: "escaped ignored",
			body: `\@mitto:session_id`,
			want: nil,
		},
		{
			name: "longest-token — parent_session_id not parent",
			body: "@mitto:parent_session_id",
			want: []string{"parent_session_id"},
		},
		{
			name: "parent token",
			body: "@mitto:parent is the parent",
			want: []string{"parent"},
		},
		{
			name: "mcp_children_count migratable vs mcp_children keep",
			body: "@mitto:mcp_children_count @mitto:mcp_children",
			want: []string{"mcp_children_count"},
		},
		{
			name: "sorted+unique — working_dir and session_id deduplicated",
			body: "@mitto:working_dir @mitto:session_id @mitto:session_id",
			want: []string{"session_id", "working_dir"},
		},
		{
			name: "periodic_forced before periodic",
			body: "@mitto:periodic_forced and @mitto:periodic",
			want: []string{"periodic", "periodic_forced"},
		},
		{
			name: "all migratable tokens",
			body: "@mitto:session_id @mitto:parent_session_id @mitto:parent @mitto:session_name @mitto:working_dir @mitto:acp_server @mitto:workspace_uuid @mitto:beads_issue @mitto:mcp_children_count @mitto:periodic @mitto:periodic_forced",
			want: []string{"acp_server", "beads_issue", "mcp_children_count", "parent", "parent_session_id", "periodic", "periodic_forced", "session_id", "session_name", "working_dir", "workspace_uuid"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DeprecatedMittoVars(tc.body)
			if len(got) == 0 && len(tc.want) == 0 {
				return // both nil/empty — pass
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got[%d]=%q, want %q (full: got %v, want %v)", i, got[i], tc.want[i], got, tc.want)
				}
			}
		})
	}
}

// TestDeprecatedMittoVarReplacement verifies the replacement lookup.
func TestDeprecatedMittoVarReplacement(t *testing.T) {
	if r := DeprecatedMittoVarReplacement("session_id"); r != "{{ .Session.ID }}" {
		t.Errorf("session_id replacement = %q", r)
	}
	if r := DeprecatedMittoVarReplacement("children"); r != "" {
		t.Errorf("keep-list token should return empty, got %q", r)
	}
	if r := DeprecatedMittoVarReplacement("unknown_xyz"); r != "" {
		t.Errorf("unknown token should return empty, got %q", r)
	}
}

// TestIterateUntilComplete_TargetResolution tests the three target-bead resolution
// branches of beads-issue-iterate-until-complete.prompt.yaml:
//
//	(a) .Session.BeadsIssue set  → preferred source, shown in rendered output
//	(b) .Args.IssueID set only   → fallback argument source
//	(c) neither set              → inference instruction text appears; no empty
//	    "bd show " commands rendered
//
// The test loads the file from the real builtin directory so it always exercises
// the current on-disk content. It is in the config package to avoid an import
// cycle (config ← processors ← config) and to reuse BuildTemplateFuncMap directly.
func TestIterateUntilComplete_TargetResolution(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issue-iterate-until-complete.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issue-iterate-until-complete.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	body := prompt.Content

	render := func(ctx *PromptEnabledContext) string {
		funcs := BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-issue-iterate-until-complete", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (a) BeadsIssue set — preferred source.
	ctxA := &PromptEnabledContext{
		Session: SessionContext{
			BeadsIssue:    "mitto-abc",
			HasBeadsIssue: true,
		},
	}
	outA := render(ctxA)
	if !strings.Contains(outA, "mitto-abc") {
		t.Errorf("branch (a): expected bead ID 'mitto-abc' in output; got:\n%s", outA)
	}
	if strings.Contains(outA, "not explicitly specified") {
		t.Errorf("branch (a): unexpected 'not explicitly specified' text; session.BeadsIssue should have been used")
	}
	if strings.Contains(outA, "bd show  ") || strings.Contains(outA, "bd show \n") {
		t.Errorf("branch (a): found broken empty 'bd show ' command in output")
	}

	// (b) Only Args.IssueID set.
	ctxB := &PromptEnabledContext{
		Args: map[string]string{"IssueID": "mitto-xyz"},
	}
	outB := render(ctxB)
	if !strings.Contains(outB, "mitto-xyz") {
		t.Errorf("branch (b): expected bead ID 'mitto-xyz' in output; got:\n%s", outB)
	}
	if strings.Contains(outB, "not explicitly specified") {
		t.Errorf("branch (b): unexpected 'not explicitly specified' text; Args.IssueID should have been used")
	}
	if strings.Contains(outB, "bd show  ") || strings.Contains(outB, "bd show \n") {
		t.Errorf("branch (b): found broken empty 'bd show ' command in output")
	}

	// (c) Neither BeadsIssue nor Args.IssueID set — inference instruction.
	ctxC := &PromptEnabledContext{}
	outC := render(ctxC)
	if !strings.Contains(outC, "not explicitly specified") {
		t.Errorf("branch (c): expected inference text 'not explicitly specified' in output; got:\n%s", outC)
	}
	if strings.Contains(outC, "bd show  ") || strings.Contains(outC, "bd show \n") {
		t.Errorf("branch (c): found broken empty 'bd show ' command in output")
	}
	// The <target-bead> placeholder should appear verbatim (it is NOT a Go template).
	if !strings.Contains(outC, "<target-bead>") {
		t.Errorf("branch (c): expected '<target-bead>' placeholder in bd commands; got:\n%s", outC)
	}
}

// TestIteratePrompts_CommitOption verifies the opt-in "Commit" boolean parameter
// on the iterating builtin prompts: the commit-instruction section is rendered
// only when the Commit argument is the string "true", and is omitted when it is
// "false" or absent. github-iterate-babysit-new-prs is intentionally excluded (it
// works via worktrees and never touches the local checkout), so it has no Commit
// option and is not covered here.
//
// Each prompt is loaded from the real builtin directory and rendered with
// BuildTemplateFuncMap so the test always exercises the current on-disk content.
func TestIteratePrompts_CommitOption(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"

	// marker is a substring that appears ONLY inside the commit section of the
	// given prompt. "git commit -a" is additionally asserted as a shared guard:
	// every commit section warns against it, and the base prompts never mention it.
	cases := []struct {
		file   string
		name   string
		marker string
	}{
		{"iterate-fixing.prompt.yaml", "iterate-fixing", "Commit your work"},
		{"iterate-implementing.prompt.yaml", "iterate-implementing", "Commit your work"},
		{"iterate-until.prompt.yaml", "iterate-until", "skip the commit"},
		{"beads-issue-iterate-until-complete.prompt.yaml", "beads-issue-iterate-until-complete", "Tell the worker to commit its work"},
	}

	const sharedGuard = "git commit -a"

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(builtinDir, tc.file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("prompt file not found at %s: %v", path, err)
			}
			prompt, err := ParsePromptFile(tc.file, data, time.Now())
			if err != nil {
				t.Fatalf("ParsePromptFile(%s): %v", tc.file, err)
			}
			body := prompt.Content

			render := func(args map[string]string) string {
				ctx := &PromptEnabledContext{Args: args}
				funcs := BuildTemplateFuncMap(ctx)
				out, rerr := RenderPromptTemplate(tc.name, body, ctx, funcs)
				if rerr != nil {
					t.Fatalf("RenderPromptTemplate(%s): %v", tc.name, rerr)
				}
				return out
			}

			// Commit="true" → commit section present.
			outTrue := render(map[string]string{"Commit": "true"})
			if !strings.Contains(outTrue, tc.marker) {
				t.Errorf("Commit=true: expected marker %q in output; got:\n%s", tc.marker, outTrue)
			}
			if !strings.Contains(outTrue, sharedGuard) {
				t.Errorf("Commit=true: expected shared guard %q in output; got:\n%s", sharedGuard, outTrue)
			}

			// Commit="false" → commit section absent.
			outFalse := render(map[string]string{"Commit": "false"})
			if strings.Contains(outFalse, tc.marker) {
				t.Errorf("Commit=false: marker %q should be absent; got:\n%s", tc.marker, outFalse)
			}
			if strings.Contains(outFalse, sharedGuard) {
				t.Errorf("Commit=false: shared guard %q should be absent; got:\n%s", sharedGuard, outFalse)
			}

			// Commit absent (nil args) → commit section absent.
			outAbsent := render(nil)
			if strings.Contains(outAbsent, tc.marker) {
				t.Errorf("Commit absent: marker %q should be absent; got:\n%s", tc.marker, outAbsent)
			}
			if strings.Contains(outAbsent, sharedGuard) {
				t.Errorf("Commit absent: shared guard %q should be absent; got:\n%s", sharedGuard, outAbsent)
			}
		})
	}
}

// TestBuiltinPrompts_NoDeprecatedMittoVars asserts that every migrated builtin
// prompt body contains ZERO deprecated @mitto: tokens (i.e. the .7/.8 migration
// is complete). This is a guard against accidental re-introduction.
func TestBuiltinPrompts_NoDeprecatedMittoVars(t *testing.T) {
	// Relative to internal/config/ (the package directory during go test).
	builtinDir := "../../config/prompts/builtin"
	// Load all builtin prompts (files that fail ParsePromptFile are skipped silently).
	prompts, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Skipf("cannot load builtins from %s: %v", builtinDir, err)
	}
	if len(prompts) == 0 {
		t.Skip("no builtin prompts found")
	}
	var failures []string
	for _, p := range prompts {
		vars := DeprecatedMittoVars(p.Content)
		if len(vars) > 0 {
			failures = append(failures, p.Name+": "+strings.Join(vars, ", "))
		}
	}
	if len(failures) > 0 {
		t.Errorf("builtin prompts still contain deprecated @mitto: tokens:\n  %s",
			strings.Join(failures, "\n  "))
	}
	t.Logf("checked %d builtin prompts — zero deprecated @mitto: tokens ✓", len(prompts))
}
