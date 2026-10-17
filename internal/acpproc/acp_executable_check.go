package acpproc

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/runner"
)

// checkACPExecutable reports a classified configuration error only when a
// direct-exec bare command is definitely absent from Mitto's process PATH.
func checkACPExecutable(workspace *config.WorkspaceSettings, command string, r *runner.Runner) error {
	if r != nil {
		return nil
	}
	args, err := mittoAcp.ParseCommand(command)
	if err != nil || len(args) == 0 {
		return nil
	}
	env := mittoAcp.BuildMittoEnv("", workspace.WorkingDir, workspace.ACPServer, "")
	args = mittoAcp.ExpandArgs(args, env)
	if len(args) == 0 || args[0] == "" || strings.ContainsAny(args[0], `/\\`) {
		return nil
	}
	_, err = exec.LookPath(args[0])
	if !errors.Is(err, exec.ErrNotFound) {
		return nil
	}
	lookupErr := fmt.Errorf("%w: %w", mittoAcp.ErrACPExecutableUnavailable, err)
	return &mittoAcp.ACPClassifiedError{
		Class:         mittoAcp.ACPErrorPermanent,
		OriginalError: fmt.Errorf("ACP executable %q for server %q in workspace %q is unavailable: %w; install it on Mitto's process PATH or configure an absolute executable path", args[0], workspace.ACPServer, workspace.UUID, lookupErr),
		UserMessage:   fmt.Sprintf("ACP executable %q is unavailable", args[0]),
		UserGuidance:  "Install it on Mitto's process PATH or configure an absolute executable path.",
	}
}
