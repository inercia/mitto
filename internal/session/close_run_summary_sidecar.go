package session

import (
	"errors"
	"os"
)

// CloseRunSummarySidecarFile is the per-session sidecar name for the
// close-phase pipeline's telemetry summary (mitto-3od.3). Passed to
// ReadSessionSidecarJSON / WriteSessionSidecarJSON via the typed helpers
// below — callers should not hardcode the filename.
const CloseRunSummarySidecarFile = "close-run-summary.json"

// CloseRunSummarySidecarVersion is the current close-run-summary.json schema
// version.
const CloseRunSummarySidecarVersion = 1

// CloseRunProcessorEntry records one processor's outcome within a single
// close-phase pipeline run. Mirrors the subset of processors.ProcessorRun
// relevant to a post-mortem summary (session package cannot import
// processors — that would invert the internal/processors -> internal/session
// dependency direction — so the fields are duplicated here as plain data).
type CloseRunProcessorEntry struct {
	// Name is the processor's Name.
	Name string `json:"name"`
	// Outcome is "ok", "error", or "skipped".
	Outcome string `json:"outcome"`
	// Mode describes how the output was applied (empty for skipped/error).
	Mode string `json:"mode,omitempty"`
	// Target describes where the output went (empty for skipped/error/discard).
	Target string `json:"target,omitempty"`
	// RenderedBytes is the UTF-8 byte length of the rendered content for an
	// "ok" run. Always 0 for "skipped"/"error".
	RenderedBytes int `json:"rendered_bytes,omitempty"`
	// EstimatedTokens is the length-based token estimate for an "ok" run's
	// rendered content. Always 0 for "skipped"/"error".
	EstimatedTokens int `json:"estimated_tokens,omitempty"`
	// SkipReason is a machine-readable skip slug when Outcome == "skipped".
	SkipReason string `json:"skip_reason,omitempty"`
	// DurationMs is the wall-clock execution time in milliseconds. Zero for
	// skipped runs and for text-mode/prompt-mode processors.
	DurationMs int64 `json:"duration_ms,omitempty"`
}

// CloseRunSummaryEntry records one close-phase pipeline execution against a
// single archived session.
type CloseRunSummaryEntry struct {
	// RunID identifies this close-phase pipeline run.
	RunID string `json:"run_id"`
	// ArchiveReason is one of "manual", "inactivity", "acp_start_failures",
	// etc. Mirrors session.ArchiveReason values.
	ArchiveReason string `json:"archive_reason,omitempty"`
	// StartedAt is when the close-phase pipeline began.
	StartedAt string `json:"started_at"`
	// CompletedAt is when the close-phase pipeline finished iterating every
	// processor (prompt-mode dispatches may still be in flight afterward —
	// this pipeline is fire-and-forget, see ApplyOnClose).
	CompletedAt string `json:"completed_at"`
	// TotalProcessors is the number of conversationClosed processors
	// considered (before the enabled/enabledWhen/cascade filters).
	TotalProcessors int `json:"total_processors"`
	// Applied is the number of processors that ran (Outcome == "ok").
	Applied int `json:"applied"`
	// Skipped is the number of processors skipped for any reason (disabled,
	// enabledWhen false, cascaded child close, no prompt executor, etc).
	Skipped int `json:"skipped"`
	// Errored is the number of processors that failed (Outcome == "error").
	Errored int `json:"errored"`
	// TotalEstTokensPrimary sums EstimatedTokens across "ok" runs with
	// Target == "primary".
	TotalEstTokensPrimary int `json:"total_est_tokens_primary,omitempty"`
	// TotalEstTokensAuxiliary sums EstimatedTokens across "ok" runs with
	// Target == "auxiliary".
	TotalEstTokensAuxiliary int `json:"total_est_tokens_auxiliary,omitempty"`
	// Processors lists one entry per processor considered during this run,
	// in the order the pipeline evaluated them.
	Processors []CloseRunProcessorEntry `json:"processors,omitempty"`
}

// CloseRunSummary is the close-run-summary.json sidecar schema: a versioned,
// append-only ledger of close-phase pipeline runs for one session.
type CloseRunSummary struct {
	// Version is the schema version (CloseRunSummarySidecarVersion).
	Version int `json:"version"`
	// Runs lists every close-phase pipeline run recorded for this session,
	// in chronological order (oldest first).
	Runs []CloseRunSummaryEntry `json:"runs,omitempty"`
}

// ReadCloseRunSummary reads the close-run-summary.json sidecar for
// sessionID. If the sidecar does not exist yet (no close-phase pipeline run
// has completed for this session), it returns a zero-run CloseRunSummary
// with the current schema version and a nil error — this is the expected
// first-run state, not a failure. If the session itself has been deleted, it
// returns ErrSessionNotFound so callers can distinguish "no runs yet" from
// "session is gone" (mirrors ReadCloseRouterState).
func ReadCloseRunSummary(store *Store, sessionID string) (CloseRunSummary, error) {
	summary := CloseRunSummary{Version: CloseRunSummarySidecarVersion}
	if store == nil {
		return summary, nil
	}
	err := store.ReadSessionSidecarJSON(sessionID, CloseRunSummarySidecarFile, &summary)
	if errors.Is(err, os.ErrNotExist) {
		return CloseRunSummary{Version: CloseRunSummarySidecarVersion}, nil
	}
	if err != nil {
		return CloseRunSummary{}, err
	}
	if summary.Version == 0 {
		summary.Version = CloseRunSummarySidecarVersion
	}
	return summary, nil
}

// WriteCloseRunSummary atomically persists summary as the
// close-run-summary.json sidecar for sessionID. Returns ErrSessionNotFound
// if the session was deleted concurrently.
func WriteCloseRunSummary(store *Store, sessionID string, summary CloseRunSummary) error {
	if store == nil {
		return nil
	}
	if summary.Version == 0 {
		summary.Version = CloseRunSummarySidecarVersion
	}
	return store.WriteSessionSidecarJSON(sessionID, CloseRunSummarySidecarFile, summary)
}

// AppendCloseRunSummary reads the existing close-run-summary.json sidecar
// for sessionID, appends entry to its Runs list, and writes the result back.
// Convenience wrapper around Read+Write for the common append-one-run case;
// not atomic across the read/write pair (fire-and-forget best-effort call
// site, same tolerance as the rest of the close-phase pipeline).
func AppendCloseRunSummary(store *Store, sessionID string, entry CloseRunSummaryEntry) error {
	summary, err := ReadCloseRunSummary(store, sessionID)
	if err != nil {
		return err
	}
	summary.Runs = append(summary.Runs, entry)
	return WriteCloseRunSummary(store, sessionID, summary)
}
