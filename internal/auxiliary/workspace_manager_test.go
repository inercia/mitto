package auxiliary

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// mockProcessProvider is a mock implementation of ProcessProvider for testing.
type mockProcessProvider struct {
	promptFunc      func(ctx context.Context, workspaceUUID, purpose, message string) (string, error)
	promptAsyncFunc func(ctx context.Context, workspaceUUID, purpose, message string) error
	closeFunc       func(workspaceUUID string) error
}

func (m *mockProcessProvider) PromptAuxiliary(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
	if m.promptFunc != nil {
		return m.promptFunc(ctx, workspaceUUID, purpose, message)
	}
	return "", errors.New("not implemented")
}

func (m *mockProcessProvider) PromptAuxiliaryAsync(ctx context.Context, workspaceUUID, purpose, message string) error {
	if m.promptAsyncFunc != nil {
		return m.promptAsyncFunc(ctx, workspaceUUID, purpose, message)
	}
	return nil
}

func (m *mockProcessProvider) CloseWorkspaceAuxiliary(workspaceUUID string) error {
	if m.closeFunc != nil {
		return m.closeFunc(workspaceUUID)
	}
	return nil
}

func TestWorkspaceAuxiliaryManager_GenerateTitle(t *testing.T) {
	tests := []struct {
		name           string
		message        string
		mockResponse   string
		mockError      error
		wantContains   string
		wantErr        bool
		checkPurpose   string
		checkWorkspace string
	}{
		{
			name:           "successful title generation",
			message:        "How do I implement authentication?",
			mockResponse:   `"Authentication Guide"`,
			wantContains:   "Authentication Guide",
			checkPurpose:   PurposeTitleGen,
			checkWorkspace: "test-workspace",
		},
		{
			name:         "title with quotes removed",
			message:      "Test message",
			mockResponse: `'Test Title'`,
			wantContains: "Test Title",
		},
		{
			name:         "title truncated if too long",
			message:      "Test",
			mockResponse: `"This is a very long title that exceeds the maximum length allowed for titles"`,
			wantContains: "...",
		},
		{
			name:      "error from provider",
			message:   "Test",
			mockError: errors.New("provider error"),
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedWorkspace, capturedPurpose string

			mock := &mockProcessProvider{
				promptFunc: func(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
					capturedWorkspace = workspaceUUID
					capturedPurpose = purpose
					if tt.mockError != nil {
						return "", tt.mockError
					}
					return tt.mockResponse, nil
				},
			}

			mgr := NewWorkspaceAuxiliaryManager(mock, nil)

			got, err := mgr.GenerateTitle(context.Background(), "test-workspace", tt.message)

			if (err != nil) != tt.wantErr {
				t.Errorf("GenerateTitle() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if !strings.Contains(got, tt.wantContains) {
					t.Errorf("GenerateTitle() = %q, want to contain %q", got, tt.wantContains)
				}

				if tt.checkPurpose != "" && capturedPurpose != tt.checkPurpose {
					t.Errorf("Purpose = %q, want %q", capturedPurpose, tt.checkPurpose)
				}

				if tt.checkWorkspace != "" && capturedWorkspace != tt.checkWorkspace {
					t.Errorf("Workspace = %q, want %q", capturedWorkspace, tt.checkWorkspace)
				}
			}
		})
	}
}

func TestWorkspaceAuxiliaryManager_ImprovePrompt(t *testing.T) {
	mock := &mockProcessProvider{
		promptFunc: func(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
			if purpose != PurposeImprovePrompt {
				t.Errorf("Expected purpose %q, got %q", PurposeImprovePrompt, purpose)
			}
			return "Improved: " + message, nil
		},
	}

	mgr := NewWorkspaceAuxiliaryManager(mock, nil)

	got, err := mgr.ImprovePrompt(context.Background(), "test-workspace", "test prompt")
	if err != nil {
		t.Fatalf("ImprovePrompt() error = %v", err)
	}

	if !strings.Contains(got, "Improved:") {
		t.Errorf("ImprovePrompt() = %q, want to contain 'Improved:'", got)
	}
}

func TestWorkspaceAuxiliaryManager_AnalyzeFollowUpQuestions(t *testing.T) {
	tests := []struct {
		name         string
		mockResponse string
		wantCount    int
		wantErr      bool
	}{
		{
			name:         "valid suggestions",
			mockResponse: `[{"label": "Yes, run tests", "value": "Yes, please run the tests"}]`,
			wantCount:    1,
		},
		{
			name:         "empty array",
			mockResponse: `[]`,
			wantCount:    0,
		},
		{
			name:         "multiple suggestions",
			mockResponse: `[{"label": "Yes", "value": "Yes, do it"}, {"label": "No", "value": "No, skip"}]`,
			wantCount:    2,
		},
		{
			name:         "invalid JSON returns empty",
			mockResponse: `not valid json`,
			wantCount:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockProcessProvider{
				promptFunc: func(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
					if purpose != PurposeFollowUp {
						t.Errorf("Expected purpose %q, got %q", PurposeFollowUp, purpose)
					}
					return tt.mockResponse, nil
				},
			}

			mgr := NewWorkspaceAuxiliaryManager(mock, nil)

			got, err := mgr.AnalyzeFollowUpQuestions(context.Background(), "test-workspace", "user prompt", "agent message")

			if tt.wantErr && err == nil {
				t.Error("AnalyzeFollowUpQuestions() expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("AnalyzeFollowUpQuestions() unexpected error: %v", err)
			}
			if len(got) != tt.wantCount {
				t.Errorf("AnalyzeFollowUpQuestions() count = %d, want %d", len(got), tt.wantCount)
			}
		})
	}
}

