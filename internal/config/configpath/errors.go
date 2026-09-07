package configpath

import "fmt"

// ErrKind classifies a parse/resolve failure.
type ErrKind string

const (
	KindErrSyntax   ErrKind = "syntax"
	KindErrLimit    ErrKind = "limit"
	KindErrConflict ErrKind = "conflict"
	KindErrType     ErrKind = "type"
)

// Error is a sanitized, typed parse/resolve error. It carries the offending
// path (safe to display) and a fixed description, but NEVER the raw input
// value, since values may contain secrets.
type Error struct {
	Kind ErrKind
	Path string
	Msg  string
}

func (e *Error) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("%s: %s: %s", e.Kind, e.Path, e.Msg)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Msg)
}

func syntaxErr(path, msg string) *Error   { return &Error{Kind: KindErrSyntax, Path: path, Msg: msg} }
func limitErr(path, msg string) *Error    { return &Error{Kind: KindErrLimit, Path: path, Msg: msg} }
func conflictErr(path, msg string) *Error { return &Error{Kind: KindErrConflict, Path: path, Msg: msg} }
