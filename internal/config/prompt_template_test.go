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

// TestValidatePromptTemplateSyntax verifies parse-only validation: plain bodies
// and bodies with valid template syntax (including FuncMap calls) pass, while
// structurally broken bodies (e.g. unbalanced actions) return an error (mitto-e7u).
func TestValidatePromptTemplateSyntax(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "plain-text", body: "Hello world", wantErr: false},
		{name: "dollar-var-only", body: "work on ${ISSUE}", wantErr: false},
		{name: "valid-action", body: "id={{ .Session.ID }}", wantErr: false},
		{name: "valid-funcmap-call", body: "{{ if .Iteration.IsUninterrupted }}x{{ end }}", wantErr: false},
		{name: "unbalanced-if", body: "{{ if .Broken }}", wantErr: true},
		{name: "unterminated-action", body: "hello {{ .Name", wantErr: true},
		{name: "empty", body: "", wantErr: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePromptTemplateSyntax("prompt", tc.body)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for body %q, got nil", tc.body)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected nil error for body %q, got: %v", tc.body, err)
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
			name: "graduated — children now migratable",
			body: "@mitto:children",
			want: []string{"children"},
		},
		{
			name: "graduated — available_acp_servers now migratable",
			body: "@mitto:available_acp_servers",
			want: []string{"available_acp_servers"},
		},
		{
			name: "graduated — mcp_children now migratable",
			body: "@mitto:mcp_children",
			want: []string{"mcp_children"},
		},
		{
			name: "graduated — user_data and user_data_schema now migratable",
			body: "@mitto:user_data @mitto:user_data_schema",
			want: []string{"user_data", "user_data_schema"},
		},
		{
			name: "mixed — both session_id and children are now migratable",
			body: "@mitto:session_id and @mitto:children",
			want: []string{"children", "session_id"},
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
			name: "mcp_children_count and mcp_children both migratable",
			body: "@mitto:mcp_children_count @mitto:mcp_children",
			want: []string{"mcp_children", "mcp_children_count"},
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
			body: "@mitto:session_id @mitto:parent_session_id @mitto:parent @mitto:session_name @mitto:working_dir @mitto:acp_server @mitto:workspace_uuid @mitto:beads_issue @mitto:mcp_children_count @mitto:periodic @mitto:periodic_forced @mitto:available_acp_servers @mitto:children @mitto:mcp_children @mitto:user_data @mitto:user_data_schema",
			want: []string{"acp_server", "available_acp_servers", "beads_issue", "children", "mcp_children", "mcp_children_count", "parent", "parent_session_id", "periodic", "periodic_forced", "session_id", "session_name", "user_data", "user_data_schema", "working_dir", "workspace_uuid"},
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
	// The 5 formerly-keep-list tokens now have template equivalents.
	if r := DeprecatedMittoVarReplacement("children"); r != "{{ .Children.AllText }}" {
		t.Errorf("children replacement = %q, want %q", r, "{{ .Children.AllText }}")
	}
	if r := DeprecatedMittoVarReplacement("mcp_children"); r != "{{ .Children.MCPText }}" {
		t.Errorf("mcp_children replacement = %q, want %q", r, "{{ .Children.MCPText }}")
	}
	if r := DeprecatedMittoVarReplacement("available_acp_servers"); r != "{{ .ACP.AvailableText }}" {
		t.Errorf("available_acp_servers replacement = %q, want %q", r, "{{ .ACP.AvailableText }}")
	}
	if r := DeprecatedMittoVarReplacement("user_data"); r != "{{ .Session.UserDataJSON }}" {
		t.Errorf("user_data replacement = %q, want %q", r, "{{ .Session.UserDataJSON }}")
	}
	if r := DeprecatedMittoVarReplacement("user_data_schema"); r != "{{ .Workspace.UserDataSchemaJSON }}" {
		t.Errorf("user_data_schema replacement = %q, want %q", r, "{{ .Workspace.UserDataSchemaJSON }}")
	}
	if r := DeprecatedMittoVarReplacement("unknown_xyz"); r != "" {
		t.Errorf("unknown token should return empty, got %q", r)
	}
}

// TestKeepListIsEmpty asserts that keepListMittoVars has been emptied after all
// formerly-kept tokens were graduated to migratableMittoVars.
func TestKeepListIsEmpty(t *testing.T) {
	if n := len(keepListMittoVars); n != 0 {
		t.Errorf("keepListMittoVars should be empty, got %d entries: %v", n, keepListMittoVars)
	}
}

