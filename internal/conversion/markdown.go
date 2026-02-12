// Package conversion provides markdown-to-HTML conversion utilities.
package conversion

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"go.abhg.dev/goldmark/mermaid"
)

// Converter handles markdown-to-HTML conversion with configurable options.
type Converter struct {
	md         goldmark.Markdown
	sanitizer  *bluemonday.Policy
	fileLinker *FileLinker
}

// Option configures the Converter.
type Option func(*Converter)

// WithHighlighting enables syntax highlighting with the specified style.
func WithHighlighting(style string) Option {
	return func(c *Converter) {
		c.md = goldmark.New(
			goldmark.WithExtensions(
				extension.GFM,
				highlighting.NewHighlighting(
					highlighting.WithStyle(style),
				),
				// Mermaid extension renders ```mermaid blocks as <pre class="mermaid">
				// for client-side rendering by Mermaid.js
				&mermaid.Extender{
					RenderMode: mermaid.RenderModeClient,
					NoScript:   true, // We load Mermaid.js ourselves in the frontend
				},
			),
			goldmark.WithParserOptions(
				parser.WithAutoHeadingID(),
			),
			goldmark.WithRendererOptions(
				html.WithHardWraps(),
				html.WithXHTML(),
			),
		)
	}
}

// WithSanitization enables HTML sanitization using the provided policy.
func WithSanitization(policy *bluemonday.Policy) Option {
	return func(c *Converter) {
		c.sanitizer = policy
	}
}

// WithFileLinks enables file path detection and linking in the output HTML.
// Detected file paths that exist on the filesystem are converted to clickable file:// links.
func WithFileLinks(config FileLinkerConfig) Option {
	return func(c *Converter) {
		c.fileLinker = NewFileLinker(config)
	}
}

// NewConverter creates a new Converter with the given options.
func NewConverter(opts ...Option) *Converter {
	c := &Converter{
		md: goldmark.New(
			goldmark.WithExtensions(
				extension.GFM,
				// Mermaid extension renders ```mermaid blocks as <pre class="mermaid">
				// for client-side rendering by Mermaid.js
				&mermaid.Extender{
					RenderMode: mermaid.RenderModeClient,
					NoScript:   true, // We load Mermaid.js ourselves in the frontend
				},
			),
			goldmark.WithParserOptions(
				parser.WithAutoHeadingID(),
			),
			goldmark.WithRendererOptions(
				html.WithHardWraps(),
				html.WithXHTML(),
			),
		),
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// DefaultConverter returns a converter with default settings suitable for agent messages.
func DefaultConverter() *Converter {
	return NewConverter(
		WithHighlighting("monokai"),
		WithSanitization(CreateSanitizer()),
	)
}

// CreateSanitizer creates a bluemonday policy that allows safe HTML for markdown rendering.
func CreateSanitizer() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()

	// Allow code highlighting classes from goldmark-highlighting
	p.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).OnElements("code", "pre", "span", "div")

	// Allow data attributes for code blocks (used by some highlighters)
	p.AllowDataAttributes()

	// Allow id attributes for heading anchors
	p.AllowAttrs("id").Matching(bluemonday.Paragraph).OnElements("h1", "h2", "h3", "h4", "h5", "h6")

	// Allow file:// URLs for file links (added by FileLinker post-processing)
	p.AllowURLSchemes("http", "https", "mailto", "file")

	// Allow class attribute on anchor tags for file-link styling
	p.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).OnElements("a")

	return p
}

// Convert converts markdown text to HTML.
func (c *Converter) Convert(markdown string) (string, error) {
	// Normalize table markdown to fix common formatting issues
	markdown = NormalizeTableMarkdown(markdown)

	var buf bytes.Buffer
	if err := c.md.Convert([]byte(markdown), &buf); err != nil {
		return "", err
	}

	result := buf.String()

	// Apply sanitization if configured
	if c.sanitizer != nil {
		result = c.sanitizer.Sanitize(result)
	}

	// Apply file linking if configured (after sanitization to avoid stripping file:// links)
	if c.fileLinker != nil {
		result = c.fileLinker.LinkFilePaths(result)
	}

	return result, nil
}

// ConvertToSafeHTML converts markdown and escapes it on error.
// This is useful for streaming scenarios where errors should not break the output.
func (c *Converter) ConvertToSafeHTML(markdown string) string {
	result, err := c.Convert(markdown)
	if err != nil {
		return "<pre>" + EscapeHTML(markdown) + "</pre>"
	}
	return result
}

