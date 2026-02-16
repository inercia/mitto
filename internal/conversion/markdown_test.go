package conversion

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConverter_Fixtures runs table-driven tests against all fixture pairs in testdata/.
func TestConverter_Fixtures(t *testing.T) {
	// Find all .md files in testdata/
	testdataDir := "testdata"
	entries, err := os.ReadDir(testdataDir)
	if err != nil {
		t.Fatalf("Failed to read testdata directory: %v", err)
	}

	var fixtures []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			name := strings.TrimSuffix(entry.Name(), ".md")
			fixtures = append(fixtures, name)
		}
	}

	if len(fixtures) == 0 {
		t.Fatal("No fixtures found in testdata/")
	}

	// Create a converter without highlighting (for deterministic output)
	converter := NewConverter()

	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			mdPath := filepath.Join(testdataDir, fixture+".md")
			htmlPath := filepath.Join(testdataDir, fixture+".html")

			// Read input markdown
			mdContent, err := os.ReadFile(mdPath)
			if err != nil {
				t.Fatalf("Failed to read markdown file %s: %v", mdPath, err)
			}

			// Read expected HTML
			expectedHTML, err := os.ReadFile(htmlPath)
			if err != nil {
				t.Fatalf("Failed to read expected HTML file %s: %v", htmlPath, err)
			}

			// Convert markdown to HTML
			actualHTML, err := converter.Convert(string(mdContent))
			if err != nil {
				t.Fatalf("Conversion failed: %v", err)
			}

			// Normalize both for comparison (trim whitespace)
			expected := strings.TrimSpace(string(expectedHTML))
			actual := strings.TrimSpace(actualHTML)

			if actual != expected {
				t.Errorf("Fixture %s: HTML mismatch\n\nExpected:\n%s\n\nActual:\n%s\n\nDiff:\n%s",
					fixture, expected, actual, diffStrings(expected, actual))
			}
		})
	}
}

// diffStrings provides a simple line-by-line diff for debugging.
func diffStrings(expected, actual string) string {
	expectedLines := strings.Split(expected, "\n")
	actualLines := strings.Split(actual, "\n")

	var diff strings.Builder
	maxLines := len(expectedLines)
	if len(actualLines) > maxLines {
		maxLines = len(actualLines)
	}

	for i := 0; i < maxLines; i++ {
		var expLine, actLine string
		if i < len(expectedLines) {
			expLine = expectedLines[i]
		}
		if i < len(actualLines) {
			actLine = actualLines[i]
		}

		if expLine != actLine {
			diff.WriteString("--- expected line ")
			diff.WriteString(string(rune('0' + i)))
			diff.WriteString(": ")
			diff.WriteString(expLine)
			diff.WriteString("\n+++ actual line ")
			diff.WriteString(string(rune('0' + i)))
			diff.WriteString(": ")
			diff.WriteString(actLine)
			diff.WriteString("\n")
		}
	}

	return diff.String()
}

// TestConverter_Convert tests basic conversion functionality.
func TestConverter_Convert(t *testing.T) {
	converter := NewConverter()

	tests := []struct {
		name     string
		input    string
		contains []string // Substrings that must be present
	}{
		{
			name:     "empty input",
			input:    "",
			contains: nil,
		},
		{
			name:     "simple paragraph",
			input:    "Hello, world!",
			contains: []string{"<p>", "Hello, world!", "</p>"},
		},
		{
			name:     "bold text",
			input:    "**bold**",
			contains: []string{"<strong>", "bold", "</strong>"},
		},
		{
			name:     "inline code",
			input:    "`code`",
			contains: []string{"<code>", "code", "</code>"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := converter.Convert(tt.input)
			if err != nil {
				t.Fatalf("Convert failed: %v", err)
			}

			for _, substr := range tt.contains {
				if !strings.Contains(result, substr) {
					t.Errorf("Expected result to contain %q, got: %s", substr, result)
				}
			}
		})
	}
}

// TestConverter_ConvertToSafeHTML tests the safe HTML conversion with error fallback.
func TestConverter_ConvertToSafeHTML(t *testing.T) {
	converter := NewConverter()

	result := converter.ConvertToSafeHTML("**bold** text")
	if !strings.Contains(result, "<strong>") {
		t.Errorf("Expected <strong> tag, got: %s", result)
	}

	// Empty input should return empty string
	result = converter.ConvertToSafeHTML("")
	if result != "" {
		t.Errorf("Expected empty string for empty input, got: %s", result)
	}
}

