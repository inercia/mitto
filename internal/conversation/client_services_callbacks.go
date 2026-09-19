package conversation

// buildNeutralSessionCallbacks assembles the *SessionCallbacks registered
// with a SharedProcess for a session, translating each ACP fs/permission/
// terminal request through webClientNeutralServices (agentbackend.
// ClientServices / TerminalServices) instead of wiring ACP callbacks
// directly to *WebClient methods (mitto-mx9.4). OnSessionUpdate remains
// client.SessionUpdate directly — session-update translation is owned by
// the mitto-mx9.2 eventprojection seam already wired into WebClient, not by
// this bead's client-services scope.

import (
	"context"
	"strings"

	acp "github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/agentbackend"
)

// buildNeutralSessionCallbacks returns the SessionCallbacks for ref, routing
// fs/permission/terminal requests through the neutral ClientServices/
// TerminalServices contracts implemented by webClientNeutralServices.
func buildNeutralSessionCallbacks(client *WebClient, ref agentbackend.SessionRef) *SessionCallbacks {
	svc := &webClientNeutralServices{client: client}
	return &SessionCallbacks{
		OnSessionUpdate:       client.SessionUpdate,
		OnReadTextFile:        onReadTextFileNeutral(client, svc, ref),
		OnWriteTextFile:       onWriteTextFileNeutral(client, svc, ref),
		OnRequestPermission:   onRequestPermissionNeutral(client, svc, ref),
		OnCreateTerminal:      onCreateTerminalNeutral(svc, ref),
		OnTerminalOutput:      onTerminalOutputNeutral(svc, ref),
		OnReleaseTerminal:     onReleaseTerminalNeutral(svc, ref),
		OnWaitForTerminalExit: onWaitForTerminalExitNeutral(svc, ref),
		OnKillTerminal:        onKillTerminalNeutral(svc, ref),
	}
}

// onReadTextFileNeutral mirrors WebClient.ReadTextFile's seq/callback timing
// (seq assigned, onFileRead invoked, only after a successful read) while
// routing the actual read through agentbackend.ClientServices.ReadFile and
// applying ACP's Line/Limit slicing to the result via sliceTextLinesLikeOSFS
// — byte-identical output to the pre-mitto-mx9.4 direct path (which passed
// Line/Limit straight into internal/acp.OSFileSystem.ReadTextFile).
func onReadTextFileNeutral(client *WebClient, svc agentbackend.ClientServices, ref agentbackend.SessionRef) func(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return func(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
		data, err := svc.ReadFile(ctx, ref, params.Path)
		if err != nil {
			return acp.ReadTextFileResponse{}, err
		}
		content := sliceTextLinesLikeOSFS(string(data), params.Line, params.Limit)
		seq := client.getNextSeq()
		if client.onFileRead != nil {
			client.onFileRead(seq, params.Path, len(content))
		}
		return acp.ReadTextFileResponse{Content: content}, nil
	}
}

// sliceTextLinesLikeOSFS reproduces internal/acp.OSFileSystem.ReadTextFile's
// Line (1-based start)/Limit (max line count) slicing exactly, so routing
// the read through the line/limit-less neutral ClientServices.ReadFile and
// slicing here afterward is byte-identical to the pre-mitto-mx9.4 behavior
// of passing Line/Limit straight into OSFileSystem.ReadTextFile. Notably,
// Limit == 0 means "no limit" here (matching OSFileSystem's `*limit > 0`
// guard) — NOT "empty result", which is what internal/acpbackend's own
// sliceTextLines (a different edge-case contract for a different caller)
// would produce; do not unify the two without re-verifying both callers.
func sliceTextLinesLikeOSFS(content string, line, limit *int) string {
	if line == nil && limit == nil {
		return content
	}
	lines := strings.Split(content, "\n")
	start := 0
	if line != nil && *line > 0 {
		start = min(max(*line-1, 0), len(lines))
	}
	end := len(lines)
	if limit != nil && *limit > 0 && start+*limit < end {
		end = start + *limit
	}
	return strings.Join(lines[start:end], "\n")
}

// onWriteTextFileNeutral mirrors WebClient.WriteTextFile's seq/callback
// timing while routing the write through agentbackend.ClientServices.WriteFile.
func onWriteTextFileNeutral(client *WebClient, svc agentbackend.ClientServices, ref agentbackend.SessionRef) func(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return func(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
		if err := svc.WriteFile(ctx, ref, params.Path, []byte(params.Content)); err != nil {
			return acp.WriteTextFileResponse{}, err
		}
		seq := client.getNextSeq()
		if client.onFileWrite != nil {
			client.onFileWrite(seq, params.Path, len(params.Content))
		}
		return acp.WriteTextFileResponse{}, nil
	}
}

