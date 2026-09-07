package acpbackend

import (
	"context"
	"errors"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
)

func TestTerminalServices_NilHooks_FailClosed(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	ref := agentbackend.SessionRef{ConversationID: "conv-1"}

	var unsupported *agentbackend.UnsupportedError

	if _, err := c.CreateTerminal(context.Background(), ref, "echo", nil, "", nil); !errors.As(err, &unsupported) || unsupported.Feature != agentbackend.FeatureTerminals {
		t.Fatalf("CreateTerminal: expected *UnsupportedError{terminals}, got %v", err)
	}
	if _, _, _, err := c.TerminalOutput(context.Background(), ref, "term-1"); !errors.As(err, &unsupported) {
		t.Fatalf("TerminalOutput: expected *UnsupportedError, got %v", err)
	}
	if _, err := c.WaitForTerminalExit(context.Background(), ref, "term-1"); !errors.As(err, &unsupported) {
		t.Fatalf("WaitForTerminalExit: expected *UnsupportedError, got %v", err)
	}
	if err := c.KillTerminal(context.Background(), ref, "term-1"); !errors.As(err, &unsupported) {
		t.Fatalf("KillTerminal: expected *UnsupportedError, got %v", err)
	}
	if err := c.ReleaseTerminal(context.Background(), ref, "term-1"); !errors.As(err, &unsupported) {
		t.Fatalf("ReleaseTerminal: expected *UnsupportedError, got %v", err)
	}
}

func TestTerminalServices_HooksInstalled_Delegates(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	ref := agentbackend.SessionRef{ConversationID: "conv-1"}

	var gotCommand string
	var gotArgs []string
	var gotCwd string
	var gotEnv map[string]string
	c.SetTerminalHooks(&TerminalHooks{
		CreateTerminal: func(_ context.Context, _ agentbackend.SessionRef, command string, args []string, cwd string, env map[string]string) (agentbackend.TerminalHandle, error) {
			gotCommand, gotArgs, gotCwd, gotEnv = command, args, cwd, env
			return "term-1", nil
		},
		TerminalOutput: func(context.Context, agentbackend.SessionRef, agentbackend.TerminalHandle) (string, bool, *agentbackend.TerminalExitStatus, error) {
			code := 0
			return "hello\n", false, &agentbackend.TerminalExitStatus{ExitCode: &code}, nil
		},
		WaitForTerminalExit: func(context.Context, agentbackend.SessionRef, agentbackend.TerminalHandle) (agentbackend.TerminalExitStatus, error) {
			code := 0
			return agentbackend.TerminalExitStatus{ExitCode: &code}, nil
		},
		KillTerminal:    func(context.Context, agentbackend.SessionRef, agentbackend.TerminalHandle) error { return nil },
		ReleaseTerminal: func(context.Context, agentbackend.SessionRef, agentbackend.TerminalHandle) error { return nil },
	})

	handle, err := c.CreateTerminal(context.Background(), ref, "echo", []string{"hello"}, "/work", map[string]string{"FOO": "bar"})
	if err != nil {
		t.Fatalf("CreateTerminal: %v", err)
	}
	if handle != "term-1" {
		t.Errorf("handle = %q, want term-1", handle)
	}
	if gotCommand != "echo" || len(gotArgs) != 1 || gotArgs[0] != "hello" || gotCwd != "/work" || gotEnv["FOO"] != "bar" {
		t.Errorf("unexpected hook args: command=%q args=%v cwd=%q env=%v", gotCommand, gotArgs, gotCwd, gotEnv)
	}

	output, truncated, exit, err := c.TerminalOutput(context.Background(), ref, handle)
	if err != nil || output != "hello\n" || truncated || exit == nil || exit.ExitCode == nil || *exit.ExitCode != 0 {
		t.Fatalf("TerminalOutput = %q, %v, %+v, %v", output, truncated, exit, err)
	}
}

func TestBuildCallbacks_CreateTerminal_TranslatesRequestAndResponse(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	ref := agentbackend.SessionRef{ConversationID: "conv-1"}

	var gotCommand, gotCwd string
	c.SetTerminalHooks(&TerminalHooks{
		CreateTerminal: func(_ context.Context, _ agentbackend.SessionRef, command string, args []string, cwd string, env map[string]string) (agentbackend.TerminalHandle, error) {
			gotCommand, gotCwd = command, cwd
			return "term-42", nil
		},
	})

	cbs := c.buildCallbacks(ref)
	cwd := "/tmp/work"
	resp, err := cbs.OnCreateTerminal(context.Background(), acp.CreateTerminalRequest{Command: "ls", Cwd: &cwd})
	if err != nil {
		t.Fatalf("OnCreateTerminal: %v", err)
	}
	if resp.TerminalId != "term-42" {
		t.Errorf("TerminalId = %q, want term-42", resp.TerminalId)
	}
	if gotCommand != "ls" || gotCwd != "/tmp/work" {
		t.Errorf("unexpected translation: command=%q cwd=%q", gotCommand, gotCwd)
	}
}

