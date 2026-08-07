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

// TestInvestigateAllMore_LoopAndModes verifies the beads/investigate-all-more
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
func TestInvestigateAllMore_LoopAndModes(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"
	name := "beads/investigate-all-more.prompt.yaml"
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
	if !prompt.Loop.hasTrigger("onTasks") {
		t.Errorf("loop.trigger = %v, want to include %q", prompt.Loop.Trigger, "onTasks")
	}
	if prompt.Loop.Mode != PromptLoopModeAlways {
		t.Errorf("loop.mode = %q, want %q", prompt.Loop.Mode, PromptLoopModeAlways)
	}
	if !strings.Contains(prompt.Loop.TasksCondition(), "implementation-refined") {
		t.Errorf("loop.onTasks.condition should gate on the implementation-refined label; got %q", prompt.Loop.TasksCondition())
	}

	body := prompt.Content
	render := func(ctx *cel.PromptEnabledContext) string {
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, rerr := RenderPromptTemplate("beads-investigate-all-more", body, ctx, funcs)
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
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issues/investigate.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issues/investigate.prompt.yaml", data, time.Now())
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
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issues/assess.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issues/assess.prompt.yaml", data, time.Now())
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
	installBuiltinFragmentsForTest(t)
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

// TestBuiltinPrompts_WithFragments (mitto-g61.6 test #2) re-runs the same
// render sweep as TestBuiltinPrompts_AllRenderWithoutError with the on-disk
// builtin fragments (if any) installed on the process-wide singleton. It
// asserts that having a real fragment registry attached does not regress
// rendering of any builtin prompt (i.e. no name collisions between fragments
// and builtin templates, no funcmap conflicts introduced by attach).
func TestBuiltinPrompts_WithFragments(t *testing.T) {
	// Isolate the singleton so parallel tests are unaffected.
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	prompts, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Skipf("cannot load builtins from %s: %v", builtinDir, err)
	}
	if len(prompts) == 0 {
		t.Skip("no builtin prompts found")
	}
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

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
		t.Errorf("builtin prompts failed to render with fragments installed:\n  %s", strings.Join(failures, "\n  "))
	}
	t.Logf("rendered %d builtin prompts against %d fragments ✓", len(prompts), reg.Len())
}

