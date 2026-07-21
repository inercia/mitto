package stats

import (
	"context"
	"sync/atomic"
	"time"
)

// NoopStore is a no-op Store implementation used by tests and by callers that
// need to disable stats persistence (e.g. `--no-stats` builds, or dry-runs).
// All read methods return zero results; UpsertDeltas / SetCursor discard their
// input; Prune reports zero removed rows. Once Close has been called every
// subsequent method returns ErrClosed.
//
// NoopStore is safe for concurrent use.
type NoopStore struct {
	closed atomic.Bool
}

// Compile-time assertion that NoopStore satisfies the Store interface. Any
// drift in the Store method set will fail the build here, not in a downstream
// caller.
var _ Store = (*NoopStore)(nil)

// UpsertDeltas silently discards deltas.
func (n *NoopStore) UpsertDeltas(ctx context.Context, deltas []Delta) error {
	if n.closed.Load() {
		return ErrClosed
	}
	return nil
}

// UpsertDeltasWithCursor silently discards deltas and the cursor advance.
func (n *NoopStore) UpsertDeltasWithCursor(ctx context.Context, deltas []Delta, cur Cursor) error {
	if n.closed.Load() {
		return ErrClosed
	}
	return nil
}

// GetCursor returns a zero Cursor (with SessionID set) and ErrNotFound.
func (n *NoopStore) GetCursor(ctx context.Context, sessionID string) (Cursor, error) {
	if n.closed.Load() {
		return Cursor{}, ErrClosed
	}
	return Cursor{SessionID: sessionID}, ErrNotFound
}

// SetCursor silently discards the cursor.
func (n *NoopStore) SetCursor(ctx context.Context, cur Cursor) error {
	if n.closed.Load() {
		return ErrClosed
	}
	return nil
}

// Query returns an empty slice.
func (n *NoopStore) Query(ctx context.Context, q Query) ([]Point, error) {
	if n.closed.Load() {
		return nil, ErrClosed
	}
	return nil, nil
}

// Prune reports zero rows removed.
func (n *NoopStore) Prune(ctx context.Context, olderThan time.Time) (int64, error) {
	if n.closed.Load() {
		return 0, ErrClosed
	}
	return 0, nil
}

// GetMeta always returns an empty value and ErrNotFound. Matches the "no
// state persisted" invariant that every other read on NoopStore observes.
func (n *NoopStore) GetMeta(ctx context.Context, key string) (string, error) {
	if n.closed.Load() {
		return "", ErrClosed
	}
	return "", ErrNotFound
}

// SetMeta silently discards the write, mirroring UpsertDeltas / SetCursor.
func (n *NoopStore) SetMeta(ctx context.Context, key, value string) error {
	if n.closed.Load() {
		return ErrClosed
	}
	return nil
}

// ResetForEstimatorBump is a no-op — NoopStore holds no state to reset.
func (n *NoopStore) ResetForEstimatorBump(ctx context.Context) error {
	if n.closed.Load() {
		return ErrClosed
	}
	return nil
}

// Close marks the store as closed. Subsequent method calls return ErrClosed.
func (n *NoopStore) Close() error {
	n.closed.Store(true)
	return nil
}
