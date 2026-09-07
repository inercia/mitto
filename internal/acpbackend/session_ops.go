package acpbackend

import (
	"context"
	"fmt"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/conversation"
)

// NewSession implements agentbackend.SessionOps.
func (c *Connection) NewSession(ctx context.Context, provider agentbackend.ProviderID) (agentbackend.Session, error) {
	c.mu.RLock()
	cwd, mcpServers, expected := c.cwd, c.mcpServers, c.provider
	c.mu.RUnlock()
	if provider != expected {
		return nil, fmt.Errorf("acpbackend: unknown provider %q (connection serves %q): %w", provider, expected, agentbackend.ErrSessionNotFound)
	}

	handle, err := c.process.NewSession(ctx, cwd, mcpServers)
	if err != nil {
		return nil, translateError(err, "")
	}

	ref := agentbackend.SessionRef{
		ConversationID:  c.nextConversationID(),
		Provider:        provider,
		ProviderSession: agentbackend.ProviderSessionID(handle.SessionID),
	}
	return c.registerSession(ref, handle), nil
}

// LoadSession implements agentbackend.SessionOps.
func (c *Connection) LoadSession(ctx context.Context, ref agentbackend.SessionRef) (agentbackend.Session, error) {
	resolved, err := c.resolveLoadRef(ref)
	if err != nil {
		return nil, err
	}
	c.mu.RLock()
	cwd, mcpServers := c.cwd, c.mcpServers
	c.mu.RUnlock()

	handle, err := c.process.LoadSession(ctx, string(resolved.ProviderSession), cwd, mcpServers)
	if err != nil {
		return nil, translateError(err, "")
	}
	return c.registerSession(resolved, handle), nil
}

// ResumeSession implements agentbackend.SessionOps.
func (c *Connection) ResumeSession(ctx context.Context, ref agentbackend.SessionRef) (agentbackend.Session, error) {
	resolved, err := c.resolveLoadRef(ref)
	if err != nil {
		return nil, err
	}
	c.mu.RLock()
	cwd, mcpServers := c.cwd, c.mcpServers
	c.mu.RUnlock()

	handle, err := c.process.ResumeSession(ctx, string(resolved.ProviderSession), cwd, mcpServers)
	if err != nil {
		return nil, translateError(err, "")
	}
	return c.registerSession(resolved, handle), nil
}

// resolveLoadRef validates ref for Load/ResumeSession and fills in a
// synthesized ConversationID when the caller didn't track one (mirroring
// NewSession's synthesis — see Connection.nextConversationID).
func (c *Connection) resolveLoadRef(ref agentbackend.SessionRef) (agentbackend.SessionRef, error) {
	c.mu.RLock()
	expected := c.provider
	c.mu.RUnlock()
	if ref.Provider != "" && ref.Provider != expected {
		return agentbackend.SessionRef{}, fmt.Errorf("acpbackend: provider %q does not match connection provider %q: %w", ref.Provider, expected, agentbackend.ErrSessionNotFound)
	}
	if ref.ProviderSession == "" {
		return agentbackend.SessionRef{}, fmt.Errorf("acpbackend: ProviderSession id is required: %w", agentbackend.ErrSessionNotFound)
	}
	resolved := ref
	resolved.Provider = expected
	if resolved.ConversationID == "" {
		resolved.ConversationID = c.nextConversationID()
	}
	return resolved, nil
}

// registerSession builds the session's capability snapshot, wires its
// SessionCallbacks into the underlying SharedProcess, and tracks it for
// later Prompt/Cancel/SetModel/SetMode/Subscribe lookups by ref.
func (c *Connection) registerSession(ref agentbackend.SessionRef, handle *conversation.SessionHandle) *acpSession {
	sess := &acpSession{ref: ref, handle: handle}
	sess.setCapabilities(newSessionCapabilities(handle.Capabilities, handle.Models, handle.Modes))

	c.process.RegisterSession(acp.SessionId(handle.SessionID), c.buildCallbacks(ref))

	c.sessMu.Lock()
	c.sessions[ref] = sess
	c.sessMu.Unlock()
	return sess
}

// lookupSession resolves a SessionRef to the acpSession registered for it by
// NewSession/LoadSession/ResumeSession.
func (c *Connection) lookupSession(ref agentbackend.SessionRef) (*acpSession, error) {
	c.sessMu.Lock()
	defer c.sessMu.Unlock()
	s, ok := c.sessions[ref]
	if !ok {
		return nil, agentbackend.ErrSessionNotFound
	}
	return s, nil
}

// Prompt implements agentbackend.SessionOps.
func (c *Connection) Prompt(ctx context.Context, ref agentbackend.SessionRef, content []agentbackend.ContentBlock) (agentbackend.PromptOutcome, error) {
	sess, err := c.lookupSession(ref)
	if err != nil {
		return agentbackend.PromptOutcome{}, err
	}
	resp, err := c.process.Prompt(ctx, acp.SessionId(sess.handle.SessionID), FromNeutralContentBlocks(content))
	if err != nil {
		return agentbackend.PromptOutcome{}, translateError(err, "")
	}
	return ToNeutralPromptOutcome(resp, nil), nil
}

// Cancel implements agentbackend.SessionOps.
func (c *Connection) Cancel(ctx context.Context, ref agentbackend.SessionRef) error {
	sess, err := c.lookupSession(ref)
	if err != nil {
		return err
	}
	return translateError(c.process.Cancel(ctx, acp.SessionId(sess.handle.SessionID)), "")
}

// SetModel implements agentbackend.SessionOps. Success is reported only
// after the underlying SharedProcess acknowledges the switch (preserving the
// existing ACP semantics — see acpproc.SharedACPProcess.SetSessionModel).
func (c *Connection) SetModel(ctx context.Context, ref agentbackend.SessionRef, modelID string) error {
	sess, err := c.lookupSession(ref)
	if err != nil {
		return err
	}
	if err := c.process.SetSessionModel(ctx, acp.SessionId(sess.handle.SessionID), modelID); err != nil {
		return translateError(err, agentbackend.FeatureModelSelection)
	}
	return nil
}

// SetMode implements agentbackend.SessionOps. Success is reported only after
// the underlying SharedProcess acknowledges the switch (preserving the
// existing ACP semantics — see acpproc.SharedACPProcess.SetSessionMode).
func (c *Connection) SetMode(ctx context.Context, ref agentbackend.SessionRef, modeID string) error {
	sess, err := c.lookupSession(ref)
	if err != nil {
		return err
	}
	if err := c.process.SetSessionMode(ctx, acp.SessionId(sess.handle.SessionID), modeID); err != nil {
		return translateError(err, agentbackend.FeatureModeSelection)
	}
	return nil
}

var _ agentbackend.SessionOps = (*Connection)(nil)
