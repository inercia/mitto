package web

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestNewWebClient(t *testing.T) {
	client := NewWebClient(WebClientConfig{
		AutoApprove: true,
		OnAgentMessage: func(html string) {
			// callback for testing
		},
	})

	if client == nil {
		t.Fatal("NewWebClient returned nil")
	}

	if !client.autoApprove {
		t.Error("autoApprove should be true")
	}

	// Verify markdown buffer is initialized
	if client.mdBuffer == nil {
		t.Error("mdBuffer should be initialized")
	}

	client.Close()
}

func TestWebClient_SessionUpdate_AgentMessageChunk(t *testing.T) {
	var messages []string
	var mu sync.Mutex

	client := NewWebClient(WebClientConfig{
		OnAgentMessage: func(html string) {
			mu.Lock()
			messages = append(messages, html)
			mu.Unlock()
		},
	})
	defer client.Close()

	text := "Hello from agent"
	err := client.SessionUpdate(context.Background(), acp.SessionNotification{
		Update: acp.SessionUpdate{
			AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
				Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: text}},
			},
		},
	})

	if err != nil {
		t.Fatalf("SessionUpdate failed: %v", err)
	}

	// Flush to get the message
	client.FlushMarkdown()

	mu.Lock()
	defer mu.Unlock()

	if len(messages) == 0 {
		t.Error("expected at least one message")
	}
}

func TestWebClient_SessionUpdate_AgentThought(t *testing.T) {
	var thought string

	client := NewWebClient(WebClientConfig{
		OnAgentThought: func(text string) {
			thought = text
		},
	})
	defer client.Close()

	err := client.SessionUpdate(context.Background(), acp.SessionNotification{
		Update: acp.SessionUpdate{
			AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
				Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: "thinking..."}},
			},
		},
	})

	if err != nil {
		t.Fatalf("SessionUpdate failed: %v", err)
	}

	if thought != "thinking..." {
		t.Errorf("thought = %q, want %q", thought, "thinking...")
	}
}

func TestWebClient_SessionUpdate_ToolCall(t *testing.T) {
	var toolID, toolTitle, toolStatus string

	client := NewWebClient(WebClientConfig{
		OnToolCall: func(id, title, status string) {
			toolID = id
			toolTitle = title
			toolStatus = status
		},
	})
	defer client.Close()

	err := client.SessionUpdate(context.Background(), acp.SessionNotification{
		Update: acp.SessionUpdate{
			ToolCall: &acp.SessionUpdateToolCall{
				ToolCallId: "tool-123",
				Title:      "Read file",
				Status:     acp.ToolCallStatusInProgress,
			},
		},
	})

	if err != nil {
		t.Fatalf("SessionUpdate failed: %v", err)
	}

	if toolID != "tool-123" {
		t.Errorf("toolID = %q, want %q", toolID, "tool-123")
	}
	if toolTitle != "Read file" {
		t.Errorf("toolTitle = %q, want %q", toolTitle, "Read file")
	}
	if toolStatus != string(acp.ToolCallStatusInProgress) {
		t.Errorf("toolStatus = %q, want %q", toolStatus, string(acp.ToolCallStatusInProgress))
	}
}

func TestWebClient_SessionUpdate_ToolUpdate(t *testing.T) {
	var updateID string
	var updateStatus *string

	client := NewWebClient(WebClientConfig{
		OnToolUpdate: func(id string, status *string) {
			updateID = id
			updateStatus = status
		},
	})
	defer client.Close()

	status := acp.ToolCallStatusCompleted
	err := client.SessionUpdate(context.Background(), acp.SessionNotification{
		Update: acp.SessionUpdate{
			ToolCallUpdate: &acp.SessionToolCallUpdate{
				ToolCallId: "tool-456",
				Status:     &status,
			},
		},
	})

	if err != nil {
		t.Fatalf("SessionUpdate failed: %v", err)
	}

	if updateID != "tool-456" {
		t.Errorf("updateID = %q, want %q", updateID, "tool-456")
	}
	if updateStatus == nil || *updateStatus != string(acp.ToolCallStatusCompleted) {
		t.Errorf("updateStatus = %v, want %q", updateStatus, string(acp.ToolCallStatusCompleted))
	}
}

