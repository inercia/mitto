package config

import (
	"fmt"
	"strings"
	"testing"
	"text/template"
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