// onRequestPermissionNeutral preserves WebClient.RequestPermission's exact
// branching (flush buffered content, then auto-approve / interactive /
// cancel), but answers the auto-approve branch via the neutral
// ClientServices.RequestPermission call. The interactive branch is an
// escape hatch: the neutral bool-only signature cannot carry the ACP
// PermissionOption list an interactive prompter needs, so it is answered
// directly via client.onPermission, unchanged from the pre-mitto-mx9.4 path.
func onRequestPermissionNeutral(client *WebClient, svc agentbackend.ClientServices, ref agentbackend.SessionRef) func(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return func(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
		client.streamBuffer.Flush()

		prompt := ""
		if params.ToolCall.Title != nil {
			prompt = *params.ToolCall.Title
		}

		if client.autoApprove {
			approved, err := svc.RequestPermission(ctx, ref, prompt)
			if err != nil {
				return acp.RequestPermissionResponse{}, err
			}
			if approved {
				// mittoAcp.AutoApprovePermission is the exact pre-mitto-mx9.4
				// option-selection strategy (WebClient.autoApprovePermission);
				// reused directly rather than a locally reimplemented option
				// picker, so the outcome is byte-identical.
				return mittoAcp.AutoApprovePermission(params.Options), nil
			}
			return acp.RequestPermissionResponse{
				Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
			}, nil
		}
		if client.onPermission != nil {
			return client.onPermission(ctx, params)
		}
		return acp.RequestPermissionResponse{
			Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
		}, nil
	}
}

func onCreateTerminalNeutral(svc agentbackend.TerminalServices, ref agentbackend.SessionRef) func(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return func(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
		cwd := ""
		if params.Cwd != nil {
			cwd = *params.Cwd
		}
		env := make(map[string]string, len(params.Env))
		for _, e := range params.Env {
			env[e.Name] = e.Value
		}
		handle, err := svc.CreateTerminal(ctx, ref, params.Command, params.Args, cwd, env)
		if err != nil {
			return acp.CreateTerminalResponse{}, err
		}
		return acp.CreateTerminalResponse{TerminalId: string(handle)}, nil
	}
}

func onTerminalOutputNeutral(svc agentbackend.TerminalServices, ref agentbackend.SessionRef) func(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return func(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
		output, truncated, exit, err := svc.TerminalOutput(ctx, ref, agentbackend.TerminalHandle(params.TerminalId))
		if err != nil {
			return acp.TerminalOutputResponse{}, err
		}
		return acp.TerminalOutputResponse{Output: output, Truncated: truncated, ExitStatus: toACPExitStatusNeutral(exit)}, nil
	}
}

func onWaitForTerminalExitNeutral(svc agentbackend.TerminalServices, ref agentbackend.SessionRef) func(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return func(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
		exit, err := svc.WaitForTerminalExit(ctx, ref, agentbackend.TerminalHandle(params.TerminalId))
		if err != nil {
			return acp.WaitForTerminalExitResponse{}, err
		}
		return acp.WaitForTerminalExitResponse{ExitCode: exit.ExitCode, Signal: exit.Signal}, nil
	}
}

func onKillTerminalNeutral(svc agentbackend.TerminalServices, ref agentbackend.SessionRef) func(ctx context.Context, params acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return func(ctx context.Context, params acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
		if err := svc.KillTerminal(ctx, ref, agentbackend.TerminalHandle(params.TerminalId)); err != nil {
			return acp.KillTerminalResponse{}, err
		}
		return acp.KillTerminalResponse{}, nil
	}
}

func onReleaseTerminalNeutral(svc agentbackend.TerminalServices, ref agentbackend.SessionRef) func(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return func(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
		if err := svc.ReleaseTerminal(ctx, ref, agentbackend.TerminalHandle(params.TerminalId)); err != nil {
			return acp.ReleaseTerminalResponse{}, err
		}
		return acp.ReleaseTerminalResponse{}, nil
	}
}

// toACPExitStatusNeutral translates a neutral *agentbackend.TerminalExitStatus
// into the ACP wire shape, preserving a nil (still running) as nil.
func toACPExitStatusNeutral(exit *agentbackend.TerminalExitStatus) *acp.TerminalExitStatus {
	if exit == nil {
		return nil
	}
	return &acp.TerminalExitStatus{ExitCode: exit.ExitCode, Signal: exit.Signal}
}
