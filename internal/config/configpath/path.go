package configpath

import "strconv"

// ParsePath parses one dotted/indexed path string (e.g.
// `task_label_colors[0].color`) into a Path. Escaped `\.`, `\,`, `\\` are
// resolved inside key segments.
func ParsePath(pathStr string) (Path, error) {
	if len(pathStr) == 0 {
		return nil, syntaxErr("", "empty path")
	}
	tokens, err := splitPathTokens(pathStr)
	if err != nil {
		return nil, err
	}
	var path Path
	for _, tok := range tokens {
		segs, err := parsePathToken(tok)
		if err != nil {
			return nil, err
		}
		path = append(path, segs...)
		if len(path) > MaxPathDepth {
			return nil, limitErr("", "path exceeds max depth")
		}
	}
	return path, nil
}

// splitPathTokens splits a path string on unescaped top-level '.', treating
// `[...]` as opaque so dots cannot appear meaningfully inside an index.
func splitPathTokens(s string) ([]string, error) {
	var tokens []string
	var cur []byte
	depth := 0
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case escaped:
			cur = append(cur, c)
			escaped = false
		case c == '\\':
			cur = append(cur, c)
			escaped = true
		case c == '[':
			depth++
			cur = append(cur, c)
		case c == ']':
			depth--
			if depth < 0 {
				return nil, syntaxErr("", "unbalanced '[' ']' in path")
			}
			cur = append(cur, c)
		case c == '.' && depth == 0:
			tokens = append(tokens, string(cur))
			cur = cur[:0]
		default:
			cur = append(cur, c)
		}
	}
	if escaped {
		return nil, syntaxErr("", "trailing backslash escape")
	}
	if depth != 0 {
		return nil, syntaxErr("", "unbalanced '[' ']' in path")
	}
	tokens = append(tokens, string(cur))
	return tokens, nil
}

// parsePathToken parses one dot-separated token into zero-or-one key
// segment followed by zero-or-more index segments (e.g. `a[0][1]`).
func parsePathToken(tok string) ([]PathSegment, error) {
	keyEnd := -1
	escaped := false
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\':
			escaped = true
		case c == '[':
			keyEnd = i
		}
		if keyEnd != -1 {
			break
		}
	}

	var keyPart, rest string
	if keyEnd == -1 {
		keyPart, rest = tok, ""
	} else {
		keyPart, rest = tok[:keyEnd], tok[keyEnd:]
	}

	var segs []PathSegment
	if keyPart != "" {
		unescaped, err := unescapeSegment(keyPart)
		if err != nil {
			return nil, err
		}
		if len(unescaped) > MaxKeySegmentLength {
			return nil, limitErr("", "path key segment too long")
		}
		segs = append(segs, PathSegment{Key: unescaped})
	}

	for len(rest) > 0 {
		if rest[0] != '[' {
			return nil, syntaxErr("", "malformed path index syntax")
		}
		end := indexByteUnescaped(rest, ']')
		if end == -1 {
			return nil, syntaxErr("", "unterminated '[' in path")
		}
		idx, err := parseArrayIndex(rest[1:end])
		if err != nil {
			return nil, err
		}
		segs = append(segs, PathSegment{Index: idx, IsIndex: true})
		rest = rest[end+1:]
	}

	if len(segs) == 0 {
		return nil, syntaxErr("", "empty path segment")
	}
	return segs, nil
}

func indexByteUnescaped(s string, target byte) int {
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == target {
			return i
		}
	}
	return -1
}

func parseArrayIndex(s string) (int, error) {
	if s == "" {
		return 0, syntaxErr("", "empty array index")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			if c == '-' {
				return 0, syntaxErr("", "negative array index not allowed")
			}
			return 0, syntaxErr("", "malformed array index")
		}
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, syntaxErr("", "array index must not have leading zeros")
	}
	// len(s) is bounded by the caller's overall MaxRawInputBytes budget, so
	// this cannot allocate an unbounded number of digits.
	n, err := strconv.Atoi(s)
	if err != nil || n > MaxArrayIndex {
		return 0, limitErr("", "array index exceeds limit")
	}
	return n, nil
}
