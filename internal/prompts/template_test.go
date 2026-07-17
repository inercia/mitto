package prompts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/inercia/mitto/internal/cel"
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

// TestRenderPromptTemplate_TriggerOnTasksChanges verifies that the new
// {{ .Trigger.OnTasks.Changes.* }} template namespace (mitto-xkn) renders
// against a populated cel.PromptEnabledContext and that the {{ with .Trigger.OnTasks }}
// guard correctly suppresses the block when the trigger context is nil
// (scheduled / onCompletion / manual "Run Now" / non-loop dispatches).
func TestRenderPromptTemplate_TriggerOnTasksChanges(t *testing.T) {
	populated := cel.PromptEnabledContext{
		Trigger: &cel.TriggerContext{
			OnTasks: &cel.TriggerOnTasksContext{
				Changes: cel.TasksChangesView{
					Added: []map[string]any{
						{"id": "mitto-a", "title": "New A"},
					},
					Updated: []map[string]any{
						{"id": "mitto-u", "title": "Upd U"},
					},
					Touched: []map[string]any{
						{"id": "mitto-a", "title": "New A"},
						{"id": "mitto-u", "title": "Upd U"},
					},
					Removed:    []map[string]any{},
					Closed:     []map[string]any{},
					Reopened:   []map[string]any{},
					LabelAdded: []map[string]any{},
				},
			},
		},
	}
	empty := cel.PromptEnabledContext{} // Trigger nil → guard must skip block

	tests := []struct {
		name string
		body string
		data any
		want string
	}{
		// (1) Range over Touched, printing per-issue id and title.
		{
			name: "range-touched-populated",
			body: `{{ range .Trigger.OnTasks.Changes.Touched }}- {{ index . "id" }}: {{ index . "title" }};{{ end }}`,
			data: populated,
			want: `- mitto-a: New A;- mitto-u: Upd U;`,
		},
		// (2) `with .Trigger` guard suppresses entire block when Trigger is nil.
		// Note: templates must guard on .Trigger first (nil pointer to a struct
		// short-circuits `with`); {{ with .Trigger.OnTasks }} alone would panic
		// on the nil *cel.TriggerContext.
		{
			name: "with-guard-suppresses-when-nil",
			body: `head{{ with .Trigger }}{{ with .OnTasks }}[{{ len .Changes.Touched }}]{{ end }}{{ end }}tail`,
			data: empty,
			want: "headtail",
		},
		// (3) `with` guard populates when Trigger is set.
		{
			name: "with-guard-populates-when-set",
			body: `head{{ with .Trigger }}{{ with .OnTasks }}[{{ len .Changes.Touched }}]{{ end }}{{ end }}tail`,
			data: populated,
			want: "head[2]tail",
		},
		// (4) Empty slice iteration is a no-op.
		{
			name: "range-removed-empty",
			body: `{{ range .Trigger.OnTasks.Changes.Removed }}X{{ end }}done`,
			data: populated,
			want: "done",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RenderPromptTemplate("trigger-ontasks-test", tc.body, tc.data, nil)
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
			name: "loop_forced before loop",
			body: "@mitto:loop_forced and @mitto:loop",
			want: []string{"loop", "loop_forced"},
		},
		{
			name: "all migratable tokens",
			body: "@mitto:session_id @mitto:parent_session_id @mitto:parent @mitto:session_name @mitto:working_dir @mitto:acp_server @mitto:workspace_uuid @mitto:beads_issue @mitto:mcp_children_count @mitto:loop @mitto:loop_forced @mitto:available_acp_servers @mitto:children @mitto:mcp_children @mitto:user_data @mitto:user_data_schema",
			want: []string{"acp_server", "available_acp_servers", "beads_issue", "children", "loop", "loop_forced", "mcp_children", "mcp_children_count", "parent", "parent_session_id", "session_id", "session_name", "user_data", "user_data_schema", "working_dir", "workspace_uuid"},
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
// cycle (config ← processors ← config) and to reuse cel.BuildTemplateFuncMap directly.
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

	render := func(ctx *cel.PromptEnabledContext) string {
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-issue-iterate-until-complete", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (a) BeadsIssue set — preferred source.
	ctxA := &cel.PromptEnabledContext{
		Session: cel.SessionContext{
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
	ctxB := &cel.PromptEnabledContext{
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
	ctxC := &cel.PromptEnabledContext{}
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

// TestRefineImplementation_LoopAndModes verifies the beads-refine-implementation
// builtin prompt (mitto-mx4):
//
//	(a) it parses cleanly — this exercises parse-time CEL validation of the
//	    onTasks loop.condition, so a broken expression fails the test;
//	(b) its loop block declares the onTasks trigger + the documented CEL
//	    condition (mode: always) so selecting it starts a beads-change loop;
//	(c) it renders without error and branches correctly between silent
//	    (scheduled loop) and interactive (forced / first send) modes — a guard
//	    against the pre-mitto-pei stale template vars (.Session.IsPeriodic*).
//
// Loaded from the real builtin directory so it exercises the on-disk content.
func TestRefineImplementation_LoopAndModes(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"
	name := "beads-refine-implementation.prompt.yaml"
	path := filepath.Join(builtinDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile(name, data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile (parse-time CEL validation of loop.condition): %v", err)
	}

	// (b) Loop block: onTasks trigger, always mode, non-empty documented condition.
	if prompt.Loop == nil {
		t.Fatalf("expected a loop block; got nil")
	}
	if prompt.Loop.Trigger != "onTasks" {
		t.Errorf("loop.trigger = %q, want %q", prompt.Loop.Trigger, "onTasks")
	}
	if prompt.Loop.Mode != PromptLoopModeAlways {
		t.Errorf("loop.mode = %q, want %q", prompt.Loop.Mode, PromptLoopModeAlways)
	}
	if !strings.Contains(prompt.Loop.Condition, "implementation-refined") {
		t.Errorf("loop.condition should gate on the implementation-refined label; got %q", prompt.Loop.Condition)
	}

	body := prompt.Content
	render := func(ctx *cel.PromptEnabledContext) string {
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-refine-implementation", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (c) Silent: a scheduled (non-forced) loop run.
	outSilent := render(&cel.PromptEnabledContext{Session: cel.SessionContext{IsLoop: true, IsLoopForced: false}})
	if !strings.Contains(outSilent, "Silent mode") {
		t.Errorf("silent loop run: expected 'Silent mode' branch; got:\n%s", outSilent)
	}
	if strings.Contains(outSilent, "Interactive mode") {
		t.Errorf("silent loop run: unexpected 'Interactive mode' branch")
	}

	// (c) Interactive: a forced loop run (user present).
	outForced := render(&cel.PromptEnabledContext{Session: cel.SessionContext{IsLoop: true, IsLoopForced: true}})
	if !strings.Contains(outForced, "Interactive mode") {
		t.Errorf("forced run: expected 'Interactive mode' branch; got:\n%s", outForced)
	}

	// (c) Interactive: a first / normal send (no loop context at all).
	outFirst := render(&cel.PromptEnabledContext{})
	if !strings.Contains(outFirst, "Interactive mode") {
		t.Errorf("first send: expected 'Interactive mode' branch; got:\n%s", outFirst)
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

	render := func(ctx *cel.PromptEnabledContext) string {
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-issue-investigate", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (a) Linked-issue mode: Session.BeadsIssue set.
	ctxA := &cel.PromptEnabledContext{
		Session: cel.SessionContext{
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
	ctxB := &cel.PromptEnabledContext{
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
	ctxC := &cel.PromptEnabledContext{}
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
// resolution branches of beads-issue-assess.prompt.yaml:
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
	path := filepath.Join(builtinDir, "beads-issue-assess.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issue-assess.prompt.yaml", data, time.Now())
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

	render := func(ctx *cel.PromptEnabledContext) string {
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-issue-assess", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (a) Linked-issue mode: Session.BeadsIssue set.
	ctxA := &cel.PromptEnabledContext{
		Session: cel.SessionContext{
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
	ctxB := &cel.PromptEnabledContext{
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
	ctxC := &cel.PromptEnabledContext{}
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
// cel.BuildTemplateFuncMap so the test always exercises the current on-disk content.
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
				ctx := &cel.PromptEnabledContext{Args: args}
				funcs := cel.BuildTemplateFuncMap(ctx)
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

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{
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
		funcs := cel.BuildTemplateFuncMap(ctx)
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

	render := func(ctx *cel.PromptEnabledContext) string {
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-issue-status", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (a) Linked-issue mode: Session.BeadsIssue set.
	ctxA := &cel.PromptEnabledContext{
		Session: cel.SessionContext{
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
	ctxB := &cel.PromptEnabledContext{
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
	ctxC := &cel.PromptEnabledContext{}
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

	render := func(ctx *cel.PromptEnabledContext) string {
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-issue-resolved", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (a) Linked-issue mode: Session.BeadsIssue set.
	ctxA := &cel.PromptEnabledContext{
		Session: cel.SessionContext{
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
	ctxB := &cel.PromptEnabledContext{
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
	ctxC := &cel.PromptEnabledContext{}
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

	render := func(ctx *cel.PromptEnabledContext) string {
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-issue-work", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (a) Linked-issue mode: Session.BeadsIssue set.
	ctxA := &cel.PromptEnabledContext{
		Session: cel.SessionContext{
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
	ctxB := &cel.PromptEnabledContext{
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
	ctxC := &cel.PromptEnabledContext{}
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

	render := func(ctx *cel.PromptEnabledContext) string {
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-followup-work", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (a) Target-bead mode: Session.BeadsIssue set.
	ctxA := &cel.PromptEnabledContext{
		Session: cel.SessionContext{
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
	ctxB := &cel.PromptEnabledContext{
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
	ctxC := &cel.PromptEnabledContext{}
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
// dumped {{ .Session.IsLoop }} / {{ .Session.IsLoopForced }}) to Go
// template conditionals render the correct branch for each of the three
// possible session states:
//
//	(1) Scheduled loop      → IsLoop=true,  IsLoopForced=false → Silent
//	(2) Force-triggered      → IsLoop=true,  IsLoopForced=true  → Interactive
//	(3) Regular conversation → IsLoop=false, IsLoopForced=false → Interactive
//
// It also asserts that no raw .Session.IsLoop* variable text survives in
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
			silentMarker:      "a scheduled loop run; the user is not watching.",
			interactiveMarker: "a regular conversation or a force-triggered loop run; the user is present.",
		},
		{
			file:              "jira-sync-tasks.prompt.yaml",
			name:              "jira-sync-tasks",
			silentMarker:      "a scheduled loop run; the user is not watching.",
			interactiveMarker: "a regular conversation or a force-triggered loop run; the user is present.",
		},
		{
			file:              "github-sync-tasks.prompt.yaml",
			name:              "github-sync-tasks",
			silentMarker:      "a scheduled loop run; the user is not watching.",
			interactiveMarker: "a regular conversation or a force-triggered loop run; the user is present.",
		},
		{
			file:              "github-babysit-contributions.prompt.yaml",
			name:              "github-babysit-contributions",
			silentMarker:      "a scheduled loop run; the user is not watching.",
			interactiveMarker: "a force-triggered run or a non-loop conversation; the user may be present.",
		},
		{
			file:              "github-babysit-my-prs.prompt.yaml",
			name:              "github-babysit-my-prs",
			silentMarker:      "a scheduled loop run; the user is not watching.",
			interactiveMarker: "a force-triggered run or a non-loop conversation; the user may be present.",
		},
		{
			file:              "beads-issue-iterate-until-complete.prompt.yaml",
			name:              "beads-issue-iterate-until-complete",
			silentMarker:      "Silent mode — a scheduled loop run.",
			interactiveMarker: "(e.g. the very first send, or a force-triggered run): a user may be",
		},
		{
			file:              "github-iterate-babysit-new-prs.prompt.yaml",
			name:              "github-iterate-babysit-new-prs",
			silentMarker:      "Silent mode — scheduled loop run.",
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

			render := func(loop, forced bool) string {
				ctx := &cel.PromptEnabledContext{
					Session: cel.SessionContext{
						IsLoop:       loop,
						IsLoopForced: forced,
					},
				}
				out, rerr := RenderPromptTemplate(tc.name, body, ctx, cel.BuildTemplateFuncMap(ctx))
				if rerr != nil {
					t.Fatalf("RenderPromptTemplate(%s) loop=%v forced=%v: %v", tc.name, loop, forced, rerr)
				}
				// The conditionals must be consumed; no raw variable dumps may survive.
				if strings.Contains(out, ".Session.IsLoop") {
					t.Errorf("%s loop=%v forced=%v: raw '.Session.IsLoop' leaked into rendered output:\n%s", tc.name, loop, forced, out)
				}
				return out
			}

			// (1) Scheduled loop → Silent branch.
			silent := render(true, false)
			if !strings.Contains(silent, tc.silentMarker) {
				t.Errorf("scheduled loop: expected silent marker %q in output; got:\n%s", tc.silentMarker, silent)
			}
			if strings.Contains(silent, tc.interactiveMarker) {
				t.Errorf("scheduled loop: unexpected interactive marker %q in silent output:\n%s", tc.interactiveMarker, silent)
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
	ctxFirst := &cel.PromptEnabledContext{
		Iteration: cel.IterationContext{
			Number:  0,
			Max:     3,
			IsLoop:  true,
			IsFirst: true,
			IsLast:  false,
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
	ctxLast := &cel.PromptEnabledContext{
		Iteration: cel.IterationContext{
			Number:  2,
			Max:     3,
			IsLoop:  true,
			IsFirst: false,
			IsLast:  true,
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

	ctxContinue := &cel.PromptEnabledContext{
		Iteration: cel.IterationContext{
			IsLoop:          true,
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

	ctxVerbose := &cel.PromptEnabledContext{
		Iteration: cel.IterationContext{
			IsLoop:          true,
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
//	    header renders (not the uninterrupted continuation form); driver
//	    dispatches phase prompts by name (per-phase model tiering).
//	(b) arg-only context      — .Args.IssueID set, .Iteration.IsUninterrupted
//	    true (silent scheduled continuation) → bead ID appears; the compact
//	    "Continuation — uninterrupted scheduled run" header renders instead of
//	    the verbose "Interaction Mode" header; Commit=true propagates into the
//	    Fix-phase dispatch arguments.
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

	render := func(ctx *cel.PromptEnabledContext) string {
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-issue-iterate-fixing-bug", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (a) Linked-issue context, first interactive run.
	ctxA := &cel.PromptEnabledContext{
		Session: cel.SessionContext{
			BeadsIssue:    "mitto-abc",
			HasBeadsIssue: true,
		},
		Iteration: cel.IterationContext{IsFirst: true},
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
	// Per-phase model tiering: the driver must dispatch phase prompts by name
	// via mitto_conversation_send_prompt (self-send), NOT do the phase work
	// inline. The three phase prompt names and the self-send tool must appear
	// in the rendered body with the resolved target as IssueID.
	for _, phaseName := range []string{
		"Bug fix — investigate phase",
		"Bug fix — reproduce phase",
		"Bug fix — fix phase",
	} {
		if !strings.Contains(outA, phaseName) {
			t.Errorf("branch (a): expected phase dispatch to %q in output; got:\n%s", phaseName, outA)
		}
	}
	if !strings.Contains(outA, "mitto_conversation_send_prompt") {
		t.Errorf("branch (a): expected 'mitto_conversation_send_prompt' self-send calls in output")
	}
	if !strings.Contains(outA, `"IssueID": "mitto-abc"`) {
		t.Errorf("branch (a): expected resolved target 'mitto-abc' passed as IssueID argument; got:\n%s", outA)
	}
	// The driver must NOT do the phase work inline anymore. It must never
	// contain `bd update ... --add-label researched|reproduced|fixed` — that
	// is the phase prompts' job.
	for _, forbidden := range []string{
		"--add-label researched",
		"--add-label reproduced",
		"--add-label fixed",
	} {
		if strings.Contains(outA, forbidden) {
			t.Errorf("branch (a): driver leaked inline phase-label write %q — must be delegated to the phase prompt; got:\n%s", forbidden, outA)
		}
	}

	// (b) Arg-only context, uninterrupted silent continuation run, with Commit=true.
	ctxB := &cel.PromptEnabledContext{
		Args:      map[string]string{"IssueID": "mitto-xyz", "Commit": "true"},
		Iteration: cel.IterationContext{IsLoop: true, IsUninterrupted: true},
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
	// Commit=true must render into the Fix-phase dispatch as "Commit": "true".
	if !strings.Contains(outB, `"Commit": "true"`) {
		t.Errorf("branch (b): expected Commit=true propagated into Fix dispatch arguments; got:\n%s", outB)
	}
	if !strings.Contains(outB, `"IssueID": "mitto-xyz"`) {
		t.Errorf("branch (b): expected resolved target 'mitto-xyz' passed as IssueID; got:\n%s", outB)
	}

	// (c) No target resolvable — neither BeadsIssue nor Args.IssueID set. Step 1
	// (state loading, "bd show") is skipped entirely without a target; the
	// Blocked → Defer + Handoff step (Step 4) still renders, using the
	// "<target-bug>" placeholder rather than an empty/broken argument, since it
	// is the documented escape hatch for this exact situation.
	ctxC := &cel.PromptEnabledContext{}
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

// TestIterateFixingBugs_RendersForRepresentativeContexts renders
// beads-issue-iterate-fixing-bugs.prompt.yaml (mitto-gap.4, note plural "bugs")
// for representative contexts and asserts it renders without error, has the
// expected list-level frontmatter (no Item.* args), and contains the outer-loop
// spawn/wait/cleanup mechanics.
//
//	(a) default context   — no Args, no Session.BeadsIssue → body renders,
//	    references the per-bug driver name "Iterate fixing bug", declares itself
//	    a list-level orchestrator, calls out the top-level-only rule, and shows
//	    the spawn+wait+archive tool triplet with the exact loop budget
//	    (30 / 20 / 14400) that mirrors the per-bug driver's own block. Commit
//	    defaults to "true" in the child arguments when the Commit arg is absent.
//	(b) Commit="false"    — the child-arguments literal for Commit flips to
//	    "false", confirming the boolean forwarding is wired correctly.
//
// The frontmatter assertions (menus: beadsList; NO loop: block; name is
// "Iterate fixing bugs") are checked once, alongside the (a) render.
//
// The test loads the file from the real builtin directory so it always
// exercises the current on-disk content; the render itself also proves the
// YAML/template parses. Mirrors TestIterateFixingBug_RendersForRepresentativeContexts.
func TestIterateFixingBugs_RendersForRepresentativeContexts(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issue-iterate-fixing-bugs.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issue-iterate-fixing-bugs.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}

	// Frontmatter assertions — this is a list-level orchestrator with no
	// Item.* context and no loop block of its own (single-run internal loop).
	if prompt.Name != "Iterate fixing bugs" {
		t.Errorf("Name = %q, want %q", prompt.Name, "Iterate fixing bugs")
	}
	if strings.TrimSpace(prompt.Menus) != "beadsList" {
		t.Errorf("Menus = %q, want %q", prompt.Menus, "beadsList")
	}
	if prompt.Loop != nil {
		t.Errorf("Loop = %+v, want nil — this orchestrator is a single-run internal loop", prompt.Loop)
	}

	body := prompt.Content

	render := func(ctx *cel.PromptEnabledContext) string {
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-issue-iterate-fixing-bugs", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (a) Default context — Commit absent → default to "true" in child args.
	outA := render(&cel.PromptEnabledContext{})

	// The orchestrator dispatches to the per-bug driver by name.
	if !strings.Contains(outA, "Iterate fixing bug") {
		t.Errorf("branch (a): expected reference to per-bug driver name \"Iterate fixing bug\"; got:\n%s", outA)
	}
	// Top-level-only + degrade-gracefully guidance must appear.
	if !strings.Contains(outA, "top-level") {
		t.Errorf("branch (a): expected 'top-level' spawn-recursion note; got:\n%s", outA)
	}
	// Spawn + wait + archive tool triplet must appear.
	for _, tool := range []string{
		"mitto_prompt_get",
		"mitto_conversation_new",
		"mitto_children_tasks_wait",
		"mitto_conversation_archive",
	} {
		if !strings.Contains(outA, tool) {
			t.Errorf("branch (a): expected orchestration tool call %q in body; got:\n%s", tool, outA)
		}
	}
	// Loop re-fire mechanics that make each child self-drive.
	for _, hint := range []string{
		"onCompletion",
		"loop_prompt",
		"loop_completion_delay_seconds: 30",
		"loop_max_iterations: 20",
		"loop_max_duration_seconds: 14400",
	} {
		if !strings.Contains(outA, hint) {
			t.Errorf("branch (a): expected loop-budget hint %q in body; got:\n%s", hint, outA)
		}
	}
	// Preflight-flag guidance so a user with either flag off gets a graceful stop.
	for _, flag := range []string{
		"Can start conversation",
		"Can Send Prompt",
	} {
		if !strings.Contains(outA, flag) {
			t.Errorf("branch (a): expected flag preflight text %q in body; got:\n%s", flag, outA)
		}
	}
	// Commit absent → defaults to "true" in the spawned child's arguments map.
	if !strings.Contains(outA, `"Commit": "true"`) {
		t.Errorf("branch (a): expected default Commit=\"true\" in child arguments when Commit arg is absent; got:\n%s", outA)
	}

	// (b) Commit="false" → the child arguments literal flips to "false".
	outB := render(&cel.PromptEnabledContext{Args: map[string]string{"Commit": "false"}})
	if !strings.Contains(outB, `"Commit": "false"`) {
		t.Errorf("branch (b): expected Commit=\"false\" in child arguments when Commit arg is \"false\"; got:\n%s", outB)
	}
	if strings.Contains(outB, `"Commit": "true"`) {
		t.Errorf("branch (b): unexpected Commit=\"true\" in child arguments when Commit arg is \"false\"; got:\n%s", outB)
	}
}

// TestIterateImplementingFeatures_RendersForRepresentativeContexts is the
// list-level orchestrator counterpart for the feature flow (mitto-gap.6):
// it parses beads-issue-iterate-implementing-features.prompt.yaml from disk,
// asserts the orchestrator frontmatter shape (menus: beadsList, no loop
// block, name = "Iterate implementing features"), and renders the body across
// two representative Args contexts:
//
//	(a) Commit absent      — the child arguments literal defaults to "true";
//	    the body dispatches to the per-feature driver by name and wires up
//	    the spawn+wait+archive tool triplet with the exact loop budget
//	    (30 / 30 / 28800) that mirrors the per-feature driver's own block.
//	(b) Commit="false"     — the child-arguments literal for Commit flips to
//	    "false", confirming the boolean forwarding is wired correctly.
//
// The frontmatter assertions (menus: beadsList; NO loop: block; name is
// "Iterate implementing features") are checked once, alongside the (a) render.
//
// The test loads the file from the real builtin directory so it always
// exercises the current on-disk content; the render itself also proves the
// YAML/template parses. Mirrors TestIterateFixingBugs_RendersForRepresentativeContexts.
func TestIterateImplementingFeatures_RendersForRepresentativeContexts(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issue-iterate-implementing-features.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issue-iterate-implementing-features.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}

	// Frontmatter assertions — this is a list-level orchestrator with no
	// Item.* context and no loop block of its own (single-run internal loop).
	if prompt.Name != "Iterate implementing features" {
		t.Errorf("Name = %q, want %q", prompt.Name, "Iterate implementing features")
	}
	if strings.TrimSpace(prompt.Menus) != "beadsList" {
		t.Errorf("Menus = %q, want %q", prompt.Menus, "beadsList")
	}
	if prompt.Loop != nil {
		t.Errorf("Loop = %+v, want nil — this orchestrator is a single-run internal loop", prompt.Loop)
	}

	body := prompt.Content

	render := func(ctx *cel.PromptEnabledContext) string {
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-issue-iterate-implementing-features", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (a) Default context — Commit absent → default to "true" in child args.
	outA := render(&cel.PromptEnabledContext{})

	// The orchestrator dispatches to the per-feature driver by name.
	if !strings.Contains(outA, "Iterate implementing feature") {
		t.Errorf("branch (a): expected reference to per-feature driver name \"Iterate implementing feature\"; got:\n%s", outA)
	}
	// Top-level-only + degrade-gracefully guidance must appear.
	if !strings.Contains(outA, "top-level") {
		t.Errorf("branch (a): expected 'top-level' spawn-recursion note; got:\n%s", outA)
	}
	// Spawn + wait + archive tool triplet must appear.
	for _, tool := range []string{
		"mitto_prompt_get",
		"mitto_conversation_new",
		"mitto_children_tasks_wait",
		"mitto_conversation_archive",
	} {
		if !strings.Contains(outA, tool) {
			t.Errorf("branch (a): expected orchestration tool call %q in body; got:\n%s", tool, outA)
		}
	}
	// Loop re-fire mechanics that make each child self-drive.
	for _, hint := range []string{
		"onCompletion",
		"loop_prompt",
		"loop_completion_delay_seconds: 30",
		"loop_max_iterations: 30",
		"loop_max_duration_seconds: 28800",
	} {
		if !strings.Contains(outA, hint) {
			t.Errorf("branch (a): expected loop-budget hint %q in body; got:\n%s", hint, outA)
		}
	}
	// Preflight-flag guidance so a user with either flag off gets a graceful stop.
	for _, flag := range []string{
		"Can start conversation",
		"Can Send Prompt",
	} {
		if !strings.Contains(outA, flag) {
			t.Errorf("branch (a): expected flag preflight text %q in body; got:\n%s", flag, outA)
		}
	}
	// The terminal label the per-feature loop self-terminates at.
	if !strings.Contains(outA, "verified") {
		t.Errorf("branch (a): expected terminal label \"verified\" reference in body; got:\n%s", outA)
	}
	// Commit absent → defaults to "true" in the spawned child's arguments map.
	if !strings.Contains(outA, `"Commit": "true"`) {
		t.Errorf("branch (a): expected default Commit=\"true\" in child arguments when Commit arg is absent; got:\n%s", outA)
	}

	// (b) Commit="false" → the child arguments literal flips to "false".
	outB := render(&cel.PromptEnabledContext{Args: map[string]string{"Commit": "false"}})
	if !strings.Contains(outB, `"Commit": "false"`) {
		t.Errorf("branch (b): expected Commit=\"false\" in child arguments when Commit arg is \"false\"; got:\n%s", outB)
	}
	if strings.Contains(outB, `"Commit": "true"`) {
		t.Errorf("branch (b): unexpected Commit=\"true\" in child arguments when Commit arg is \"false\"; got:\n%s", outB)
	}
}

// TestBugFixPhasePrompts_ParseAndDeclarePreferredModels verifies that the three
// per-phase bug-fix prompts (Option A tiering, mitto-gap.1) parse from disk,
// stay hidden from user-facing menus (menus: internal so no UI consumes them),
// and declare the expected preferredModels tag so
// resolvePreferredModelsByPromptName → SelectPreferredModel → setActiveModelOnly
// switches to the right tier when they are dispatched by name from the driver.
func TestBugFixPhasePrompts_ParseAndDeclarePreferredModels(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"

	cases := []struct {
		file         string
		name         string
		expectedTier string
	}{
		{
			file:         "beads-issue-fix-phase-investigate.prompt.yaml",
			name:         "Bug fix — investigate phase",
			expectedTier: "Reasoning",
		},
		{
			file:         "beads-issue-fix-phase-reproduce.prompt.yaml",
			name:         "Bug fix — reproduce phase",
			expectedTier: "Coding",
		},
		{
			file:         "beads-issue-fix-phase-fix.prompt.yaml",
			name:         "Bug fix — fix phase",
			expectedTier: "Coding",
		},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			path := filepath.Join(builtinDir, tc.file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("prompt file not found at %s: %v", path, err)
			}
			p, err := ParsePromptFile(tc.file, data, time.Now())
			if err != nil {
				t.Fatalf("ParsePromptFile(%s): %v", tc.file, err)
			}
			if p.Name != tc.name {
				t.Errorf("%s: Name = %q, want %q", tc.file, p.Name, tc.name)
			}
			// menus: internal keeps the prompt out of every UI menu (no
			// frontend filter consumes "internal") while leaving it
			// resolvable by name for programmatic dispatch.
			if strings.TrimSpace(p.Menus) != "internal" {
				t.Errorf("%s: Menus = %q, want \"internal\" (must stay hidden from UI menus)", tc.file, p.Menus)
			}
			if len(p.PreferredModels) == 0 {
				t.Fatalf("%s: PreferredModels is empty; per-phase tiering requires a preferredModels entry", tc.file)
			}
			got := p.PreferredModels[0].ModelTag
			if got != tc.expectedTier {
				t.Errorf("%s: PreferredModels[0].ModelTag = %q, want %q", tc.file, got, tc.expectedTier)
			}
		})
	}
}

// TestBugFixPhasePrompts_RenderForRepresentativeContexts renders each of the
// three phase-tier prompts with (a) a linked-issue context, (b) an arg-only
// context, and (c) a no-target context, and asserts each render succeeds and
// picks the right branch (target resolved → Step 1/2/3 renders; no target →
// missing-target guidance renders, no broken "bd show" command leaks). This
// mirrors TestIterateFixingBug_RendersForRepresentativeContexts and guards
// against future template regressions in the phase prompts themselves.
func TestBugFixPhasePrompts_RenderForRepresentativeContexts(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"

	files := []string{
		"beads-issue-fix-phase-investigate.prompt.yaml",
		"beads-issue-fix-phase-reproduce.prompt.yaml",
		"beads-issue-fix-phase-fix.prompt.yaml",
	}

	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			path := filepath.Join(builtinDir, file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("prompt file not found at %s: %v", path, err)
			}
			p, err := ParsePromptFile(file, data, time.Now())
			if err != nil {
				t.Fatalf("ParsePromptFile(%s): %v", file, err)
			}
			body := p.Content

			render := func(ctx *cel.PromptEnabledContext) string {
				funcs := cel.BuildTemplateFuncMap(ctx)
				out, rerr := RenderPromptTemplate(p.Name, body, ctx, funcs)
				if rerr != nil {
					t.Fatalf("%s: RenderPromptTemplate: %v", file, rerr)
				}
				return out
			}

			// (a) Linked-issue context.
			outA := render(&cel.PromptEnabledContext{
				Session: cel.SessionContext{BeadsIssue: "mitto-abc", HasBeadsIssue: true},
			})
			if !strings.Contains(outA, "mitto-abc") {
				t.Errorf("%s branch (a): expected bead ID 'mitto-abc' in output", file)
			}
			if !strings.Contains(outA, "bd show mitto-abc --json --include-comments") {
				t.Errorf("%s branch (a): expected 'bd show mitto-abc --json --include-comments' in output", file)
			}

			// (b) Arg-only context — IssueID supplied by the dispatching
			// driver (which is the primary invocation path for phase prompts).
			args := map[string]string{"IssueID": "mitto-xyz"}
			if file == "beads-issue-fix-phase-fix.prompt.yaml" {
				args["Commit"] = "true"
			}
			outB := render(&cel.PromptEnabledContext{Args: args})
			if !strings.Contains(outB, "mitto-xyz") {
				t.Errorf("%s branch (b): expected bead ID 'mitto-xyz' in output", file)
			}
			if strings.Contains(outB, "bd show  ") || strings.Contains(outB, "bd show \n") {
				t.Errorf("%s branch (b): found broken empty 'bd show ' command in output", file)
			}

			// Fix phase must render commit-enabled scaffolding when Commit=true.
			if file == "beads-issue-fix-phase-fix.prompt.yaml" {
				if !strings.Contains(outB, "git commit -m") {
					t.Errorf("%s branch (b): expected 'git commit -m' scaffolding when Commit=true; got:\n%s", file, outB)
				}
			}

			// (c) No target resolvable — the phase prompt must not run any
			// `bd` command (no broken empty invocations) and must render its
			// missing-target guidance.
			outC := render(&cel.PromptEnabledContext{})
			if strings.Contains(outC, "bd show  ") || strings.Contains(outC, "bd show \n") {
				t.Errorf("%s branch (c): found broken empty 'bd show ' command in output", file)
			}
			if !strings.Contains(outC, "No target bug is resolvable") {
				t.Errorf("%s branch (c): expected 'No target bug is resolvable' guidance; got:\n%s", file, outC)
			}
			// Even without a target, the Step 4 handoff block still renders
			// its placeholder for user-driven recovery.
			if !strings.Contains(outC, "<target-bug>") {
				t.Errorf("%s branch (c): expected '<target-bug>' placeholder in Step 4 handoff", file)
			}
		})
	}
}

// TestIterateImplementingFeature_RendersForRepresentativeContexts renders
// beads-issue-iterate-implementing-feature.prompt.yaml (mitto-gap.5) for
// representative contexts and asserts it renders without error and picks the
// right branch:
//
//	(a) linked-issue context  — .Session.BeadsIssue set, first run (default
//	    zero-value Iteration) → bead ID appears; interactive "Interaction Mode"
//	    header renders (not the uninterrupted continuation form); driver
//	    dispatches phase prompts by name (per-phase model tiering).
//	(b) arg-only context      — .Args.IssueID set, .Iteration.IsUninterrupted
//	    true (silent scheduled continuation) → bead ID appears; the compact
//	    "Continuation — uninterrupted scheduled run" header renders instead of
//	    the verbose "Interaction Mode" header; Commit=true propagates into the
//	    Implement/Test/Review-phase dispatch arguments.
//	(c) no-target context     — neither BeadsIssue nor IssueID set → the
//	    "not explicitly specified" guidance appears and no `bd show` command
//	    leaks (Step 1 is skipped entirely without a resolved target); the
//	    Step 4 handoff commands still use the "<target-feature>" placeholder.
//
// The test loads the file from the real builtin directory so it always
// exercises the current on-disk content; the render itself also proves the
// YAML/template parses. Mirrors TestIterateFixingBug_RendersForRepresentativeContexts.
func TestIterateImplementingFeature_RendersForRepresentativeContexts(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issue-iterate-implementing-feature.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issue-iterate-implementing-feature.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	body := prompt.Content

	render := func(ctx *cel.PromptEnabledContext) string {
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-issue-iterate-implementing-feature", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (a) Linked-issue context, first interactive run.
	ctxA := &cel.PromptEnabledContext{
		Session: cel.SessionContext{
			BeadsIssue:    "mitto-abc",
			HasBeadsIssue: true,
		},
		Iteration: cel.IterationContext{IsFirst: true},
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
	// Per-phase model tiering: the driver must dispatch phase prompts by name
	// via mitto_conversation_send_prompt (self-send), NOT do the phase work
	// inline. The four phase prompt names and the self-send tool must appear
	// in the rendered body with the resolved target as IssueID.
	for _, phaseName := range []string{
		"Feature — plan phase",
		"Feature — implement phase",
		"Feature — test phase",
		"Feature — review phase",
	} {
		if !strings.Contains(outA, phaseName) {
			t.Errorf("branch (a): expected phase dispatch to %q in output; got:\n%s", phaseName, outA)
		}
	}
	if !strings.Contains(outA, "mitto_conversation_send_prompt") {
		t.Errorf("branch (a): expected 'mitto_conversation_send_prompt' self-send calls in output")
	}
	if !strings.Contains(outA, `"IssueID": "mitto-abc"`) {
		t.Errorf("branch (a): expected resolved target 'mitto-abc' passed as IssueID argument; got:\n%s", outA)
	}
	// The driver must NOT do the phase work inline anymore. It must never
	// contain `bd update ... --add-label planned|implemented|tested|verified`
	// — that is the phase prompts' job.
	for _, forbidden := range []string{
		"--add-label planned",
		"--add-label implemented",
		"--add-label tested",
		"--add-label verified",
	} {
		if strings.Contains(outA, forbidden) {
			t.Errorf("branch (a): driver leaked inline phase-label write %q — must be delegated to the phase prompt; got:\n%s", forbidden, outA)
		}
	}

	// (b) Arg-only context, uninterrupted silent continuation run, with Commit=true.
	ctxB := &cel.PromptEnabledContext{
		Args:      map[string]string{"IssueID": "mitto-xyz", "Commit": "true"},
		Iteration: cel.IterationContext{IsLoop: true, IsUninterrupted: true},
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
	// Commit=true must render into the Implement/Test/Review dispatch args.
	if got, want := strings.Count(outB, `"Commit": "true"`), 3; got != want {
		t.Errorf("branch (b): expected Commit=true propagated into %d phase dispatch arguments (Implement/Test/Review), got %d; output:\n%s", want, got, outB)
	}
	if !strings.Contains(outB, `"IssueID": "mitto-xyz"`) {
		t.Errorf("branch (b): expected resolved target 'mitto-xyz' passed as IssueID; got:\n%s", outB)
	}

	// (c) No target resolvable — neither BeadsIssue nor Args.IssueID set. Step 1
	// (state loading, "bd show") is skipped entirely without a target; the
	// Blocked → Defer + Handoff step (Step 4) still renders, using the
	// "<target-feature>" placeholder rather than an empty/broken argument, since
	// it is the documented escape hatch for this exact situation.
	ctxC := &cel.PromptEnabledContext{}
	outC := render(ctxC)
	if !strings.Contains(outC, "not explicitly specified") {
		t.Errorf("branch (c): expected 'not explicitly specified' guidance; got:\n%s", outC)
	}
	if !strings.Contains(outC, "No target feature to work on") {
		t.Errorf("branch (c): expected the 'No target feature to work on' Step 1 fallback; got:\n%s", outC)
	}
	if strings.Contains(outC, "bd show  ") || strings.Contains(outC, "bd show \n") {
		t.Errorf("branch (c): found broken empty 'bd show ' command in output")
	}
	if !strings.Contains(outC, "<target-feature>") {
		t.Errorf("branch (c): expected the '<target-feature>' placeholder in the Step 4 handoff commands; got:\n%s", outC)
	}
}

// TestFeaturePhasePrompts_ParseAndDeclarePreferredModels verifies that the
// four per-phase feature-implementation prompts (Option A tiering, mitto-gap.5)
// parse from disk, stay hidden from user-facing menus (menus: internal so no
// UI consumes them), and declare the expected preferredModels tag so
// resolvePreferredModelsByPromptName → SelectPreferredModel → setActiveModelOnly
// switches to the right tier when they are dispatched by name from the driver.
// Mirrors TestBugFixPhasePrompts_ParseAndDeclarePreferredModels.
func TestFeaturePhasePrompts_ParseAndDeclarePreferredModels(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"

	cases := []struct {
		file         string
		name         string
		expectedTier string
	}{
		{
			file:         "beads-issue-feature-phase-plan.prompt.yaml",
			name:         "Feature — plan phase",
			expectedTier: "Reasoning",
		},
		{
			file:         "beads-issue-feature-phase-implement.prompt.yaml",
			name:         "Feature — implement phase",
			expectedTier: "Coding",
		},
		{
			file:         "beads-issue-feature-phase-test.prompt.yaml",
			name:         "Feature — test phase",
			expectedTier: "Coding",
		},
		{
			file:         "beads-issue-feature-phase-review.prompt.yaml",
			name:         "Feature — review phase",
			expectedTier: "Reasoning",
		},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			path := filepath.Join(builtinDir, tc.file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("prompt file not found at %s: %v", path, err)
			}
			p, err := ParsePromptFile(tc.file, data, time.Now())
			if err != nil {
				t.Fatalf("ParsePromptFile(%s): %v", tc.file, err)
			}
			if p.Name != tc.name {
				t.Errorf("%s: Name = %q, want %q", tc.file, p.Name, tc.name)
			}
			// menus: internal keeps the prompt out of every UI menu (no
			// frontend filter consumes "internal") while leaving it
			// resolvable by name for programmatic dispatch.
			if strings.TrimSpace(p.Menus) != "internal" {
				t.Errorf("%s: Menus = %q, want \"internal\" (must stay hidden from UI menus)", tc.file, p.Menus)
			}
			if len(p.PreferredModels) == 0 {
				t.Fatalf("%s: PreferredModels is empty; per-phase tiering requires a preferredModels entry", tc.file)
			}
			got := p.PreferredModels[0].ModelTag
			if got != tc.expectedTier {
				t.Errorf("%s: PreferredModels[0].ModelTag = %q, want %q", tc.file, got, tc.expectedTier)
			}
		})
	}
}

// TestFeaturePhasePrompts_RenderForRepresentativeContexts renders each of the
// four phase-tier prompts with (a) a linked-issue context, (b) an arg-only
// context, and (c) a no-target context, and asserts each render succeeds and
// picks the right branch (target resolved → Step 1/2/3 renders; no target →
// missing-target guidance renders, no broken "bd show" command leaks). Mirrors
// TestBugFixPhasePrompts_RenderForRepresentativeContexts and guards against
// future template regressions in the feature phase prompts themselves.
func TestFeaturePhasePrompts_RenderForRepresentativeContexts(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"

	files := []string{
		"beads-issue-feature-phase-plan.prompt.yaml",
		"beads-issue-feature-phase-implement.prompt.yaml",
		"beads-issue-feature-phase-test.prompt.yaml",
		"beads-issue-feature-phase-review.prompt.yaml",
	}

	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			path := filepath.Join(builtinDir, file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("prompt file not found at %s: %v", path, err)
			}
			p, err := ParsePromptFile(file, data, time.Now())
			if err != nil {
				t.Fatalf("ParsePromptFile(%s): %v", file, err)
			}
			body := p.Content

			render := func(ctx *cel.PromptEnabledContext) string {
				funcs := cel.BuildTemplateFuncMap(ctx)
				out, rerr := RenderPromptTemplate(p.Name, body, ctx, funcs)
				if rerr != nil {
					t.Fatalf("%s: RenderPromptTemplate: %v", file, rerr)
				}
				return out
			}

			// (a) Linked-issue context.
			outA := render(&cel.PromptEnabledContext{
				Session: cel.SessionContext{BeadsIssue: "mitto-abc", HasBeadsIssue: true},
			})
			if !strings.Contains(outA, "mitto-abc") {
				t.Errorf("%s branch (a): expected bead ID 'mitto-abc' in output", file)
			}
			if !strings.Contains(outA, "bd show mitto-abc --json --include-comments") {
				t.Errorf("%s branch (a): expected 'bd show mitto-abc --json --include-comments' in output", file)
			}

			// (b) Arg-only context — IssueID supplied by the dispatching
			// driver (which is the primary invocation path for phase prompts).
			args := map[string]string{"IssueID": "mitto-xyz"}
			if file != "beads-issue-feature-phase-plan.prompt.yaml" {
				args["Commit"] = "true"
			}
			outB := render(&cel.PromptEnabledContext{Args: args})
			if !strings.Contains(outB, "mitto-xyz") {
				t.Errorf("%s branch (b): expected bead ID 'mitto-xyz' in output", file)
			}
			if strings.Contains(outB, "bd show  ") || strings.Contains(outB, "bd show \n") {
				t.Errorf("%s branch (b): found broken empty 'bd show ' command in output", file)
			}

			// Implement/Test/Review phases must render commit-enabled
			// scaffolding when Commit=true. The Plan phase has no Commit param.
			if file != "beads-issue-feature-phase-plan.prompt.yaml" {
				if !strings.Contains(outB, "git commit -m") {
					t.Errorf("%s branch (b): expected 'git commit -m' scaffolding when Commit=true; got:\n%s", file, outB)
				}
			}

			// (c) No target resolvable — the phase prompt must not run any
			// `bd` command (no broken empty invocations) and must render its
			// missing-target guidance.
			outC := render(&cel.PromptEnabledContext{})
			if strings.Contains(outC, "bd show  ") || strings.Contains(outC, "bd show \n") {
				t.Errorf("%s branch (c): found broken empty 'bd show ' command in output", file)
			}
			if !strings.Contains(outC, "No target feature is resolvable") {
				t.Errorf("%s branch (c): expected 'No target feature is resolvable' guidance; got:\n%s", file, outC)
			}
			// Even without a target, the Step 4 handoff block still renders
			// its placeholder for user-driven recovery.
			if !strings.Contains(outC, "<target-feature>") {
				t.Errorf("%s branch (c): expected '<target-feature>' placeholder in Step 4 handoff", file)
			}
		})
	}
}

// TestPhasePrompts_TierCheckRendersForModelTags is the mitto-mpu5 acceptance
// test: every §B (bug-fix) and §C (feature) phase prompt must render a
// tier-check block that
//
//  1. always displays the active model name + tags at dispatch time, so a
//     tier-degraded run is observable in the transcript, and
//  2. branches on Model(<declared tier>) — "✓ <tier> tier confirmed" when the
//     session's ModelTags include the tier, and a "⚠ Tier-degraded run." block
//     with an inline `bd comment` recording the degradation when they do not.
//
// Test guards against future regressions in ANY of the 7 phase prompts
// (drop-out of the tier-check block, wrong tier name, or wrong comment prefix).
func TestPhasePrompts_TierCheckRendersForModelTags(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"

	cases := []struct {
		file string
		tier string // declared tier per the phase's preferredModels
	}{
		{"beads-issue-fix-phase-investigate.prompt.yaml", "Reasoning"},
		{"beads-issue-fix-phase-reproduce.prompt.yaml", "Coding"},
		{"beads-issue-fix-phase-fix.prompt.yaml", "Coding"},
		{"beads-issue-feature-phase-plan.prompt.yaml", "Reasoning"},
		{"beads-issue-feature-phase-implement.prompt.yaml", "Coding"},
		{"beads-issue-feature-phase-test.prompt.yaml", "Coding"},
		{"beads-issue-feature-phase-review.prompt.yaml", "Reasoning"},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			path := filepath.Join(builtinDir, tc.file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("prompt file not found at %s: %v", path, err)
			}
			p, err := ParsePromptFile(tc.file, data, time.Now())
			if err != nil {
				t.Fatalf("ParsePromptFile(%s): %v", tc.file, err)
			}
			body := p.Content

			render := func(ctx *cel.PromptEnabledContext) string {
				funcs := cel.BuildTemplateFuncMap(ctx)
				out, rerr := RenderPromptTemplate(p.Name, body, ctx, funcs)
				if rerr != nil {
					t.Fatalf("%s: RenderPromptTemplate: %v", tc.file, rerr)
				}
				return out
			}

			// Common context: arg-only IssueID so the tier-check block renders
			// (the block is gated by target-resolved).
			args := map[string]string{"IssueID": "mitto-xyz"}
			if tc.file == "beads-issue-fix-phase-fix.prompt.yaml" ||
				tc.file == "beads-issue-feature-phase-implement.prompt.yaml" ||
				tc.file == "beads-issue-feature-phase-test.prompt.yaml" ||
				tc.file == "beads-issue-feature-phase-review.prompt.yaml" {
				args["Commit"] = "false"
			}

			// (1) Confirmed tier: session model carries the declared tier tag.
			outOK := render(&cel.PromptEnabledContext{
				Args: args,
				Session: cel.SessionContext{
					ModelName: "TestModel",
					ModelTags: []string{tc.tier},
				},
			})
			if !strings.Contains(outOK, "## Tier check") {
				t.Errorf("%s: expected '## Tier check' section in rendered output", tc.file)
			}
			if !strings.Contains(outOK, "TestModel") {
				t.Errorf("%s: expected active model name 'TestModel' in tier-check block", tc.file)
			}
			wantOK := "✓ " + tc.tier + " tier confirmed"
			if !strings.Contains(outOK, wantOK) {
				t.Errorf("%s: expected confirmed-tier marker %q; got:\n%s", tc.file, wantOK, outOK)
			}
			if strings.Contains(outOK, "⚠ **Tier-degraded run.**") {
				t.Errorf("%s: unexpected tier-degraded warning on confirmed-tier run", tc.file)
			}

			// (2) Degraded tier: session model carries a DIFFERENT tier tag.
			otherTier := "Coding"
			if tc.tier == "Coding" {
				otherTier = "Reasoning"
			}
			outDeg := render(&cel.PromptEnabledContext{
				Args: args,
				Session: cel.SessionContext{
					ModelName: "WrongTierModel",
					ModelTags: []string{otherTier},
				},
			})
			if !strings.Contains(outDeg, "⚠ **Tier-degraded run.**") {
				t.Errorf("%s: expected tier-degraded warning when active tags do not include %q; got:\n%s", tc.file, tc.tier, outDeg)
			}
			if !strings.Contains(outDeg, "WrongTierModel") {
				t.Errorf("%s: expected mismatched model name 'WrongTierModel' in degraded block", tc.file)
			}
			if !strings.Contains(outDeg, "tier-degraded [phase:") {
				t.Errorf("%s: expected 'tier-degraded [phase: …]' marker in bd comment fallback; got:\n%s", tc.file, outDeg)
			}
			if !strings.Contains(outDeg, "declared "+tc.tier) {
				t.Errorf("%s: expected 'declared %s' in bd comment fallback", tc.file, tc.tier)
			}
			// The bd comment must reference the resolved target ID.
			if !strings.Contains(outDeg, "bd comment mitto-xyz") {
				t.Errorf("%s: expected 'bd comment mitto-xyz' in degraded fallback", tc.file)
			}

			// (3) Unknown model (cold start / no profiles match): ModelName empty,
			// ModelTags nil. Must render the degraded branch with "<unknown>" +
			// "none" tags — no template errors, no crash.
			outUnknown := render(&cel.PromptEnabledContext{
				Args:    args,
				Session: cel.SessionContext{},
			})
			if !strings.Contains(outUnknown, "⚠ **Tier-degraded run.**") {
				t.Errorf("%s: expected tier-degraded warning when model unknown; got:\n%s", tc.file, outUnknown)
			}
			if !strings.Contains(outUnknown, "<unknown>") {
				t.Errorf("%s: expected '<unknown>' placeholder when ModelName empty", tc.file)
			}
			if !strings.Contains(outUnknown, "tags: none") {
				t.Errorf("%s: expected 'tags: none' when ModelTags empty", tc.file)
			}
		})
	}
}

// TestPhasePrompts_TierTaggedCommentPrefix verifies the mitto-mpu5 §B/§C
// observability requirement: each phase's Step 3 `bd comment` starts with a
// tier-tagged prefix (e.g. "Plan [tier: Reasoning]:", "Fix [tier: Coding]:")
// so a bead's audit trail makes the tier split visible without needing to
// cross-reference the run's active model.
func TestPhasePrompts_TierTaggedCommentPrefix(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"

	cases := []struct {
		file   string
		prefix string // exact "<Noun> [tier: <Tier>]:" fragment expected in the rendered body
	}{
		{"beads-issue-fix-phase-investigate.prompt.yaml", "Investigation [tier: Reasoning]:"},
		{"beads-issue-fix-phase-reproduce.prompt.yaml", "Reproduction [tier: Coding]:"},
		{"beads-issue-fix-phase-fix.prompt.yaml", "Fix [tier: Coding]:"},
		{"beads-issue-feature-phase-plan.prompt.yaml", "Plan [tier: Reasoning]:"},
		{"beads-issue-feature-phase-implement.prompt.yaml", "Implementation [tier: Coding]:"},
		{"beads-issue-feature-phase-test.prompt.yaml", "Testing [tier: Coding]:"},
		{"beads-issue-feature-phase-review.prompt.yaml", "Review [tier: Reasoning]:"},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			path := filepath.Join(builtinDir, tc.file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("prompt file not found at %s: %v", path, err)
			}
			p, err := ParsePromptFile(tc.file, data, time.Now())
			if err != nil {
				t.Fatalf("ParsePromptFile(%s): %v", tc.file, err)
			}

			args := map[string]string{"IssueID": "mitto-xyz"}
			if tc.file == "beads-issue-fix-phase-fix.prompt.yaml" ||
				tc.file == "beads-issue-feature-phase-implement.prompt.yaml" ||
				tc.file == "beads-issue-feature-phase-test.prompt.yaml" ||
				tc.file == "beads-issue-feature-phase-review.prompt.yaml" {
				args["Commit"] = "false"
			}
			ctx := &cel.PromptEnabledContext{Args: args}
			funcs := cel.BuildTemplateFuncMap(ctx)
			out, err := RenderPromptTemplate(p.Name, p.Content, ctx, funcs)
			if err != nil {
				t.Fatalf("RenderPromptTemplate: %v", err)
			}
			if !strings.Contains(out, tc.prefix) {
				t.Errorf("%s: expected tier-tagged Step 3 comment prefix %q in rendered body; got:\n%s", tc.file, tc.prefix, out)
			}
		})
	}
}

// TestBuiltinPromptLoopModes verifies the mitto-92x.6 mechanical flagging
// pass: every builtin prompt assigned a mode/default in the epic's
// classification table parses with the expected PromptLoop.Mode/Default,
// and a representative sample of the "never loop" set has no loop
// block at all.
func TestBuiltinPromptLoopModes(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"

	boolPtr := func(b bool) *bool { return &b }

	type want struct {
		mode string
		def  *bool // nil means PromptLoop.Default must be nil
	}

	cases := map[string]want{
		// Group A — always (5).
		"beads-issue-iterate-until-complete.prompt.yaml": {mode: "always", def: nil},
		"github-iterate-babysit-new-prs.prompt.yaml":     {mode: "always", def: nil},
		"iterate-until.prompt.yaml":                      {mode: "always", def: nil},
		"iterate-fixing.prompt.yaml":                     {mode: "always", def: nil},
		"iterate-implementing.prompt.yaml":               {mode: "always", def: nil},

		// Group B — optional / default:true (4).
		"github-babysit-contributions.prompt.yaml": {mode: "optional", def: boolPtr(true)},
		"github-babysit-my-prs.prompt.yaml":        {mode: "optional", def: boolPtr(true)},
		"github-sync-tasks.prompt.yaml":            {mode: "optional", def: boolPtr(true)},
		"jira-sync-tasks.prompt.yaml":              {mode: "optional", def: boolPtr(true)},

		// Group C — optional / default:false (11).
		"check-ci.prompt.yaml":                   {mode: "optional", def: boolPtr(false)},
		"continue.prompt.yaml":                   {mode: "optional", def: boolPtr(false)},
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
			if prompt.Loop == nil {
				t.Fatalf("%s: Loop = nil, want non-nil", file)
			}
			if prompt.Loop.Mode != w.mode {
				t.Errorf("%s: Loop.Mode = %q, want %q", file, prompt.Loop.Mode, w.mode)
			}
			if w.def == nil {
				if prompt.Loop.Default != nil {
					t.Errorf("%s: Loop.Default = %v, want nil", file, *prompt.Loop.Default)
				}
			} else {
				if prompt.Loop.Default == nil {
					t.Errorf("%s: Loop.Default = nil, want %v", file, *w.def)
				} else if *prompt.Loop.Default != *w.def {
					t.Errorf("%s: Loop.Default = %v, want %v", file, *prompt.Loop.Default, *w.def)
				}
			}
		})
	}

	// Representative sample of the "never loop" set: no loop block at all.
	neverFiles := []string{
		"explain.prompt.yaml",
		"refactor.prompt.yaml",
		"review.prompt.yaml",
		"add-tests.prompt.yaml",
		"whats-next.prompt.yaml",
		"child-create-minions.prompt.yaml",
		"github-post-merge-cleanup.prompt.yaml",
		"beads-issue-decompose.prompt.yaml",
		// Tasks prompts that are one-shot reports, context-bound, or
		// confirmation-gated — loop re-firing makes no sense for them.
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
			if prompt.Loop != nil {
				t.Errorf("%s: Loop = %+v, want nil (never-loop set)", file, prompt.Loop)
			}
		})
	}
}

// TestMentionDriver_RendersForRepresentativeContexts renders
// beads-issue-mention-driver.prompt.yaml (mitto-91wk) for representative
// contexts and asserts it renders without error and picks the right branch:
//
//	(a) linked-issue context — .Session.BeadsIssue set, args carry a mention
//	    timestamp + body, Commit=true → the target bead ID renders, the
//	    mention timestamp/body flow into every phase dispatch, all four phase
//	    prompt NAMES appear (router dispatches via `prompt_name`, per
//	    mitto-dj9), the "handled inline" Finalize branch is present, and the
//	    Commit=true branch of the finalize step surfaces the git-add-by-path
//	    guidance (not the "do NOT commit" copy).
//	(b) arg-only context, Commit=false — .Args.IssueID set, no .Session.BeadsIssue,
//	    Commit=false → bead ID still resolves via Args.IssueID; the Commit=false
//	    copy renders ("do NOT commit" / "review and commit manually"); the
//	    Commit=true copy is absent.
//	(c) no target resolvable — neither BeadsIssue nor Args.IssueID set →
//	    the "No target bead is resolvable" fallback renders; Step 1..3 phase
//	    dispatch scaffolding is skipped (no `bd show ` broken command),
//	    Step 4 still renders using the "<target-bead>" placeholder.
//
// The test loads the file from the real builtin directory so it always
// exercises the current on-disk content; the render itself also proves the
// YAML/template parses.
func TestMentionDriver_RendersForRepresentativeContexts(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issue-mention-driver.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issue-mention-driver.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	body := prompt.Content

	render := func(ctx *cel.PromptEnabledContext) string {
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-issue-mention-driver", body, ctx, funcs)
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate: %v", rerr)
		}
		return out
	}

	// (a) Linked-issue context with Commit=true.
	ctxA := &cel.PromptEnabledContext{
		Session: cel.SessionContext{
			BeadsIssue:    "mitto-abc",
			HasBeadsIssue: true,
		},
		Args: map[string]string{
			"IssueID":     "mitto-abc",
			"MentionTS":   "2026-07-16T10:00:00Z",
			"MentionBody": "please fix the crash",
			"Commit":      "true",
		},
		Iteration: cel.IterationContext{IsFirst: true},
	}
	outA := render(ctxA)
	if !strings.Contains(outA, "mitto-abc") {
		t.Errorf("branch (a): expected bead ID 'mitto-abc' in output; got:\n%s", outA)
	}
	if !strings.Contains(outA, "2026-07-16T10:00:00Z") {
		t.Errorf("branch (a): expected mention timestamp in output; got:\n%s", outA)
	}
	// Per-phase model tiering: the router must dispatch phase prompts by
	// name via `mitto_conversation_send_prompt` (self-send), NOT do the
	// phase work inline. All four phase names must appear in the rendered
	// body (mitto-91wk acceptance criterion).
	for _, phaseName := range []string{
		"Mention — investigate phase",
		"Mention — plan phase",
		"Mention — implement phase",
		"Mention — answer phase",
	} {
		if !strings.Contains(outA, phaseName) {
			t.Errorf("branch (a): expected phase dispatch to %q in output; got:\n%s", phaseName, outA)
		}
	}
	if !strings.Contains(outA, "mitto_conversation_send_prompt") {
		t.Errorf("branch (a): expected 'mitto_conversation_send_prompt' self-send calls in output")
	}
	if !strings.Contains(outA, `"IssueID": "mitto-abc"`) {
		t.Errorf("branch (a): expected resolved target 'mitto-abc' passed as IssueID argument; got:\n%s", outA)
	}
	// Finalize branch must render inline (never dispatched as a phase).
	if !strings.Contains(outA, "Finalize") {
		t.Errorf("branch (a): expected 'Finalize' branch text in output")
	}
	if !strings.Contains(outA, "[addressed-comment:") {
		t.Errorf("branch (a): expected back-reference marker '[addressed-comment:' in output")
	}
	// Commit=true branch: git-add-by-path guidance must render, "do NOT commit" must not.
	if !strings.Contains(outA, "git add <file>") {
		t.Errorf("branch (a): expected Commit=true git-add guidance in output; got:\n%s", outA)
	}
	if strings.Contains(outA, "do NOT commit") {
		t.Errorf("branch (a): Commit=true rendered the Commit=false 'do NOT commit' copy")
	}
	// The router must NOT do the phase work inline — it must never contain
	// `bd update ... --add-label mention-{investigated|planned|implemented|answered}`.
	for _, forbidden := range []string{
		"--add-label mention-investigated",
		"--add-label mention-planned",
		"--add-label mention-implemented",
		"--add-label mention-answered",
	} {
		if strings.Contains(outA, forbidden) {
			t.Errorf("branch (a): router leaked inline phase-label write %q — must be delegated to the phase prompt; got:\n%s", forbidden, outA)
		}
	}

	// (b) Arg-only context, Commit=false.
	ctxB := &cel.PromptEnabledContext{
		Args: map[string]string{
			"IssueID":     "mitto-xyz",
			"MentionTS":   "2026-07-16T11:00:00Z",
			"MentionBody": "how do I run tests?",
			"Commit":      "false",
		},
		Iteration: cel.IterationContext{IsLoop: true},
	}
	outB := render(ctxB)
	if !strings.Contains(outB, "mitto-xyz") {
		t.Errorf("branch (b): expected bead ID 'mitto-xyz' in output; got:\n%s", outB)
	}
	if !strings.Contains(outB, "do NOT commit") {
		t.Errorf("branch (b): expected Commit=false 'do NOT commit' copy in output; got:\n%s", outB)
	}
	if strings.Contains(outB, "git add <file>") {
		t.Errorf("branch (b): Commit=false rendered the Commit=true git-add guidance")
	}
	if !strings.Contains(outB, `"IssueID": "mitto-xyz"`) {
		t.Errorf("branch (b): expected resolved target 'mitto-xyz' passed as IssueID; got:\n%s", outB)
	}

	// (c) No target resolvable — neither BeadsIssue nor Args.IssueID set.
	ctxC := &cel.PromptEnabledContext{}
	outC := render(ctxC)
	if !strings.Contains(outC, "No target bead is resolvable") {
		t.Errorf("branch (c): expected 'No target bead is resolvable' guidance; got:\n%s", outC)
	}
	if !strings.Contains(outC, "No target bead to work on") {
		t.Errorf("branch (c): expected 'No target bead to work on' Step 1 fallback; got:\n%s", outC)
	}
	if strings.Contains(outC, "bd show  ") || strings.Contains(outC, "bd show \n") {
		t.Errorf("branch (c): found broken empty 'bd show ' command in output")
	}
	if !strings.Contains(outC, "<target-bead>") {
		t.Errorf("branch (c): expected the '<target-bead>' placeholder in the Step 4 handoff commands; got:\n%s", outC)
	}
}

// TestLoopProcessingSpawns_MirrorArgumentsIntoLoopArguments reproduces mitto-rtdr.
//
// beads-issue-loop-processing.prompt.yaml spawns per-mention (§A), per-bug (§B) and
// per-feature (§C) child conversations, all with loop_prompt_name and
// loop_trigger: onCompletion — i.e. their onCompletion re-fires must render the
// loop body with the same .Args as the initial run. In internal/mcpserver the
// initial-prompt path reads input.Arguments (tools_conversation_new.go:651)
// while the loop-body path reads a separate input.LoopArguments field
// (:538 → session.LoopPrompt.Arguments). If the spawn block passes only
// arguments: and not loop_arguments:, every re-fire renders the loop body with
// .Args = nil (missingkey=zero → .Args.Commit == ""), which in the loop-body
// phase-dispatch template's positive-match gate
// (`{{ if eq .Args.Commit "true" }}true{{ else }}false{{ end }}`) resolves to
// "false" — silently disabling commits.
//
// The reproduction: render the orchestrator body with .Args.Commit = "true" and
// assert each of the §A, §B, §C spawn blocks includes BOTH `arguments:` AND
// `loop_arguments:` fields — with the resolved Commit value. The current
// template only sets `arguments:`, so this test fails and pins the bug in place
// until fix layer 1 lands.
func TestLoopProcessingSpawns_MirrorArgumentsIntoLoopArguments(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issue-loop-processing.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issue-loop-processing.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	body := prompt.Content

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{
			ID:            "orch-1",
			BeadsIssue:    "",
			HasBeadsIssue: false,
		},
		Args: map[string]string{"Commit": "true"},
	}
	funcs := cel.BuildTemplateFuncMap(ctx)
	out, rerr := RenderPromptTemplate("beads-issue-loop-processing", body, ctx, funcs)
	if rerr != nil {
		t.Fatalf("RenderPromptTemplate: %v", rerr)
	}

	// The three named-prompt spawn blocks — one per section. Each maps to a
	// loop_prompt_name whose loop body renders on onCompletion re-fires.
	sections := []struct {
		section    string // §A / §B / §C label for error messages
		promptName string // prompt_name string that anchors the spawn block
	}{
		{"§A", `prompt_name: "Mention — driver",`},
		{"§B", `prompt_name: "Loop fixing bug",`},
		{"§C", `prompt_name: "Loop implementing feature",`},
	}

	for _, sec := range sections {
		anchor := strings.Index(out, sec.promptName)
		if anchor < 0 {
			t.Errorf("%s: spawn block anchor %q not found in rendered orchestrator; got:\n%s",
				sec.section, sec.promptName, out)
			continue
		}
		// The spawn block is a compact mitto_conversation_new(...) call — bound
		// the window generously to the next closing paren.
		end := strings.Index(out[anchor:], "\n  )\n")
		if end < 0 {
			end = len(out) - anchor
			if end > 2000 {
				end = 2000
			}
		}
		block := out[anchor : anchor+end]

		if !strings.Contains(block, "arguments:") {
			t.Errorf("%s: spawn block is missing an `arguments:` field entirely; block:\n%s",
				sec.section, block)
		}
		if !strings.Contains(block, "loop_arguments:") {
			t.Errorf("%s: spawn block sets loop_prompt_name + onCompletion but is missing `loop_arguments:` — every re-fire will render the loop body with an empty .Args. Mirror `arguments:` into `loop_arguments:`. Block:\n%s",
				sec.section, block)
		}
		// loop_arguments must carry the resolved Commit value so the
		// positive-match gate in the loop body resolves correctly on every
		// re-fire, not just the initial prompt.
		if strings.Contains(block, "loop_arguments:") &&
			!strings.Contains(block, `"Commit": "true"`) {
			t.Errorf("%s: rendered .Args.Commit=\"true\" but no `\"Commit\": \"true\"` in the spawn block; block:\n%s",
				sec.section, block)
		}
	}
}