// TestMigratableMittoVars_ContainsGraduatedTokens asserts that migratableMittoVars
// contains the 5 tokens graduated from the keep-list, with the expected replacements.
func TestMigratableMittoVars_ContainsGraduatedTokens(t *testing.T) {
	expected := map[string]string{
		"available_acp_servers": "{{ .ACP.AvailableText }}",
		"children":              "{{ .Children.AllText }}",
		"mcp_children":          "{{ .Children.MCPText }}",
		"user_data":             "{{ .Session.UserDataJSON }}",
		"user_data_schema":      "{{ .Workspace.UserDataSchemaJSON }}",
	}
	for token, want := range expected {
		got, ok := migratableMittoVars[token]
		if !ok {
			t.Errorf("migratableMittoVars missing key %q", token)
			continue
		}
		if got != want {
			t.Errorf("migratableMittoVars[%q] = %q, want %q", token, got, want)
		}
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

// TestInvestigate_ThreeModeTargetResolution tests the three target-bead
// resolution branches of beads-issue-investigate.prompt.yaml:
//
//	(a) .Session.BeadsIssue set  → "linked-issue" mode: bead ID appears, no
//	    "no linked bead" prose
//	(b) .Args.IssueID set only   → "arg" mode: bead ID appears, no
//	    "no linked bead" prose
//	(c) neither set              → "current problem" mode: "no linked bead"
//	    prose appears AND no bd commands leak (bd show/update/comment/create/dep)
//
// Also asserts the YAML header migration: menus includes both "beadsIssues"
// and "conversation", and the IssueID parameter is non-required.
func TestInvestigate_ThreeModeTargetResolution(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issue-investigate.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issue-investigate.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	body := prompt.Content

	// Header assertions: menus widened to include "conversation"; IssueID
	// parameter marked optional via required: false.
	if !strings.Contains(prompt.Menus, "beadsIssues") {
		t.Errorf("expected Menus to contain 'beadsIssues'; got %q", prompt.Menus)
	}
	if !strings.Contains(prompt.Menus, "conversation") {
		t.Errorf("expected Menus to contain 'conversation'; got %q", prompt.Menus)
	}
	var issueParam *PromptParameter
	for i := range prompt.Parameters {
		if prompt.Parameters[i].Name == "IssueID" {
			issueParam = &prompt.Parameters[i]
			break
		}
	}
	if issueParam == nil {
		t.Fatalf("IssueID parameter not found in prompt.Parameters")
	}
	if issueParam.Required == nil {
		t.Errorf("IssueID parameter: expected Required to be explicitly set (*bool non-nil); got nil")
	} else if *issueParam.Required {
		t.Errorf("IssueID parameter: expected Required == false; got true")
	}

	render := func(ctx *PromptEnabledContext) string {
		funcs := BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-issue-investigate", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (a) Linked-issue mode: Session.BeadsIssue set.
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
	if strings.Contains(outA, "no linked bead") {
		t.Errorf("branch (a): unexpected 'no linked bead' text; session.BeadsIssue should have been used")
	}
	if strings.Contains(outA, "bd show  ") || strings.Contains(outA, "bd show \n") {
		t.Errorf("branch (a): found broken empty 'bd show ' command in output")
	}

	// (b) Arg mode: only Args.IssueID set.
	ctxB := &PromptEnabledContext{
		Args: map[string]string{"IssueID": "mitto-xyz"},
	}
	outB := render(ctxB)
	if !strings.Contains(outB, "mitto-xyz") {
		t.Errorf("branch (b): expected bead ID 'mitto-xyz' in output; got:\n%s", outB)
	}
	if strings.Contains(outB, "no linked bead") {
		t.Errorf("branch (b): unexpected 'no linked bead' text; Args.IssueID should have been used")
	}
	if strings.Contains(outB, "bd show  ") || strings.Contains(outB, "bd show \n") {
		t.Errorf("branch (b): found broken empty 'bd show ' command in output")
	}

	// (c) Current-problem mode: neither BeadsIssue nor Args.IssueID set.
	ctxC := &PromptEnabledContext{}
	outC := render(ctxC)
	if !strings.Contains(outC, "no linked bead") {
		t.Errorf("branch (c): expected 'no linked bead' prose in output; got:\n%s", outC)
	}
	// In current-problem mode NO bd commands must leak — the prompt explicitly
	// instructs the agent not to touch any tracker.
	forbidden := []string{"bd show", "bd update", "bd comment", "bd create", "bd dep"}
	for _, cmd := range forbidden {
		if strings.Contains(outC, cmd) {
			t.Errorf("branch (c): forbidden bd command %q leaked into current-problem-mode output:\n%s", cmd, outC)
		}
	}
}

// TestDiscuss_ThreeModeTargetResolution tests the three target-bead
// resolution branches of beads-issue-discuss.prompt.yaml:
//
//	(a) .Session.BeadsIssue set  → "linked-issue" mode: bead ID appears, no
//	    "no linked bead" prose
//	(b) .Args.IssueID set only   → "arg" mode: bead ID appears, no
//	    "no linked bead" prose
//	(c) neither set              → "current problem" mode: "no linked bead"
//	    prose appears AND no bd commands leak (bd show/update/comment/create/dep)
//
// Also asserts the YAML header migration: menus includes both "beadsIssues"
// and "conversation", and the IssueID parameter is non-required.
func TestDiscuss_ThreeModeTargetResolution(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issue-discuss.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issue-discuss.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	body := prompt.Content

	// Header assertions.
	if !strings.Contains(prompt.Menus, "beadsIssues") {
		t.Errorf("expected Menus to contain 'beadsIssues'; got %q", prompt.Menus)
	}
	if !strings.Contains(prompt.Menus, "conversation") {
		t.Errorf("expected Menus to contain 'conversation'; got %q", prompt.Menus)
	}
	var issueParam *PromptParameter
	for i := range prompt.Parameters {
		if prompt.Parameters[i].Name == "IssueID" {
			issueParam = &prompt.Parameters[i]
			break
		}
	}
	if issueParam == nil {
		t.Fatalf("IssueID parameter not found in prompt.Parameters")
	}
	if issueParam.Required == nil {
		t.Errorf("IssueID parameter: expected Required to be explicitly set (*bool non-nil); got nil")
	} else if *issueParam.Required {
		t.Errorf("IssueID parameter: expected Required == false; got true")
	}

	render := func(ctx *PromptEnabledContext) string {
		funcs := BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-issue-discuss", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (a) Linked-issue mode: Session.BeadsIssue set.
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
	if strings.Contains(outA, "no linked bead") {
		t.Errorf("branch (a): unexpected 'no linked bead' text; session.BeadsIssue should have been used")
	}
	if strings.Contains(outA, "bd show  ") || strings.Contains(outA, "bd show \n") {
		t.Errorf("branch (a): found broken empty 'bd show ' command in output")
	}

	// (b) Arg mode: only Args.IssueID set.
	ctxB := &PromptEnabledContext{
		Args: map[string]string{"IssueID": "mitto-xyz"},
	}
	outB := render(ctxB)
	if !strings.Contains(outB, "mitto-xyz") {
		t.Errorf("branch (b): expected bead ID 'mitto-xyz' in output; got:\n%s", outB)
	}
	if strings.Contains(outB, "no linked bead") {
		t.Errorf("branch (b): unexpected 'no linked bead' text; Args.IssueID should have been used")
	}
	if strings.Contains(outB, "bd show  ") || strings.Contains(outB, "bd show \n") {
		t.Errorf("branch (b): found broken empty 'bd show ' command in output")
	}

	// (c) Current-problem mode: neither BeadsIssue nor Args.IssueID set.
	ctxC := &PromptEnabledContext{}
	outC := render(ctxC)
	if !strings.Contains(outC, "no linked bead") {
		t.Errorf("branch (c): expected 'no linked bead' prose in output; got:\n%s", outC)
	}
	// No bd commands must appear in current-problem mode.
	forbidden := []string{"bd show", "bd update", "bd comment", "bd create", "bd dep"}
	for _, cmd := range forbidden {
		if strings.Contains(outC, cmd) {
			t.Errorf("branch (c): forbidden bd command %q leaked into current-problem-mode output:\n%s", cmd, outC)
		}
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

// TestBuiltinPrompts_AllRenderWithoutError is a regression test for mitto-vjos.2.
// TestBuiltinPrompts_NoDeprecatedMittoVars (above) loads but never RENDERS builtins,
// so a broken template expression like {{ Name }} instead of {{ .Session.Name }}
// would only fail-open silently in production. This test actually renders every
// builtin prompt with a representative context and fails if any template errors out.
func TestBuiltinPrompts_AllRenderWithoutError(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"
	prompts, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Skipf("cannot load builtins from %s: %v", builtinDir, err)
	}
	if len(prompts) == 0 {
		t.Skip("no builtin prompts found")
	}

	ctx := &PromptEnabledContext{
		Session: SessionContext{
			ID:            "test-session",
			Name:          "Test Conversation",
			BeadsIssue:    "mitto-test",
			HasBeadsIssue: true,
			ParentID:      "parent-1",
			IsChild:       true,
		},
		Args: map[string]string{"IssueID": "mitto-test", "Condition": "all tests pass"},
	}

	var failures []string
	for _, p := range prompts {
		funcs := BuildTemplateFuncMap(ctx)
		if _, rerr := RenderPromptTemplate(p.Name, p.Content, ctx, funcs); rerr != nil {
			failures = append(failures, p.Name+": "+rerr.Error())
		}
	}
	if len(failures) > 0 {
		t.Errorf("builtin prompts failed to render (broken template funcs / fields):\n  %s", strings.Join(failures, "\n  "))
	}
	t.Logf("rendered %d builtin prompts — all templates valid ✓", len(prompts))
}

// TestStatus_ThreeModeTargetResolution tests the three target-bead
// resolution branches of beads-issue-status.prompt.yaml:
//
//	(a) .Session.BeadsIssue set  → "linked-issue" mode: bead ID appears, no
//	    "no linked bead" prose
//	(b) .Args.IssueID set only   → "arg" mode: bead ID appears, no
//	    "no linked bead" prose
//	(c) neither set              → "current problem" mode: "no linked bead"
//	    prose appears AND no bd commands or id-greps leak
//
// Also asserts the YAML header migration: menus includes both "beadsIssues"
// and "conversation", and the IssueID parameter is non-required.
func TestStatus_ThreeModeTargetResolution(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issue-status.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issue-status.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	body := prompt.Content

	// Header assertions.
	if !strings.Contains(prompt.Menus, "beadsIssues") {
		t.Errorf("expected Menus to contain 'beadsIssues'; got %q", prompt.Menus)
	}
	if !strings.Contains(prompt.Menus, "conversation") {
		t.Errorf("expected Menus to contain 'conversation'; got %q", prompt.Menus)
	}
	var issueParam *PromptParameter
	for i := range prompt.Parameters {
		if prompt.Parameters[i].Name == "IssueID" {
			issueParam = &prompt.Parameters[i]
			break
		}
	}
	if issueParam == nil {
		t.Fatalf("IssueID parameter not found in prompt.Parameters")
	}
	if issueParam.Required == nil {
		t.Errorf("IssueID parameter: expected Required to be explicitly set (*bool non-nil); got nil")
	} else if *issueParam.Required {
		t.Errorf("IssueID parameter: expected Required == false; got true")
	}

	render := func(ctx *PromptEnabledContext) string {
		funcs := BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-issue-status", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (a) Linked-issue mode: Session.BeadsIssue set.
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
	if strings.Contains(outA, "no linked bead") {
		t.Errorf("branch (a): unexpected 'no linked bead' text; session.BeadsIssue should have been used")
	}

	// (b) Arg mode: only Args.IssueID set.
	ctxB := &PromptEnabledContext{
		Args: map[string]string{"IssueID": "mitto-xyz"},
	}
	outB := render(ctxB)
	if !strings.Contains(outB, "mitto-xyz") {
		t.Errorf("branch (b): expected bead ID 'mitto-xyz' in output; got:\n%s", outB)
	}
	if strings.Contains(outB, "no linked bead") {
		t.Errorf("branch (b): unexpected 'no linked bead' text; Args.IssueID should have been used")
	}

	// (c) Current-problem mode: neither BeadsIssue nor Args.IssueID set.
	ctxC := &PromptEnabledContext{}
	outC := render(ctxC)
	if !strings.Contains(outC, "no linked bead") {
		t.Errorf("branch (c): expected 'no linked bead' prose in output; got:\n%s", outC)
	}
	// No bd commands or id-greps must appear in current-problem mode.
	forbidden := []string{"bd show", "bd dep", `grep -i "`, "bd update", "bd comment"}
	for _, cmd := range forbidden {
		if strings.Contains(outC, cmd) {
			t.Errorf("branch (c): forbidden pattern %q leaked into current-problem-mode output:\n%s", cmd, outC)
		}
	}
}

// TestResolved_ThreeModeTargetResolution tests the three target-bead
// resolution branches of beads-issue-resolved.prompt.yaml:
//
//	(a) .Session.BeadsIssue set  → "linked-issue" mode: bead ID appears, no
//	    "no linked bead" prose
//	(b) .Args.IssueID set only   → "arg" mode: bead ID appears, no
//	    "no linked bead" prose
//	(c) neither set              → "current problem" mode: "no linked bead"
//	    prose appears AND no bd commands or id-greps leak
//
// Also asserts the YAML header migration: menus includes both "beadsIssues"
// and "conversation", and the IssueID parameter is non-required.
func TestResolved_ThreeModeTargetResolution(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issue-resolved.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issue-resolved.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	body := prompt.Content

	// Header assertions.
	if !strings.Contains(prompt.Menus, "beadsIssues") {
		t.Errorf("expected Menus to contain 'beadsIssues'; got %q", prompt.Menus)
	}
	if !strings.Contains(prompt.Menus, "conversation") {
		t.Errorf("expected Menus to contain 'conversation'; got %q", prompt.Menus)
	}
	var issueParam *PromptParameter
	for i := range prompt.Parameters {
		if prompt.Parameters[i].Name == "IssueID" {
			issueParam = &prompt.Parameters[i]
			break
		}
	}
	if issueParam == nil {
		t.Fatalf("IssueID parameter not found in prompt.Parameters")
	}
	if issueParam.Required == nil {
		t.Errorf("IssueID parameter: expected Required to be explicitly set (*bool non-nil); got nil")
	} else if *issueParam.Required {
		t.Errorf("IssueID parameter: expected Required == false; got true")
	}

	render := func(ctx *PromptEnabledContext) string {
		funcs := BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-issue-resolved", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (a) Linked-issue mode: Session.BeadsIssue set.
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
	if strings.Contains(outA, "no linked bead") {
		t.Errorf("branch (a): unexpected 'no linked bead' text; session.BeadsIssue should have been used")
	}

	// (b) Arg mode: only Args.IssueID set.
	ctxB := &PromptEnabledContext{
		Args: map[string]string{"IssueID": "mitto-xyz"},
	}
	outB := render(ctxB)
	if !strings.Contains(outB, "mitto-xyz") {
		t.Errorf("branch (b): expected bead ID 'mitto-xyz' in output; got:\n%s", outB)
	}
	if strings.Contains(outB, "no linked bead") {
		t.Errorf("branch (b): unexpected 'no linked bead' text; Args.IssueID should have been used")
	}

	// (c) Current-problem mode: neither BeadsIssue nor Args.IssueID set.
	ctxC := &PromptEnabledContext{}
	outC := render(ctxC)
	if !strings.Contains(outC, "no linked bead") {
		t.Errorf("branch (c): expected 'no linked bead' prose in output; got:\n%s", outC)
	}
	// No bd commands or id-greps must appear in current-problem mode.
	forbidden := []string{"bd show", "bd dep", "bd close", "bd create", "bd update", `grep -i "`}
	for _, cmd := range forbidden {
		if strings.Contains(outC, cmd) {
			t.Errorf("branch (c): forbidden pattern %q leaked into current-problem-mode output:\n%s", cmd, outC)
		}
	}
}

// TestWork_ThreeModeTargetResolution tests the three target-bead
// resolution branches of beads-issue-work.prompt.yaml:
//
//	(a) .Session.BeadsIssue set  → "linked-issue" mode: bead ID appears, no
//	    "no linked bead" prose
//	(b) .Args.IssueID set only   → "arg" mode: bead ID appears, no
//	    "no linked bead" prose
//	(c) neither set              → "current problem" mode: "no linked bead"
//	    prose appears AND no bd commands leak
//
// Also asserts the YAML header migration: menus includes both "beadsIssues"
// and "conversation", and the IssueID parameter is non-required.
func TestWork_ThreeModeTargetResolution(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issue-work.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issue-work.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	body := prompt.Content

	// Header assertions.
	if !strings.Contains(prompt.Menus, "beadsIssues") {
		t.Errorf("expected Menus to contain 'beadsIssues'; got %q", prompt.Menus)
	}
	if !strings.Contains(prompt.Menus, "conversation") {
		t.Errorf("expected Menus to contain 'conversation'; got %q", prompt.Menus)
	}
	var issueParam *PromptParameter
	for i := range prompt.Parameters {
		if prompt.Parameters[i].Name == "IssueID" {
			issueParam = &prompt.Parameters[i]
			break
		}
	}
	if issueParam == nil {
		t.Fatalf("IssueID parameter not found in prompt.Parameters")
	}
	if issueParam.Required == nil {
		t.Errorf("IssueID parameter: expected Required to be explicitly set (*bool non-nil); got nil")
	} else if *issueParam.Required {
		t.Errorf("IssueID parameter: expected Required == false; got true")
	}

	render := func(ctx *PromptEnabledContext) string {
		funcs := BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-issue-work", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (a) Linked-issue mode: Session.BeadsIssue set.
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
	if strings.Contains(outA, "no linked bead") {
		t.Errorf("branch (a): unexpected 'no linked bead' text; session.BeadsIssue should have been used")
	}

	// (b) Arg mode: only Args.IssueID set.
	ctxB := &PromptEnabledContext{
		Args: map[string]string{"IssueID": "mitto-xyz"},
	}
	outB := render(ctxB)
	if !strings.Contains(outB, "mitto-xyz") {
		t.Errorf("branch (b): expected bead ID 'mitto-xyz' in output; got:\n%s", outB)
	}
	if strings.Contains(outB, "no linked bead") {
		t.Errorf("branch (b): unexpected 'no linked bead' text; Args.IssueID should have been used")
	}

	// (c) Current-problem mode: neither BeadsIssue nor Args.IssueID set.
	ctxC := &PromptEnabledContext{}
	outC := render(ctxC)
	if !strings.Contains(outC, "no linked bead") {
		t.Errorf("branch (c): expected 'no linked bead' prose in output; got:\n%s", outC)
	}
	// No bd commands must appear in current-problem mode.
	forbidden := []string{"bd show", "bd dep", "bd update", "bd close", "bd comment"}
	for _, cmd := range forbidden {
		if strings.Contains(outC, cmd) {
			t.Errorf("branch (c): forbidden bd command %q leaked into current-problem-mode output:\n%s", cmd, outC)
		}
	}
}

// TestFollowupWork_ThreeModeTargetResolution tests the target-bead resolution
// branches of beads-followup-work.prompt.yaml:
//
//	(a) .Session.BeadsIssue set → target-bead mode: bead ID appears, the
//	    "target bead" prose and child-default guidance appear, and the
//	    conversation-mining intro is absent.
//	(b) .Args.IssueID set only  → target-bead mode via arg: same as (a) with
//	    the arg bead ID.
//	(c) neither set             → conversation mode: the conversation-mining
//	    intro appears and no "target bead" prose leaks. Unlike investigate/work,
//	    bd commands ARE expected here (this prompt files beads from the
//	    conversation), so they are not forbidden — instead we assert no
//	    target-only fragments leaked with an empty target.
//
// Also asserts the YAML header migration: menus includes both "beadsIssues"
// and "conversation", and the IssueID parameter is non-required.
func TestFollowupWork_ThreeModeTargetResolution(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-followup-work.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-followup-work.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	body := prompt.Content

	// Header assertions.
	if !strings.Contains(prompt.Menus, "beadsIssues") {
		t.Errorf("expected Menus to contain 'beadsIssues'; got %q", prompt.Menus)
	}
	if !strings.Contains(prompt.Menus, "conversation") {
		t.Errorf("expected Menus to contain 'conversation'; got %q", prompt.Menus)
	}
	var issueParam *PromptParameter
	for i := range prompt.Parameters {
		if prompt.Parameters[i].Name == "IssueID" {
			issueParam = &prompt.Parameters[i]
			break
		}
	}
	if issueParam == nil {
		t.Fatalf("IssueID parameter not found in prompt.Parameters")
	}
	if issueParam.Required == nil {
		t.Errorf("IssueID parameter: expected Required to be explicitly set (*bool non-nil); got nil")
	} else if *issueParam.Required {
		t.Errorf("IssueID parameter: expected Required == false; got true")
	}

	render := func(ctx *PromptEnabledContext) string {
		funcs := BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-followup-work", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (a) Target-bead mode: Session.BeadsIssue set.
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
	if !strings.Contains(outA, "target bead") {
		t.Errorf("branch (a): expected 'target bead' prose in target mode; got:\n%s", outA)
	}
	if strings.Contains(outA, "comb back through") {
		t.Errorf("branch (a): unexpected conversation-mining intro in target mode")
	}
	if strings.Contains(outA, "--parent  ") {
		t.Errorf("branch (a): found broken empty '--parent' (missing target) in output")
	}

	// (b) Target-bead mode via arg: only Args.IssueID set.
	ctxB := &PromptEnabledContext{
		Args: map[string]string{"IssueID": "mitto-xyz"},
	}
	outB := render(ctxB)
	if !strings.Contains(outB, "mitto-xyz") {
		t.Errorf("branch (b): expected bead ID 'mitto-xyz' in output; got:\n%s", outB)
	}
	if !strings.Contains(outB, "target bead") {
		t.Errorf("branch (b): expected 'target bead' prose in target mode; got:\n%s", outB)
	}
	if strings.Contains(outB, "comb back through") {
		t.Errorf("branch (b): unexpected conversation-mining intro in target mode")
	}

	// (c) Conversation mode: neither BeadsIssue nor Args.IssueID set.
	ctxC := &PromptEnabledContext{}
	outC := render(ctxC)
	if !strings.Contains(outC, "comb back through") {
		t.Errorf("branch (c): expected conversation-mining intro in conversation mode; got:\n%s", outC)
	}
	if strings.Contains(outC, "target bead") {
		t.Errorf("branch (c): unexpected 'target bead' prose in conversation mode")
	}
	// The target-only child-parent example must not leak with an empty target.
	if strings.Contains(outC, "Child of the target bead") {
		t.Errorf("branch (c): target-only 'Child of the target bead' example leaked into conversation mode")
	}
}

// TestInteractionMode_ConditionalRendering verifies that the builtin prompts
// which were migrated from verbose "Interaction Mode" prose (that manually
// dumped {{ .Session.IsPeriodic }} / {{ .Session.IsPeriodicForced }}) to Go
// template conditionals render the correct branch for each of the three
// possible session states:
//
//	(1) Scheduled periodic  → IsPeriodic=true,  IsPeriodicForced=false → Silent
//	(2) Force-triggered      → IsPeriodic=true,  IsPeriodicForced=true  → Interactive
//	(3) Regular conversation → IsPeriodic=false, IsPeriodicForced=false → Interactive
//
// It also asserts that no raw .Session.IsPeriodic* variable text survives in
// the rendered output — proving the conditional directives were consumed by the
// template engine and that the old verbose variable dumps are gone.
//
// The test loads each file from the real builtin directory so it always
// exercises the current on-disk content.
func TestInteractionMode_ConditionalRendering(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"

	// silentMarker/interactiveMarker are substrings that appear ONLY in the
	// silent / interactive branch of the top "Interaction Mode" block of each
	// prompt (verified to not occur elsewhere in the file as prose).
	cases := []struct {
		file              string
		name              string
		silentMarker      string
		interactiveMarker string
	}{
		{
			file:              "architectural-analysis.prompt.yaml",
			name:              "architectural-analysis",
			silentMarker:      "a scheduled periodic run; the user is not watching.",
			interactiveMarker: "a regular conversation or a force-triggered periodic run; the user is present.",
		},
		{
			file:              "jira-sync-tasks.prompt.yaml",
			name:              "jira-sync-tasks",
			silentMarker:      "a scheduled periodic run; the user is not watching.",
			interactiveMarker: "a regular conversation or a force-triggered periodic run; the user is present.",
		},
		{
			file:              "github-sync-tasks.prompt.yaml",
			name:              "github-sync-tasks",
			silentMarker:      "a scheduled periodic run; the user is not watching.",
			interactiveMarker: "a regular conversation or a force-triggered periodic run; the user is present.",
		},
		{
			file:              "github-babysit-contributions.prompt.yaml",
			name:              "github-babysit-contributions",
			silentMarker:      "a scheduled periodic run; the user is not watching.",
			interactiveMarker: "a force-triggered run or a non-periodic conversation; the user may be present.",
		},
		{
			file:              "github-babysit-my-prs.prompt.yaml",
			name:              "github-babysit-my-prs",
			silentMarker:      "a scheduled periodic run; the user is not watching.",
			interactiveMarker: "a force-triggered run or a non-periodic conversation; the user may be present.",
		},
		{
			file:              "beads-issue-iterate-until-complete.prompt.yaml",
			name:              "beads-issue-iterate-until-complete",
			silentMarker:      "Silent mode — a scheduled periodic run.",
			interactiveMarker: "(e.g. the very first send, or a force-triggered run): a user may be",
		},
		{
			file:              "github-iterate-babysit-new-prs.prompt.yaml",
			name:              "github-iterate-babysit-new-prs",
			silentMarker:      "Silent mode — scheduled periodic run.",
			interactiveMarker: "(e.g. the very first send, or a force-triggered run): a user may be",
		},
	}

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

			render := func(periodic, forced bool) string {
				ctx := &PromptEnabledContext{
					Session: SessionContext{
						IsPeriodic:       periodic,
						IsPeriodicForced: forced,
					},
				}
				out, rerr := RenderPromptTemplate(tc.name, body, ctx, BuildTemplateFuncMap(ctx))
				if rerr != nil {
					t.Fatalf("RenderPromptTemplate(%s) periodic=%v forced=%v: %v", tc.name, periodic, forced, rerr)
				}
				// The conditionals must be consumed; no raw variable dumps may survive.
				if strings.Contains(out, ".Session.IsPeriodic") {
					t.Errorf("%s periodic=%v forced=%v: raw '.Session.IsPeriodic' leaked into rendered output:\n%s", tc.name, periodic, forced, out)
				}
				return out
			}

			// (1) Scheduled periodic → Silent branch.
			silent := render(true, false)
			if !strings.Contains(silent, tc.silentMarker) {
				t.Errorf("scheduled periodic: expected silent marker %q in output; got:\n%s", tc.silentMarker, silent)
			}
			if strings.Contains(silent, tc.interactiveMarker) {
				t.Errorf("scheduled periodic: unexpected interactive marker %q in silent output:\n%s", tc.interactiveMarker, silent)
			}

			// (2) Force-triggered → Interactive branch.
			forced := render(true, true)
			if !strings.Contains(forced, tc.interactiveMarker) {
				t.Errorf("force-triggered: expected interactive marker %q in output; got:\n%s", tc.interactiveMarker, forced)
			}
			if strings.Contains(forced, tc.silentMarker) {
				t.Errorf("force-triggered: unexpected silent marker %q in interactive output:\n%s", tc.silentMarker, forced)
			}

			// (3) Regular conversation → Interactive branch.
			regular := render(false, false)
			if !strings.Contains(regular, tc.interactiveMarker) {
				t.Errorf("regular conversation: expected interactive marker %q in output; got:\n%s", tc.interactiveMarker, regular)
			}
			if strings.Contains(regular, tc.silentMarker) {
				t.Errorf("regular conversation: unexpected silent marker %q in interactive output:\n%s", tc.silentMarker, regular)
			}
		})
	}
}

// TestRenderPromptTemplate_Iteration verifies that the {{ .Iteration.* }} template
// namespace is available and branches correctly on Number=0 vs Number=2 (Max=3).
func TestRenderPromptTemplate_Iteration(t *testing.T) {
	body := `{{ if .Iteration.IsFirst }}first run{{ else }}run {{ .Iteration.Number }} of {{ .Iteration.Max }}{{ end }}`

	// Number=0, Max=3 → "first run"
	ctxFirst := &PromptEnabledContext{
		Iteration: IterationContext{
			Number:     0,
			Max:        3,
			IsPeriodic: true,
			IsFirst:    true,
			IsLast:     false,
		},
	}
	gotFirst, err := RenderPromptTemplate("test-first", body, ctxFirst, nil)
	if err != nil {
		t.Fatalf("RenderPromptTemplate(first): unexpected error: %v", err)
	}
	if gotFirst != "first run" {
		t.Errorf("first run: got %q, want %q", gotFirst, "first run")
	}

	// Number=2, Max=3 → "run 2 of 3"
	ctxLast := &PromptEnabledContext{
		Iteration: IterationContext{
			Number:     2,
			Max:        3,
			IsPeriodic: true,
			IsFirst:    false,
			IsLast:     true,
		},
	}
	gotLast, err := RenderPromptTemplate("test-last", body, ctxLast, nil)
	if err != nil {
		t.Fatalf("RenderPromptTemplate(last): unexpected error: %v", err)
	}
	if gotLast != "run 2 of 3" {
		t.Errorf("last run: got %q, want %q", gotLast, "run 2 of 3")
	}

	if gotFirst == gotLast {
		t.Error("expected different output for Number=0 vs Number=2, but got the same")
	}

	// IsUninterrupted=true → compact branch; IsUninterrupted=false → verbose branch (mitto-5xjn).
	bodyU := `{{ if .Iteration.IsUninterrupted }}continue{{ else }}verbose{{ end }}`

	ctxContinue := &PromptEnabledContext{
		Iteration: IterationContext{
			IsPeriodic:      true,
			IsUninterrupted: true,
		},
	}
	gotContinue, err := RenderPromptTemplate("test-continue", bodyU, ctxContinue, nil)
	if err != nil {
		t.Fatalf("RenderPromptTemplate(continue): unexpected error: %v", err)
	}
	if gotContinue != "continue" {
		t.Errorf("IsUninterrupted=true: got %q, want %q", gotContinue, "continue")
	}

	ctxVerbose := &PromptEnabledContext{
		Iteration: IterationContext{
			IsPeriodic:      true,
			IsUninterrupted: false,
		},
	}
	gotVerbose, err := RenderPromptTemplate("test-verbose", bodyU, ctxVerbose, nil)
	if err != nil {
		t.Fatalf("RenderPromptTemplate(verbose): unexpected error: %v", err)
	}
	if gotVerbose != "verbose" {
		t.Errorf("IsUninterrupted=false: got %q, want %q", gotVerbose, "verbose")
	}
}

// TestIterateFixingBug_RendersForRepresentativeContexts renders
// beads-issue-iterate-fixing-bug.prompt.yaml (mitto-gap.1) for representative
// contexts and asserts it renders without error and picks the right branch:
//
//	(a) linked-issue context  — .Session.BeadsIssue set, first run (default
//	    zero-value Iteration) → bead ID appears; interactive "Interaction Mode"
//	    header renders (not the uninterrupted continuation form).
//	(b) arg-only context      — .Args.IssueID set, .Iteration.IsUninterrupted
//	    true (silent scheduled continuation) → bead ID appears; the compact
//	    "Continuation — uninterrupted scheduled run" header renders instead of
//	    the verbose "Interaction Mode" header.
//	(c) first-run interactive — neither BeadsIssue nor IssueID set → the
//	    "not explicitly specified" guidance appears and no `bd` command leaks
//	    (Step 1 is skipped entirely without a resolved target).
//
// The test loads the file from the real builtin directory so it always
// exercises the current on-disk content; the render itself also proves the
// YAML/template parses.
func TestIterateFixingBug_RendersForRepresentativeContexts(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issue-iterate-fixing-bug.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issue-iterate-fixing-bug.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	body := prompt.Content

	render := func(ctx *PromptEnabledContext) string {
		funcs := BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-issue-iterate-fixing-bug", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (a) Linked-issue context, first interactive run.
	ctxA := &PromptEnabledContext{
		Session: SessionContext{
			BeadsIssue:    "mitto-abc",
			HasBeadsIssue: true,
		},
		Iteration: IterationContext{IsFirst: true},
	}
	outA := render(ctxA)
	if !strings.Contains(outA, "mitto-abc") {
		t.Errorf("branch (a): expected bead ID 'mitto-abc' in output; got:\n%s", outA)
	}
	if !strings.Contains(outA, "Interaction Mode — READ THIS FIRST") {
		t.Errorf("branch (a): expected interactive 'Interaction Mode' header; got:\n%s", outA)
	}
	if strings.Contains(outA, "Continuation — uninterrupted scheduled run") {
		t.Errorf("branch (a): unexpected uninterrupted-continuation header on a first interactive run")
	}
	if strings.Contains(outA, "bd show  ") || strings.Contains(outA, "bd show \n") {
		t.Errorf("branch (a): found broken empty 'bd show ' command in output")
	}

	// (b) Arg-only context, uninterrupted silent continuation run.
	ctxB := &PromptEnabledContext{
		Args:      map[string]string{"IssueID": "mitto-xyz"},
		Iteration: IterationContext{IsPeriodic: true, IsUninterrupted: true},
	}
	outB := render(ctxB)
	if !strings.Contains(outB, "mitto-xyz") {
		t.Errorf("branch (b): expected bead ID 'mitto-xyz' in output; got:\n%s", outB)
	}
	if !strings.Contains(outB, "Continuation — uninterrupted scheduled run") {
		t.Errorf("branch (b): expected uninterrupted-continuation header; got:\n%s", outB)
	}
	if strings.Contains(outB, "Interaction Mode — READ THIS FIRST") {
		t.Errorf("branch (b): unexpected verbose 'Interaction Mode' header on an uninterrupted run")
	}
	if strings.Contains(outB, "bd show  ") || strings.Contains(outB, "bd show \n") {
		t.Errorf("branch (b): found broken empty 'bd show ' command in output")
	}

	// (c) No target resolvable — neither BeadsIssue nor Args.IssueID set. Step 1
	// (state loading, "bd show") is skipped entirely without a target; the
	// Blocked → Defer + Handoff step (Step 4) still renders, using the
	// "<target-bug>" placeholder rather than an empty/broken argument, since it
	// is the documented escape hatch for this exact situation.
	ctxC := &PromptEnabledContext{}
	outC := render(ctxC)
	if !strings.Contains(outC, "not explicitly specified") {
		t.Errorf("branch (c): expected 'not explicitly specified' guidance; got:\n%s", outC)
	}
	if !strings.Contains(outC, "No target bug to work on") {
		t.Errorf("branch (c): expected the 'No target bug to work on' Step 1 fallback; got:\n%s", outC)
	}
	if strings.Contains(outC, "bd show  ") || strings.Contains(outC, "bd show \n") {
		t.Errorf("branch (c): found broken empty 'bd show ' command in output")
	}
	if !strings.Contains(outC, "<target-bug>") {
		t.Errorf("branch (c): expected the '<target-bug>' placeholder in the Step 4 handoff commands; got:\n%s", outC)
	}
}

// TestBuiltinPromptPeriodicModes verifies the mitto-92x.6 mechanical flagging
// pass: every builtin prompt assigned a mode/default in the epic's
// classification table parses with the expected PromptPeriodic.Mode/Default,
// and a representative sample of the "never periodic" set has no periodic
// block at all.
func TestBuiltinPromptPeriodicModes(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"

	boolPtr := func(b bool) *bool { return &b }

	type want struct {
		mode string
		def  *bool // nil means PromptPeriodic.Default must be nil
	}

	cases := map[string]want{
		// Group A — always (6).
		"beads-issue-iterate-until-complete.prompt.yaml": {mode: "always", def: nil},
		"github-iterate-babysit-new-prs.prompt.yaml":     {mode: "always", def: nil},
		"github-post-merge-cleanup.prompt.yaml":          {mode: "always", def: nil},
		"iterate-until.prompt.yaml":                      {mode: "always", def: nil},
		"iterate-fixing.prompt.yaml":                     {mode: "always", def: nil},
		"iterate-implementing.prompt.yaml":               {mode: "always", def: nil},

		// Group B — optional / default:true (4).
		"github-babysit-contributions.prompt.yaml": {mode: "optional", def: boolPtr(true)},
		"github-babysit-my-prs.prompt.yaml":        {mode: "optional", def: boolPtr(true)},
		"github-sync-tasks.prompt.yaml":            {mode: "optional", def: boolPtr(true)},
		"jira-sync-tasks.prompt.yaml":              {mode: "optional", def: boolPtr(true)},

		// Group C — optional / default:false (10).
		"check-ci.prompt.yaml":                   {mode: "optional", def: boolPtr(false)},
		"fix-ci.prompt.yaml":                     {mode: "optional", def: boolPtr(false)},
		"run-tests.prompt.yaml":                  {mode: "optional", def: boolPtr(false)},
		"analyze-logs.prompt.yaml":               {mode: "optional", def: boolPtr(false)},
		"architectural-analysis.prompt.yaml":     {mode: "optional", def: boolPtr(false)},
		"beads-work.prompt.yaml":                 {mode: "optional", def: boolPtr(false)},
		"github-review-slack-prs.prompt.yaml":    {mode: "optional", def: boolPtr(false)},
		"jira-status-all-inprogress.prompt.yaml": {mode: "optional", def: boolPtr(false)},
		"jira-status-one-inprogress.prompt.yaml": {mode: "optional", def: boolPtr(false)},
		"jira-work.prompt.yaml":                  {mode: "optional", def: boolPtr(false)},
	}

	for file, w := range cases {
		t.Run(file, func(t *testing.T) {
			path := filepath.Join(builtinDir, file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("prompt file not found at %s: %v", path, err)
			}
			prompt, err := ParsePromptFile(file, data, time.Now())
			if err != nil {
				t.Fatalf("ParsePromptFile(%s): %v", file, err)
			}
			if prompt.Periodic == nil {
				t.Fatalf("%s: Periodic = nil, want non-nil", file)
			}
			if prompt.Periodic.Mode != w.mode {
				t.Errorf("%s: Periodic.Mode = %q, want %q", file, prompt.Periodic.Mode, w.mode)
			}
			if w.def == nil {
				if prompt.Periodic.Default != nil {
					t.Errorf("%s: Periodic.Default = %v, want nil", file, *prompt.Periodic.Default)
				}
			} else {
				if prompt.Periodic.Default == nil {
					t.Errorf("%s: Periodic.Default = nil, want %v", file, *w.def)
				} else if *prompt.Periodic.Default != *w.def {
					t.Errorf("%s: Periodic.Default = %v, want %v", file, *prompt.Periodic.Default, *w.def)
				}
			}
		})
	}

	// Representative sample of the "never periodic" set: no periodic block at all.
	neverFiles := []string{
		"explain.prompt.yaml",
		"refactor.prompt.yaml",
		"review.prompt.yaml",
		"add-tests.prompt.yaml",
		"whats-next.prompt.yaml",
		"child-create-minions.prompt.yaml",
		"continue.prompt.yaml",
		"beads-issue-decompose.prompt.yaml",
		// Tasks prompts that are one-shot reports, context-bound, or
		// confirmation-gated — periodic re-firing makes no sense for them.
		"beads-followup-work.prompt.yaml",
		"beads-cleanup-stale.prompt.yaml",
		"beads-group-epics.prompt.yaml",
		"beads-overview.prompt.yaml",
		"beads-reevaluate.prompt.yaml",
		"beads-status-all-inprogress.prompt.yaml",
		"beads-status-one-inprogress.prompt.yaml",
		"beads-issue-status.prompt.yaml",
		"beads-issue-work.prompt.yaml",
	}

	for _, file := range neverFiles {
		t.Run("never/"+file, func(t *testing.T) {
			path := filepath.Join(builtinDir, file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("prompt file not found at %s: %v", path, err)
			}
			prompt, err := ParsePromptFile(file, data, time.Now())
			if err != nil {
				t.Fatalf("ParsePromptFile(%s): %v", file, err)
			}
			if prompt.Periodic != nil {
				t.Errorf("%s: Periodic = %+v, want nil (never-periodic set)", file, prompt.Periodic)
			}
		})
	}
}
