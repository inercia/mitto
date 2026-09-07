package configpath

// ParseSet parses a `--set` raw flag value (comma-separated `path=value`
// assignments, typed value detection) into Assignments.
func ParseSet(raw string) ([]Assignment, error) {
	return parseAssignments(raw, ModeTyped)
}

// ParseSetString parses a `--set-string` raw flag value (comma-separated
// `path=value` assignments, values always forced to string) into
// Assignments.
func ParseSetString(raw string) ([]Assignment, error) {
	return parseAssignments(raw, ModeString)
}

// ParseSetJSON parses a `--set-json` raw flag value (comma-separated
// `path=value` assignments, values are strict JSON) into Assignments.
func ParseSetJSON(raw string) ([]Assignment, error) {
	return parseAssignments(raw, ModeJSON)
}

// ParseSetFile parses a single `--set-file` raw `path=content` pair. The
// content must already have been read by the caller (bounded, one explicit
// stdin source) — this function does not touch the filesystem and preserves
// content literally, with no comma-splitting (content may contain arbitrary
// bytes including commas and newlines).
func ParseSetFile(raw string) (Assignment, error) {
	if len(raw) == 0 {
		return Assignment{}, syntaxErr("", "empty input")
	}
	if len(raw) > MaxFileValueBytes {
		return Assignment{}, limitErr("", "file value exceeds max bytes")
	}
	pathStr, content, err := splitAssignment(raw)
	if err != nil {
		return Assignment{}, err
	}
	path, err := ParsePath(pathStr)
	if err != nil {
		return Assignment{}, err
	}
	return Assignment{Path: path, Value: Value{Kind: KindString, Str: content}, Mode: ModeFile}, nil
}

func parseAssignments(raw string, mode Mode) ([]Assignment, error) {
	if len(raw) == 0 {
		return nil, syntaxErr("", "empty input")
	}
	if len(raw) > MaxRawInputBytes {
		return nil, limitErr("", "input exceeds max bytes")
	}
	segments, err := splitTopLevel(raw)
	if err != nil {
		return nil, err
	}
	if len(segments) > MaxOperations {
		return nil, limitErr("", "too many assignments in one flag")
	}

	result := make([]Assignment, 0, len(segments))
	for _, seg := range segments {
		pathStr, valueStr, err := splitAssignment(seg)
		if err != nil {
			return nil, err
		}
		path, err := ParsePath(pathStr)
		if err != nil {
			return nil, err
		}

		var val Value
		switch mode {
		case ModeTyped:
			val, err = parseTypedValue(valueStr)
		case ModeString:
			val, err = parseStringValue(valueStr)
		case ModeJSON:
			val, err = parseJSONValue(valueStr)
		default:
			return nil, syntaxErr(path.String(), "unsupported mode")
		}
		if err != nil {
			if pe, ok := err.(*Error); ok && pe.Path == "" {
				pe.Path = path.String()
			}
			return nil, err
		}
		result = append(result, Assignment{Path: path, Value: val, Mode: mode})
	}
	return result, nil
}
