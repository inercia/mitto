package session

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"time"
)

// CloseRouterSidecarFile is the per-session sidecar name for the close-phase
// knowledge router's completion ledger (mitto-3od.1). Passed to
// ReadSessionSidecarJSON / WriteSessionSidecarJSON via the typed helpers
// below — callers should not hardcode the filename.
const CloseRouterSidecarFile = "close-router.json"

// CloseRouterSidecarVersion is the current close-router.json schema version.
const CloseRouterSidecarVersion = 1

// CloseRouterFinding records the persistence outcome for exactly one
// classified close-history finding within a single knowledge-router run.
// LogicalKey is stable across retries (CloseRouterLogicalKey), so a
// duplicate finding hashes to the same key and can be recognized as
// already-written without re-persisting it.
type CloseRouterFinding struct {
	// LogicalKey is the SHA-256 hex digest of the normalized finding text
	// (see CloseRouterLogicalKey) — the idempotency key for this finding.
	LogicalKey string `json:"logical_key"`
	// Destination is one of "preferences", "rules", "memory", "issue", or
	// "none" — the single persistence destination this finding was routed
	// to (the AC's "classify into at most one destination" guarantee).
	Destination string `json:"destination"`
	// Written reports whether this finding was actually persisted (false
	// for "none"/dropped findings, or when persistence was skipped because
	// an identical logical key was already written in a prior run).
	Written bool `json:"written"`
	// TargetPath is the file the finding was written to, when applicable
	// (e.g. the preferences file or a rules file). Empty for destinations
	// with no single target file (e.g. "memory", "issue") or when unwritten.
	TargetPath string `json:"target_path,omitempty"`
}

// CloseRouterRun records one knowledge-router execution against a single
// bounded close-history snapshot.
type CloseRouterRun struct {
	// RunID identifies this run (e.g. a UUID or timestamp-derived ID).
	RunID string `json:"run_id"`
	// StartedAt is when the router prompt was dispatched.
	StartedAt time.Time `json:"started_at"`
	// CompletedAt is when the router's output was parsed and applied. Zero
	// while a run is in flight, or if a prior run crashed before completing.
	CompletedAt time.Time `json:"completed_at,omitempty"`
	// HistorySnapshotHash is the SHA-256 hex digest of the exact JSON
	// snapshot handed to the router (see CloseRouterSnapshotHash). Retrying
	// against the SAME snapshot hash lets the caller skip already-written
	// logical keys; a DIFFERENT hash (history grew between runs) means a
	// full pass is warranted.
	HistorySnapshotHash string `json:"history_snapshot_hash"`
	// Findings lists every candidate classified during this run, in the
	// order the router emitted them.
	Findings []CloseRouterFinding `json:"findings,omitempty"`
}

// CloseRouterState is the close-router.json sidecar schema: a versioned,
// append-only ledger of knowledge-router runs for one session.
type CloseRouterState struct {
	// Version is the schema version (CloseRouterSidecarVersion). Present so
	// a future incompatible schema change can be detected and migrated.
	Version int `json:"version"`
	// Runs lists every knowledge-router run recorded for this session, in
	// chronological order (oldest first).
	Runs []CloseRouterRun `json:"runs,omitempty"`
}

// CloseRouterLogicalKey returns the stable idempotency key for a finding:
// the SHA-256 hex digest of its normalized (trimmed) text. Two findings
// with the same normalized text — even across separate router runs — hash
// to the same key, which is what lets a retry recognize "already written"
// without re-persisting it.
func CloseRouterLogicalKey(findingText string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(findingText)))
	return hex.EncodeToString(sum[:])
}

// CloseRouterSnapshotHash returns the SHA-256 hex digest of the exact bytes
// of the close-history snapshot passed to the router (typically its
// marshaled JSON form). Comparing this hash across runs is how a retry
// distinguishes "same snapshot, safe to skip already-written findings" from
// "history changed, run a full pass".
func CloseRouterSnapshotHash(snapshotJSON []byte) string {
	sum := sha256.Sum256(snapshotJSON)
	return hex.EncodeToString(sum[:])
}

// ReadCloseRouterState reads the close-router.json sidecar for sessionID.
// If the sidecar does not exist yet (no knowledge-router run has completed
// for this session), it returns a zero-run CloseRouterState with the
// current schema version and a nil error — this is the expected first-run
// state, not a failure. If the session itself has been deleted, it returns
// ErrSessionNotFound so callers can distinguish "no runs yet" from "session
// is gone" (mirrors the child-report sidecar convention in
// internal/mcpserver/child_report_store.go).
func ReadCloseRouterState(store *Store, sessionID string) (CloseRouterState, error) {
	state := CloseRouterState{Version: CloseRouterSidecarVersion}
	if store == nil {
		return state, nil
	}
	err := store.ReadSessionSidecarJSON(sessionID, CloseRouterSidecarFile, &state)
	if errors.Is(err, os.ErrNotExist) {
		return CloseRouterState{Version: CloseRouterSidecarVersion}, nil
	}
	if err != nil {
		return CloseRouterState{}, err
	}
	if state.Version == 0 {
		state.Version = CloseRouterSidecarVersion
	}
	return state, nil
}

// WriteCloseRouterState atomically persists state as the close-router.json
// sidecar for sessionID. Returns ErrSessionNotFound if the session was
// deleted concurrently.
func WriteCloseRouterState(store *Store, sessionID string, state CloseRouterState) error {
	if store == nil {
		return nil
	}
	if state.Version == 0 {
		state.Version = CloseRouterSidecarVersion
	}
	return store.WriteSessionSidecarJSON(sessionID, CloseRouterSidecarFile, state)
}
