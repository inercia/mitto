package termmd

import "strings"

// stablePrefixLen returns the byte offset of the latest position in body
// that is safe to treat as a "stable prefix" for StreamRenderer: a point
// immediately after a blank line at which no markdown construct spans the
// cut (no unclosed fenced code block, list, table, block quote, setext
// header, indented code block, HTML block, or link reference definition).
// Returns 0 when no such position exists yet (the whole body must still be
// rendered as a unit).
//
// This is deliberately conservative, mirroring crush's
// streaming_markdown.go findSafeMarkdownBoundary/prefixHasOpenHazard (the
// mitto-pscc.8.1 plan's stated reusable part — the correctness rules, not
// the implementation): any of the "anywhere in body[:p]" hazards below
// (list marker, HTML block opener, link reference definition) forces 0 for
// the rest of the scan, even past a blank line that would otherwise look
// safe, because CommonMark's loose-list/HTML-block/reference-definition
// semantics cannot be decided from the boundary line alone.
func stablePrefixLen(body string) int {
	best := 0
	hasListMarker := false
	inFence := false

	pos := 0
	for pos <= len(body) {
		nl := strings.IndexByte(body[pos:], '\n')
		var line string
		var lineEnd int
		atEOF := nl < 0
		if atEOF {
			line = body[pos:]
			lineEnd = len(body)
		} else {
			line = body[pos : pos+nl]
			lineEnd = pos + nl + 1
		}

		if isFenceLine(line) {
			inFence = !inFence
		} else if !inFence {
			trimmed := strings.TrimLeft(line, " \t")
			if trimmed == "" {
				// A blank line: the offset right after it is a boundary
				// candidate (start of whatever follows). Repeated blank
				// lines just re-evaluate the same candidate harmlessly.
				if !atEOF && lineEnd > best && isSafeBoundary(body, lineEnd, hasListMarker) {
					best = lineEnd
				}
			} else {
				if isListItemMarker(trimmed) {
					hasListMarker = true
				}
				if isHTMLBlockOpener(line) || isLinkRefDefinition(line) {
					return best
				}
			}
		}

		if atEOF {
			break
		}
		pos = lineEnd
	}
	return best
}

// isSafeBoundary reports whether body[:p] is safe to cut given that p sits
// immediately after a blank line and hasListMarker already reflects
// whether a list marker has appeared anywhere before p.
func isSafeBoundary(body string, p int, hasListMarker bool) bool {
	prefix := body[:p]
	last := lastNonBlankLine(prefix)
	if last != "" {
		trimmedLast := strings.TrimLeft(last, " \t")
		if lineOpensConstruct(last, trimmedLast) {
			return false
		}
		// A list marker seen anywhere in the prefix, combined with an
		// indented-but-non-marker last line, means a loose list's
		// continuation paragraph is open — lineOpensConstruct's plain
		// "4+ leading spaces" rule alone would miss a 1-3-space
		// continuation. Reject conservatively.
		if hasListMarker && trimmedLast != last && !isListItemMarker(trimmedLast) {
			return false
		}
	}

	// If anything follows, reject when the first non-blank line after p
	// looks like a setext underline: that would retroactively turn the
	// prefix's trailing paragraph into a header.
	rest := body[p:]
	if rest != "" {
		if first := firstNonBlankLine(rest); isSetextUnderlineCandidate(first) {
			return false
		}
	}
	return true
}

// isFenceLine reports whether line opens or closes a fenced code block: up
// to 3 leading spaces, then 3+ of the same fence character ('`' or '~').
func isFenceLine(line string) bool {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	if i >= len(line) {
		return false
	}
	c := line[i]
	if c != '`' && c != '~' {
		return false
	}
	run := 0
	for i < len(line) && line[i] == c {
		i++
		run++
	}
	return run >= 3
}

// lastNonBlankLine returns the last non-blank line of s, or "" when every
// line is blank.
func lastNonBlankLine(s string) string {
	last := ""
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			last = line
		}
	}
	return last
}

