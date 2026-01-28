package acp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestNewClient(t *testing.T) {
	var output string
	client := NewClient(true, func(msg string) {
		output = msg
	})

	if client == nil {
		t.Fatal("NewClient returned nil")
	}

	if !client.autoApprove {
		t.Error("autoApprove should be true")
	}

	// Test output function
	client.print("test %s", "message")
	if output != "test message" {
		t.Errorf("output = %q, want %q", output, "test message")
	}
}

func TestClient_Print_NilOutput(t *testing.T) {
	client := NewClient(true, nil)

	// Should not panic with nil output
	client.print("test message")
}

func TestClient_AutoApprovePermission_PreferAllow(t *testing.T) {
	client := NewClient(true, nil)

	resp, err := client.autoApprovePermission(acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{OptionId: "deny", Name: "Deny", Kind: acp.PermissionOptionKindRejectOnce},
			{OptionId: "allow-once", Name: "Allow Once", Kind: acp.PermissionOptionKindAllowOnce},
			{OptionId: "allow-always", Name: "Allow Always", Kind: acp.PermissionOptionKindAllowAlways},
		},
	})

	if err != nil {
		t.Fatalf("autoApprovePermission failed: %v", err)
	}

	if resp.Outcome.Selected == nil {
		t.Fatal("expected Selected outcome")
	}

	// Should prefer AllowOnce (first allow option)
	if resp.Outcome.Selected.OptionId != "allow-once" {
		t.Errorf("OptionId = %q, want %q", resp.Outcome.Selected.OptionId, "allow-once")
	}
}

func TestClient_AutoApprovePermission_FallbackToFirst(t *testing.T) {
	client := NewClient(true, nil)

	resp, err := client.autoApprovePermission(acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{OptionId: "first", Name: "First", Kind: acp.PermissionOptionKindRejectOnce},
			{OptionId: "second", Name: "Second", Kind: acp.PermissionOptionKindRejectOnce},
		},
	})

	if err != nil {
		t.Fatalf("autoApprovePermission failed: %v", err)
	}

	if resp.Outcome.Selected == nil {
		t.Fatal("expected Selected outcome")
	}

	if resp.Outcome.Selected.OptionId != "first" {
		t.Errorf("OptionId = %q, want %q", resp.Outcome.Selected.OptionId, "first")
	}
}

func TestClient_AutoApprovePermission_NoOptions(t *testing.T) {
	client := NewClient(true, nil)

	resp, err := client.autoApprovePermission(acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{},
	})

	if err != nil {
		t.Fatalf("autoApprovePermission failed: %v", err)
	}

	if resp.Outcome.Cancelled == nil {
		t.Error("expected Cancelled outcome when no options")
	}
}

func TestClient_RequestPermission_AutoApprove(t *testing.T) {
	client := NewClient(true, nil)

	resp, err := client.RequestPermission(context.Background(), acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{OptionId: "allow", Name: "Allow", Kind: acp.PermissionOptionKindAllowOnce},
		},
	})

	if err != nil {
		t.Fatalf("RequestPermission failed: %v", err)
	}

	if resp.Outcome.Selected == nil {
		t.Fatal("expected Selected outcome")
	}
}

func TestClient_SessionUpdate_AgentMessageChunk(t *testing.T) {
	var output string
	client := NewClient(false, func(msg string) {
		output += msg
	})

	err := client.SessionUpdate(context.Background(), acp.SessionNotification{
		Update: acp.SessionUpdate{
			AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
				Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: "Hello"}},
			},
		},
	})

	if err != nil {
		t.Fatalf("SessionUpdate failed: %v", err)
	}

	if output != "Hello" {
		t.Errorf("output = %q, want %q", output, "Hello")
	}
}

func TestClient_SessionUpdate_ToolCall(t *testing.T) {
	var output string
	client := NewClient(false, func(msg string) {
		output += msg
	})

	err := client.SessionUpdate(context.Background(), acp.SessionNotification{
		Update: acp.SessionUpdate{
			ToolCall: &acp.SessionUpdateToolCall{
				Title:  "Read file",
				Status: acp.ToolCallStatusInProgress,
			},
		},
	})

	if err != nil {
		t.Fatalf("SessionUpdate failed: %v", err)
	}

	if !strings.Contains(output, "Read file") {
		t.Errorf("output should contain tool title, got %q", output)
	}
}

