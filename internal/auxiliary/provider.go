package auxiliary

import "context"

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