func TestWorkspaceAuxiliaryManager_CheckMCPAvailability(t *testing.T) {
	tests := []struct {
		name          string
		mockResponse  string
		mockError     error
		wantAvailable bool
		wantErr       bool
		checkCached   bool
	}{
		{
			name:          "tool available",
			mockResponse:  `{"available": true, "message": "Tool is available"}`,
			wantAvailable: true,
			wantErr:       false,
		},
		{
			name:          "tool not available with command",
			mockResponse:  `{"available": false, "suggested_run": "npm install -g @mitto/mcp-server"}`,
			wantAvailable: false,
			wantErr:       false,
		},
		{
			name:          "tool not available with instructions",
			mockResponse:  `{"available": false, "suggested_instructions": "1. Install Node.js\n2. Run npm install"}`,
			wantAvailable: false,
			wantErr:       false,
		},
		{
			name:          "JSON with extra text",
			mockResponse:  `Here is the result: {"available": true, "message": "OK"}`,
			wantAvailable: true,
			wantErr:       false,
		},
		{
			name:      "error from provider",
			mockError: errors.New("provider error"),
			wantErr:   true,
		},
		{
			name:         "invalid JSON",
			mockResponse: `not valid json`,
			wantErr:      true,
		},
		{
			name:          "cached result",
			mockResponse:  `{"available": true, "message": "Tool is available"}`,
			wantAvailable: true,
			checkCached:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			mock := &mockProcessProvider{
				promptFunc: func(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
					callCount++
					if purpose != PurposeMCPCheck {
						t.Errorf("Expected purpose %q, got %q", PurposeMCPCheck, purpose)
					}
					if tt.mockError != nil {
						return "", tt.mockError
					}
					return tt.mockResponse, nil
				},
			}

			mgr := NewWorkspaceAuxiliaryManager(mock, nil)

			// First call
			result, err := mgr.CheckMCPAvailability(context.Background(), "test-workspace", "http://127.0.0.1:3000")

			if (err != nil) != tt.wantErr {
				t.Errorf("CheckMCPAvailability() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if result.Available != tt.wantAvailable {
					t.Errorf("CheckMCPAvailability() available = %v, want %v", result.Available, tt.wantAvailable)
				}
			}

			// Test caching if requested
			if tt.checkCached && !tt.wantErr {
				// Second call should use cache
				result2, err2 := mgr.CheckMCPAvailability(context.Background(), "test-workspace", "http://127.0.0.1:3000")
				if err2 != nil {
					t.Errorf("Second call error = %v", err2)
				}
				if result2.Available != result.Available {
					t.Errorf("Cached result different from original")
				}
				if callCount != 1 {
					t.Errorf("Expected 1 provider call (cached), got %d", callCount)
				}
			}
		})
	}
}

func TestWorkspaceAuxiliaryManager_ClearMCPCheckCache(t *testing.T) {
	callCount := 0
	mock := &mockProcessProvider{
		promptFunc: func(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
			callCount++
			return `{"available": true, "message": "OK"}`, nil
		},
	}

	mgr := NewWorkspaceAuxiliaryManager(mock, nil)

	// First call - should hit provider
	_, err := mgr.CheckMCPAvailability(context.Background(), "test-workspace", "http://127.0.0.1:3000")
	if err != nil {
		t.Fatalf("First call error = %v", err)
	}

	if callCount != 1 {
		t.Errorf("Expected 1 call, got %d", callCount)
	}

	// Second call - should use cache
	_, err = mgr.CheckMCPAvailability(context.Background(), "test-workspace", "http://127.0.0.1:3000")
	if err != nil {
		t.Fatalf("Second call error = %v", err)
	}

	if callCount != 1 {
		t.Errorf("Expected still 1 call (cached), got %d", callCount)
	}

	// Clear cache
	mgr.ClearMCPCheckCache("test-workspace")

	// Third call - should hit provider again
	_, err = mgr.CheckMCPAvailability(context.Background(), "test-workspace", "http://127.0.0.1:3000")
	if err != nil {
		t.Fatalf("Third call error = %v", err)
	}

	if callCount != 2 {
		t.Errorf("Expected 2 calls after cache clear, got %d", callCount)
	}
}