func TestClient_SessionUpdate_ToolCallUpdate(t *testing.T) {
	var output string
	client := NewClient(false, func(msg string) {
		output += msg
	})

	status := acp.ToolCallStatusCompleted
	err := client.SessionUpdate(context.Background(), acp.SessionNotification{
		Update: acp.SessionUpdate{
			ToolCallUpdate: &acp.SessionToolCallUpdate{
				ToolCallId: "tool-123",
				Status:     &status,
			},
		},
	})

	if err != nil {
		t.Fatalf("SessionUpdate failed: %v", err)
	}

	if !strings.Contains(output, "completed") {
		t.Errorf("output should contain status, got %q", output)
	}
}

func TestClient_SessionUpdate_Plan(t *testing.T) {
	var output string
	client := NewClient(false, func(msg string) {
		output += msg
	})

	err := client.SessionUpdate(context.Background(), acp.SessionNotification{
		Update: acp.SessionUpdate{
			Plan: &acp.SessionUpdatePlan{},
		},
	})

	if err != nil {
		t.Fatalf("SessionUpdate failed: %v", err)
	}

	if !strings.Contains(output, "plan") {
		t.Errorf("output should contain 'plan', got %q", output)
	}
}

func TestClient_SessionUpdate_AgentThought(t *testing.T) {
	var output string
	client := NewClient(false, func(msg string) {
		output += msg
	})

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

	if !strings.Contains(output, "thinking...") {
		t.Errorf("output should contain thought, got %q", output)
	}
}

func TestClient_WriteTextFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	var output string
	client := NewClient(false, func(msg string) {
		output += msg
	})

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

	// Verify output message
	if !strings.Contains(output, "Wrote") {
		t.Errorf("output should contain 'Wrote', got %q", output)
	}
}

func TestClient_WriteTextFile_RelativePath(t *testing.T) {
	client := NewClient(false, nil)

	_, err := client.WriteTextFile(context.Background(), acp.WriteTextFileRequest{
		Path:    "relative/path.txt",
		Content: "test",
	})

	if err == nil {
		t.Error("expected error for relative path")
	}
}

func TestClient_WriteTextFile_CreateDir(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "subdir", "nested", "test.txt")

	client := NewClient(false, nil)

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

func TestClient_ReadTextFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")
	content := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	var output string
	client := NewClient(false, func(msg string) {
		output += msg
	})

	resp, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{
		Path: path,
	})

	if err != nil {
		t.Fatalf("ReadTextFile failed: %v", err)
	}

	if resp.Content != content {
		t.Errorf("Content = %q, want %q", resp.Content, content)
	}

	if !strings.Contains(output, "Read") {
		t.Errorf("output should contain 'Read', got %q", output)
	}
}

func TestClient_ReadTextFile_WithLineAndLimit(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")
	content := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	client := NewClient(false, nil)

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

func TestClient_ReadTextFile_RelativePath(t *testing.T) {
	client := NewClient(false, nil)

	_, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{
		Path: "relative/path.txt",
	})

	if err == nil {
		t.Error("expected error for relative path")
	}
}

func TestClient_ReadTextFile_NotFound(t *testing.T) {
	client := NewClient(false, nil)

	_, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{
		Path: "/nonexistent/file.txt",
	})

	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestClient_ReadTextFile_LineOnly(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")
	content := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	client := NewClient(false, nil)

	line := 3
	resp, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{
		Path: path,
		Line: &line,
	})

	if err != nil {
		t.Fatalf("ReadTextFile failed: %v", err)
	}

	// Should return from line 3 to end
	expected := "Line 3\nLine 4\nLine 5"
	if resp.Content != expected {
		t.Errorf("Content = %q, want %q", resp.Content, expected)
	}
}

func TestClient_ReadTextFile_LimitOnly(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")
	content := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	client := NewClient(false, nil)

	limit := 2
	resp, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{
		Path:  path,
		Limit: &limit,
	})

	if err != nil {
		t.Fatalf("ReadTextFile failed: %v", err)
	}

	// Should return first 2 lines
	expected := "Line 1\nLine 2"
	if resp.Content != expected {
		t.Errorf("Content = %q, want %q", resp.Content, expected)
	}
}