func TestBuildCallbacks_TerminalOutput_TranslatesRequestAndResponse(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	ref := agentbackend.SessionRef{ConversationID: "conv-1"}

	var gotHandle agentbackend.TerminalHandle
	c.SetTerminalHooks(&TerminalHooks{
		TerminalOutput: func(_ context.Context, _ agentbackend.SessionRef, handle agentbackend.TerminalHandle) (string, bool, *agentbackend.TerminalExitStatus, error) {
			gotHandle = handle
			code := 7
			return "partial output", true, &agentbackend.TerminalExitStatus{ExitCode: &code}, nil
		},
	})

	cbs := c.buildCallbacks(ref)
	resp, err := cbs.OnTerminalOutput(context.Background(), acp.TerminalOutputRequest{TerminalId: "term-9"})
	if err != nil {
		t.Fatalf("OnTerminalOutput: %v", err)
	}
	if gotHandle != "term-9" {
		t.Errorf("handle passed to hook = %q, want term-9", gotHandle)
	}
	if resp.Output != "partial output" || !resp.Truncated {
		t.Errorf("resp = %+v, want Output=%q Truncated=true", resp, "partial output")
	}
	if resp.ExitStatus == nil || resp.ExitStatus.ExitCode == nil || *resp.ExitStatus.ExitCode != 7 {
		t.Errorf("resp.ExitStatus = %+v, want ExitCode=7", resp.ExitStatus)
	}
}

func TestBuildCallbacks_WaitForTerminalExit_TranslatesRequestAndResponse(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	ref := agentbackend.SessionRef{ConversationID: "conv-1"}

	var gotHandle agentbackend.TerminalHandle
	c.SetTerminalHooks(&TerminalHooks{
		WaitForTerminalExit: func(_ context.Context, _ agentbackend.SessionRef, handle agentbackend.TerminalHandle) (agentbackend.TerminalExitStatus, error) {
			gotHandle = handle
			signal := "SIGTERM"
			return agentbackend.TerminalExitStatus{Signal: &signal}, nil
		},
	})

	cbs := c.buildCallbacks(ref)
	resp, err := cbs.OnWaitForTerminalExit(context.Background(), acp.WaitForTerminalExitRequest{TerminalId: "term-9"})
	if err != nil {
		t.Fatalf("OnWaitForTerminalExit: %v", err)
	}
	if gotHandle != "term-9" {
		t.Errorf("handle passed to hook = %q, want term-9", gotHandle)
	}
	if resp.Signal == nil || *resp.Signal != "SIGTERM" {
		t.Errorf("resp.Signal = %v, want SIGTERM", resp.Signal)
	}
}

func TestBuildCallbacks_KillAndReleaseTerminal_Delegate(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	ref := agentbackend.SessionRef{ConversationID: "conv-1"}

	var killedHandle, releasedHandle agentbackend.TerminalHandle
	c.SetTerminalHooks(&TerminalHooks{
		KillTerminal: func(_ context.Context, _ agentbackend.SessionRef, handle agentbackend.TerminalHandle) error {
			killedHandle = handle
			return nil
		},
		ReleaseTerminal: func(_ context.Context, _ agentbackend.SessionRef, handle agentbackend.TerminalHandle) error {
			releasedHandle = handle
			return nil
		},
	})

	cbs := c.buildCallbacks(ref)
	if _, err := cbs.OnKillTerminal(context.Background(), acp.KillTerminalRequest{TerminalId: "term-1"}); err != nil {
		t.Fatalf("OnKillTerminal: %v", err)
	}
	if killedHandle != "term-1" {
		t.Errorf("killedHandle = %q, want term-1", killedHandle)
	}
	if _, err := cbs.OnReleaseTerminal(context.Background(), acp.ReleaseTerminalRequest{TerminalId: "term-2"}); err != nil {
		t.Fatalf("OnReleaseTerminal: %v", err)
	}
	if releasedHandle != "term-2" {
		t.Errorf("releasedHandle = %q, want term-2", releasedHandle)
	}
}

func TestBuildCallbacks_TerminalCallbacks_NilHooksPropagateUnsupported(t *testing.T) {
	// With no TerminalHooks installed, every ACP terminal/* callback must
	// surface *agentbackend.UnsupportedError through the wire-translation
	// layer too, not just the direct TerminalServices methods (covered by
	// TestTerminalServices_NilHooks_FailClosed) — this pins the whole path
	// an inbound ACP terminal/create (etc.) request actually takes.
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	ref := agentbackend.SessionRef{ConversationID: "conv-1"}
	cbs := c.buildCallbacks(ref)

	var unsupported *agentbackend.UnsupportedError
	if _, err := cbs.OnTerminalOutput(context.Background(), acp.TerminalOutputRequest{TerminalId: "t"}); !errors.As(err, &unsupported) {
		t.Errorf("OnTerminalOutput: expected *UnsupportedError, got %v", err)
	}
	if _, err := cbs.OnWaitForTerminalExit(context.Background(), acp.WaitForTerminalExitRequest{TerminalId: "t"}); !errors.As(err, &unsupported) {
		t.Errorf("OnWaitForTerminalExit: expected *UnsupportedError, got %v", err)
	}
	if _, err := cbs.OnKillTerminal(context.Background(), acp.KillTerminalRequest{TerminalId: "t"}); !errors.As(err, &unsupported) {
		t.Errorf("OnKillTerminal: expected *UnsupportedError, got %v", err)
	}
	if _, err := cbs.OnReleaseTerminal(context.Background(), acp.ReleaseTerminalRequest{TerminalId: "t"}); !errors.As(err, &unsupported) {
		t.Errorf("OnReleaseTerminal: expected *UnsupportedError, got %v", err)
	}
}

func TestConnection_Ownership_AlwaysLocal(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	ref := agentbackend.SessionRef{ConversationID: "conv-1"}
	if got := c.Ownership(ref); got != agentbackend.OwnershipLocal {
		t.Fatalf("Ownership() = %v, want OwnershipLocal", got)
	}
}
