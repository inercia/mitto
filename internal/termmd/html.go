package termmd

import (
	"html"
	"regexp"
	"strings"
)

// htmlBlockTag matches an opening or closing HTML block-level tag whose
// presence should force a newline in RenderHTMLFallback's output — enough to
// keep paragraphs, list items and headings visually separated without
// attempting to reconstruct real markdown structure.
var htmlBlockTag = regexp.MustCompile(`(?i)</?(p|div|br|li|ul|ol|h[1-6]|blockquote|pre|tr|table)[^>]*>`)

// htmlAnyTag matches any remaining HTML tag, stripped after htmlBlockTag has
// already contributed its newlines.
var htmlAnyTag = regexp.MustCompile(`<[^>]+>`)

// blankLineRun collapses 3+ consecutive newlines (left behind once block
// tags are replaced) down to a single blank line.
var blankLineRun = regexp.MustCompile(`\n{3,}`)

// RenderHTMLFallback renders pre-mitto-pscc.3 events that carry HTML only
// (no persisted markdown) into plain text good enough to read in a terminal.
// It is deliberately crude and does NOT go through glamour: unescape HTML
// entities, turn block-level tags into newlines, drop every other tag. Its
// job is legibility for legacy history, not fidelity — it must not grow
// features that make it a second markdown renderer; new formatting needs
// belong in Render's glamour path instead.
func RenderHTMLFallback(htmlBody string) string {
	s := htmlBlockTag.ReplaceAllString(htmlBody, "\n")
	s = htmlAnyTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = blankLineRun.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