// TestEscapeHTML tests HTML escaping.
func TestEscapeHTML(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"<script>alert('xss')</script>", "&lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt;"},
		{"a & b", "a &amp; b"},
		{`"quoted"`, "&quot;quoted&quot;"},
		{"normal text", "normal text"},
		{"<>&\"'", "&lt;&gt;&amp;&quot;&#39;"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := EscapeHTML(tt.input)
			if result != tt.expected {
				t.Errorf("EscapeHTML(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestHasUnmatchedInlineFormatting tests the inline formatting detection.
func TestHasUnmatchedInlineFormatting(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"empty", "", false},
		{"no formatting", "hello world", false},
		{"matched bold", "**bold**", false},
		{"unmatched bold", "**bold", true},
		{"matched code", "`code`", false},
		{"unmatched code", "`code", true},
		{"multiple matched", "**bold** and `code`", false},
		{"one unmatched bold", "**bold** and **more", true},
		{"code fence ignored", "```go\ncode\n```", false},
		{"double backtick ignored", "``escaped``", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HasUnmatchedInlineFormatting(tt.input)
			if result != tt.expected {
				t.Errorf("HasUnmatchedInlineFormatting(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestIsCodeBlockStart tests code block detection.
func TestIsCodeBlockStart(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"```", true},
		{"```go", true},
		{"```python", true},
		{"``", false},
		{"`", false},
		{"text", false},
		{"  ```", false}, // indented
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := IsCodeBlockStart(tt.input)
			if result != tt.expected {
				t.Errorf("IsCodeBlockStart(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestIsListItem tests list item detection.
func TestIsListItem(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"- item", true},
		{"* item", true},
		{"+ item", true},
		{"1. item", true},
		{"10. item", true},
		{"  - nested", true},
		{"text", false},
		{"-no space", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := IsListItem(tt.input)
			if result != tt.expected {
				t.Errorf("IsListItem(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestIsTableRow tests table row detection.
func TestIsTableRow(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"| cell |", true},
		{"|---|---|", true},
		{"  | indented", true},
		{"text", false},
		{"no | pipe at start", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := IsTableRow(tt.input)
			if result != tt.expected {
				t.Errorf("IsTableRow(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestDefaultConverter tests the default converter factory.
func TestDefaultConverter(t *testing.T) {
	converter := DefaultConverter()
	if converter == nil {
		t.Fatal("DefaultConverter returned nil")
	}

	// Test that it can convert markdown
	result, err := converter.Convert("**bold**")
	if err != nil {
		t.Fatalf("Convert failed: %v", err)
	}
	if !strings.Contains(result, "<strong>") {
		t.Errorf("Expected <strong> tag, got: %s", result)
	}
}

// TestMermaidWithSanitization verifies mermaid blocks are preserved through sanitization.
func TestMermaidWithSanitization(t *testing.T) {
	converter := DefaultConverter()

	markdown := "```mermaid\ngraph TD\n    A --> B\n```"
	result, err := converter.Convert(markdown)
	if err != nil {
		t.Fatalf("Convert failed: %v", err)
	}

	t.Logf("Input markdown:\n%s", markdown)
	t.Logf("Output HTML:\n%s", result)

	// Check that the mermaid class is preserved
	if !strings.Contains(result, `class="mermaid"`) {
		t.Errorf("Expected class=\"mermaid\" to be preserved after sanitization, got:\n%s", result)
	}

	// Check that the pre element exists
	if !strings.Contains(result, "<pre") {
		t.Errorf("Expected <pre> element, got:\n%s", result)
	}

	// Check that the code fence markers are NOT in the output (they should be parsed, not shown)
	if strings.Contains(result, "```") {
		t.Errorf("Code fence markers should not appear in HTML output, got:\n%s", result)
	}

	// Check that the word "mermaid" as language identifier is NOT in the content
	// (it should only appear in class="mermaid", not as visible text)
	if strings.Contains(result, ">mermaid<") || strings.Contains(result, "```mermaid") {
		t.Errorf("Language identifier 'mermaid' should not appear as content, got:\n%s", result)
	}
}

// TestCodeBlocksWithLanguageIdentifiers tests that fenced code blocks with
// various language identifiers are rendered correctly.
// This is a regression test for: https://github.com/inercia/mitto/issues/22
//
// Note: When syntax highlighting is enabled (DefaultConverter), the language-xxx
// class may not appear on the <code> element because the highlighter uses
// inline <span> elements for token coloring instead.
func TestCodeBlocksWithLanguageIdentifiers(t *testing.T) {
	converter := DefaultConverter()

	testCases := []struct {
		name            string
		markdown        string
		wantPreCode     bool   // Expect <pre><code> structure
		wantContent     string // Content that must be present in the code block (may be inside spans)
		notWantInOutput string // String that should NOT appear in output
	}{
		{
			name:            "python",
			markdown:        "Here is code:\n\n```python\ndef hello():\n    print(\"Hello\")\n```\n\nAfter code.",
			wantPreCode:     true,
			wantContent:     "def",
			notWantInOutput: "```python",
		},
		{
			name:            "javascript",
			markdown:        "```javascript\nconsole.log('hello');\n```",
			wantPreCode:     true,
			wantContent:     "console",
			notWantInOutput: "```javascript",
		},
		{
			name:            "html",
			markdown:        "```html\n<div class=\"test\">Hello</div>\n```",
			wantPreCode:     true,
			wantContent:     "div",
			notWantInOutput: "```html",
		},
		{
			name:            "yaml",
			markdown:        "```yaml\nkey: value\nnested:\n  item: 1\n```",
			wantPreCode:     true,
			wantContent:     "key",
			notWantInOutput: "```yaml",
		},
		{
			name:            "json",
			markdown:        "```json\n{\"key\": \"value\"}\n```",
			wantPreCode:     true,
			wantContent:     "key",
			notWantInOutput: "```json",
		},
		{
			name:            "go",
			markdown:        "```go\nfunc main() {\n    fmt.Println(\"Hello\")\n}\n```",
			wantPreCode:     true,
			wantContent:     "func",
			notWantInOutput: "```go",
		},
		{
			name:            "no language",
			markdown:        "```\nplain code\n```",
			wantPreCode:     true,
			wantContent:     "plain code",
			notWantInOutput: "```\n",
		},
		{
			name:            "bash",
			markdown:        "```bash\necho \"Hello World\"\n```",
			wantPreCode:     true,
			wantContent:     "echo",
			notWantInOutput: "```bash",
		},
		{
			name:            "mermaid",
			markdown:        "```mermaid\ngraph TD\n    A --> B\n```",
			wantPreCode:     true,
			wantContent:     "graph TD",
			notWantInOutput: "```mermaid",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := converter.Convert(tc.markdown)
			if err != nil {
				t.Fatalf("Convert failed: %v", err)
			}

			t.Logf("Input:\n%s", tc.markdown)
			t.Logf("Output:\n%s", result)

			// Check for <pre> structure (code blocks use <pre>)
			if tc.wantPreCode {
				if !strings.Contains(result, "<pre") {
					t.Errorf("Expected <pre> tag, got:\n%s", result)
				}
			}

			// Check content is present (may be inside span elements for highlighting)
			if tc.wantContent != "" {
				if !strings.Contains(result, tc.wantContent) {
					t.Errorf("Expected content %q in output, got:\n%s", tc.wantContent, result)
				}
			}

			// Check that raw markdown fence is NOT in output
			if tc.notWantInOutput != "" {
				if strings.Contains(result, tc.notWantInOutput) {
					t.Errorf("Output should NOT contain %q, got:\n%s", tc.notWantInOutput, result)
				}
			}

			// Check that code block is properly closed
			if tc.wantPreCode {
				if !strings.Contains(result, "</pre>") {
					t.Errorf("Expected </pre> closing tag, got:\n%s", result)
				}
			}

			// Issue #22: Verify text after closing fence is rendered correctly
			// and NOT rendered as monospace/code
			if strings.Contains(tc.markdown, "After code.") {
				if !strings.Contains(result, "<p>After code.</p>") {
					t.Errorf("Text after code block should be in <p> tag, got:\n%s", result)
				}
			}
		})
	}
}

// TestCodeBlockNotGarbled tests that code blocks with language identifiers
// don't produce garbled output as described in issue #22.
// The issue states: "an empty monospace block appears before the code,
// and the text after the closing fence is rendered as monospace too"
func TestCodeBlockNotGarbled(t *testing.T) {
	converter := DefaultConverter()

	// This is the exact scenario from the issue
	markdown := `Here is some Python code:

` + "```python" + `
def hello():
    print("Hello, World!")
` + "```" + `

This text should NOT be monospace.`

	result, err := converter.Convert(markdown)
	if err != nil {
		t.Fatalf("Convert failed: %v", err)
	}

	t.Logf("Input markdown:\n%s", markdown)
	t.Logf("Output HTML:\n%s", result)

	// 1. Check that there's no empty <pre> or <code> block
	if strings.Contains(result, "<pre></pre>") || strings.Contains(result, "<code></code>") {
		t.Errorf("Found empty <pre> or <code> block - this is the garbled output bug")
	}

	// 2. Check that the text after the code block is in a <p> tag, not <pre>/<code>
	// Find where "This text" appears and ensure it's not inside a code block
	if !strings.Contains(result, "<p>This text should NOT be monospace.</p>") {
		// Check if it's incorrectly wrapped in pre/code
		if strings.Contains(result, "<pre>") && strings.Contains(result, "This text should NOT be monospace") {
			// Check if the text appears after the closing </pre>
			preCloseIndex := strings.LastIndex(result, "</pre>")
			textIndex := strings.Index(result, "This text should NOT be monospace")
			if textIndex < preCloseIndex {
				t.Errorf("Text after code fence is inside <pre> block - this is the garbled output bug\n%s", result)
			}
		}
		// Just warn if the exact format is different but acceptable
		if !strings.Contains(result, "This text should NOT be monospace") {
			t.Errorf("Text after code block is missing entirely, got:\n%s", result)
		}
	}

	// 3. The language identifier should NOT appear as visible text
	// (it should be stripped from output or only in class attributes)
	if strings.Contains(result, ">python<") || strings.Contains(result, "```python") {
		t.Errorf("Language identifier 'python' appears as visible text, got:\n%s", result)
	}

	// 4. The code block should have proper <pre><code> structure
	// Note: With syntax highlighting enabled, the language class may not be present
	// because the highlighter uses inline spans for token coloring
	if !strings.Contains(result, "<pre><code") {
		t.Errorf("Expected <pre><code> structure, got:\n%s", result)
	}

	// 5. Verify the code content is present
	if !strings.Contains(result, "def") || !strings.Contains(result, "hello") {
		t.Errorf("Expected code content to be present, got:\n%s", result)
	}
}

// TestIsTableSeparator tests table separator detection.
func TestIsTableSeparator(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"|---|---|", true},
		{"|------|-------|----------|", true},
		{"| --- | --- |", true},
		{"|:---|:---:|---:|", true},
		{"| cell |", false},
		{"text", false},
		{"|---|---|  |", true}, // Extra column (malformed but still a separator)
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := IsTableSeparator(tt.input)
			if result != tt.expected {
				t.Errorf("IsTableSeparator(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestCountTableColumns tests column counting.
func TestCountTableColumns(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"| a | b | c |", 3},
		{"| a | b |", 2},
		{"|---|---|", 2},
		{"|------|-------|----------|", 3},
		{"|------|-------|----------|  |", 4}, // Extra empty column
		{"| File | Issue | Severity", 3},      // No trailing pipe
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := countTableColumns(tt.input)
			if result != tt.expected {
				t.Errorf("countTableColumns(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestNormalizeTableMarkdown tests table normalization.
func TestNormalizeTableMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "well-formed table unchanged",
			input:    "| a | b | c |\n|---|---|---|\n| 1 | 2 | 3 |",
			expected: "| a | b | c |\n|---|---|---|\n| 1 | 2 | 3 |",
		},
		{
			name:     "extra column in separator fixed",
			input:    "| File | Issue | Severity\n|------|-------|----------|  |\n| f1 | i1 | Low |",
			expected: "| File | Issue | Severity\n|------|-------|----------|\n| f1 | i1 | Low |",
		},
		{
			name:     "no table unchanged",
			input:    "Just some text\nwith multiple lines",
			expected: "Just some text\nwith multiple lines",
		},
		{
			name:     "separator without header unchanged",
			input:    "|---|---|---|\n| 1 | 2 | 3 |",
			expected: "|---|---|---|\n| 1 | 2 | 3 |",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeTableMarkdown(tt.input)
			if result != tt.expected {
				t.Errorf("NormalizeTableMarkdown:\ninput:    %q\nexpected: %q\ngot:      %q", tt.input, tt.expected, result)
			}
		})
	}
}

// TestConverter_MalformedTable tests that malformed tables are now rendered correctly.
func TestConverter_MalformedTable(t *testing.T) {
	converter := NewConverter()

	// This is the exact case from the bug report - extra | at end of separator
	input := "| File | Issue | Severity\n|------|-------|----------|  |\n| file1 | issue1 | Low |"

	result, err := converter.Convert(input)
	if err != nil {
		t.Fatalf("Convert failed: %v", err)
	}

	// Should render as a table, not a paragraph
	if !strings.Contains(result, "<table>") {
		t.Errorf("Expected <table> tag, got: %s", result)
	}
	if strings.Contains(result, "|---") {
		t.Errorf("Raw markdown separator should not appear in output: %s", result)
	}
}

// TestDataURLImages tests that images with data: URLs are rendered correctly.
// See: https://github.com/inercia/mitto/issues/20
func TestDataURLImages(t *testing.T) {
	// A minimal 1x1 red PNG image as base64
	redPixelBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="

	tests := []struct {
		name                       string
		markdown                   string
		wantImgTag                 bool
		wantDataURLNoSanitization  bool // Expected when NOT sanitizing
		wantDataURLWithSanitization bool // Expected when sanitizing (bluemonday blocks SVG)
	}{
		{
			name:                       "data URL PNG image",
			markdown:                   "![red pixel](data:image/png;base64," + redPixelBase64 + ")",
			wantImgTag:                 true,
			wantDataURLNoSanitization:  true,
			wantDataURLWithSanitization: true,
		},
		{
			name:                       "data URL with alt text",
			markdown:                   "Here is an inline image: ![inline graphic](data:image/png;base64," + redPixelBase64 + ")",
			wantImgTag:                 true,
			wantDataURLNoSanitization:  true,
			wantDataURLWithSanitization: true,
		},
		{
			name:                       "data URL JPEG image",
			markdown:                   "![photo](data:image/jpeg;base64,/9j/4AAQSkZJRg==)",
			wantImgTag:                 true,
			wantDataURLNoSanitization:  true,
			wantDataURLWithSanitization: true,
		},
		{
			name:                       "data URL GIF image",
			markdown:                   "![animation](data:image/gif;base64,R0lGODlhAQABAIAAAP///wAAACH5BAEAAAAALAAAAAABAAEAAAICRAEAOw==)",
			wantImgTag:                 true,
			wantDataURLNoSanitization:  true,
			wantDataURLWithSanitization: true,
		},
		{
			// WebP is a modern image format also supported
			name:                       "data URL WebP image",
			markdown:                   "![webp](data:image/webp;base64,UklGRh4AAABXRUJQVlA4TBEAAAAvAAAAAAfQ//73v/+BiOh/AAA=)",
			wantImgTag:                 true,
			wantDataURLNoSanitization:  true,
			wantDataURLWithSanitization: true,
		},
		{
			// SVG data URLs are blocked for security - SVG can contain embedded JavaScript
			name:                       "data URL SVG image - blocked for security",
			markdown:                   "![icon](data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciLz4=)",
			wantImgTag:                 true,
			wantDataURLNoSanitization:  true,  // Goldmark renders it
			wantDataURLWithSanitization: false, // bluemonday's AllowDataURIImages() blocks SVG
		},
		{
			name:                       "regular https image still works",
			markdown:                   "![photo](https://example.com/image.png)",
			wantImgTag:                 true,
			wantDataURLNoSanitization:  false,
			wantDataURLWithSanitization: false,
		},
	}

	// Test without sanitization (goldmark should render all data URLs including SVG)
	t.Run("without sanitization", func(t *testing.T) {
		converter := NewConverter() // No sanitization

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result, err := converter.Convert(tt.markdown)
				if err != nil {
					t.Fatalf("Convert failed: %v", err)
				}

				hasImgTag := strings.Contains(result, "<img")
				if hasImgTag != tt.wantImgTag {
					t.Errorf("img tag presence: got %v, want %v\nresult: %s", hasImgTag, tt.wantImgTag, result)
				}

				hasDataURL := strings.Contains(result, "data:image/")
				if hasDataURL != tt.wantDataURLNoSanitization {
					t.Errorf("data URL presence: got %v, want %v\nresult: %s", hasDataURL, tt.wantDataURLNoSanitization, result)
				}
			})
		}
	})

	// Test with sanitization - bluemonday blocks SVG but allows png/jpeg/gif/webp
	t.Run("with sanitization", func(t *testing.T) {
		converter := DefaultConverter() // Uses CreateSanitizer()

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result, err := converter.Convert(tt.markdown)
				if err != nil {
					t.Fatalf("Convert failed: %v", err)
				}

				hasImgTag := strings.Contains(result, "<img")
				if hasImgTag != tt.wantImgTag {
					t.Errorf("img tag presence: got %v, want %v\nresult: %s", hasImgTag, tt.wantImgTag, result)
				}

				hasDataURL := strings.Contains(result, "data:image/")
				if hasDataURL != tt.wantDataURLWithSanitization {
					t.Errorf("data URL presence: got %v, want %v\nresult: %s", hasDataURL, tt.wantDataURLWithSanitization, result)
				}
			})
		}
	})
}