// EscapeHTML escapes special HTML characters.
func EscapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&#39;")
	return s
}

// codeBlockPattern matches opening/closing code fences.
var codeBlockPattern = regexp.MustCompile("^```")

// listItemPattern matches list item lines (ordered or unordered).
var listItemPattern = regexp.MustCompile(`^\s*(\d+\.\s+|[-*+]\s+)`)

// tableRowPattern matches table row lines.
var tableRowPattern = regexp.MustCompile(`^\s*\|`)

// IsCodeBlockStart returns true if the line starts a code block.
func IsCodeBlockStart(line string) bool {
	return codeBlockPattern.MatchString(line)
}

// IsListItem returns true if the line is a list item.
func IsListItem(line string) bool {
	return listItemPattern.MatchString(line)
}

// IsTableRow returns true if the line is a table row.
func IsTableRow(line string) bool {
	return tableRowPattern.MatchString(line)
}

// tableSeparatorPattern matches table separator lines (e.g., |---|---|).
var tableSeparatorPattern = regexp.MustCompile(`^\s*\|[\s\-:|]+\|[\s\-:|]*$`)

// IsTableSeparator returns true if the line is a table separator line.
func IsTableSeparator(line string) bool {
	return tableSeparatorPattern.MatchString(line)
}

// NormalizeTableMarkdown fixes common table formatting issues that prevent proper rendering.
// It ensures separator lines have the correct number of columns to match the header.
func NormalizeTableMarkdown(content string) string {
	lines := strings.Split(content, "\n")
	result := make([]string, 0, len(lines))

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// Check if this is a table separator line
		if IsTableSeparator(line) && i > 0 {
			// Get the header line (previous line)
			headerLine := lines[i-1]
			if IsTableRow(headerLine) {
				// Count columns in header
				headerCols := countTableColumns(headerLine)
				// Count columns in separator
				sepCols := countTableColumns(line)

				// If separator has more columns than header, fix it
				if sepCols > headerCols && headerCols > 0 {
					line = fixTableSeparator(line, headerCols)
				}
			}
		}

		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// countTableColumns counts the number of columns in a table row.
func countTableColumns(line string) int {
	// Remove leading/trailing whitespace
	line = strings.TrimSpace(line)

	// Remove leading and trailing pipes if present
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")

	// Count remaining pipes + 1 = number of columns
	return strings.Count(line, "|") + 1
}

// fixTableSeparator adjusts a table separator to have the specified number of columns.
func fixTableSeparator(line string, targetCols int) string {
	// Remove leading/trailing whitespace
	line = strings.TrimSpace(line)

	// Split by pipe
	parts := strings.Split(line, "|")

	// Filter out empty parts at the beginning and end
	var cells []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		// Keep separator cells (contain dashes)
		if strings.Contains(trimmed, "-") {
			cells = append(cells, trimmed)
		}
	}

	// If we have more cells than needed, truncate
	if len(cells) > targetCols {
		cells = cells[:targetCols]
	}

	// Rebuild the separator line
	return "|" + strings.Join(cells, "|") + "|"
}

// HasUnmatchedInlineFormatting checks if the content has unmatched inline formatting markers.
// This includes **, *, _, and ` markers that would be broken if we flush mid-content.
// It also checks for unmatched parentheses which indicate a sentence was split mid-thought.
func HasUnmatchedInlineFormatting(content string) bool {
	// Count occurrences of formatting markers
	// For **, we need to count pairs
	boldCount := strings.Count(content, "**")
	if boldCount%2 != 0 {
		return true
	}

	// For inline code, count backticks (but not code blocks which start with ```)
	inlineCodeCount := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '`' {
			// Check if this is part of a code fence (```)
			if i+2 < len(content) && content[i+1] == '`' && content[i+2] == '`' {
				i += 2
				continue
			}
			// Check if this is a double backtick (``)
			if i+1 < len(content) && content[i+1] == '`' {
				i++
				continue
			}
			inlineCodeCount++
		}
	}
	if inlineCodeCount%2 != 0 {
		return true
	}

	// Check for unmatched parentheses - indicates sentence split mid-thought
	// Count total parentheses in the entire content
	openParens := strings.Count(content, "(")
	closeParens := strings.Count(content, ")")
	return openParens > closeParens
}