func TestWebClient_SessionUpdate_Plan(t *testing.T) {
	planCalled := false

	client := NewWebClient(WebClientConfig{
		OnPlan: func() {
			planCalled = true
		},
	})
	defer client.Close()

	err := client.SessionUpdate(context.Background(), acp.SessionNotification{
		Update: acp.SessionUpdate{
			Plan: &acp.SessionUpdatePlan{},
		},
	})

	if err != nil {
		t.Fatalf("SessionUpdate failed: %v", err)
	}

	if !planCalled {
		t.Error("OnPlan callback was not called")
	}
}

func TestWebClient_RequestPermission_AutoApprove(t *testing.T) {
	client := NewWebClient(WebClientConfig{
		AutoApprove: true,
	})
	defer client.Close()

	resp, err := client.RequestPermission(context.Background(), acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{OptionId: "deny", Name: "Deny", Kind: acp.PermissionOptionKindRejectOnce},
			{OptionId: "allow", Name: "Allow", Kind: acp.PermissionOptionKindAllowOnce},
		},
	})

	if err != nil {
		t.Fatalf("RequestPermission failed: %v", err)
	}

	// Should prefer allow option
	if resp.Outcome.Selected == nil {
		t.Fatal("expected Selected outcome")
	}
	if resp.Outcome.Selected.OptionId != "allow" {
		t.Errorf("OptionId = %q, want %q", resp.Outcome.Selected.OptionId, "allow")
	}
}

func TestWebClient_RequestPermission_AutoApprove_NoAllowOption(t *testing.T) {
	client := NewWebClient(WebClientConfig{
		AutoApprove: true,
	})
	defer client.Close()

	resp, err := client.RequestPermission(context.Background(), acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{OptionId: "first", Name: "First", Kind: acp.PermissionOptionKindRejectOnce},
			{OptionId: "second", Name: "Second", Kind: acp.PermissionOptionKindRejectOnce},
		},
	})

	if err != nil {
		t.Fatalf("RequestPermission failed: %v", err)
	}

	// Should fall back to first option
	if resp.Outcome.Selected == nil {
		t.Fatal("expected Selected outcome")
	}
	if resp.Outcome.Selected.OptionId != "first" {
		t.Errorf("OptionId = %q, want %q", resp.Outcome.Selected.OptionId, "first")
	}
}

func TestWebClient_RequestPermission_NoHandler(t *testing.T) {
	client := NewWebClient(WebClientConfig{
		AutoApprove: false,
		// No OnPermission handler
	})
	defer client.Close()

	resp, err := client.RequestPermission(context.Background(), acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{OptionId: "allow", Name: "Allow", Kind: acp.PermissionOptionKindAllowOnce},
		},
	})

	if err != nil {
		t.Fatalf("RequestPermission failed: %v", err)
	}

	// Should cancel when no handler
	if resp.Outcome.Cancelled == nil {
		t.Error("expected Cancelled outcome when no handler")
	}
}

func TestWebClient_WriteTextFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	var writePath string
	var writeSize int

	client := NewWebClient(WebClientConfig{
		OnFileWrite: func(p string, size int) {
			writePath = p
			writeSize = size
		},
	})
	defer client.Close()

	content := "Hello, World!"
	_, err := client.WriteTextFile(context.Background(), acp.WriteTextFileRequest{
		Path:    path,
		Content: content,
	})

	if err != nil {
		t.Fatalf("WriteTextFile failed: %v", err)
	}

	// Verify file was written
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}
	if string(data) != content {
		t.Errorf("file content = %q, want %q", string(data), content)
	}

	// Verify callback was called
	if writePath != path {
		t.Errorf("writePath = %q, want %q", writePath, path)
	}
	if writeSize != len(content) {
		t.Errorf("writeSize = %d, want %d", writeSize, len(content))
	}
}

func TestWebClient_WriteTextFile_RelativePath(t *testing.T) {
	client := NewWebClient(WebClientConfig{})
	defer client.Close()

	_, err := client.WriteTextFile(context.Background(), acp.WriteTextFileRequest{
		Path:    "relative/path.txt",
		Content: "test",
	})

	if err == nil {
		t.Error("expected error for relative path")
	}
}

