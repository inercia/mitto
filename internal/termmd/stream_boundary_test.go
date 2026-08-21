package termmd

import "testing"

// TestStablePrefixLen covers stablePrefixLen's per-construct hazards: each
// case pins the exact byte offset returned for a body containing one
// specific markdown construct, so a boundary-scan regression shows up as a
// precise offset mismatch rather than a downstream rendering diff.
func TestStablePrefixLen(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string // want is the expected body[:want] prefix, for readability
	}{
		{
			name: "plain paragraphs boundary after each blank line",
			body: "Para one\n\nPara two\n",
			want: "Para one\n\n",
		},
		{
			name: "unclosed fence excluded from prefix",
			body: "para\n\n```go\ncode\n",
			want: "para\n\n",
		},
		{
			name: "closed fence allows boundary past it",
			body: "Para\n\n```go\ncode line\n```\n\nAfter\n",
			want: "Para\n\n```go\ncode line\n```\n\n",
		},
		{
			name: "list item followed by blank rejects boundary right after it",
			body: "- item one\n\ncontinued\n",
			want: "",
		},
		{
			name: "loose-list indented continuation rejects boundary",
			body: "- item one\n\n  continued paragraph\n\nAfter list\n",
			want: "",
		},
		{
			name: "list marker anywhere, later non-indented line still accepted",
			body: "- item one\n\nAfter list\n\nMore text\n",
			want: "- item one\n\nAfter list\n\n",
		},
		{
			name: "table row excluded from prefix",
			body: "Para\n\n| A | B |\n|---|---|\n\nAfter table\n",
			want: "Para\n\n",
		},
		{
			name: "blockquote excluded from prefix",
			body: "Para\n\n> quoted line\n\nAfter quote\n",
			want: "Para\n\n",
		},
		{
			name: "indented code block excluded from prefix",
			body: "Para\n\n    indented code line\n\nAfter\n",
			want: "Para\n\n",
		},
		{
			name: "setext underline right after boundary rejects it",
			body: "Para one\n\n---\nabove was a divider\n",
			want: "",
		},
		{
			name: "html block opener stops the scan for the rest of the body",
			body: "Para\n\n<div>\nsome html\n</div>\n\nAfter\n",
			want: "Para\n\n",
		},
		{
			name: "link reference definition stops the scan for the rest of the body",
			body: "Para\n\n[label]: http://example.com\n\nAfter\n",
			want: "Para\n\n",
		},
		{
			name: "no blank line yet: whole body still open",
			body: "Still writing this sentence",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stablePrefixLen(tt.body)
			if got != len(tt.want) || tt.body[:got] != tt.want {
				t.Errorf("stablePrefixLen(%q) = %d (prefix %q), want %d (prefix %q)",
					tt.body, got, tt.body[:got], len(tt.want), tt.want)
			}
		})
	}
}
