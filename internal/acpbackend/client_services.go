package acpbackend

import (
	"context"
	"strings"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
)

// ClientHooks lets a caller supply real file-access and permission-prompt
// behavior for the neutral ClientServices contract. A nil *ClientHooks (the
// default) means the adapter answers every fs/permission request from the
// agent with *agentbackend.UnsupportedError rather than silently
// auto-approving or reading/writing files with no oversight — callers that
// want the existing Mitto behavior (auto-approve, direct filesystem access)
// must wire it in explicitly via Connection.SetClientHooks. Real handlers
// (backed by the existing WebClient/internal/acp.DefaultFileSystem) are
// plugged in by the BackgroundSession wiring work in mitto-lrt.7.
type ClientHooks struct {
	ReadFile          func(ctx context.Context, ref agentbackend.SessionRef, path string) ([]byte, error)
	WriteFile         func(ctx context.Context, ref agentbackend.SessionRef, path string, data []byte) error
	RequestPermission func(ctx context.Context, ref agentbackend.SessionRef, prompt string) (bool, error)
}

// ReadFile implements agentbackend.ClientServices by delegating to the
// installed ClientHooks, or returning *agentbackend.UnsupportedError when
// none is installed.
func (c *Connection) ReadFile(ctx context.Context, ref agentbackend.SessionRef, path string) ([]byte, error) {
	c.mu.RLock()
	hooks := c.hooks
	c.mu.RUnlock()
	if hooks == nil || hooks.ReadFile == nil {
		return nil, &agentbackend.UnsupportedError{Feature: agentbackend.FeatureFiles}
	}
	return hooks.ReadFile(ctx, ref, path)
}

// WriteFile implements agentbackend.ClientServices.
func (c *Connection) WriteFile(ctx context.Context, ref agentbackend.SessionRef, path string, data []byte) error {
	c.mu.RLock()
	hooks := c.hooks
	c.mu.RUnlock()
	if hooks == nil || hooks.WriteFile == nil {
		return &agentbackend.UnsupportedError{Feature: agentbackend.FeatureFiles}
	}
	return hooks.WriteFile(ctx, ref, path, data)
}

// RequestPermission implements agentbackend.ClientServices.
func (c *Connection) RequestPermission(ctx context.Context, ref agentbackend.SessionRef, prompt string) (bool, error) {
	c.mu.RLock()
	hooks := c.hooks
	c.mu.RUnlock()
	if hooks == nil || hooks.RequestPermission == nil {
		return false, &agentbackend.UnsupportedError{Feature: agentbackend.FeaturePermissions}
	}
	return hooks.RequestPermission(ctx, ref, prompt)
}

// onReadTextFile builds the conversation.SessionCallbacks.OnReadTextFile
// handler for ref, translating the ACP request into a ClientServices.ReadFile
// call. Line/Limit slicing (ACP's fs/read_text_file supports reading a
// sub-range) is applied client-side after the full read since neutral
// ClientServices.ReadFile has no line/limit parameters.
func (c *Connection) onReadTextFile(ref agentbackend.SessionRef) func(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return func(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
		data, err := c.ReadFile(ctx, ref, params.Path)
		if err != nil {
			return acp.ReadTextFileResponse{}, err
		}
		return acp.ReadTextFileResponse{Content: sliceTextLines(string(data), params.Line, params.Limit)}, nil
	}
}

// onWriteTextFile builds the OnWriteTextFile handler for ref.
func (c *Connection) onWriteTextFile(ref agentbackend.SessionRef) func(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return func(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
		if err := c.WriteFile(ctx, ref, params.Path, []byte(params.Content)); err != nil {
			return acp.WriteTextFileResponse{}, err
		}
		return acp.WriteTextFileResponse{}, nil
	}
}

// onRequestPermission builds the OnRequestPermission handler for ref,
// translating the ACP request/options into a single
// ClientServices.RequestPermission(prompt) bool call, then translating the
// bool answer back into a selected ACP PermissionOption — preferring an
// Allow* option when approved and a Reject* option when denied, mirroring
// internal/acp.AutoApprovePermission's existing option-selection strategy so
// the two paths choose identically shaped responses.
func (c *Connection) onRequestPermission(ref agentbackend.SessionRef) func(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return func(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
		prompt := ""
		if params.ToolCall.Title != nil {
			prompt = *params.ToolCall.Title
		}
		approved, err := c.RequestPermission(ctx, ref, prompt)
		if err != nil {
			return acp.RequestPermissionResponse{}, err
		}
		return selectPermissionOutcome(params.Options, approved), nil
	}
}

// selectPermissionOutcome picks the ACP PermissionOption matching approved
// (an Allow* kind when true, a Reject* kind when false), falling back to the
// first option, and finally to a Cancelled outcome when there are none.
func selectPermissionOutcome(options []acp.PermissionOption, approved bool) acp.RequestPermissionResponse {
	for _, opt := range options {
		isAllow := opt.Kind == acp.PermissionOptionKindAllowOnce || opt.Kind == acp.PermissionOptionKindAllowAlways
		isReject := opt.Kind == acp.PermissionOptionKindRejectOnce || opt.Kind == acp.PermissionOptionKindRejectAlways
		if (approved && isAllow) || (!approved && isReject) {
			return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{OptionId: opt.OptionId}}}
		}
	}
	if len(options) > 0 {
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{OptionId: options[0].OptionId}}}
	}
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}
}

// sliceTextLines applies ACP's fs/read_text_file Line (1-based start) and
// Limit (max line count) semantics to an already-fully-read file's content.
func sliceTextLines(content string, line, limit *int) string {
	if line == nil && limit == nil {
		return content
	}
	lines := strings.Split(content, "\n")
	start := 0
	if line != nil && *line > 1 {
		start = *line - 1
	}
	if start > len(lines) {
		start = len(lines)
	}
	end := len(lines)
	if limit != nil && *limit >= 0 && start+*limit < end {
		end = start + *limit
	}
	return strings.Join(lines[start:end], "\n")
}

var _ agentbackend.ClientServices = (*Connection)(nil)
