package acp

import (
	"context"
	"strings"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestClient_CreateTerminal(t *testing.T) {
	var output strings.Builder
	client := NewClient(false, func(msg string) {
		output.WriteString(msg)
	})

	ctx := context.Background()
	resp, err := client.CreateTerminal(ctx, acp.CreateTerminalRequest{
		Command: "echo hello",
	})

	if err != nil {
		t.Fatalf("CreateTerminal failed: %v", err)
	}

	// Should return a terminal ID
	if resp.TerminalId == "" {
		t.Error("TerminalId should not be empty")
	}

	// Should log the operation
	if !strings.Contains(output.String(), "CreateTerminal") {
		t.Errorf("output should contain 'CreateTerminal', got %q", output.String())
	}
}

func TestClient_TerminalOutput(t *testing.T) {
	var output strings.Builder
	client := NewClient(false, func(msg string) {
		output.WriteString(msg)
	})

	ctx := context.Background()
	resp, err := client.TerminalOutput(ctx, acp.TerminalOutputRequest{
		TerminalId: "term-1",
	})

	if err != nil {
		t.Fatalf("TerminalOutput failed: %v", err)
	}

	// Stub returns empty output
	if resp.Output != "" {
		t.Errorf("Output = %q, want empty", resp.Output)
	}

	// Should log the operation
	if !strings.Contains(output.String(), "TerminalOutput") {
		t.Errorf("output should contain 'TerminalOutput', got %q", output.String())
	}
}

func TestClient_ReleaseTerminal(t *testing.T) {
	var output strings.Builder
	client := NewClient(false, func(msg string) {
		output.WriteString(msg)
	})

	ctx := context.Background()
	_, err := client.ReleaseTerminal(ctx, acp.ReleaseTerminalRequest{
		TerminalId: "term-1",
	})

	if err != nil {
		t.Fatalf("ReleaseTerminal failed: %v", err)
	}

	// Should log the operation
	if !strings.Contains(output.String(), "ReleaseTerminal") {
		t.Errorf("output should contain 'ReleaseTerminal', got %q", output.String())
	}
}

func TestClient_WaitForTerminalExit(t *testing.T) {
	var output strings.Builder
	client := NewClient(false, func(msg string) {
		output.WriteString(msg)
	})

	ctx := context.Background()
	_, err := client.WaitForTerminalExit(ctx, acp.WaitForTerminalExitRequest{
		TerminalId: "term-1",
	})

	if err != nil {
		t.Fatalf("WaitForTerminalExit failed: %v", err)
	}

	// Should log the operation
	if !strings.Contains(output.String(), "WaitForTerminalExit") {
		t.Errorf("output should contain 'WaitForTerminalExit', got %q", output.String())
	}
}

func TestClient_KillTerminalCommand(t *testing.T) {
	var output strings.Builder
	client := NewClient(false, func(msg string) {
		output.WriteString(msg)
	})

	ctx := context.Background()
	_, err := client.KillTerminalCommand(ctx, acp.KillTerminalCommandRequest{
		TerminalId: "term-1",
	})

	if err != nil {
		t.Fatalf("KillTerminalCommand failed: %v", err)
	}

	// Should log the operation
	if !strings.Contains(output.String(), "KillTerminalCommand") {
		t.Errorf("output should contain 'KillTerminalCommand', got %q", output.String())
	}
}
