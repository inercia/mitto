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

func TestConnection_Ownership_AlwaysLocal(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	ref := agentbackend.SessionRef{ConversationID: "conv-1"}
	if got := c.Ownership(ref); got != agentbackend.OwnershipLocal {
		t.Fatalf("Ownership() = %v, want OwnershipLocal", got)
	}
}
