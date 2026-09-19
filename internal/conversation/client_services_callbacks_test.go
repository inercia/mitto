package conversation

// Tests for buildNeutralSessionCallbacks and its translator functions
// (client_services_callbacks.go, mitto-mx9.4).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
)

func TestBuildNeutralSessionCallbacks_AllHooksPopulated(t *testing.T) {
	client := NewWebClient(WebClientConfig{})
	defer client.Close()
	cb := buildNeutralSessionCallbacks(client, agentbackend.SessionRef{})

	if cb.OnSessionUpdate == nil || cb.OnReadTextFile == nil || cb.OnWriteTextFile == nil ||
		cb.OnRequestPermission == nil || cb.OnCreateTerminal == nil || cb.OnTerminalOutput == nil ||
		cb.OnReleaseTerminal == nil || cb.OnWaitForTerminalExit == nil || cb.OnKillTerminal == nil {
		t.Fatalf("expected every SessionCallbacks hook populated, got %+v", cb)
	}
}

func TestSliceTextLinesLikeOSFS(t *testing.T) {
	content := "L1\nL2\nL3\nL4\nL5"
	intp := func(v int) *int { return &v }

	tests := []struct {
		name  string
		line  *int
		limit *int
		want  string
	}{
		{"nil_nil_returns_unchanged", nil, nil, content},
		{"line_only_from_2", intp(2), nil, "L2\nL3\nL4\nL5"},
		{"line_and_limit", intp(2), intp(2), "L2\nL3"},
		{"limit_zero_means_no_limit", intp(1), intp(0), content},
		{"line_beyond_end_clamped", intp(100), nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sliceTextLinesLikeOSFS(content, tt.line, tt.limit)
			if got != tt.want {
				t.Errorf("sliceTextLinesLikeOSFS() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOnReadTextFileNeutral_AppliesLineLimitAndFiresCallback(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(path, []byte("L1\nL2\nL3\nL4\nL5"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	var readPath string
	var readSize int
	client := NewWebClient(WebClientConfig{
		OnFileRead: func(seq int64, p string, size int) { readPath, readSize = p, size },
	})
	defer client.Close()
	svc := &webClientNeutralServices{client: client}

	line, limit := 2, 2
	resp, err := onReadTextFileNeutral(client, svc, agentbackend.SessionRef{})(context.Background(), acp.ReadTextFileRequest{
		Path: path, Line: &line, Limit: &limit,
	})
	if err != nil {
		t.Fatalf("onReadTextFileNeutral failed: %v", err)
	}
	if resp.Content != "L2\nL3" {
		t.Errorf("Content = %q, want %q", resp.Content, "L2\nL3")
	}
	if readPath != path || readSize != len(resp.Content) {
		t.Errorf("OnFileRead callback = (%q, %d), want (%q, %d)", readPath, readSize, path, len(resp.Content))
	}
}

func TestOnWriteTextFileNeutral_FiresCallback(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "out.txt")
	var writePath string
	var writeSize int
	client := NewWebClient(WebClientConfig{
		OnFileWrite: func(seq int64, p string, size int) { writePath, writeSize = p, size },
	})
	defer client.Close()
	svc := &webClientNeutralServices{client: client}

	_, err := onWriteTextFileNeutral(client, svc, agentbackend.SessionRef{})(context.Background(), acp.WriteTextFileRequest{
		Path: path, Content: "hello",
	})
	if err != nil {
		t.Fatalf("onWriteTextFileNeutral failed: %v", err)
	}
	if writePath != path || writeSize != len("hello") {
		t.Errorf("OnFileWrite callback = (%q, %d), want (%q, %d)", writePath, writeSize, path, len("hello"))
	}
}

// fakeClientServicesPermission is a minimal agentbackend.ClientServices whose
// RequestPermission return value/error is controlled per-test, independent
// of webClientNeutralServices's real (always-mirrors-autoApprove) behavior —
// used to exercise onRequestPermissionNeutral's branches directly.
type fakeClientServicesPermission struct {
	approved bool
	err      error
}

func (f *fakeClientServicesPermission) ReadFile(context.Context, agentbackend.SessionRef, string) ([]byte, error) {
	return nil, nil
}
func (f *fakeClientServicesPermission) WriteFile(context.Context, agentbackend.SessionRef, string, []byte) error {
	return nil
}
func (f *fakeClientServicesPermission) RequestPermission(context.Context, agentbackend.SessionRef, string) (bool, error) {
	return f.approved, f.err
}

var _ agentbackend.ClientServices = (*fakeClientServicesPermission)(nil)

func TestOnRequestPermissionNeutral_AutoApprove_ServiceApproves_PrefersAllow(t *testing.T) {
	client := NewWebClient(WebClientConfig{AutoApprove: true})
	defer client.Close()
	fn := onRequestPermissionNeutral(client, &fakeClientServicesPermission{approved: true}, agentbackend.SessionRef{})

	resp, err := fn(context.Background(), acp.RequestPermissionRequest{Options: []acp.PermissionOption{
		{OptionId: "deny", Kind: acp.PermissionOptionKindRejectOnce},
		{OptionId: "allow", Kind: acp.PermissionOptionKindAllowOnce},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "allow" {
		t.Fatalf("expected 'allow' selected, got %+v", resp.Outcome)
	}
}

func TestOnRequestPermissionNeutral_AutoApprove_ServiceDenies_Cancels(t *testing.T) {
	client := NewWebClient(WebClientConfig{AutoApprove: true})
	defer client.Close()
	fn := onRequestPermissionNeutral(client, &fakeClientServicesPermission{approved: false}, agentbackend.SessionRef{})

	resp, err := fn(context.Background(), acp.RequestPermissionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Outcome.Cancelled == nil {
		t.Fatalf("expected Cancelled outcome, got %+v", resp.Outcome)
	}
}

func TestOnRequestPermissionNeutral_AutoApprove_ServiceError_Propagates(t *testing.T) {
	client := NewWebClient(WebClientConfig{AutoApprove: true})
	defer client.Close()
	wantErr := errors.New("boom")
	fn := onRequestPermissionNeutral(client, &fakeClientServicesPermission{err: wantErr}, agentbackend.SessionRef{})

	_, err := fn(context.Background(), acp.RequestPermissionRequest{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected error %v, got %v", wantErr, err)
	}
}

func TestOnRequestPermissionNeutral_Interactive_UsesOnPermissionCallback(t *testing.T) {
	var gotParams acp.RequestPermissionRequest
	client := NewWebClient(WebClientConfig{
		AutoApprove: false,
		OnPermission: func(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
			gotParams = params
			return acp.RequestPermissionResponse{
				Outcome: acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{OptionId: "picked"}},
			}, nil
		},
	})
	defer client.Close()
	// svc is unreachable on this branch; nil-safe because RequestPermission
	// is never invoked when client.autoApprove is false.
	fn := onRequestPermissionNeutral(client, nil, agentbackend.SessionRef{})

	title := "Run rm -rf"
	resp, err := fn(context.Background(), acp.RequestPermissionRequest{
		ToolCall: acp.ToolCallUpdate{Title: &title},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "picked" {
		t.Fatalf("expected interactive handler's response passed through, got %+v", resp.Outcome)
	}
	if gotParams.ToolCall.Title == nil || *gotParams.ToolCall.Title != title {
		t.Fatalf("expected raw ACP request forwarded unchanged to the interactive handler, got %+v", gotParams.ToolCall)
	}
}

func TestOnRequestPermissionNeutral_NoHandler_Cancels(t *testing.T) {
	client := NewWebClient(WebClientConfig{AutoApprove: false})
	defer client.Close()
	fn := onRequestPermissionNeutral(client, nil, agentbackend.SessionRef{})

	resp, err := fn(context.Background(), acp.RequestPermissionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Outcome.Cancelled == nil {
		t.Fatalf("expected Cancelled outcome when no handler set, got %+v", resp.Outcome)
	}
}

func TestOnCreateTerminalNeutral_TranslatesParamsAndHandle(t *testing.T) {
	svc := &webClientNeutralServices{client: NewWebClient(WebClientConfig{})}
	fn := onCreateTerminalNeutral(svc, agentbackend.SessionRef{ProviderSession: "sess-1"})

	resp, err := fn(context.Background(), acp.CreateTerminalRequest{Command: "echo", Args: []string{"hi"}})
	if err != nil {
		t.Fatalf("onCreateTerminalNeutral failed: %v", err)
	}
	if resp.TerminalId != "term-1" {
		t.Errorf("TerminalId = %q, want %q", resp.TerminalId, "term-1")
	}
}

func TestToACPExitStatusNeutral(t *testing.T) {
	if got := toACPExitStatusNeutral(nil); got != nil {
		t.Errorf("toACPExitStatusNeutral(nil) = %+v, want nil", got)
	}
	code := 3
	got := toACPExitStatusNeutral(&agentbackend.TerminalExitStatus{ExitCode: &code})
	if got == nil || got.ExitCode == nil || *got.ExitCode != code {
		t.Errorf("toACPExitStatusNeutral mapping mismatch, got %+v", got)
	}
}
