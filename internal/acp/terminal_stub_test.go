package acp

import (
	"context"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestStubTerminalHandler_CreateTerminal(t *testing.T) {
	handler := &StubTerminalHandler{}
	ctx := context.Background()

	resp, err := handler.CreateTerminal(ctx, acp.CreateTerminalRequest{})
	if err != nil {
		t.Fatalf("CreateTerminal failed: %v", err)
	}

	// Default terminal ID should be "term-1"
	if resp.TerminalId != "term-1" {
		t.Errorf("TerminalId = %q, want %q", resp.TerminalId, "term-1")
	}
}

func TestStubTerminalHandler_CreateTerminal_CustomID(t *testing.T) {
	handler := &StubTerminalHandler{TerminalID: "custom-terminal"}
	ctx := context.Background()

	resp, err := handler.CreateTerminal(ctx, acp.CreateTerminalRequest{})
	if err != nil {
		t.Fatalf("CreateTerminal failed: %v", err)
	}

	if resp.TerminalId != "custom-terminal" {
		t.Errorf("TerminalId = %q, want %q", resp.TerminalId, "custom-terminal")
	}
}

func TestStubTerminalHandler_TerminalOutput(t *testing.T) {
	handler := &StubTerminalHandler{}
	ctx := context.Background()

	resp, err := handler.TerminalOutput(ctx, acp.TerminalOutputRequest{
		TerminalId: "term-1",
	})
	if err != nil {
		t.Fatalf("TerminalOutput failed: %v", err)
	}

	if resp.Output != "" {
		t.Errorf("Output = %q, want empty string", resp.Output)
	}
	if resp.Truncated {
		t.Error("Truncated should be false")
	}
}

func TestStubTerminalHandler_ReleaseTerminal(t *testing.T) {
	handler := &StubTerminalHandler{}
	ctx := context.Background()

	_, err := handler.ReleaseTerminal(ctx, acp.ReleaseTerminalRequest{
		TerminalId: "term-1",
	})
	if err != nil {
		t.Fatalf("ReleaseTerminal failed: %v", err)
	}
}

func TestStubTerminalHandler_WaitForTerminalExit(t *testing.T) {
	handler := &StubTerminalHandler{}
	ctx := context.Background()

	_, err := handler.WaitForTerminalExit(ctx, acp.WaitForTerminalExitRequest{
		TerminalId: "term-1",
	})
	if err != nil {
		t.Fatalf("WaitForTerminalExit failed: %v", err)
	}
}

func TestStubTerminalHandler_KillTerminalCommand(t *testing.T) {
	handler := &StubTerminalHandler{}
	ctx := context.Background()

	_, err := handler.KillTerminalCommand(ctx, acp.KillTerminalCommandRequest{
		TerminalId: "term-1",
	})
	if err != nil {
		t.Fatalf("KillTerminalCommand failed: %v", err)
	}
}

func TestStubTerminalHandler_ImplementsInterface(t *testing.T) {
	// Compile-time check that StubTerminalHandler implements TerminalHandler
	var _ TerminalHandler = (*StubTerminalHandler)(nil)
}
