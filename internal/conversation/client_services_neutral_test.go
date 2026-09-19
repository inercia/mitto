package conversation

// Tests for webClientNeutralServices (client_services_neutral.go, mitto-mx9.4):
// the in-package adapter that implements agentbackend.ClientServices /
// agentbackend.TerminalServices on top of a *WebClient's existing fs/terminal
// primitives. Mirrors the equivalent direct-path assertions in client_test.go
// (TestWebClient_ReadTextFile, TestWebClient_WriteTextFile,
// TestWebClient_TerminalMethods) so behavior parity with the pre-mitto-mx9.4
// path is pinned for the neutral seam too.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
)

func TestWebClientNeutralServices_ReadFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")
	content := "Line 1\nLine 2\nLine 3"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	svc := &webClientNeutralServices{client: NewWebClient(WebClientConfig{})}
	data, err := svc.ReadFile(context.Background(), agentbackend.SessionRef{}, path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != content {
		t.Errorf("ReadFile = %q, want %q (full file, no Line/Limit slicing at this layer)", string(data), content)
	}
}

func TestWebClientNeutralServices_ReadFile_NotFound(t *testing.T) {
	svc := &webClientNeutralServices{client: NewWebClient(WebClientConfig{})}
	if _, err := svc.ReadFile(context.Background(), agentbackend.SessionRef{}, "/nonexistent/file.txt"); err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestWebClientNeutralServices_WriteFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "out.txt")
	svc := &webClientNeutralServices{client: NewWebClient(WebClientConfig{})}

	if err := svc.WriteFile(context.Background(), agentbackend.SessionRef{}, path, []byte("hello")); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("written content = %q, want %q", string(data), "hello")
	}
}

func TestWebClientNeutralServices_WriteFile_RelativePath(t *testing.T) {
	svc := &webClientNeutralServices{client: NewWebClient(WebClientConfig{})}
	if err := svc.WriteFile(context.Background(), agentbackend.SessionRef{}, "relative/path.txt", []byte("x")); err == nil {
		t.Error("expected error for relative path")
	}
}

func TestWebClientNeutralServices_RequestPermission_ReflectsAutoApprove(t *testing.T) {
	for _, autoApprove := range []bool{true, false} {
		client := NewWebClient(WebClientConfig{AutoApprove: autoApprove})
		svc := &webClientNeutralServices{client: client}

		approved, err := svc.RequestPermission(context.Background(), agentbackend.SessionRef{}, "do something")
		if err != nil {
			t.Fatalf("RequestPermission failed: %v", err)
		}
		if approved != autoApprove {
			t.Errorf("RequestPermission returned %v, want %v (client.autoApprove)", approved, autoApprove)
		}
	}
}

func TestWebClientNeutralServices_TerminalMethods(t *testing.T) {
	svc := &webClientNeutralServices{client: NewWebClient(WebClientConfig{})}
	ctx := context.Background()

	handle, err := svc.CreateTerminal(ctx, agentbackend.SessionRef{ProviderSession: "sess-1"}, "echo", []string{"hi"}, "", map[string]string{"K": "V"})
	if err != nil {
		t.Fatalf("CreateTerminal failed: %v", err)
	}
	if handle != "term-1" {
		t.Errorf("CreateTerminal handle = %q, want %q (WebTerminalStub)", handle, "term-1")
	}

	output, truncated, exit, err := svc.TerminalOutput(ctx, agentbackend.SessionRef{}, handle)
	if err != nil {
		t.Fatalf("TerminalOutput failed: %v", err)
	}
	if output != "" || truncated || exit != nil {
		t.Errorf("TerminalOutput = (%q, %v, %v), want (\"\", false, nil)", output, truncated, exit)
	}

	if _, err := svc.WaitForTerminalExit(ctx, agentbackend.SessionRef{}, handle); err != nil {
		t.Errorf("WaitForTerminalExit failed: %v", err)
	}
	if err := svc.KillTerminal(ctx, agentbackend.SessionRef{}, handle); err != nil {
		t.Errorf("KillTerminal failed: %v", err)
	}
	if err := svc.ReleaseTerminal(ctx, agentbackend.SessionRef{}, handle); err != nil {
		t.Errorf("ReleaseTerminal failed: %v", err)
	}
}

func TestFromACPExitStatus(t *testing.T) {
	if got := fromACPExitStatus(nil); got != nil {
		t.Errorf("fromACPExitStatus(nil) = %+v, want nil (still-running is preserved)", got)
	}

	code := 7
	sig := "KILL"
	got := fromACPExitStatus(&acp.TerminalExitStatus{ExitCode: &code, Signal: &sig})
	if got == nil || got.ExitCode == nil || *got.ExitCode != code || got.Signal == nil || *got.Signal != sig {
		t.Errorf("fromACPExitStatus mapping mismatch, got %+v", got)
	}
}
