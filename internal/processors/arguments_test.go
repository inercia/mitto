package processors

import "testing"

func TestSubstituteArguments(t *testing.T) {
	tests := []struct {
		name string
		text string
		args map[string]string
		want string
	}{
		{
			name: "no placeholders fast path",
			text: "plain text with no vars",
			args: map[string]string{"VAR": "x"},
			want: "plain text with no vars",
		},
		{
			name: "simple variable present",
			text: "issue ${ISSUE_ID} here",
			args: map[string]string{"ISSUE_ID": "mitto-t93"},
			want: "issue mitto-t93 here",
		},
		{
			name: "missing variable becomes empty",
			text: "value=[${MISSING}]",
			args: map[string]string{},
			want: "value=[]",
		},
		{
			name: "default used when missing",
			text: "n=${COUNT:-5}",
			args: map[string]string{},
			want: "n=5",
		},
		{
			name: "default used when empty",
			text: "n=${COUNT:-5}",
			args: map[string]string{"COUNT": ""},
			want: "n=5",
		},
		{
			name: "value wins over default when non-empty",
			text: "n=${COUNT:-5}",
			args: map[string]string{"COUNT": "9"},
			want: "n=9",
		},
		{
			name: "double-quoted default is stripped",
			text: "x=${NAME:-\"a random number\"}",
			args: map[string]string{},
			want: "x=a random number",
		},
		{
			name: "single-quoted default is stripped",
			text: "x=${NAME:-'hello world'}",
			args: map[string]string{},
			want: "x=hello world",
		},
		{
			name: "empty default yields empty",
			text: "x=[${NAME:-}]",
			args: map[string]string{},
			want: "x=[]",
		},
		{
			name: "multiple variables",
			text: "${A}-${B}-${C:-z}",
			args: map[string]string{"A": "1", "B": "2"},
			want: "1-2-z",
		},
		{
			name: "escaped placeholder is literal",
			text: `keep \${VAR} literal`,
			args: map[string]string{"VAR": "x"},
			want: "keep ${VAR} literal",
		},
		{
			name: "escaped and substituted mix",
			text: `\${LITERAL} but ${REAL}`,
			args: map[string]string{"REAL": "ok"},
			want: "${LITERAL} but ok",
		},
		{
			name: "unmatched brace left untouched",
			text: "shell ${ malformed",
			args: map[string]string{},
			want: "shell ${ malformed",
		},
		{
			name: "lowercase and digits in name",
			text: "${my_var2}",
			args: map[string]string{"my_var2": "ok"},
			want: "ok",
		},
		{
			name: "nil args map",
			text: "a=${X:-def} b=${Y}",
			args: nil,
			want: "a=def b=",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SubstituteArguments(tt.text, tt.args)
			if got != tt.want {
				t.Errorf("SubstituteArguments(%q, %v) = %q, want %q", tt.text, tt.args, got, tt.want)
			}
		})
	}
}

func TestStripSurroundingQuotes(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{`"quoted"`, "quoted"},
		{`'quoted'`, "quoted"},
		{`unquoted`, "unquoted"},
		{`"mismatched'`, `"mismatched'`},
		{`"`, `"`},
		{``, ``},
		{`""`, ``},
	}
	for _, tt := range tests {
		if got := stripSurroundingQuotes(tt.in); got != tt.want {
			t.Errorf("stripSurroundingQuotes(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
