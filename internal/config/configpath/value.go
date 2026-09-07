package configpath

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

var (
	integerTokenRE = regexp.MustCompile(`^-?[0-9]+$`)
	floatTokenRE   = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?[eE][-+]?[0-9]+$|^-?[0-9]+\.[0-9]+$`)
)

// parseTypedValue parses a `--set` value: either a flat brace list
// `{a,b,c}` or a single typed scalar.
func parseTypedValue(raw string) (Value, error) {
	if isBraceList(raw) {
		return parseListValue(raw[1:len(raw)-1], parseTypedScalar)
	}
	return parseTypedScalar(raw)
}

// parseStringValue parses a `--set-string` value: either a flat brace list
// of forced-string elements, or a single forced-string scalar.
func parseStringValue(raw string) (Value, error) {
	if isBraceList(raw) {
		return parseListValue(raw[1:len(raw)-1], parseStringScalar)
	}
	return parseStringScalar(raw)
}

func isBraceList(raw string) bool {
	return len(raw) >= 2 && raw[0] == '{' && raw[len(raw)-1] == '}'
}

func parseListValue(inner string, scalarParser func(string) (Value, error)) (Value, error) {
	if strings.ContainsAny(inner, "{}") {
		return Value{}, syntaxErr("", "nested lists are not supported; use --set-json")
	}
	if inner == "" {
		return Value{Kind: KindList, List: []Value{}}, nil
	}
	items := splitUnescaped(inner, ',')
	if len(items) > MaxListLength {
		return Value{}, limitErr("", "list exceeds max length")
	}
	vals := make([]Value, 0, len(items))
	for _, it := range items {
		v, err := scalarParser(it)
		if err != nil {
			return Value{}, err
		}
		vals = append(vals, v)
	}
	return Value{Kind: KindList, List: vals}, nil
}

func parseStringScalar(raw string) (Value, error) {
	unescaped, err := unescapeSegment(raw)
	if err != nil {
		return Value{}, err
	}
	return Value{Kind: KindString, Str: unescaped}, nil
}

// parseTypedScalar implements the `--set` value-typing rules: bool,
// integer, float, null (a value, not a delete), empty string, and
// leading-zero tokens preserved as strings (e.g. "007").
func parseTypedScalar(raw string) (Value, error) {
	unescaped, err := unescapeSegment(raw)
	if err != nil {
		return Value{}, err
	}
	switch unescaped {
	case "":
		return Value{Kind: KindString, Str: ""}, nil
	case "null":
		return Value{Kind: KindNull}, nil
	case "true":
		return Value{Kind: KindBool, Bool: true}, nil
	case "false":
		return Value{Kind: KindBool, Bool: false}, nil
	}
	if integerTokenRE.MatchString(unescaped) {
		if hasLeadingZero(unescaped) {
			return Value{Kind: KindString, Str: unescaped}, nil
		}
		if n, err := strconv.ParseInt(unescaped, 10, 64); err == nil {
			return Value{Kind: KindInt, Int: n}, nil
		}
		// Out of int64 range: cannot be represented losslessly as typed
		// int; fall back to string and let the caller prefer --set-json.
		return Value{Kind: KindString, Str: unescaped}, nil
	}
	if floatTokenRE.MatchString(unescaped) {
		if f, err := strconv.ParseFloat(unescaped, 64); err == nil {
			return Value{Kind: KindFloat, Float: f}, nil
		}
	}
	return Value{Kind: KindString, Str: unescaped}, nil
}

func hasLeadingZero(digits string) bool {
	d := digits
	if len(d) > 0 && d[0] == '-' {
		d = d[1:]
	}
	return len(d) > 1 && d[0] == '0'
}

// parseJSONValue parses a `--set-json` value: strict JSON, no trailing
// content, depth- and size-bounded before and after decoding.
func parseJSONValue(raw string) (Value, error) {
	if strings.TrimSpace(raw) == "" {
		return Value{}, syntaxErr("", "empty JSON value")
	}
	depth, err := jsonNestingDepth(raw)
	if err != nil {
		return Value{}, err
	}
	if depth > MaxJSONNestingDepth {
		return Value{}, limitErr("", "JSON nesting exceeds limit")
	}

	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return Value{}, syntaxErr("", "malformed JSON value")
	}
	if dec.More() {
		return Value{}, syntaxErr("", "trailing content after JSON value")
	}

	converted, err := convertJSONValue(v)
	if err != nil {
		return Value{}, err
	}
	return Value{Kind: KindJSON, JSON: converted}, nil
}

func convertJSONValue(v interface{}) (interface{}, error) {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i, nil
		}
		f, err := t.Float64()
		if err != nil {
			return nil, syntaxErr("", "malformed JSON number")
		}
		return f, nil
	case map[string]interface{}:
		if len(t) > MaxJSONObjectKeys {
			return nil, limitErr("", "JSON object exceeds max keys")
		}
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			if len(k) > MaxKeySegmentLength {
				return nil, limitErr("", "JSON object key too long")
			}
			cv, err := convertJSONValue(val)
			if err != nil {
				return nil, err
			}
			out[k] = cv
		}
		return out, nil
	case []interface{}:
		if len(t) > MaxListLength {
			return nil, limitErr("", "JSON array exceeds max length")
		}
		out := make([]interface{}, len(t))
		for i, val := range t {
			cv, err := convertJSONValue(val)
			if err != nil {
				return nil, err
			}
			out[i] = cv
		}
		return out, nil
	default:
		return v, nil
	}
}
