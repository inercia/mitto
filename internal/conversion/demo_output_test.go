package conversion

import (
	"fmt"
	"testing"
)

// TestDemoOutput demonstrates the actual HTML output for URLs in backticks.
// This test is for documentation purposes to show what the output looks like.
func TestDemoOutput(t *testing.T) {
	converter := NewConverter(
		WithFileLinks(FileLinkerConfig{
			WorkingDir: "/tmp",
			Enabled:    true,
		}),
	)

	testCases := []struct {
		name     string
		markdown string
	}{
		{
			name:     "Simple URL",
			markdown: "Check out `https://example.com` for more info",
		},
		{
			name:     "URL with path",
			markdown: "API docs: `https://api.example.com/v1/docs`",
		},
		{
			name:     "Multiple URLs",
			markdown: "Visit `https://github.com` and `https://docs.example.com`",
		},
		{
			name:     "mailto URL",
			markdown: "Contact: `mailto:user@example.com`",
		},
		{
			name:     "FTP URL",
			markdown: "Download: `ftp://ftp.example.com/file.txt`",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			html, err := converter.Convert(tc.markdown)
			if err != nil {
				t.Fatalf("Conversion failed: %v", err)
			}

			fmt.Printf("\n=== %s ===\n", tc.name)
			fmt.Printf("Markdown: %s\n", tc.markdown)
			fmt.Printf("HTML:     %s\n", html)
		})
	}
}
