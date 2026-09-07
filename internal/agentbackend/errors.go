package agentbackend

import (
	"errors"
	"fmt"
)

// Sentinel errors returned by backend implementations. Use errors.Is to test
// for these; UnsupportedError additionally carries the specific Feature via
// errors.As.
var (
	// ErrUnsupported is returned when a backend does not support a requested
	// optional feature. Implementations must return this (or an
	// *UnsupportedError wrapping it) rather than faking success.
	ErrUnsupported = errors.New("agentbackend: feature not supported")
	// ErrNotConnected is returned when an operation requires an active
	// Connection that hasn't been established (or has since been closed).
	ErrNotConnected = errors.New("agentbackend: not connected")
	// ErrSessionNotFound is returned when a SessionRef does not resolve to a
	// known session on the backend.
	ErrSessionNotFound = errors.New("agentbackend: session not found")
	// ErrCancelled is returned when an in-flight operation was cancelled.
	ErrCancelled = errors.New("agentbackend: operation cancelled")
	// ErrConflictingIdentity is returned when a legacy and a neutral
	// identity field both name the same entity but disagree (e.g. an old
	// alias name and a new BackendID/ProviderID pointing at different
	// things). Callers must reject rather than silently preferring one
	// side (see docs/devel/agent-backend-architecture.md, mitto-lrt.5).
	ErrConflictingIdentity = errors.New("agentbackend: conflicting legacy/neutral identity fields")
	// ErrAmbiguousAlias is returned when a legacy alias/name resolves to
	// two or more candidate backends/providers with no unambiguous winner.
	// Callers must reject rather than guessing.
	ErrAmbiguousAlias = errors.New("agentbackend: alias resolves to more than one candidate")
)

// UnsupportedError reports that a specific Feature is not supported by a
// backend or session. It wraps ErrUnsupported so callers can use
// errors.Is(err, ErrUnsupported) without caring which feature was involved.
type UnsupportedError struct {
	Feature Feature
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("agentbackend: feature %q not supported", string(e.Feature))
}

func (e *UnsupportedError) Unwrap() error { return ErrUnsupported }
