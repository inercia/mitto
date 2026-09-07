// Package configpath implements a pure, dependency-free parser for a
// Helm-style compatible subset of dotted-path / assignment syntax used by
// the (future) `mitto config get`/`config set` CLI commands.
//
// The package has NO side effects: no filesystem access, no network, no CLI
// globals, and it does not depend on internal/cmd or internal/web. It only
// turns raw flag strings into typed, ordered operations. Applying those
// operations to a live configuration is explicitly out of scope here.
package configpath

import (
	"fmt"
	"strings"
)

// PathSegment is one step of a Path: either a map key or a nonnegative array
// index. Exactly one of Key/Index is meaningful, selected by IsIndex.
type PathSegment struct {
	Key     string
	Index   int
	IsIndex bool
}

// Path is an ordered sequence of PathSegments, e.g. `a.b[0].c`.
type Path []PathSegment

// String renders the path back into its canonical escaped textual form. It
// is safe to include in error messages and logs (it never contains values).
func (p Path) String() string {
	var b strings.Builder
	for i, seg := range p {
		if seg.IsIndex {
			fmt.Fprintf(&b, "[%d]", seg.Index)
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(escapeKeyForDisplay(seg.Key))
	}
	return b.String()
}

func escapeKeyForDisplay(key string) string {
	var b strings.Builder
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch c {
		case '\\', '.', ',':
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	return b.String()
}

// Mode selects how a raw assignment value string is interpreted.
type Mode int

const (
	// ModeTyped is `--set`: bool/int/float/null/string auto-detection.
	ModeTyped Mode = iota
	// ModeString is `--set-string`: value is always a literal string.
	ModeString
	// ModeJSON is `--set-json`: value is strict JSON (scalar/object/array).
	ModeJSON
	// ModeFile is `--set-file`: value is already-read content, preserved
	// literally. The parser never performs the file/stdin read itself.
	ModeFile
)

func (m Mode) String() string {
	switch m {
	case ModeTyped:
		return "typed"
	case ModeString:
		return "string"
	case ModeJSON:
		return "json"
	case ModeFile:
		return "file"
	default:
		return "unknown"
	}
}

// ValueKind identifies the shape of a resolved Value.
type ValueKind int

const (
	KindNull ValueKind = iota
	KindBool
	KindInt
	KindFloat
	KindString
	KindList
	KindJSON
)

// Value is a typed assignment value. Only the field matching Kind is
// meaningful.
type Value struct {
	Kind  ValueKind
	Bool  bool
	Int   int64
	Float float64
	Str   string
	// List holds elements for KindList (flat `{a,b,c}` brace lists).
	List []Value
	// JSON holds the decoded tree for KindJSON: nil, bool, int64, float64,
	// string, []interface{}, or map[string]interface{}.
	JSON interface{}
}

// Assignment is one resolved `path=value` pair together with the mode that
// produced it.
type Assignment struct {
	Path  Path
	Value Value
	Mode  Mode
}

// Op is a single operation in a resolved OpSet.
type Op struct {
	Path  Path
	Value Value
}

// OpSet is an ordered, deduplicated, conflict-checked list of operations
// produced by Resolve.
type OpSet struct {
	Ops []Op
}
