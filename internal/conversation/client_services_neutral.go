package conversation

// Neutral ClientServices/TerminalServices routing for the shared-process ACP
// path (mitto-mx9.4). internal/acpbackend already implements this exact
// adapter pattern (ClientHooks/TerminalHooks + Connection.buildCallbacks),
// but internal/acpbackend imports internal/conversation (for SharedProcess /
// SessionCallbacks) — reusing acpbackend.Connection here would create an
// import cycle. This file is the in-package counterpart: it wires a
// *WebClient's fs/permission/terminal handling through the SAME
// agentbackend.ClientServices / agentbackend.TerminalServices contracts, so
// production ACP requests are provably routed through the neutral seam
// (see internal/acpbackend/client_services.go and terminal_services.go for
// the mirrored translation strategy).

import (
	"context"

	acp "github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/agentbackend"
)

// webClientNeutralServices adapts a *WebClient onto agentbackend.ClientServices
// and agentbackend.TerminalServices.
type webClientNeutralServices struct {
	client *WebClient
}

// ReadFile implements agentbackend.ClientServices. Reads the full file; ACP's
// Line/Limit sub-range slicing is applied by the caller (onReadTextFileNeutral)
// after this returns, since the neutral contract carries no line/limit
// parameters — mirrors acpbackend.Connection.ReadFile + onReadTextFile.
func (s *webClientNeutralServices) ReadFile(ctx context.Context, ref agentbackend.SessionRef, path string) ([]byte, error) {
	content, err := mittoAcp.DefaultFileSystem.ReadTextFile(path, nil, nil)
	if err != nil {
		return nil, err
	}
	return []byte(content), nil
}

// WriteFile implements agentbackend.ClientServices.
func (s *webClientNeutralServices) WriteFile(ctx context.Context, ref agentbackend.SessionRef, path string, data []byte) error {
	return mittoAcp.DefaultFileSystem.WriteTextFile(path, string(data))
}

// RequestPermission implements agentbackend.ClientServices. Only reached from
// the auto-approve path — an interactive prompt (client.onPermission set)
// needs the full ACP PermissionOption list, which this neutral bool-only
// signature cannot carry, so onRequestPermissionNeutral answers those
// directly via the raw ACP callback instead of calling this method (the
// documented escape hatch — see the mitto-mx9.4 Plan comment).
func (s *webClientNeutralServices) RequestPermission(ctx context.Context, ref agentbackend.SessionRef, prompt string) (bool, error) {
	return s.client.autoApprove, nil
}

// CreateTerminal implements agentbackend.TerminalServices by delegating to
// the shared WebTerminalStub (mirrors WebClient.CreateTerminal).
func (s *webClientNeutralServices) CreateTerminal(ctx context.Context, ref agentbackend.SessionRef, command string, args []string, cwd string, env map[string]string) (agentbackend.TerminalHandle, error) {
	envPairs := make([]acp.EnvVariable, 0, len(env))
	for k, v := range env {
		envPairs = append(envPairs, acp.EnvVariable{Name: k, Value: v})
	}
	var cwdPtr *string
	if cwd != "" {
		cwdPtr = &cwd
	}
	resp, err := WebTerminalStub.CreateTerminal(ctx, acp.CreateTerminalRequest{
		SessionId: acp.SessionId(ref.ProviderSession),
		Command:   command,
		Args:      args,
		Cwd:       cwdPtr,
		Env:       envPairs,
	})
	if err != nil {
		return "", err
	}
	return agentbackend.TerminalHandle(resp.TerminalId), nil
}

// TerminalOutput implements agentbackend.TerminalServices.
func (s *webClientNeutralServices) TerminalOutput(ctx context.Context, ref agentbackend.SessionRef, handle agentbackend.TerminalHandle) (string, bool, *agentbackend.TerminalExitStatus, error) {
	resp, err := WebTerminalStub.TerminalOutput(ctx, acp.TerminalOutputRequest{
		SessionId:  acp.SessionId(ref.ProviderSession),
		TerminalId: string(handle),
	})
	if err != nil {
		return "", false, nil, err
	}
	return resp.Output, resp.Truncated, fromACPExitStatus(resp.ExitStatus), nil
}

// WaitForTerminalExit implements agentbackend.TerminalServices.
func (s *webClientNeutralServices) WaitForTerminalExit(ctx context.Context, ref agentbackend.SessionRef, handle agentbackend.TerminalHandle) (agentbackend.TerminalExitStatus, error) {
	resp, err := WebTerminalStub.WaitForTerminalExit(ctx, acp.WaitForTerminalExitRequest{
		SessionId:  acp.SessionId(ref.ProviderSession),
		TerminalId: string(handle),
	})
	if err != nil {
		return agentbackend.TerminalExitStatus{}, err
	}
	return agentbackend.TerminalExitStatus{ExitCode: resp.ExitCode, Signal: resp.Signal}, nil
}

// KillTerminal implements agentbackend.TerminalServices.
func (s *webClientNeutralServices) KillTerminal(ctx context.Context, ref agentbackend.SessionRef, handle agentbackend.TerminalHandle) error {
	_, err := WebTerminalStub.KillTerminal(ctx, acp.KillTerminalRequest{
		SessionId:  acp.SessionId(ref.ProviderSession),
		TerminalId: string(handle),
	})
	return err
}

// ReleaseTerminal implements agentbackend.TerminalServices.
func (s *webClientNeutralServices) ReleaseTerminal(ctx context.Context, ref agentbackend.SessionRef, handle agentbackend.TerminalHandle) error {
	_, err := WebTerminalStub.ReleaseTerminal(ctx, acp.ReleaseTerminalRequest{
		SessionId:  acp.SessionId(ref.ProviderSession),
		TerminalId: string(handle),
	})
	return err
}

// fromACPExitStatus translates an ACP terminal exit status into the neutral
// shape, preserving a nil (still running) as nil.
func fromACPExitStatus(exit *acp.TerminalExitStatus) *agentbackend.TerminalExitStatus {
	if exit == nil {
		return nil
	}
	return &agentbackend.TerminalExitStatus{ExitCode: exit.ExitCode, Signal: exit.Signal}
}

var (
	_ agentbackend.ClientServices   = (*webClientNeutralServices)(nil)
	_ agentbackend.TerminalServices = (*webClientNeutralServices)(nil)
)
