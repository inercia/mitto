package acpproc

import (
	"context"
	"errors"
	"log/slog"
	"time"
	"unicode/utf8"

	mittoAcp "github.com/inercia/mitto/internal/acp"
)

// auxPromptTrace collects privacy-safe telemetry for one auxiliary prompt.
// It holds only sizes, counts, timing and identifiers — never prompt or
// response content. It is used by a single goroutine, so it needs no locking.
type auxPromptTrace struct {
	workspaceUUID string
	purpose       string
	sessionID     string
	async         bool
	start         time.Time

	promptChars    int
	promptBytes    int
	promptMessages int // content blocks sent per attempt (each aux prompt is one text block)

	attempts      int
	retried       bool
	modelID       string
	modelReported bool

	responseRecorded bool
	responseChars    int
	responseBytes    int
	responseChunks   int // agent message text chunks streamed back (not whole messages)
}

func newAuxPromptTrace(workspaceUUID, purpose, message string) *auxPromptTrace {
	return &auxPromptTrace{
		workspaceUUID:  workspaceUUID,
		purpose:        purpose,
		start:          time.Now(),
		promptChars:    utf8.RuneCountInString(message),
		promptBytes:    len(message),
		promptMessages: 1,
	}
}

// beginAttempt records the session/model used by the latest prompt attempt.
// The model is only reported when the agent reliably advertised it.
func (t *auxPromptTrace) beginAttempt(state *auxiliarySessionState, retry bool) {
	if t == nil {
		return
	}
	t.attempts++
	t.retried = t.retried || retry
	if state != nil {
		t.sessionID = state.sessionID
		t.modelID, t.modelReported = state.modelForLog()
	}
}

func (t *auxPromptTrace) recordResponse(c *auxiliaryClient) {
	if t == nil || c == nil {
		return
	}
	t.responseChars, t.responseBytes, t.responseChunks = c.responseStats()
	t.responseRecorded = true
}

// auxErrorClass maps an error to a small, fixed-cardinality label.
func auxErrorClass(ctx context.Context, err error) string {
	switch {
	case err == nil:
		return ""
	case (ctx != nil && errors.Is(ctx.Err(), context.Canceled)) || errors.Is(err, context.Canceled):
		return "cancelled"
	case (ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)) || errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case mittoAcp.IsACPConnectionError(err):
		return "connection"
	}
	if _, ok := rpcErrorCode(err); ok {
		return "rpc"
	}
	return "other"
}

// logAuxPromptOutcome emits exactly one structured completion record for an
// auxiliary prompt. Failures log error_class/rpc_code instead of the error
// text, which can embed prompt fragments.
func (m *ACPProcessManager) logAuxPromptOutcome(ctx context.Context, t *auxPromptTrace, err error) {
	if m == nil || m.logger == nil || t == nil {
		return
	}
	attrs := []any{
		"workspace_uuid", t.workspaceUUID,
		"purpose", t.purpose,
		"async", t.async,
		"duration_ms", time.Since(t.start).Milliseconds(),
		"prompt_chars", t.promptChars,
		"prompt_bytes", t.promptBytes,
		"prompt_messages", t.promptMessages,
		"attempts", t.attempts,
		"retry", t.retried,
		"success", err == nil,
	}
	if t.attempts > 0 {
		attrs = append(attrs,
			"session_id", t.sessionID,
			"model_id", t.modelID,
			"model_reported", t.modelReported)
	}
	if t.responseRecorded {
		attrs = append(attrs,
			"response_chars", t.responseChars,
			"response_bytes", t.responseBytes,
			"response_chunks", t.responseChunks)
	}
	if err == nil {
		m.logger.Info("Auxiliary prompt completed", attrs...)
		return
	}
	errorClass := auxErrorClass(ctx, err)
	attrs = append(attrs, "error_class", errorClass)
	if code, ok := rpcErrorCode(err); ok {
		attrs = append(attrs, "rpc_code", code)
	}
	level := slog.LevelError
	if errorClass == "cancelled" {
		level = slog.LevelWarn
	}
	m.logger.Log(context.Background(), level, "Auxiliary prompt failed", attrs...)
}
