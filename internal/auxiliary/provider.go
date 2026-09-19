package auxiliary

import "context"

// mitto-mx9.3 audit: this package's public surface (ProcessProvider,
// ProcessQuiescenceProvider below, plus prompts.go/outcome.go/utils.go) is
// neutral by construction — it takes/returns plain strings and errors, never
// an ACP SDK type (acp.PromptResponse, acp.StopReason, etc.) or a raw
// *acp-go-sdk connection. Auxiliary/hidden-session utility tasks (title
// generation, follow-up analysis, improve-prompt) already flow through this
// seam rather than touching ACP directly, so there is nothing to wire onto
// internal/acpbackend's translators here. Keep it that way: a future change
// that has this package import "github.com/coder/acp-go-sdk" or
// internal/acpbackend directly would reintroduce the protocol coupling this
// package exists to avoid.

// ProcessProvider creates and manages auxiliary ACP sessions within workspace processes.
// This interface allows the auxiliary package to remain independent of the web package
// while still leveraging workspace-scoped ACP processes.
type ProcessProvider interface {
	// PromptAuxiliary sends a prompt to an auxiliary session for the given workspace and purpose.
	// The provider manages session creation and reuse internally.
	//
	// Parameters:
	//   - workspaceUUID: Identifies which workspace's ACP process to use
	//   - purpose: Identifies the session type (e.g., "title-gen", "follow-up", "improve-prompt")
	//   - message: The prompt message to send to the auxiliary session
	//
	// Returns the agent's response text or an error.
	PromptAuxiliary(ctx context.Context, workspaceUUID, purpose, message string) (string, error)

	// PromptAuxiliaryAsync sends a prompt to an auxiliary session without waiting for the response.
	// The prompt is dispatched and the method returns immediately. The agent processes in the background.
	// Returns error only if the prompt couldn't be dispatched (no process, no session, context cancelled).
	PromptAuxiliaryAsync(ctx context.Context, workspaceUUID, purpose, message string) error

	// CloseWorkspaceAuxiliary closes all auxiliary sessions for a workspace.
	// This should be called when a workspace is removed or its ACP process is stopped.
	CloseWorkspaceAuxiliary(workspaceUUID string) error
}

// ProcessQuiescenceProvider is an optional ProcessProvider capability used by
// deferred auxiliary work that can wait for a safe zero-RPC admission edge.
type ProcessQuiescenceProvider interface {
	WaitForProcessQuiescence(ctx context.Context, workspaceUUID string) bool
}
