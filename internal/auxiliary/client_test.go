package auxiliary

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestAuxiliaryClient_NewClient(t *testing.T) {
	client := newAuxiliaryClient()
	if client == nil {
		t.Fatal("newAuxiliaryClient returned nil")
	}
}

func TestAuxiliaryClient_ResetAndGetResponse(t *testing.T) {
	client := newAuxiliaryClient()

	// Initially empty
	if got := client.getResponse(); got != "" {
		t.Errorf("Initial response should be empty, got %q", got)
	}

	// Write some text
	client.response.WriteString("Hello ")
	client.response.WriteString("World")

	// Get response
	if got := client.getResponse(); got != "Hello World" {
		t.Errorf("getResponse = %q, want %q", got, "Hello World")
	}

	// Reset
	client.reset()

	// Should be empty again
	if got := client.getResponse(); got != "" {
		t.Errorf("After reset, response should be empty, got %q", got)
	}
}

func TestAuxiliaryClient_SessionUpdate_AgentMessageChunk(t *testing.T) {
	client := newAuxiliaryClient()
	ctx := context.Background()

	// Send agent message chunk
	err := client.SessionUpdate(ctx, acp.SessionNotification{
		Update: acp.SessionUpdate{
			AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
				Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: "Hello"}},
			},
		},
	})

	if err != nil {
		t.Fatalf("SessionUpdate failed: %v", err)
	}

	if got := client.getResponse(); got != "Hello" {
		t.Errorf("getResponse = %q, want %q", got, "Hello")
	}
}

func TestAuxiliaryClient_SessionUpdate_MultipleChunks(t *testing.T) {
	client := newAuxiliaryClient()
	ctx := context.Background()

	chunks := []string{"Hello ", "World", "!"}
	for _, chunk := range chunks {
		err := client.SessionUpdate(ctx, acp.SessionNotification{
			Update: acp.SessionUpdate{
				AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
					Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: chunk}},
				},
			},
		})
		if err != nil {
			t.Fatalf("SessionUpdate failed: %v", err)
		}
	}

	if got := client.getResponse(); got != "Hello World!" {
		t.Errorf("getResponse = %q, want %q", got, "Hello World!")
	}
}

func TestAuxiliaryClient_SessionUpdate_IgnoresOtherUpdates(t *testing.T) {
	client := newAuxiliaryClient()
	ctx := context.Background()

	// Tool call should be ignored
	err := client.SessionUpdate(ctx, acp.SessionNotification{
		Update: acp.SessionUpdate{
			ToolCall: &acp.SessionUpdateToolCall{
				Title:  "Some tool",
				Status: acp.ToolCallStatusInProgress,
			},
		},
	})
	if err != nil {
		t.Fatalf("SessionUpdate failed: %v", err)
	}

	// Agent thought should be ignored
	err = client.SessionUpdate(ctx, acp.SessionNotification{
		Update: acp.SessionUpdate{
			AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
				Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: "thinking..."}},
			},
		},
	})
	if err != nil {
		t.Fatalf("SessionUpdate failed: %v", err)
	}

	// Response should still be empty
	if got := client.getResponse(); got != "" {
		t.Errorf("Response should be empty after ignored updates, got %q", got)
	}
}

func TestAuxiliaryClient_RequestPermission_AutoApproves(t *testing.T) {
	client := newAuxiliaryClient()
	ctx := context.Background()

	resp, err := client.RequestPermission(ctx, acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{OptionId: "deny", Name: "Deny", Kind: acp.PermissionOptionKindRejectOnce},
			{OptionId: "allow", Name: "Allow", Kind: acp.PermissionOptionKindAllowOnce},
		},
	})

	if err != nil {
		t.Fatalf("RequestPermission failed: %v", err)
	}

	if resp.Outcome.Selected == nil {
		t.Fatal("Expected Selected outcome")
	}

	// Should auto-approve with allow option
	if resp.Outcome.Selected.OptionId != "allow" {
		t.Errorf("OptionId = %q, want %q", resp.Outcome.Selected.OptionId, "allow")
	}
}

func TestAuxiliaryClient_WriteTextFile_Denied(t *testing.T) {
	client := newAuxiliaryClient()
	ctx := context.Background()

	// WriteTextFile should return empty response (effectively denying)
	resp, err := client.WriteTextFile(ctx, acp.WriteTextFileRequest{
		Path:    "/tmp/test.txt",
		Content: "test content",
	})

	if err != nil {
		t.Fatalf("WriteTextFile failed: %v", err)
	}

	// Response should be empty (no bytes written)
	_ = resp // Just checking it doesn't error
}

func TestAuxiliaryClient_ReadTextFile(t *testing.T) {
	client := newAuxiliaryClient()
	ctx := context.Background()

	// Create a temp file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	content := "Hello, World!"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	resp, err := client.ReadTextFile(ctx, acp.ReadTextFileRequest{
		Path: testFile,
	})

	if err != nil {
		t.Fatalf("ReadTextFile failed: %v", err)
	}

	if resp.Content != content {
		t.Errorf("Content = %q, want %q", resp.Content, content)
	}
}

func TestAuxiliaryClient_ReadTextFile_NotFound(t *testing.T) {
	client := newAuxiliaryClient()
	ctx := context.Background()

	_, err := client.ReadTextFile(ctx, acp.ReadTextFileRequest{
		Path: "/nonexistent/file.txt",
	})

	if err == nil {
		t.Error("ReadTextFile should fail for non-existent file")
	}
}

func TestAuxiliaryClient_TerminalMethods(t *testing.T) {
	client := newAuxiliaryClient()
	ctx := context.Background()

	// CreateTerminal
	createResp, err := client.CreateTerminal(ctx, acp.CreateTerminalRequest{})
	if err != nil {
		t.Fatalf("CreateTerminal failed: %v", err)
	}
	if createResp.TerminalId == "" {
		t.Error("CreateTerminal should return a terminal ID")
	}

	// TerminalOutput
	outputResp, err := client.TerminalOutput(ctx, acp.TerminalOutputRequest{})
	if err != nil {
		t.Fatalf("TerminalOutput failed: %v", err)
	}
	if outputResp.Truncated {
		t.Error("TerminalOutput should not be truncated")
	}

	// ReleaseTerminal
	_, err = client.ReleaseTerminal(ctx, acp.ReleaseTerminalRequest{})
	if err != nil {
		t.Fatalf("ReleaseTerminal failed: %v", err)
	}

	// WaitForTerminalExit
	_, err = client.WaitForTerminalExit(ctx, acp.WaitForTerminalExitRequest{})
	if err != nil {
		t.Fatalf("WaitForTerminalExit failed: %v", err)
	}

	// KillTerminalCommand
	_, err = client.KillTerminalCommand(ctx, acp.KillTerminalCommandRequest{})
	if err != nil {
		t.Fatalf("KillTerminalCommand failed: %v", err)
	}
}
