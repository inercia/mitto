package conversion

import (
	"strings"
	"testing"
)

// TestURLsInBackticks_Integration tests the end-to-end behavior of URL detection
// in backticks through the full markdown conversion pipeline.
func TestURLsInBackticks_Integration(t *testing.T) {
	converter := NewConverter(
		WithFileLinks(FileLinkerConfig{
			WorkingDir: "/tmp",
			Enabled:    true,
		}),
	)

	tests := []struct {
		name     string
		markdown string
		contains []string
		excludes []string
	}{
		{
			name:     "URL in backticks",
			markdown: "Check out `https://example.com` for more info",
			contains: []string{
				`<a href="https://example.com"`,
				`target="_blank"`,
				`rel="noopener noreferrer"`,
				`<code>https://example.com</code></a>`,
			},
		},
		{
			name:     "multiple URLs in backticks",
			markdown: "Visit `https://github.com/user/repo` and `https://docs.example.com`",
			contains: []string{
				`<a href="https://github.com/user/repo"`,
				`<code>https://github.com/user/repo</code></a>`,
				`<a href="https://docs.example.com"`,
				`<code>https://docs.example.com</code></a>`,
			},
		},
		{
			name:     "mailto URL in backticks",
			markdown: "Email me at `mailto:user@example.com`",
			contains: []string{
				`<a href="mailto:user@example.com"`,
				`class="url-link mailto-link"`,
				`<code>mailto:user@example.com</code></a>`,
			},
			excludes: []string{
				`target="_blank"`,
			},
		},
		{
			name:     "FTP URL in backticks",
			markdown: "Download from `ftp://ftp.example.com/file.txt`",
			contains: []string{
				`<a href="ftp://ftp.example.com/file.txt"`,
				`<code>ftp://ftp.example.com/file.txt</code></a>`,
			},
		},
		{
			name:     "URL with path and query in backticks",
			markdown: "API endpoint: `https://api.example.com/v1/users?limit=10&offset=0`",
			contains: []string{
				// Note: & is escaped to &amp; by the HTML sanitizer
				`<a href="https://api.example.com/v1/users?limit=10&amp;offset=0"`,
				`<code>https://api.example.com/v1/users?limit=10&amp;offset=0</code></a>`,
			},
		},
		{
			name:     "URL in code block should not be linked",
			markdown: "```\nhttps://example.com\n```",
			excludes: []string{
				`<a href="https://example.com"`,
			},
		},
		{
			name:     "regular markdown link should not be affected",
			markdown: "[Click here](https://example.com)",
			contains: []string{
				`<a href="https://example.com">Click here</a>`,
			},
		},
		{
			name:     "URL in backticks within a list",
			markdown: "- Item 1: `https://example.com`\n- Item 2: `https://another.com`",
			contains: []string{
				`<a href="https://example.com"`,
				`<code>https://example.com</code></a>`,
				`<a href="https://another.com"`,
				`<code>https://another.com</code></a>`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html, err := converter.Convert(tt.markdown)
			if err != nil {
				t.Fatalf("Conversion failed: %v", err)
			}

			for _, expected := range tt.contains {
				if !strings.Contains(html, expected) {
					t.Errorf("Expected HTML to contain %q\nMarkdown: %s\nHTML: %s",
						expected, tt.markdown, html)
				}
			}

			for _, excluded := range tt.excludes {
				if strings.Contains(html, excluded) {
					t.Errorf("Expected HTML to NOT contain %q\nMarkdown: %s\nHTML: %s",
						excluded, tt.markdown, html)
				}
			}
		})
	}
}
