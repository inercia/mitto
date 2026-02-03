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
)

// Converter handles markdown-to-HTML conversion with configurable options.
type Converter struct {
	md        goldmark.Markdown
	sanitizer *bluemonday.Policy
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

// NewConverter creates a new Converter with the given options.
func NewConverter(opts ...Option) *Converter {
	c := &Converter{
		md: goldmark.New(
			goldmark.WithExtensions(
				extension.GFM,
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

	return p
}

// Convert converts markdown text to HTML.
func (c *Converter) Convert(markdown string) (string, error) {
	var buf bytes.Buffer
	if err := c.md.Convert([]byte(markdown), &buf); err != nil {
		return "", err
	}

	result := buf.String()

	// Apply sanitization if configured
	if c.sanitizer != nil {
		result = c.sanitizer.Sanitize(result)
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

// HasUnmatchedInlineFormatting checks if the content has unmatched inline formatting markers.
// This includes **, *, _, and ` markers that would be broken if we flush mid-content.
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
	return inlineCodeCount%2 != 0
}
