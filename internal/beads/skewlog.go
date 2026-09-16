package beads

import (
	"log/slog"
	"sync"
)

// skewWarnLogged deduplicates schema-skew WARN log lines for the whole
// process lifetime, keyed by an identity string supplied by the caller (see
// WarnSchemaSkewOnce). It intentionally never expires or shrinks: a schema
// skew is a deterministic, sticky condition (mitto-790) — the identity
// naming it does not change until a human migrates the database or the bd
// binary version changes — so "log it once per identity, ever" is the
// correct policy here, not a time-windowed rate limit.
var skewWarnLogged sync.Map

// WarnSchemaSkewOnce logs a schema-skew WARN via logger.Warn(msg, args...)
// only the first time it is called for a given key (typically the affected
// workspace's working directory or database path). Subsequent calls with the
// same key are silently suppressed — this is what prevents background
// watcher cycles and repeated client polling against a deterministically
// broken database from producing a WARN storm (mitto-790): six skewed
// workspaces times multiple watcher-driven refresh sites times a short cycle
// cadence otherwise multiplies into hundreds of duplicate WARN lines that
// carry no new information after the first.
//
// It is a no-op when logger is nil or err does not represent a schema-skew
// failure (per IsSchemaSkew) — callers should keep emitting their own
// unconditional WARN for non-skew errors, since those may be transient and
// every occurrence is diagnostically useful. When key is empty, err.Error()
// is used instead so distinct failures still get distinct identities rather
// than colliding on a shared blank key. Safe for concurrent use.
func WarnSchemaSkewOnce(logger *slog.Logger, key string, err error, msg string, args ...any) {
	if logger == nil || !IsSchemaSkew(err) {
		return
	}
	if key == "" {
		key = err.Error()
	}
	if _, loaded := skewWarnLogged.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	logger.Warn(msg, args...)
}
