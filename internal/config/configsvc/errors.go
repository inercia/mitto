// Package configsvc provides side-effect-free settings.json snapshots and
// transactional, validated settings mutations, shared by future offline CLI
// (`mitto config get`/`config set`) and live web handlers.
//
// The read path (snapshot.go) never creates, migrates, or dedup-saves
// settings.json, and never touches secure storage (Keychain) — it is a pure
// mirror of on-disk state plus a pure re-derivation of the "effective" merged
// config using the same rules as internal/config's loader, without any of
// that loader's side effects. The write path (mutate.go) validates a whole
// batch of typed configpath operations before persisting, and persists
// atomically with restrictive (0600) permissions.
//
// This package does not import internal/cmd or internal/web.
package configsvc

import "fmt"

// ErrKind classifies a configsvc failure so callers (CLI, web handlers) can
// map it to a distinct exit code / HTTP status without string-matching.
type ErrKind string

const (
	// ErrKindNotFound: settings.json does not exist (pure read never creates it).
	ErrKindNotFound ErrKind = "not_found"
	// ErrKindUnknownField: path does not match any registered field.
	ErrKindUnknownField ErrKind = "unknown_field"
	// ErrKindRejected: path matches a credential/system field that is never
	// writable through this service.
	ErrKindRejected ErrKind = "rejected"
	// ErrKindReadOnly: path is a valid, known field but only available in the
	// effective (merged) view — not stored, and not settable in v1.
	ErrKindReadOnly ErrKind = "read_only"
	// ErrKindValidation: value failed the field's validator.
	ErrKindValidation ErrKind = "validation"
	// ErrKindConflict: ancestor/descendant path conflict within one batch, or
	// a parent-object replacement that would smuggle in a rejected/unknown
	// nested field.
	ErrKindConflict ErrKind = "conflict"
	// ErrKindStructure: the target path cannot be applied against the actual
	// document shape (e.g. indexing into a scalar, sparse array index).
	ErrKindStructure ErrKind = "structure"
	// ErrKindRevisionMismatch: optimistic-concurrency check failed — the
	// on-disk settings changed since the snapshot the caller read.
	ErrKindRevisionMismatch ErrKind = "revision_mismatch"
	// ErrKindLocked: another writer (process or detected running server)
	// currently owns the offline settings lock.
	ErrKindLocked ErrKind = "locked"
	// ErrKindIO: an underlying filesystem operation failed.
	ErrKindIO ErrKind = "io"
)

// Error is a typed configsvc error. Path is safe to display (never a raw
// value). Msg is a fixed, non-value-carrying description.
type Error struct {
	Kind ErrKind
	Path string
	Msg  string
	// Err wraps an underlying error (e.g. an I/O error), if any. Never a
	// value-carrying configpath error with user data — those are already
	// sanitized by internal/config/configpath.
	Err error
}

func (e *Error) Error() string {
	if e.Path != "" {
		if e.Err != nil {
			return fmt.Sprintf("%s: %s: %s: %v", e.Kind, e.Path, e.Msg, e.Err)
		}
		return fmt.Sprintf("%s: %s: %s", e.Kind, e.Path, e.Msg)
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Kind, e.Msg, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Msg)
}

func (e *Error) Unwrap() error { return e.Err }

func newErr(kind ErrKind, path, msg string) *Error {
	return &Error{Kind: kind, Path: path, Msg: msg}
}

func wrapErr(kind ErrKind, path, msg string, err error) *Error {
	return &Error{Kind: kind, Path: path, Msg: msg, Err: err}
}