// NOTE (mitto-g61.6 tests #5, #6, #7): the caller-context / narrowed-context /
// funcmap-inheritance behaviors are already locked in by the existing
// TestRenderPromptTemplate_Fragments (basic-render, data-narrowing,
// funcmap-inheritance subtests, near the bottom of this file). No new
// duplicate tests are added here.
//
// TestRenderPromptTemplate_FragmentCycleFailsAtRender (mitto-g61.6 test #8)
// verifies that a fragment that references itself (or a cycle of fragments)
// triggers Go's text/template recursion limit at Execute time and returns a
// non-nil error rather than infinite-looping or panicking. Fail-closed is the
// documented contract of RenderPromptTemplate.
func TestRenderPromptTemplate_FragmentCycleFailsAtRender(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	reg := NewFragmentRegistry()
	// Direct self-reference: recurses without a termination condition.
	reg.entries["loop-a"] = `A{{ template "loop-b" . }}`
	reg.entries["loop-b"] = `B{{ template "loop-a" . }}`
	SetCurrentFragments(reg)

	ctx := &cel.PromptEnabledContext{}
	funcs := cel.BuildTemplateFuncMap(ctx)
	_, err := RenderPromptTemplate("t", `{{ template "loop-a" . }}`, ctx, funcs)
	if err == nil {
		t.Fatal("expected render error from cyclic fragment references, got nil")
	}
	// text/template's message for this is "exceeded maximum template depth".
	if !strings.Contains(err.Error(), "depth") && !strings.Contains(err.Error(), "recurs") {
		t.Logf("render error (accepted): %v", err)
	}
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
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issues/status.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issues/status.prompt.yaml", data, time.Now())
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
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issues/resolved.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issues/resolved.prompt.yaml", data, time.Now())
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
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issues/work.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issues/work.prompt.yaml", data, time.Now())
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
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads/followup-work.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads/followup-work.prompt.yaml", data, time.Now())
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

	// Install the on-disk fragment registry so ParsePromptFile can resolve
	// `{{ template "github/shared/pr-comments" . }}` at parse-time precompile
	// (mitto-g61.4). Restored on cleanup.
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

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
			file:              "docs/architectural-analysis.prompt.yaml",
			name:              "architectural-analysis",
			silentMarker:      "a scheduled loop run; the user is not watching.",
			interactiveMarker: "a regular conversation or a force-triggered loop run; the user is present.",
		},
		{
			file:              "jira/sync-tasks.prompt.yaml",
			name:              "jira-sync-tasks",
			silentMarker:      "a scheduled loop run; the user is not watching.",
			interactiveMarker: "a regular conversation or a force-triggered loop run; the user is present.",
		},
		{
			file:              "github/sync-tasks.prompt.yaml",
			name:              "github-sync-tasks",
			silentMarker:      "a scheduled loop run; the user is not watching.",
			interactiveMarker: "a regular conversation or a force-triggered loop run; the user is present.",
		},
		{
			file:              "github/babysit-contributions.prompt.yaml",
			name:              "github-babysit-contributions",
			silentMarker:      "a scheduled loop run; the user is not watching.",
			interactiveMarker: "a force-triggered run or a non-loop conversation; the user may be present.",
		},
		{
			file:              "github/babysit-my-prs.prompt.yaml",
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
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"

	cases := []struct {
		file         string
		name         string
		expectedTier string
	}{
		{
			file:         "beads-issues/fix-phase-investigate.prompt.yaml",
			name:         "Bug fix — investigate phase",
			expectedTier: "Reasoning",
		},
		{
			file:         "beads-issues/fix-phase-reproduce.prompt.yaml",
			name:         "Bug fix — reproduce phase",
			expectedTier: "Coding",
		},
		{
			file:         "beads-issues/fix-phase-fix.prompt.yaml",
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
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"

	files := []string{
		"beads-issues/fix-phase-investigate.prompt.yaml",
		"beads-issues/fix-phase-reproduce.prompt.yaml",
		"beads-issues/fix-phase-fix.prompt.yaml",
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
			if file == "beads-issues/fix-phase-fix.prompt.yaml" {
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
			if file == "beads-issues/fix-phase-fix.prompt.yaml" {
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
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"

	cases := []struct {
		file         string
		name         string
		expectedTier string
	}{
		{
			file:         "beads-issues/feature-phase-plan.prompt.yaml",
			name:         "Feature — plan phase",
			expectedTier: "Reasoning",
		},
		{
			file:         "beads-issues/feature-phase-implement.prompt.yaml",
			name:         "Feature — implement phase",
			expectedTier: "Coding",
		},
		{
			file:         "beads-issues/feature-phase-test.prompt.yaml",
			name:         "Feature — test phase",
			expectedTier: "Coding",
		},
		{
			file:         "beads-issues/feature-phase-review.prompt.yaml",
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

// TestHighConfidenceBuiltinPrompts_DeclarePairedFallback pins the three
// high-confidence builtin prompts scoped by mitto-42t to the paired
// `[Cheap, Coding]` preferredModels fallback (matching the existing convention
// on beads-overview, github-sync-tasks, jira-sync-tasks, report-to-parent,
// child-cleanup, check-ci). Regression guard: silently dropping the tag on any
// of these three would revert them to the session baseline and lose the
// deliberate cost-tier decision recorded on the bead.
func TestHighConfidenceBuiltinPrompts_DeclarePairedFallback(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"

	wantTags := []string{"Cheap", "Coding"}

	files := []string{
		"beads-issues/dependencies.prompt.yaml",
		"support/check-status.prompt.yaml",
		"beads/triage-bugs.prompt.yaml",
	}

	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			path := filepath.Join(builtinDir, file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			p, err := ParsePromptFile(file, data, time.Now())
			if err != nil {
				t.Fatalf("ParsePromptFile(%s): %v", file, err)
			}
			if len(p.PreferredModels) != len(wantTags) {
				t.Fatalf("%s: len(PreferredModels) = %d, want %d ([%s])",
					file, len(p.PreferredModels), len(wantTags), strings.Join(wantTags, ", "))
			}
			for i, want := range wantTags {
				if got := p.PreferredModels[i].ModelTag; got != want {
					t.Errorf("%s: PreferredModels[%d].ModelTag = %q, want %q",
						file, i, got, want)
				}
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
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"

	files := []string{
		"beads-issues/feature-phase-plan.prompt.yaml",
		"beads-issues/feature-phase-implement.prompt.yaml",
		"beads-issues/feature-phase-test.prompt.yaml",
		"beads-issues/feature-phase-review.prompt.yaml",
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
			if file != "beads-issues/feature-phase-plan.prompt.yaml" {
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
			if file != "beads-issues/feature-phase-plan.prompt.yaml" {
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
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"

	cases := []struct {
		file string
		tier string // declared tier per the phase's preferredModels
	}{
		{"beads-issues/fix-phase-investigate.prompt.yaml", "Reasoning"},
		{"beads-issues/fix-phase-reproduce.prompt.yaml", "Coding"},
		{"beads-issues/fix-phase-fix.prompt.yaml", "Coding"},
		{"beads-issues/feature-phase-plan.prompt.yaml", "Reasoning"},
		{"beads-issues/feature-phase-implement.prompt.yaml", "Coding"},
		{"beads-issues/feature-phase-test.prompt.yaml", "Coding"},
		{"beads-issues/feature-phase-review.prompt.yaml", "Reasoning"},
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
			if tc.file == "beads-issues/fix-phase-fix.prompt.yaml" ||
				tc.file == "beads-issues/feature-phase-implement.prompt.yaml" ||
				tc.file == "beads-issues/feature-phase-test.prompt.yaml" ||
				tc.file == "beads-issues/feature-phase-review.prompt.yaml" {
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
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"

	cases := []struct {
		file   string
		prefix string // exact "<Noun> [tier: <Tier>]:" fragment expected in the rendered body
	}{
		{"beads-issues/fix-phase-investigate.prompt.yaml", "Investigation [tier: Reasoning]:"},
		{"beads-issues/fix-phase-reproduce.prompt.yaml", "Reproduction [tier: Coding]:"},
		{"beads-issues/fix-phase-fix.prompt.yaml", "Fix [tier: Coding]:"},
		{"beads-issues/feature-phase-plan.prompt.yaml", "Plan [tier: Reasoning]:"},
		{"beads-issues/feature-phase-implement.prompt.yaml", "Implementation [tier: Coding]:"},
		{"beads-issues/feature-phase-test.prompt.yaml", "Testing [tier: Coding]:"},
		{"beads-issues/feature-phase-review.prompt.yaml", "Review [tier: Reasoning]:"},
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
			if tc.file == "beads-issues/fix-phase-fix.prompt.yaml" ||
				tc.file == "beads-issues/feature-phase-implement.prompt.yaml" ||
				tc.file == "beads-issues/feature-phase-test.prompt.yaml" ||
				tc.file == "beads-issues/feature-phase-review.prompt.yaml" {
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

	// Install the on-disk fragment registry so ParsePromptFile can resolve
	// `{{ template "github/shared/pr-comments" . }}` at parse-time precompile
	// (mitto-g61.4). Restored on cleanup.
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	boolPtr := func(b bool) *bool { return &b }

	type want struct {
		mode string
		def  *bool // nil means PromptLoop.Default must be nil
	}

	cases := map[string]want{
		// Group A — always (5).
		"beads-issue-iterate-until-complete.prompt.yaml": {mode: "always", def: nil},
		"iterate-until.prompt.yaml":                      {mode: "always", def: nil},
		"iterate-fixing.prompt.yaml":                     {mode: "always", def: nil},
		"iterate-implementing.prompt.yaml":               {mode: "always", def: nil},

		// Group B — optional / default:true (4).
		"github/babysit-contributions.prompt.yaml": {mode: "optional", def: boolPtr(true)},
		"github/babysit-my-prs.prompt.yaml":        {mode: "optional", def: boolPtr(true)},
		"github/sync-tasks.prompt.yaml":            {mode: "optional", def: boolPtr(true)},
		"jira/sync-tasks.prompt.yaml":              {mode: "optional", def: boolPtr(true)},

		// Group C — optional / default:false (11).
		"ci/check-ci.prompt.yaml":                 {mode: "optional", def: boolPtr(false)},
		"misc/continue.prompt.yaml":               {mode: "optional", def: boolPtr(false)},
		"ci/fix-ci.prompt.yaml":                   {mode: "optional", def: boolPtr(false)},
		"testing/run-tests.prompt.yaml":           {mode: "optional", def: boolPtr(false)},
		"ci/analyze-logs.prompt.yaml":             {mode: "optional", def: boolPtr(false)},
		"docs/architectural-analysis.prompt.yaml": {mode: "optional", def: boolPtr(false)},
		"beads/work.prompt.yaml":                  {mode: "optional", def: boolPtr(false)},
		"github/review-slack-prs.prompt.yaml":     {mode: "optional", def: boolPtr(false)},
		"jira/status-all-inprogress.prompt.yaml":  {mode: "optional", def: boolPtr(false)},
		"jira/status-one-inprogress.prompt.yaml":  {mode: "optional", def: boolPtr(false)},
		"jira/work.prompt.yaml":                   {mode: "optional", def: boolPtr(false)},
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
		"code/explain.prompt.yaml",
		"code/refactor.prompt.yaml",
		"review.prompt.yaml",
		"testing/add-tests.prompt.yaml",
		"misc/whats-next.prompt.yaml",
		"child/create-minions.prompt.yaml",
		"github/post-merge-cleanup.prompt.yaml",
		"beads-issues/decompose.prompt.yaml",
		// Tasks prompts that are one-shot reports, context-bound, or
		// confirmation-gated — loop re-firing makes no sense for them.
		"beads/followup-work.prompt.yaml",
		"beads/cleanup-stale.prompt.yaml",
		"beads/group-epics.prompt.yaml",
		"beads/overview.prompt.yaml",
		"beads/reevaluate.prompt.yaml",
		"beads/status-all-inprogress.prompt.yaml",
		"beads/status-one-inprogress.prompt.yaml",
		"beads-issues/status.prompt.yaml",
		"beads-issues/work.prompt.yaml",
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
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issues/mention-driver.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issues/mention-driver.prompt.yaml", data, time.Now())
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
			"IssueID":        "mitto-abc",
			"MentionTS":      "2026-07-16T10:00:00Z",
			"MentionBody":    "please fix the crash",
			"SubmitStrategy": "Commit",
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
	// SubmitStrategy=Commit branch: git-add-by-path guidance must render, "do NOT commit" must not.
	if !strings.Contains(outA, "git add <file>") {
		t.Errorf("branch (a): expected SubmitStrategy=Commit git-add guidance in output; got:\n%s", outA)
	}
	if strings.Contains(outA, "do NOT commit") {
		t.Errorf("branch (a): SubmitStrategy=Commit rendered the SubmitStrategy=None 'do NOT commit' copy")
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

	// (b) Arg-only context, SubmitStrategy=None.
	ctxB := &cel.PromptEnabledContext{
		Args: map[string]string{
			"IssueID":        "mitto-xyz",
			"MentionTS":      "2026-07-16T11:00:00Z",
			"MentionBody":    "how do I run tests?",
			"SubmitStrategy": "None",
		},
		Iteration: cel.IterationContext{IsLoop: true},
	}
	outB := render(ctxB)
	if !strings.Contains(outB, "mitto-xyz") {
		t.Errorf("branch (b): expected bead ID 'mitto-xyz' in output; got:\n%s", outB)
	}
	if !strings.Contains(outB, "do NOT commit") {
		t.Errorf("branch (b): expected SubmitStrategy=None 'do NOT commit' copy in output; got:\n%s", outB)
	}
	if strings.Contains(outB, "git add <file>") {
		t.Errorf("branch (b): SubmitStrategy=None rendered the SubmitStrategy=Commit git-add guidance")
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

// TestLoopProcessingSpawns_MirrorArgumentsIntoLoopArguments reproduces mitto-rtdr,
// updated for the mitto-cwz.3 rename of .Args.Commit -> .Args.SubmitStrategy.
//
// beads-issue-loop-processing.prompt.yaml spawns per-mention (SS A), per-bug (SS B) and
// per-feature (SS C) child conversations, all with loop_prompt_name and
// loop_trigger: onCompletion -- i.e. their onCompletion re-fires must render the
// loop body with the same .Args as the initial run. In internal/mcpserver the
// initial-prompt path reads input.Arguments (tools_conversation_new.go:651)
// while the loop-body path reads a separate input.LoopArguments field
// (:538 -> session.LoopPrompt.Arguments). If the spawn block passes only
// arguments: and not loop_arguments:, every re-fire renders the loop body with
// .Args = nil (missingkey=zero => .Args.SubmitStrategy == ""), which in the
// loop-body phase-dispatch templates' default-on gate
// (`{{ $commit := ne $submit "None" }}`) still resolves to "commit" for an
// empty string -- so an unmirrored spawn does not silently disable commits the
// way the old positive-match `eq .Args.Commit "true"` gate did. The invariant
// this test pins is narrower but still real: the spawn block must literally
// mirror the SAME resolved SubmitStrategy value into both `arguments:` and
// `loop_arguments:`, for every value the picker can produce ("Commit",
// "Pull Request", "None") and for the unset case (default-on fallback to
// "Commit") -- a spawn block that mirrors the literal into `arguments:` only,
// or mirrors a stale/different value into `loop_arguments:`, would still pass
// a "value present somewhere in the block" check but desyncs the initial run
// from every re-fire.
//
// The reproduction: render the orchestrator body with each SubmitStrategy
// value (including unset) and assert each of the SS A, SS B, SS C spawn blocks
// includes BOTH `arguments:` AND `loop_arguments:` fields, with the identical
// resolved SubmitStrategy literal on both sides.
func TestLoopProcessingSpawns_MirrorArgumentsIntoLoopArguments(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issues/loop-processing.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issues/loop-processing.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	body := prompt.Content

	// The three named-prompt spawn blocks -- one per section. Each maps to a
	// loop_prompt_name whose loop body renders on onCompletion re-fires.
	sections := []struct {
		section    string // section label for error messages
		promptName string // prompt_name string that anchors the spawn block
	}{
		{"section A", `prompt_name: "Mention — driver",`},
		{"section B", `prompt_name: "Loop fixing bug",`},
		{"section C", `prompt_name: "Loop implementing feature",`},
	}

	// cases covers the three SubmitStrategy picker values plus the unset case
	// (default-on fallback to "Commit" per the template's
	// `{{ if .Args.SubmitStrategy }}...{{ else }}Commit{{ end }}` literal).
	cases := []struct {
		name    string
		args    map[string]string
		wantVal string
		// wantInstr, when non-empty, is the AdditionalInstructions literal that
		// must be mirrored into both sides alongside SubmitStrategy. The
		// operator's instructions only reach the §A/§B/§C drivers (and from
		// there their phase prompts) through the spawn maps, so an unmirrored
		// value silently drops them on every onCompletion re-fire.
		wantInstr string
	}{
		{"unset defaults to Commit", map[string]string{}, "Commit", ""},
		{"Commit", map[string]string{"SubmitStrategy": "Commit"}, "Commit", ""},
		{"Pull Request", map[string]string{"SubmitStrategy": "Pull Request"}, "Pull Request", ""},
		{"None", map[string]string{"SubmitStrategy": "None"}, "None", ""},
		{
			"AdditionalInstructions propagated",
			map[string]string{"SubmitStrategy": "Commit", "AdditionalInstructions": "Only touch pkg/foo"},
			"Commit",
			"Only touch pkg/foo",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx := &cel.PromptEnabledContext{
				Session: cel.SessionContext{
					ID:            "orch-1",
					BeadsIssue:    "",
					HasBeadsIssue: false,
				},
				Args: tc.args,
			}
			funcs := cel.BuildTemplateFuncMap(ctx)
			out, rerr := RenderPromptTemplate("beads-issue-loop-processing", body, ctx, funcs)
			if rerr != nil {
				t.Fatalf("RenderPromptTemplate: %v", rerr)
			}

			for _, sec := range sections {
				anchor := strings.Index(out, sec.promptName)
				if anchor < 0 {
					t.Errorf("%s: spawn block anchor %q not found in rendered orchestrator; got:\n%s",
						sec.section, sec.promptName, out)
					continue
				}
				// The spawn block is a compact mitto_conversation_new(...) call -- bound
				// the window generously to the next closing paren.
				end := strings.Index(out[anchor:], "\n  )\n")
				if end < 0 {
					end = len(out) - anchor
					if end > 2000 {
						end = 2000
					}
				}
				block := out[anchor : anchor+end]

				argsIdx := strings.Index(block, "arguments:")
				loopArgsIdx := strings.Index(block, "loop_arguments:")
				if argsIdx < 0 {
					t.Errorf("%s: spawn block is missing an `arguments:` field entirely; block:\n%s",
						sec.section, block)
					continue
				}
				if loopArgsIdx < 0 {
					t.Errorf("%s: spawn block sets loop_prompt_name + onCompletion but is missing `loop_arguments:` -- every re-fire will render the loop body with an empty .Args. Mirror `arguments:` into `loop_arguments:`. Block:\n%s",
						sec.section, block)
					continue
				}

				// Split the block at the loop_arguments: anchor so the two
				// halves can be checked independently -- a block that only
				// sets the literal in `arguments:` (and leaves
				// `loop_arguments:` referencing something else, or omits the
				// key) must fail here, not just "somewhere in the block".
				argsSide := block[argsIdx:loopArgsIdx]
				loopArgsSide := block[loopArgsIdx:]

				wantLiteral := `"SubmitStrategy": "` + tc.wantVal + `"`
				if !strings.Contains(argsSide, wantLiteral) {
					t.Errorf("%s: arguments: side missing %s; args-side:\n%s", sec.section, wantLiteral, argsSide)
				}
				if !strings.Contains(loopArgsSide, wantLiteral) {
					t.Errorf("%s: loop_arguments: side missing %s -- re-fires would desync from the initial run; loop_arguments-side:\n%s", sec.section, wantLiteral, loopArgsSide)
				}

				if tc.wantInstr != "" {
					wantInstrLiteral := `"AdditionalInstructions": "` + tc.wantInstr + `"`
					if !strings.Contains(argsSide, wantInstrLiteral) {
						t.Errorf("%s: arguments: side missing %s; args-side:\n%s", sec.section, wantInstrLiteral, argsSide)
					}
					if !strings.Contains(loopArgsSide, wantInstrLiteral) {
						t.Errorf("%s: loop_arguments: side missing %s -- re-fires would drop the operator's instructions; loop_arguments-side:\n%s", sec.section, wantInstrLiteral, loopArgsSide)
					}
				}
			}
		})
	}
}

// TestIssueLoopProcessing_CoalesceDuringBusyIsFalse reproduces mitto-cwg.
//
// beads-issue-loop-processing.prompt.yaml is an onTasks supervisor: it fires on
// beads changes and spawns worker children that themselves mutate .beads/
// (add labels, close). At quiescence, LoopRunner.fireTasksRebase (in
// internal/conversation/loop_runner_tasks.go) branches on
// loop.ShouldCoalesceDuringBusy(): the default (CoalesceDuringBusy *bool == nil
// → true, see internal/session/loop.go) overwrites the pre-run baseline with
// the post-mutation snapshot silently, so the supervisor never sees the
// terminal-label / close events its own children produced. The opt-in re-fire
// path (maybeFireAccumulatedDelta) only runs when CoalesceDuringBusy is *false.
//
// The sibling builtin beads/investigate-all-more.prompt.yaml already declares
// `coalesceDuringBusy: false` in its loop block for exactly this reason
// (mitto-dmb). This test pins that same convention onto the L1 supervisor
// prompt. It fails until the loop frontmatter adds `coalesceDuringBusy: false`;
// after the fix, the parser resolves CoalesceDuringBusy to *false and the
// assertion passes.
func TestIssueLoopProcessing_CoalesceDuringBusyIsFalse(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"
	name := "beads-issues/loop-processing.prompt.yaml"
	path := filepath.Join(builtinDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile(name, data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	if prompt.Loop == nil {
		t.Fatalf("expected a loop block; got nil")
	}
	// Guard: coalesceDuringBusy is only meaningful for trigger: onTasks. If this
	// ever regresses, the rest of the assertion is moot — fail loudly.
	if !prompt.Loop.hasTrigger("onTasks") {
		t.Fatalf("loop.trigger = %v, want to include %q (mitto-cwg guard)",
			prompt.Loop.Trigger, "onTasks")
	}
	if prompt.Loop.TasksCoalesceDuringBusy() == nil {
		t.Fatalf("loop.onTasks.coalesceDuringBusy is unset; the deployed supervisor takes the silent-swallow branch at fireTasksRebase (mitto-cwg). Declare `coalesceDuringBusy: false` in the loop.onTasks: frontmatter of %s, mirroring beads/investigate-all-more.prompt.yaml", name)
	}
	if *prompt.Loop.TasksCoalesceDuringBusy() {
		t.Errorf("loop.onTasks.coalesceDuringBusy = true, want false (mitto-cwg): supervisor must react to its own subtree's beads mutations, not silently absorb them into a baseline rebase")
	}
}

// TestIssueLoopProcessing_EpicReaperPresent pins mitto-qxb: the L1 orchestrator
// (config/prompts/builtin/beads-issues/loop-processing.prompt.yaml) must contain
// a pass-local epic-reaper in Step 2P that auto-closes epics whose children are
// all closed by the same pass.
//
// The reaper is a prompt-template edit (bash inside the rendered body executed
// by the LLM at runtime), so behavioural coverage lives in the live orchestrator.
// This test locks the *structural* contract so an accidental prompt-edit that
// drops any of the reaper's load-bearing pieces fails CI:
//
//   - Step 2P captures closed_this_pass as it closes terminal-label beads
//     (used by both the recently-closed-parent exclusion and the reaper below).
//   - A "Reap epics whose children are all closed" sub-block is rendered when
//     bugs and/or features are enabled — this is the reaper itself. It must
//     derive touched_epics, guard on epic|open, count non-closed children via
//     bd children, invoke bd close with the canonical reason, and enforce a
//     hard cap (20) with overflow logging.
//   - Step 2T's transparency table header includes Reaped-epics=<count>.
//   - Step 6's mitto_ui_notify message includes Reaped-epics: <count>.
//   - The stale "human owns closing the epic" language is gone from the intro
//     paragraph and the "Expand epics; don't spawn them" Guidelines bullet
//     (those two spots now describe the reaper closing the epic).
func TestIssueLoopProcessing_EpicReaperPresent(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"
	name := "beads-issues/loop-processing.prompt.yaml"
	path := filepath.Join(builtinDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile(name, data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}

	// Render with both bugs and features enabled — that is the default
	// deployment (FixBugs="true", WorkOnFeatures="true") and the only mode
	// where the reaper block is meaningful (it lives inside the same
	// {{ if or ... }} guard as the terminal-label close loop).
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "orch-1"},
		Args:    map[string]string{"FixBugs": "true", "WorkOnFeatures": "true"},
	}
	funcs := cel.BuildTemplateFuncMap(ctx)
	out, rerr := RenderPromptTemplate("beads-issue-loop-processing", prompt.Content, ctx, funcs)
	if rerr != nil {
		t.Fatalf("RenderPromptTemplate: %v", rerr)
	}

	// --- Terminal-label loop tracks closed_this_pass ---
	if !strings.Contains(out, "closed_this_pass=()") {
		t.Errorf("expected the terminal-label close loop to initialise `closed_this_pass=()` so the reaper below can walk parents of just-closed children; not found in rendered body")
	}
	if !strings.Contains(out, `closed_this_pass+=("$id")`) {
		t.Errorf("expected the terminal-label close loop to append closed bead IDs into closed_this_pass (`closed_this_pass+=(\"$id\")`); not found in rendered body")
	}

	// --- Reaper sub-block anchors ---
	reaperHeader := "### Reap epics whose children are all closed"
	anchor := strings.Index(out, reaperHeader)
	if anchor < 0 {
		t.Fatalf("expected Step 2P sub-heading %q in rendered body (mitto-qxb); not found — the epic-reaper block is missing", reaperHeader)
	}
	// Bound the reaper block to the next H3/H2 heading or the end of body.
	tail := out[anchor+len(reaperHeader):]
	nextH := len(tail)
	for _, marker := range []string{"\n### ", "\n## "} {
		if idx := strings.Index(tail, marker); idx >= 0 && idx < nextH {
			nextH = idx
		}
	}
	block := tail[:nextH]

	// Load-bearing pieces of the reaper bash: derive touched_epics from
	// parents of closed_this_pass, guard on epic|open, count open children,
	// close with the canonical reason, and enforce the 20/pass cap.
	mustContain := []struct {
		frag string
		why  string
	}{
		{`for cid in "${closed_this_pass[@]}"`, "reaper must iterate closed_this_pass to derive touched epics (pass-local scope — never sweep all epics)"},
		{`.[0].parent`, "reaper must read the .parent field from `bd show <id> --json` to find each closed child's owning epic"},
		{`"epic|open"`, "reaper must guard on issue_type==epic && status==open before closing (idempotency + type safety)"},
		{`bd children`, "reaper must count non-closed children via `bd children <epic> --json` — the all-children-closed check"},
		{`bd close "$epic_id"`, "reaper must actually close the epic via bd close"},
		{"All children closed by L1 orchestrator pass", "reaper must use the canonical close reason from the plan/design"},
		{"reap_cap=20", "reaper must enforce a hard cap of 20 reaped epics per pass (bounded blast radius)"},
		{"Epic reaper cap", "reaper must log an overflow message when the 20/pass cap is hit so operators can see the backlog"},
	}
	for _, m := range mustContain {
		if !strings.Contains(block, m.frag) {
			t.Errorf("epic-reaper block is missing %q (%s); block:\n%s", m.frag, m.why, block)
		}
	}

	// --- Step 2T transparency: Reaped-epics counter in the candidates header ---
	if !strings.Contains(out, "Reaped-epics=<count>") {
		t.Errorf("Step 2T candidates header must include `Reaped-epics=<count>` alongside A/B/C counts so reap actions surface in the transparency table (mitto-qxb acceptance criterion)")
	}
	if !strings.Contains(out, "Reaped epics:") {
		t.Errorf("Step 2T must define a `Reaped epics:` bulleted list below the candidates table (rendered when the reaper closed any epics this pass)")
	}

	// --- Step 6 notification includes Reaped-epics count ---
	if !strings.Contains(out, "Reaped-epics: <count from Step 2P epic-reaper") {
		t.Errorf("Step 6 mitto_ui_notify message must include the `Reaped-epics: <count from Step 2P epic-reaper, 0 if none>` field so end-of-pass notifications report reaper activity")
	}

	// --- Stale "human owns closing the epic" language must be gone ---
	stale := "human owns closing the epic"
	if strings.Contains(out, stale) {
		t.Errorf("rendered orchestrator still contains stale language %q — the reaper now closes the epic in the same pass; intro paragraph (Step 2) and Guidelines `Expand epics; don't spawn them` bullet must both be updated (mitto-qxb)", stale)
	}
}

// TestRenderPromptTemplate_Fragments verifies the fragment-attach behavior wired
// into RenderPromptTemplate in mitto-g61.3: when a FragmentRegistry is installed
// via SetCurrentFragments, every entry is attached to the template set as an
// associated sub-template before the caller body is parsed, so
// `{{ template "name" . }}` in the body resolves at render time.
//
// Covers the three bead acceptance criteria:
//
//	(a) basic render — {{ template "test/hello" . }} against a registry entry
//	(b) data narrowing — {{ template "test/args-only" .Args }} renders with
//	    the fragment referencing .Foo (not .Args.Foo)
//	(c) FuncMap inheritance — the fragment uses {{ Arg "X" "def" }} (a builtin
//	    from cel.BuildTemplateFuncMap) and resolves against the caller's .Args
//
// Also asserts (d) nil-registry passthrough (behavior bytewise-identical to
// pre-fragment renders) and (e) fail-closed on a fragment parse error.
func TestRenderPromptTemplate_Fragments(t *testing.T) {
	// installFragments installs r as the package-wide registry for the test
	// and clears it on teardown so no other test observes it.
	installFragments := func(t *testing.T, r *FragmentRegistry) {
		t.Helper()
		SetCurrentFragments(r)
		t.Cleanup(func() { SetCurrentFragments(nil) })
	}

	// newTestRegistry builds a FragmentRegistry directly from a name→body map.
	// FragmentRegistry has no public AddOrReplace helper — LoadFragmentsFromDir
	// is the only supported ingest path — but the entries field is
	// package-internal so tests inside the prompts package can construct
	// synthetic registries this way (matches the pattern used by
	// TestFragmentRegistry_*).
	newTestRegistry := func(entries map[string]string) *FragmentRegistry {
		return &FragmentRegistry{entries: entries}
	}

	t.Run("basic-render", func(t *testing.T) {
		reg := newTestRegistry(map[string]string{
			"test/hello": "Hello {{ .Name }}",
		})
		installFragments(t, reg)

		body := `intro | {{ template "test/hello" . }} | outro`
		data := struct{ Name string }{Name: "world"}
		got, err := RenderPromptTemplate("basic", body, data, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "intro | Hello world | outro"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("data-narrowing", func(t *testing.T) {
		// Fragment references .Foo directly (not .Args.Foo) — proving the body
		// can pass a narrowed sub-context to the fragment.
		reg := newTestRegistry(map[string]string{
			"test/args-only": "foo={{ .Foo }}",
		})
		installFragments(t, reg)

		body := `{{ template "test/args-only" .Args }}`
		data := struct {
			Args map[string]string
		}{Args: map[string]string{"Foo": "bar"}}
		got, err := RenderPromptTemplate("narrowing", body, data, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "foo=bar"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("funcmap-inheritance", func(t *testing.T) {
		// Fragment uses the Arg helper from cel.BuildTemplateFuncMap. It must
		// resolve inside the fragment because associated sub-templates inherit
		// the parent's FuncMap.
		reg := newTestRegistry(map[string]string{
			"test/arg-helper": `x={{ Arg "X" "def" }}`,
		})
		installFragments(t, reg)

		body := `{{ template "test/arg-helper" . }}`

		// (i) With Arg set → helper returns the caller's value.
		ctxSet := &cel.PromptEnabledContext{Args: map[string]string{"X": "provided"}}
		got, err := RenderPromptTemplate("funcmap-set", body, ctxSet, cel.BuildTemplateFuncMap(ctxSet))
		if err != nil {
			t.Fatalf("unexpected error (X set): %v", err)
		}
		if got != "x=provided" {
			t.Errorf("Arg set: got %q, want %q", got, "x=provided")
		}

		// (ii) Without Arg set → helper falls back to the fragment's default.
		ctxAbsent := &cel.PromptEnabledContext{Args: map[string]string{}}
		got, err = RenderPromptTemplate("funcmap-absent", body, ctxAbsent, cel.BuildTemplateFuncMap(ctxAbsent))
		if err != nil {
			t.Fatalf("unexpected error (X absent): %v", err)
		}
		if got != "x=def" {
			t.Errorf("Arg absent: got %q, want %q", got, "x=def")
		}
	})

	t.Run("nil-registry-passthrough", func(t *testing.T) {
		// Explicitly clear any registry a previous parallel subtest might have
		// installed (defensive — subtests above use t.Cleanup, but this
		// documents the invariant).
		SetCurrentFragments(nil)
		t.Cleanup(func() { SetCurrentFragments(nil) })

		// A body that uses template syntax but no fragments must render
		// identically to the pre-fragment behavior.
		body := "Hello {{ .Name }}"
		data := struct{ Name string }{Name: "world"}
		got, err := RenderPromptTemplate("nil-reg", body, data, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "Hello world" {
			t.Errorf("got %q, want %q", got, "Hello world")
		}
	})

	t.Run("fragment-parse-error-fails-closed", func(t *testing.T) {
		// LoadFragmentsFromDir validates fragment bodies at load time so a
		// broken fragment cannot normally reach the registry. But if one is
		// installed directly (e.g. by a test or a broken bootstrap), the
		// attach loop in RenderPromptTemplate must fail closed with a
		// wrapped error that names the offending fragment.
		reg := newTestRegistry(map[string]string{
			"test/broken": "{{ if .Flag }}oops", // missing {{ end }}
		})
		installFragments(t, reg)

		body := "any {{ .X }} body"
		got, err := RenderPromptTemplate("fail-closed", body, struct{ X string }{X: "y"}, nil)
		if err == nil {
			t.Fatalf("expected error, got nil (output=%q)", got)
		}
		if !strings.Contains(err.Error(), "fragment") {
			t.Errorf("error %q should mention 'fragment'", err.Error())
		}
		if !strings.Contains(err.Error(), "test/broken") {
			t.Errorf("error %q should name the offending fragment %q", err.Error(), "test/broken")
		}
		if got != "" {
			t.Errorf("on error want empty output, got %q", got)
		}
	})
}

// TestValidatePromptTemplateSyntax_Fragments pins mitto-g61.4: load-time
// validation attaches the fragment registry before parsing the body, so a
// reference to an unknown fragment fails at parse time (same class of failure
// as an unbalanced `{{ ... }}`), while a reference to a known fragment
// validates cleanly.
//
// Covers: (a) unknown fragment fails with a wrapped error naming the prompt,
// (b) known fragment validates, (c) nil registry (default) still validates a
// syntactically-valid body without fragment refs.
func TestValidatePromptTemplateSyntax_Fragments(t *testing.T) {
	newTestRegistry := func(entries map[string]string) *FragmentRegistry {
		return &FragmentRegistry{entries: entries}
	}
	installFragments := func(t *testing.T, r *FragmentRegistry) {
		t.Helper()
		SetCurrentFragments(r)
		t.Cleanup(func() { SetCurrentFragments(nil) })
	}

	t.Run("unknown-fragment-fails", func(t *testing.T) {
		installFragments(t, newTestRegistry(map[string]string{
			"test/known": "hello",
		}))
		body := `{{ template "test/unknown" . }}`
		err := ValidatePromptTemplateSyntax("caller", body)
		if err == nil {
			t.Fatalf("expected error for unknown fragment reference, got nil")
		}
		if !strings.Contains(err.Error(), "caller") {
			t.Errorf("error %q should mention the prompt name %q", err.Error(), "caller")
		}
	})

	t.Run("known-fragment-validates", func(t *testing.T) {
		installFragments(t, newTestRegistry(map[string]string{
			"test/known": "hello {{ .Name }}",
		}))
		body := `intro {{ template "test/known" . }} outro`
		if err := ValidatePromptTemplateSyntax("caller", body); err != nil {
			t.Fatalf("expected nil error for known fragment reference, got: %v", err)
		}
	})

	t.Run("nil-registry-passthrough", func(t *testing.T) {
		SetCurrentFragments(nil)
		t.Cleanup(func() { SetCurrentFragments(nil) })
		// Body has template syntax but no fragment refs — nil registry must
		// preserve pre-fragment behavior (validates cleanly).
		if err := ValidatePromptTemplateSyntax("caller", "hello {{ .Name }}"); err != nil {
			t.Fatalf("expected nil error, got: %v", err)
		}
	})

	t.Run("fragment-parse-error-fails-closed", func(t *testing.T) {
		installFragments(t, newTestRegistry(map[string]string{
			"test/broken": "{{ if .Flag }}oops", // missing {{ end }}
		}))
		body := `hello {{ .X }}`
		err := ValidatePromptTemplateSyntax("caller", body)
		if err == nil {
			t.Fatalf("expected error when an installed fragment fails to parse, got nil")
		}
		if !strings.Contains(err.Error(), "test/broken") {
			t.Errorf("error %q should name the offending fragment %q", err.Error(), "test/broken")
		}
	})
}

// TestPrecompileTemplateConds_Fragments mirrors the fragment attachment
// contract on the sibling path exercised by ParsePromptFile. Unknown fragment
// refs must fail at precompile so a broken prompt file is rejected at load
// (not silently deferred to render time). Known fragments must succeed even
// though the fragment body references funcs from the FuncMap (Cond/When are
// stubbed to keep precompile side-effect free).
func TestPrecompileTemplateConds_Fragments(t *testing.T) {
	newTestRegistry := func(entries map[string]string) *FragmentRegistry {
		return &FragmentRegistry{entries: entries}
	}
	installFragments := func(t *testing.T, r *FragmentRegistry) {
		t.Helper()
		SetCurrentFragments(r)
		t.Cleanup(func() { SetCurrentFragments(nil) })
	}

	t.Run("unknown-fragment-fails", func(t *testing.T) {
		installFragments(t, newTestRegistry(map[string]string{
			"test/known": "hello",
		}))
		body := `pre {{ template "test/unknown" . }} post`
		err := PrecompileTemplateConds("caller", body)
		if err == nil {
			t.Fatalf("expected error for unknown fragment reference, got nil")
		}
		if !strings.Contains(err.Error(), "caller") {
			t.Errorf("error %q should mention the prompt name %q", err.Error(), "caller")
		}
	})

	t.Run("known-fragment-succeeds", func(t *testing.T) {
		installFragments(t, newTestRegistry(map[string]string{
			"test/known": "hello",
		}))
		body := `{{ template "test/known" . }}`
		if err := PrecompileTemplateConds("caller", body); err != nil {
			t.Fatalf("expected nil error for known fragment reference, got: %v", err)
		}
	})

	t.Run("nil-registry-passthrough", func(t *testing.T) {
		SetCurrentFragments(nil)
		t.Cleanup(func() { SetCurrentFragments(nil) })
		if err := PrecompileTemplateConds("caller", "hello {{ .Session.ID }}"); err != nil {
			t.Fatalf("expected nil error, got: %v", err)
		}
	})
}
