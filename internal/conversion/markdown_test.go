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
