package termmd

import "testing"

// TestRenderHTMLFallback_Behaviors pins RenderHTMLFallback's documented,
// individually-composed behaviors (entity unescaping, block-tag-to-newline,
// non-block-tag stripping, blank-line collapsing) beyond the single combined
// case in TestRenderHTMLFallback_Golden, so a regression in any one rule is
// caught even if it happens not to shift the combined golden output.
func TestRenderHTMLFallback_Behaviors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "entities are unescaped",
			in:   "A &amp; B &lt;tag&gt; C&#39;s &quot;quote&quot;",
			want: `A & B <tag> C's "quote"`,
		},
		{
			name: "adjacent block tags separate into a blank line",
			in:   "<p>A</p><p>B</p>",
			want: "A\n\nB",
		},
		{
			name: "block tag attributes are ignored, still treated as a block tag",
			in:   `<p class="x" id="y">Hi</p>`,
			want: "Hi",
		},
		{
			name: "non-block inline tags are stripped without inserting a newline",
			in:   "<b>bold</b> and <i>italic</i>",
			want: "bold and italic",
		},
		{
			name: "list items each force a newline (adjacent </li><li> yields a blank line)",
			in:   "<ul><li>one</li><li>two</li></ul>",
			want: "one\n\ntwo",
		},
		{
			name: "br forces a newline",
			in:   "line one<br>line two",
			want: "line one\nline two",
		},
		{
			name: "3+ consecutive newlines collapse to a single blank line",
			in:   "A\n\n\n\nB",
			want: "A\n\nB",
		},
		{
			name: "leading and trailing whitespace is trimmed",
			in:   "  <p>  padded  </p>  ",
			want: "padded",
		},
		{
			name: "empty input renders to empty output",
			in:   "",
			want: "",
		},
		{
			name: "plain text with no tags passes through unchanged",
			in:   "just plain text",
			want: "just plain text",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RenderHTMLFallback(tc.in)
			if got != tc.want {
				t.Errorf("RenderHTMLFallback(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
