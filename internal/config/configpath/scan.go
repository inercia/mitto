package configpath

import "strings"

// allowedEscapes lists characters that may be backslash-escaped in keys and
// typed/string values. Anything else after a backslash is a syntax error.
var allowedEscapes = map[byte]byte{
	'.': '.', ',': ',', '\\': '\\', '=': '=', '{': '{', '}': '}', '[': '[', ']': ']', '"': '"',
}

// unescapeSegment resolves backslash escapes in a key or plain value
// segment. It never echoes the offending character in error messages.
func unescapeSegment(s string) (string, error) {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(s) {
			return "", syntaxErr("", "trailing backslash escape")
		}
		repl, ok := allowedEscapes[s[i+1]]
		if !ok {
			return "", syntaxErr("", "invalid escape sequence")
		}
		b.WriteByte(repl)
		i++
	}
	return b.String(), nil
}

// splitTopLevel splits a raw --set/--set-string/--set-json flag value on
// unescaped top-level commas, treating `{...}`/`[...]` as opaque nested
// regions (brace lists, JSON arrays/objects) and `"..."` as opaque JSON
// string literals so embedded commas do not split.
func splitTopLevel(raw string) ([]string, error) {
	var parts []string
	var cur strings.Builder
	depth := 0
	inQuote := false
	escaped := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case escaped:
			cur.WriteByte(c)
			escaped = false
		case c == '\\':
			cur.WriteByte(c)
			escaped = true
		case inQuote:
			cur.WriteByte(c)
			if c == '"' {
				inQuote = false
			}
		case c == '"':
			inQuote = true
			cur.WriteByte(c)
		case c == '{' || c == '[':
			depth++
			cur.WriteByte(c)
		case c == '}' || c == ']':
			depth--
			if depth < 0 {
				return nil, syntaxErr("", "unbalanced brackets")
			}
			cur.WriteByte(c)
		case c == ',' && depth == 0:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if escaped {
		return nil, syntaxErr("", "trailing backslash escape")
	}
	if inQuote {
		return nil, syntaxErr("", "unterminated quoted string")
	}
	if depth != 0 {
		return nil, syntaxErr("", "unbalanced brackets")
	}
	parts = append(parts, cur.String())
	return parts, nil
}

// splitAssignment splits one `path=value` segment on the first unescaped
// top-level '=', respecting the same nesting rules as splitTopLevel.
func splitAssignment(segment string) (pathStr, valueStr string, err error) {
	depth := 0
	inQuote := false
	escaped := false
	for i := 0; i < len(segment); i++ {
		c := segment[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\':
			escaped = true
		case inQuote:
			if c == '"' {
				inQuote = false
			}
		case c == '"':
			inQuote = true
		case c == '{' || c == '[':
			depth++
		case c == '}' || c == ']':
			depth--
		case c == '=' && depth == 0:
			return segment[:i], segment[i+1:], nil
		}
	}
	return "", "", syntaxErr("", "missing '=' in assignment")
}

// splitUnescaped splits s on unescaped occurrences of sep, preserving
// backslash sequences in each part for later unescapeSegment processing.
func splitUnescaped(s string, sep byte) []string {
	var parts []string
	var cur strings.Builder
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case escaped:
			cur.WriteByte(c)
			escaped = false
		case c == '\\':
			cur.WriteByte(c)
			escaped = true
		case c == sep:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	parts = append(parts, cur.String())
	return parts
}

// jsonNestingDepth returns the maximum `{}`/`[]` nesting depth of a JSON
// text, ignoring bracket-like characters inside string literals, without
// invoking encoding/json (so we can reject pathological input before
// decoding it).
func jsonNestingDepth(s string) (int, error) {
	depth, maxDepth := 0, 0
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			depth++
			if depth > maxDepth {
				maxDepth = depth
			}
		case '}', ']':
			depth--
			if depth < 0 {
				return 0, syntaxErr("", "unbalanced JSON structure")
			}
		}
	}
	if inString {
		return 0, syntaxErr("", "unterminated JSON string")
	}
	if depth != 0 {
		return 0, syntaxErr("", "unbalanced JSON structure")
	}
	return maxDepth, nil
}