func TestWebClient_WriteTextFile_CreateDir(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "subdir", "nested", "test.txt")

	client := NewWebClient(WebClientConfig{})
	defer client.Close()

	_, err := client.WriteTextFile(context.Background(), acp.WriteTextFileRequest{
		Path:    path,
		Content: "nested content",
	})

	if err != nil {
		t.Fatalf("WriteTextFile failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("file was not created")
	}
}

func TestWebClient_ReadTextFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")
	content := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	var readPath string
	var readSize int

	client := NewWebClient(WebClientConfig{
		OnFileRead: func(p string, size int) {
			readPath = p
			readSize = size
		},
	})
	defer client.Close()

	resp, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{
		Path: path,
	})

	if err != nil {
		t.Fatalf("ReadTextFile failed: %v", err)
	}

	if resp.Content != content {
		t.Errorf("Content = %q, want %q", resp.Content, content)
	}

	if readPath != path {
		t.Errorf("readPath = %q, want %q", readPath, path)
	}
	if readSize != len(content) {
		t.Errorf("readSize = %d, want %d", readSize, len(content))
	}
}

func TestWebClient_ReadTextFile_WithLineAndLimit(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")
	content := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	client := NewWebClient(WebClientConfig{})
	defer client.Close()

	line := 2
	limit := 2
	resp, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{
		Path:  path,
		Line:  &line,
		Limit: &limit,
	})

	if err != nil {
		t.Fatalf("ReadTextFile failed: %v", err)
	}

	expected := "Line 2\nLine 3"
	if resp.Content != expected {
		t.Errorf("Content = %q, want %q", resp.Content, expected)
	}
}

func TestWebClient_ReadTextFile_RelativePath(t *testing.T) {
	client := NewWebClient(WebClientConfig{})
	defer client.Close()

	_, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{
		Path: "relative/path.txt",
	})

	if err == nil {
		t.Error("expected error for relative path")
	}
}

func TestWebClient_ReadTextFile_NotFound(t *testing.T) {
	client := NewWebClient(WebClientConfig{})
	defer client.Close()

	_, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{
		Path: "/nonexistent/file.txt",
	})

	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestWebClient_FlushMarkdown(t *testing.T) {
	var messages []string
	var mu sync.Mutex

	client := NewWebClient(WebClientConfig{
		OnAgentMessage: func(html string) {
			mu.Lock()
			messages = append(messages, html)
			mu.Unlock()
		},
	})
	defer client.Close()

	// Write some content
	client.mdBuffer.Write("test content")

	// Flush should trigger callback
	client.FlushMarkdown()

	mu.Lock()
	defer mu.Unlock()

	found := false
	for _, msg := range messages {
		if strings.Contains(msg, "test content") {
			found = true
			break
		}
	}

	if !found {
		t.Error("FlushMarkdown did not flush buffered content")
	}
}

func TestWebClient_TerminalMethods(t *testing.T) {
	client := NewWebClient(WebClientConfig{})
	defer client.Close()

	ctx := context.Background()

	// CreateTerminal
	createResp, err := client.CreateTerminal(ctx, acp.CreateTerminalRequest{})
	if err != nil {
		t.Errorf("CreateTerminal failed: %v", err)
	}
	if createResp.TerminalId != "term-1" {
		t.Errorf("TerminalId = %q, want %q", createResp.TerminalId, "term-1")
	}

	// TerminalOutput
	outputResp, err := client.TerminalOutput(ctx, acp.TerminalOutputRequest{})
	if err != nil {
		t.Errorf("TerminalOutput failed: %v", err)
	}
	if outputResp.Output != "" {
		t.Errorf("Output = %q, want empty", outputResp.Output)
	}

	// ReleaseTerminal
	_, err = client.ReleaseTerminal(ctx, acp.ReleaseTerminalRequest{})
	if err != nil {
		t.Errorf("ReleaseTerminal failed: %v", err)
	}

	// WaitForTerminalExit
	_, err = client.WaitForTerminalExit(ctx, acp.WaitForTerminalExitRequest{})
	if err != nil {
		t.Errorf("WaitForTerminalExit failed: %v", err)
	}

	// KillTerminalCommand
	_, err = client.KillTerminalCommand(ctx, acp.KillTerminalCommandRequest{})
	if err != nil {
		t.Errorf("KillTerminalCommand failed: %v", err)
	}
}