// firstNonBlankLine returns the first non-blank line of s, or "" when
// every line is blank.
func firstNonBlankLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			return line
		}
	}
	return ""
}

// lineOpensConstruct reports whether line (with trimmed already computed
// as its left-trimmed form) keeps a markdown construct open across a
// boundary drawn right after it: indented code, block quote, list item,
// table row, or a setext underline candidate. Any of these returns true so
// the caller rejects the boundary.
func lineOpensConstruct(line, trimmed string) bool {
	if len(line) > 0 && line[0] == '\t' {
		return true
	}
	if strings.HasPrefix(line, "    ") {
		return true
	}
	if trimmed == "" {
		return false
	}
	if trimmed[0] == '>' {
		return true
	}
	if isListItemMarker(trimmed) {
		return true
	}
	if strings.ContainsRune(line, '|') {
		return true
	}
	if isSetextUnderlineCandidate(trimmed) {
		return true
	}
	return false
}

// isListItemMarker reports whether line (already left-trimmed) starts
// with a CommonMark list-item marker followed by a space or tab.
func isListItemMarker(line string) bool {
	if line == "" {
		return false
	}
	c := line[0]
	if c == '-' || c == '*' || c == '+' {
		return len(line) >= 2 && (line[1] == ' ' || line[1] == '\t')
	}
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i > 9 || i >= len(line) {
		return false
	}
	if line[i] != '.' && line[i] != ')' {
		return false
	}
	if i+1 >= len(line) {
		return false
	}
	return line[i+1] == ' ' || line[i+1] == '\t'
}

// isSetextUnderlineCandidate reports whether line (with optional leading
// whitespace) consists entirely of '=' or entirely of '-' characters with
// optional trailing whitespace, up to 3 leading spaces.
func isSetextUnderlineCandidate(line string) bool {
	i := 0
	for i < len(line) && i < 3 && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i == len(line) {
		return false
	}
	c := line[i]
	if c != '=' && c != '-' {
		return false
	}
	j := i
	for j < len(line) && line[j] == c {
		j++
	}
	for j < len(line) {
		if line[j] != ' ' && line[j] != '\t' {
			return false
		}
		j++
	}
	return j-i >= 1
}

// isHTMLBlockOpener reports whether line begins one of the CommonMark HTML
// block patterns, loosely matched: up to 3 leading spaces, then '<'
// followed by a recognised block tag, a comment/doctype/CDATA/processing
// instruction opener, or any other open/close tag name.
func isHTMLBlockOpener(line string) bool {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	rest := line[i:]
	if len(rest) < 2 || rest[0] != '<' {
		return false
	}
	if strings.HasPrefix(rest, "<!--") || strings.HasPrefix(rest, "<?") || strings.HasPrefix(rest, "<![CDATA[") {
		return true
	}
	if len(rest) >= 3 && rest[1] == '!' && isASCIILetter(rest[2]) {
		return true
	}
	low := strings.ToLower(rest)
	for _, t := range []string{"<script", "<pre", "<style", "<textarea"} {
		if strings.HasPrefix(low, t) {
			var next byte
			if len(low) > len(t) {
				next = low[len(t)]
			}
			if next == 0 || next == ' ' || next == '\t' || next == '>' {
				return true
			}
		}
	}
	j := 1
	if j < len(rest) && rest[j] == '/' {
		j++
	}
	return j < len(rest) && isASCIILetter(rest[j])
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// isLinkRefDefinition reports whether line matches a CommonMark link
// reference definition opener: up to 3 spaces, "[label]:", whitespace,
// then at least one non-whitespace destination character.
func isLinkRefDefinition(line string) bool {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	if i >= len(line) || line[i] != '[' {
		return false
	}
	i++
	labelStart := i
	for i < len(line) && line[i] != ']' {
		i++
	}
	if i >= len(line) || i == labelStart {
		return false
	}
	i++
	if i >= len(line) || line[i] != ':' {
		return false
	}
	i++
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return i < len(line)
}
