package web

import (
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/runner"
)

// acpProcessManagerAdapter adapts *ACPProcessManager to conversation.ProcessManager.
// It promotes all methods via embedding (EnsurePrewarmed, ClearGCSuspended,
// IsGCSuspended, StopGC, Close, ProcessCount) and wraps GetOrCreateProcess to
// convert the concrete *SharedACPProcess return to conversation.SharedProcess while
// guarding against the typed-nil-interface Go gotcha.
type acpProcessManagerAdapter struct{ *ACPProcessManager }

// GetOrCreateProcess delegates to ACPProcessManager and converts the concrete
// *SharedACPProcess return value to a conversation.SharedProcess interface.
// A nil *SharedACPProcess is returned as a nil interface (not a typed nil).
func (a acpProcessManagerAdapter) GetOrCreateProcess(workspace *config.WorkspaceSettings, acpCommand, acpCwd string, acpEnv map[string]string, r *runner.Runner, prewarm bool) (conversation.SharedProcess, error) {
	p, err := a.ACPProcessManager.GetOrCreateProcess(workspace, acpCommand, acpCwd, acpEnv, r, prewarm)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil // avoid typed-nil interface
	}
	return p, nil
}

// compile-time assertions: ensure both concrete types satisfy their interfaces.
var _ conversation.ProcessManager = acpProcessManagerAdapter{}
var _ conversation.EventsBroadcaster = (*GlobalEventsManager)(nil)
