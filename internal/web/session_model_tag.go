package web

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/inercia/mitto/internal/conversation"
)

// modelTagApplier keeps outcome mapping independent of session lifecycle. The
// concrete BackgroundSession remains the only implementation used by dispatch.
type modelTagApplier interface {
	ApplyModelTagWithResult(context.Context, string) (conversation.ModelTagApplyResult, error)
}

func applyModelTag(ctx context.Context, applier modelTagApplier, result *ModelTagAppliedResult) error {
	applied, err := applier.ApplyModelTagWithResult(ctx, result.ModelTag)
	if err != nil {
		switch {
		case errors.Is(err, conversation.ErrModelTagCatalogUnavailable):
			result.Status, result.Message = "catalog_unavailable", "Agent model catalog is unavailable"
		case errors.Is(err, conversation.ErrModelTagNoMatch):
			result.Status, result.Message = "no_match", "No available model matches this tag"
		default:
			result.Status, result.Message = "internal_error", "Failed to apply model tag"
		}
		return err
	}
	result.Status, result.ModelID = "applied", applied.ModelID
	if applied.AlreadyActive {
		result.Status = "already_active"
	}
	return nil
}

func (c *SessionWSClient) handleApplyModelTag(raw json.RawMessage) {
	result := ModelTagAppliedResult{SessionID: c.sessionID}
	defer func() { c.sendMessage(WSMsgTypeModelTagApplied, result) }()
	var request ApplyModelTagRequest
	data := strings.TrimSpace(string(raw))
	if len(data) == 0 || data[0] != '{' || json.Unmarshal(raw, &request) != nil {
		result.Status, result.Message = "invalid_request", "Invalid model-tag request"
		return
	}
	result.ModelTag, result.RequestID = strings.TrimSpace(request.ModelTag), request.RequestID
	if result.ModelTag == "" {
		result.Status, result.Message = "invalid_tag", "Model tag is required"
		return
	}
	c.initialLoadMu.Lock()
	bgSession := c.bgSession
	c.initialLoadMu.Unlock()
	if bgSession == nil {
		c.tryAttachToSession()
		c.initialLoadMu.Lock()
		bgSession = c.bgSession
		c.initialLoadMu.Unlock()
	}
	if bgSession == nil || bgSession.IsClosed() {
		result.Status, result.Message = "missing_session", "No active session"
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), modelConfigOptionCallerBudget)
	defer cancel()
	if err := applyModelTag(ctx, bgSession, &result); err != nil {
		if c.logger != nil {
			c.logger.Error("Failed to apply model tag", "session_id", c.sessionID, "model_tag", result.ModelTag, "error", err)
		}
		c.broadcastConfigSnapshotForSession(bgSession, string(conversation.ModelConfigId))
	}
}
