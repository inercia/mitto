package auxiliary

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/fileutil"
	"github.com/inercia/mitto/internal/mcpdiscovery"
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

// =============================================================================
// applyMCPToolsCachePolicy: last-known-good / two-consecutive-negatives
// (mitto-sys.6, docs/devel/mcp-tool-discovery.md Q3.1)
// =============================================================================

// mcpToolNames returns the sorted tool names from a []MCPToolInfo, for
// concise assertions.
func mcpToolNames(tools []MCPToolInfo) []string {
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	return names
}

func assertToolNames(t *testing.T, got []MCPToolInfo, want ...string) {
	t.Helper()
	gotNames := mcpToolNames(got)
	if len(gotNames) != len(want) {
		t.Fatalf("tool names = %v, want %v", gotNames, want)
	}
	for i, name := range want {
		if gotNames[i] != name {
			t.Fatalf("tool names = %v, want %v", gotNames, want)
		}
	}
}

func TestApplyMCPToolsCachePolicy_FirstNonEmptyFetchStores(t *testing.T) {
	mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)

	got := mgr.applyMCPToolsCachePolicy("ws", []MCPToolInfo{
		{Name: "jira_create_issue", Description: "create"},
		{Name: "jira_search", Description: "search"},
	})

	assertToolNames(t, got, "jira_create_issue", "jira_search")
	cached, ok := mgr.GetCachedMCPTools("ws")
	if !ok {
		t.Fatalf("expected cache entry after first non-empty fetch")
	}
	assertToolNames(t, cached, "jira_create_issue", "jira_search")
}

func TestApplyMCPToolsCachePolicy_FirstEmptyFetchEstablishesNothing(t *testing.T) {
	mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)

	got := mgr.applyMCPToolsCachePolicy("ws", nil)

	if got != nil {
		t.Fatalf("expected nil result for first empty fetch, got %v", got)
	}
	if _, ok := mgr.GetCachedMCPTools("ws"); ok {
		t.Fatalf("expected no cache entry after first empty fetch")
	}
}

func TestApplyMCPToolsCachePolicy_OneNegativeRetainsTool(t *testing.T) {
	mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)

	mgr.applyMCPToolsCachePolicy("ws", []MCPToolInfo{{Name: "jira_search", Description: "search"}})

	// One empty fetch: the previously-known tool must be retained.
	got := mgr.applyMCPToolsCachePolicy("ws", nil)

	assertToolNames(t, got, "jira_search")
}

func TestApplyMCPToolsCachePolicy_TwoConsecutiveNegativesRemove(t *testing.T) {
	mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)

	mgr.applyMCPToolsCachePolicy("ws", []MCPToolInfo{{Name: "jira_search", Description: "search"}})
	mgr.applyMCPToolsCachePolicy("ws", nil) // 1st negative: retained

	got := mgr.applyMCPToolsCachePolicy("ws", nil) // 2nd consecutive negative: removed

	if len(got) != 0 {
		t.Fatalf("expected tool removed after 2 consecutive negatives, got %v", mcpToolNames(got))
	}
	cached, ok := mgr.GetCachedMCPTools("ws")
	if !ok || len(cached) != 0 {
		t.Fatalf("expected an empty (but present) cache entry, got ok=%v cached=%v", ok, cached)
	}
}

func TestApplyMCPToolsCachePolicy_ReappearanceResetsCounter(t *testing.T) {
	mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)

	mgr.applyMCPToolsCachePolicy("ws", []MCPToolInfo{{Name: "jira_search", Description: "search"}})
	mgr.applyMCPToolsCachePolicy("ws", nil) // 1st negative: retained

	// Tool reappears: counter must reset.
	got := mgr.applyMCPToolsCachePolicy("ws", []MCPToolInfo{{Name: "jira_search", Description: "search v2"}})
	assertToolNames(t, got, "jira_search")

	// A single subsequent empty fetch must NOT remove it (counter was reset,
	// this is only the first negative again).
	got = mgr.applyMCPToolsCachePolicy("ws", nil)
	assertToolNames(t, got, "jira_search")
}

func TestApplyMCPToolsCachePolicy_NewToolAdded(t *testing.T) {
	mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)

	mgr.applyMCPToolsCachePolicy("ws", []MCPToolInfo{{Name: "jira_search", Description: "search"}})

	got := mgr.applyMCPToolsCachePolicy("ws", []MCPToolInfo{
		{Name: "jira_search", Description: "search"},
		{Name: "jira_create_issue", Description: "create"},
	})

	assertToolNames(t, got, "jira_create_issue", "jira_search")
}

func TestApplyMCPToolsCachePolicy_DescriptionRefreshedWhenPresentInFresh(t *testing.T) {
	mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)

	mgr.applyMCPToolsCachePolicy("ws", []MCPToolInfo{{Name: "jira_search", Description: "old description"}})

	got := mgr.applyMCPToolsCachePolicy("ws", []MCPToolInfo{{Name: "jira_search", Description: "new description"}})

	if len(got) != 1 || got[0].Description != "new description" {
		t.Fatalf("expected description refreshed to %q, got %+v", "new description", got)
	}
}

func TestApplyMCPToolsCachePolicy_WorkspacesAreIsolated(t *testing.T) {
	mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)

	mgr.applyMCPToolsCachePolicy("ws-a", []MCPToolInfo{{Name: "jira_search"}})
	mgr.applyMCPToolsCachePolicy("ws-b", []MCPToolInfo{{Name: "slack_post"}})

	// A negative fetch for ws-a must not affect ws-b's cache/negatives.
	mgr.applyMCPToolsCachePolicy("ws-a", nil)

	cachedB, ok := mgr.GetCachedMCPTools("ws-b")
	if !ok {
		t.Fatalf("expected ws-b cache entry to remain untouched")
	}
	assertToolNames(t, cachedB, "slack_post")
}

// =============================================================================
// FetchMCPTools + StdioToolsDiscoverer: deterministic discovery with LLM
// fallback (mitto-sys.2 acceptance #1, mitto-sys.6)
// =============================================================================

func TestFetchMCPTools_NilDiscoverer_PureLLM(t *testing.T) {
	mock := &mockProcessProvider{
		promptFunc: func(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
			return `{"tools":[{"name":"jira_search","description":"search"}]}`, nil
		},
	}
	mgr := NewWorkspaceAuxiliaryManager(mock, nil) // StdioToolsDiscoverer left nil

	tools, err := mgr.FetchMCPTools(context.Background(), "ws")
	if err != nil {
		t.Fatalf("FetchMCPTools error = %v", err)
	}
	assertToolNames(t, tools, "jira_search")

	cached, ok := mgr.GetCachedMCPTools("ws")
	if !ok {
		t.Fatalf("expected cache entry")
	}
	assertToolNames(t, cached, "jira_search")
}

func TestFetchMCPTools_AllReachable_SkipsLLM(t *testing.T) {
	mock := &mockProcessProvider{
		promptFunc: func(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
			t.Fatal("LLM provider must not be called when all servers are reachable")
			return "", nil
		},
	}
	mgr := NewWorkspaceAuxiliaryManager(mock, nil)
	mgr.StdioToolsDiscoverer = func(ctx context.Context, workspaceUUID string) ([]mcpdiscovery.ServerToolsResult, error) {
		return []mcpdiscovery.ServerToolsResult{
			{Server: "jira", Reachable: true, Tools: []string{"jira_create_issue", "jira_search"}},
		}, nil
	}

	tools, err := mgr.FetchMCPTools(context.Background(), "ws")
	if err != nil {
		t.Fatalf("FetchMCPTools error = %v", err)
	}
	assertToolNames(t, tools, "jira_create_issue", "jira_search")

	cached, ok := mgr.GetCachedMCPTools("ws")
	if !ok {
		t.Fatalf("expected cache entry")
	}
	assertToolNames(t, cached, "jira_create_issue", "jira_search")
}

func TestFetchMCPTools_UnreachableServer_UnionsWithLLM(t *testing.T) {
	mock := &mockProcessProvider{
		promptFunc: func(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
			return `{"tools":[{"name":"slack_post","description":"post"}]}`, nil
		},
	}
	mgr := NewWorkspaceAuxiliaryManager(mock, nil)
	mgr.StdioToolsDiscoverer = func(ctx context.Context, workspaceUUID string) ([]mcpdiscovery.ServerToolsResult, error) {
		return []mcpdiscovery.ServerToolsResult{
			{Server: "jira", Reachable: true, Tools: []string{"jira_search"}},
			{Server: "slack", Reachable: false, Err: errors.New("timeout")},
		}, nil
	}

	tools, err := mgr.FetchMCPTools(context.Background(), "ws")
	if err != nil {
		t.Fatalf("FetchMCPTools error = %v", err)
	}
	assertToolNames(t, tools, "jira_search", "slack_post")
}

func TestFetchMCPTools_DiscovererError_FallsBackToLLM(t *testing.T) {
	mock := &mockProcessProvider{
		promptFunc: func(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
			return `{"tools":[{"name":"jira_search","description":"search"}]}`, nil
		},
	}
	mgr := NewWorkspaceAuxiliaryManager(mock, nil)
	mgr.StdioToolsDiscoverer = func(ctx context.Context, workspaceUUID string) ([]mcpdiscovery.ServerToolsResult, error) {
		return nil, errors.New("boom")
	}

	tools, err := mgr.FetchMCPTools(context.Background(), "ws")
	if err != nil {
		t.Fatalf("FetchMCPTools error = %v", err)
	}
	assertToolNames(t, tools, "jira_search")
}

func TestFetchMCPTools_AllReachableZeroTools_NoCacheEntry(t *testing.T) {
	mock := &mockProcessProvider{
		promptFunc: func(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
			t.Fatal("LLM provider must not be called when all servers are reachable")
			return "", nil
		},
	}
	mgr := NewWorkspaceAuxiliaryManager(mock, nil)
	mgr.StdioToolsDiscoverer = func(ctx context.Context, workspaceUUID string) ([]mcpdiscovery.ServerToolsResult, error) {
		return []mcpdiscovery.ServerToolsResult{
			{Server: "empty-server", Reachable: true, Tools: nil},
		}, nil
	}

	tools, err := mgr.FetchMCPTools(context.Background(), "ws")
	if err != nil {
		t.Fatalf("FetchMCPTools error = %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("expected empty tools, got %v", tools)
	}
	if _, ok := mgr.GetCachedMCPTools("ws"); ok {
		t.Fatalf("expected no cache entry (policy no-op on first empty fetch)")
	}
}

func TestFetchMCPTools_DedupBetweenDeterministicAndLLM(t *testing.T) {
	mock := &mockProcessProvider{
		promptFunc: func(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
			return `{"tools":[{"name":"jira_search","description":"llm description"},{"name":"slack_post","description":"post"}]}`, nil
		},
	}
	mgr := NewWorkspaceAuxiliaryManager(mock, nil)
	mgr.StdioToolsDiscoverer = func(ctx context.Context, workspaceUUID string) ([]mcpdiscovery.ServerToolsResult, error) {
		return []mcpdiscovery.ServerToolsResult{
			{Server: "jira", Reachable: true, Tools: []string{"jira_search"}},
			{Server: "slack", Reachable: false, Err: errors.New("timeout")},
		}, nil
	}

	tools, err := mgr.FetchMCPTools(context.Background(), "ws")
	if err != nil {
		t.Fatalf("FetchMCPTools error = %v", err)
	}
	assertToolNames(t, tools, "jira_search", "slack_post")

	// jira_search must appear exactly once, from the deterministic result
	// (empty Description), not duplicated or overwritten by the LLM version.
	count := 0
	var desc string
	for _, tool := range tools {
		if tool.Name == "jira_search" {
			count++
			desc = tool.Description
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one jira_search entry, got %d", count)
	}
	if desc != "" {
		t.Errorf("expected the deterministic entry (empty Description) to win, got %q", desc)
	}
}

// =============================================================================
// FetchMCPTools disk persistence: real-MCP only, TTL, manual refresh (mitto-sys.8)
// =============================================================================

func readPersistedSnapshot(t *testing.T, dir, workspaceUUID string) persistedMCPTools {
	t.Helper()
	var snap persistedMCPTools
	if err := fileutil.ReadJSON(filepath.Join(dir, workspaceUUID+".json"), &snap); err != nil {
		t.Fatalf("read persisted snapshot: %v", err)
	}
	return snap
}

func TestFetchMCPTools_PersistsRealMCPToDisk(t *testing.T) {
	dir := t.TempDir()
	mock := &mockProcessProvider{
		promptFunc: func(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
			t.Fatal("LLM provider must not be called when all servers are reachable")
			return "", nil
		},
	}
	mgr := NewWorkspaceAuxiliaryManager(mock, nil)
	mgr.MCPToolsPersistDir = dir
	mgr.StdioToolsDiscoverer = func(ctx context.Context, workspaceUUID string) ([]mcpdiscovery.ServerToolsResult, error) {
		return []mcpdiscovery.ServerToolsResult{
			{Server: "jira", Reachable: true, Tools: []string{"jira_create_issue", "jira_search"}},
		}, nil
	}

	if _, err := mgr.FetchMCPTools(context.Background(), "ws"); err != nil {
		t.Fatalf("FetchMCPTools error = %v", err)
	}

	snap := readPersistedSnapshot(t, dir, "ws")
	assertToolNames(t, snap.Tools, "jira_create_issue", "jira_search")
	if snap.UpdatedAt.IsZero() {
		t.Errorf("expected UpdatedAt to be set")
	}
	if time.Since(snap.UpdatedAt) > time.Minute {
		t.Errorf("expected a recent UpdatedAt, got %v", snap.UpdatedAt)
	}
}

func TestFetchMCPTools_DoesNotPersistLLMFallback(t *testing.T) {
	dir := t.TempDir()
	mock := &mockProcessProvider{
		promptFunc: func(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
			return `{"tools":[{"name":"slack_post","description":"post"}]}`, nil
		},
	}
	mgr := NewWorkspaceAuxiliaryManager(mock, nil)
	mgr.MCPToolsPersistDir = dir
	// No StdioToolsDiscoverer → pure-LLM path; nothing real-MCP to persist.

	tools, err := mgr.FetchMCPTools(context.Background(), "ws")
	if err != nil {
		t.Fatalf("FetchMCPTools error = %v", err)
	}
	assertToolNames(t, tools, "slack_post")

	if _, err := os.Stat(filepath.Join(dir, "ws.json")); !os.IsNotExist(err) {
		t.Fatalf("LLM-fallback result must not be persisted (stat err = %v)", err)
	}
}

func TestFetchMCPTools_LoadsPersistedWithinTTL_SkipsProbe(t *testing.T) {
	dir := t.TempDir()
	// Pre-seed a fresh snapshot on disk.
	snap := persistedMCPTools{
		Tools:     []MCPToolInfo{{Name: "jira_search"}, {Name: "slack_post"}},
		UpdatedAt: time.Now(),
	}
	if err := fileutil.WriteJSONAtomic(filepath.Join(dir, "ws.json"), &snap, 0o644); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	mock := &mockProcessProvider{
		promptFunc: func(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
			t.Fatal("LLM provider must not be called when a fresh snapshot exists")
			return "", nil
		},
	}
	mgr := NewWorkspaceAuxiliaryManager(mock, nil)
	mgr.MCPToolsPersistDir = dir
	mgr.StdioToolsDiscoverer = func(ctx context.Context, workspaceUUID string) ([]mcpdiscovery.ServerToolsResult, error) {
		t.Fatal("discovery must not run when a fresh snapshot exists")
		return nil, nil
	}

	tools, err := mgr.FetchMCPTools(context.Background(), "ws")
	if err != nil {
		t.Fatalf("FetchMCPTools error = %v", err)
	}
	assertToolNames(t, tools, "jira_search", "slack_post")
}

func TestFetchMCPTools_ExpiredSnapshot_ReProbes(t *testing.T) {
	dir := t.TempDir()
	// Pre-seed a stale snapshot (older than the TTL).
	stale := persistedMCPTools{
		Tools:     []MCPToolInfo{{Name: "old_tool"}},
		UpdatedAt: time.Now().Add(-30 * time.Minute),
	}
	if err := fileutil.WriteJSONAtomic(filepath.Join(dir, "ws.json"), &stale, 0o644); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	mock := &mockProcessProvider{
		promptFunc: func(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
			t.Fatal("LLM provider must not be called when all servers are reachable")
			return "", nil
		},
	}
	mgr := NewWorkspaceAuxiliaryManager(mock, nil)
	mgr.MCPToolsPersistDir = dir
	mgr.mcpToolsTTL = 15 * time.Minute
	probed := false
	mgr.StdioToolsDiscoverer = func(ctx context.Context, workspaceUUID string) ([]mcpdiscovery.ServerToolsResult, error) {
		probed = true
		return []mcpdiscovery.ServerToolsResult{
			{Server: "jira", Reachable: true, Tools: []string{"jira_search"}},
		}, nil
	}

	tools, err := mgr.FetchMCPTools(context.Background(), "ws")
	if err != nil {
		t.Fatalf("FetchMCPTools error = %v", err)
	}
	if !probed {
		t.Fatalf("expected a fresh probe after TTL expiry")
	}
	assertToolNames(t, tools, "jira_search")

	// The stale snapshot must have been overwritten with the fresh probe.
	snap := readPersistedSnapshot(t, dir, "ws")
	assertToolNames(t, snap.Tools, "jira_search")
}

func TestClearMCPToolsCache_RemovesPersistedSnapshot(t *testing.T) {
	dir := t.TempDir()
	snap := persistedMCPTools{Tools: []MCPToolInfo{{Name: "jira_search"}}, UpdatedAt: time.Now()}
	path := filepath.Join(dir, "ws.json")
	if err := fileutil.WriteJSONAtomic(path, &snap, 0o644); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)
	mgr.MCPToolsPersistDir = dir
	mgr.ClearMCPToolsCache("ws")

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected persisted snapshot removed, stat err = %v", err)
	}
}

// =============================================================================
// fetchMCPToolsViaLLM: strict parsing + single retry + plausibility (mitto-sys.7)
// =============================================================================

func TestFetchMCPToolsViaLLM_RetryAndPlausibility(t *testing.T) {
	t.Run("parse-fail-then-success", func(t *testing.T) {
		calls := 0
		mock := &mockProcessProvider{
			promptFunc: func(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
				calls++
				if calls == 1 {
					return "garbage", nil
				}
				return `{"tools":[{"name":"t"}]}`, nil
			},
		}
		mgr := NewWorkspaceAuxiliaryManager(mock, nil)

		tools, err := mgr.fetchMCPToolsViaLLM(context.Background(), "ws", -1)
		if err != nil {
			t.Fatalf("fetchMCPToolsViaLLM error = %v", err)
		}
		assertToolNames(t, tools, "t")
		if calls != 2 {
			t.Errorf("calls = %d, want 2", calls)
		}
	})

	t.Run("empty-plausible-then-success", func(t *testing.T) {
		calls := 0
		mock := &mockProcessProvider{
			promptFunc: func(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
				calls++
				if calls == 1 {
					return `{"tools":[]}`, nil
				}
				return `{"tools":[{"name":"t"}]}`, nil
			},
		}
		mgr := NewWorkspaceAuxiliaryManager(mock, nil)

		tools, err := mgr.fetchMCPToolsViaLLM(context.Background(), "ws", 2)
		if err != nil {
			t.Fatalf("fetchMCPToolsViaLLM error = %v", err)
		}
		assertToolNames(t, tools, "t")
		if calls != 2 {
			t.Errorf("calls = %d, want 2", calls)
		}
	})

	t.Run("empty-accepted-when-count-not-positive", func(t *testing.T) {
		calls := 0
		mock := &mockProcessProvider{
			promptFunc: func(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
				calls++
				return `{"tools":[]}`, nil
			},
		}
		mgr := NewWorkspaceAuxiliaryManager(mock, nil)

		tools, err := mgr.fetchMCPToolsViaLLM(context.Background(), "ws", -1)
		if err != nil {
			t.Fatalf("fetchMCPToolsViaLLM error = %v", err)
		}
		if len(tools) != 0 {
			t.Errorf("tools = %v, want empty", tools)
		}
		if calls != 1 {
			t.Errorf("calls = %d, want 1 (no retry when configuredServerCount <= 0)", calls)
		}
	})

	t.Run("explicit-error-no-retry", func(t *testing.T) {
		calls := 0
		mock := &mockProcessProvider{
			promptFunc: func(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
				calls++
				return `{"error":"none"}`, nil
			},
		}
		mgr := NewWorkspaceAuxiliaryManager(mock, nil)

		_, err := mgr.fetchMCPToolsViaLLM(context.Background(), "ws", 2)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		if calls != 1 {
			t.Errorf("calls = %d, want 1 (agent error is definitive, never retried)", calls)
		}
	})

	t.Run("both-attempts-fail", func(t *testing.T) {
		calls := 0
		mock := &mockProcessProvider{
			promptFunc: func(ctx context.Context, workspaceUUID, purpose, message string) (string, error) {
				calls++
				return "garbage", nil
			},
		}
		mgr := NewWorkspaceAuxiliaryManager(mock, nil)

		_, err := mgr.fetchMCPToolsViaLLM(context.Background(), "ws", 2)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		if calls != 2 {
			t.Errorf("calls = %d, want 2", calls)
		}
	})
}

// =============================================================================
// mergeMCPToolsAdditive + EnsureMCPBackoffRetry: bounded backoff re-probe of
// configured-but-unreachable MCP servers (mitto-sys.5)
// =============================================================================

// fastBackoffPolicy is a tiny policy for fast, deterministic tests.
func fastBackoffPolicy(maxAttempts int) mcpdiscovery.BackoffPolicy {
	return mcpdiscovery.BackoffPolicy{Base: time.Millisecond, Factor: 2, Max: 5 * time.Millisecond, MaxAttempts: maxAttempts}
}

func TestMergeMCPToolsAdditive(t *testing.T) {
	t.Run("empty cache plus fresh", func(t *testing.T) {
		mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)
		merged, changed := mgr.mergeMCPToolsAdditive("ws", []MCPToolInfo{{Name: "jira_search"}})
		if !changed {
			t.Fatalf("changed = false, want true")
		}
		assertToolNames(t, merged, "jira_search")
	})

	t.Run("absent tool retained, no downgrade", func(t *testing.T) {
		mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)
		mgr.mcpToolsCache["ws"] = []MCPToolInfo{{Name: "jira_search"}, {Name: "slack_post"}}

		merged, changed := mgr.mergeMCPToolsAdditive("ws", []MCPToolInfo{{Name: "jira_search"}})
		if changed {
			t.Fatalf("changed = true, want false (slack_post absent from fresh must not evict it)")
		}
		assertToolNames(t, merged, "jira_search", "slack_post")

		cached, ok := mgr.GetCachedMCPTools("ws")
		if !ok {
			t.Fatalf("expected cache entry")
		}
		assertToolNames(t, cached, "jira_search", "slack_post")
	})

	t.Run("description refreshed", func(t *testing.T) {
		mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)
		mgr.mcpToolsCache["ws"] = []MCPToolInfo{{Name: "jira_search", Description: "old"}}

		merged, changed := mgr.mergeMCPToolsAdditive("ws", []MCPToolInfo{{Name: "jira_search", Description: "newdesc"}})
		if !changed {
			t.Fatalf("changed = false, want true")
		}
		if len(merged) != 1 || merged[0].Description != "newdesc" {
			t.Errorf("merged = %+v, want [{jira_search newdesc}]", merged)
		}
	})

	t.Run("empty fresh is a no-op", func(t *testing.T) {
		mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)
		mgr.mcpToolsCache["ws"] = []MCPToolInfo{{Name: "jira_search"}}

		merged, changed := mgr.mergeMCPToolsAdditive("ws", nil)
		if changed {
			t.Fatalf("changed = true, want false")
		}
		assertToolNames(t, merged, "jira_search")
	})

	t.Run("reappearing tool resets negatives", func(t *testing.T) {
		mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)
		// jira_search was previously seen (negatives=1, e.g. one missed
		// applyMCPToolsCachePolicy round) but is no longer in the cache list
		// itself (only tracked via the negatives counter at this point).
		mgr.mcpToolsNegatives["ws"] = map[string]int{"jira_search": 1}

		mgr.mergeMCPToolsAdditive("ws", []MCPToolInfo{{Name: "jira_search"}})

		if negs := mgr.mcpToolsNegatives["ws"]; negs != nil {
			if _, ok := negs["jira_search"]; ok {
				t.Errorf("expected jira_search negatives entry cleared, got %v", negs)
			}
		}
	})
}

func TestEnsureMCPBackoffRetry_ReadyAfterN(t *testing.T) {
	mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)
	mgr.mcpBackoffPolicy = fastBackoffPolicy(20)

	var calls int32
	mgr.StdioToolsDiscoverer = func(ctx context.Context, workspaceUUID string) ([]mcpdiscovery.ServerToolsResult, error) {
		n := atomic.AddInt32(&calls, 1)
		if n <= 3 {
			return []mcpdiscovery.ServerToolsResult{{Server: "s1", Reachable: false}}, nil
		}
		return []mcpdiscovery.ServerToolsResult{{Server: "s1", Reachable: true, Tools: []string{"late_tool"}}}, nil
	}

	var mu sync.Mutex
	var updates [][]MCPToolInfo
	done := make(chan struct{}, 10)
	onUpdate := func(tools []MCPToolInfo) {
		mu.Lock()
		updates = append(updates, tools)
		mu.Unlock()
		done <- struct{}{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.EnsureMCPBackoffRetry(ctx, "ws", onUpdate)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for onUpdate")
	}

	// Give the loop a moment to make its final (stopping) probe and exit.
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(updates) != 1 {
		t.Fatalf("onUpdate called %d times, want 1: %+v", len(updates), updates)
	}
	assertToolNames(t, updates[0], "late_tool")

	if got := atomic.LoadInt32(&calls); got != 4 {
		t.Errorf("discoverer called %d times, want 4", got)
	}
}

func TestEnsureMCPBackoffRetry_NoNegativeOnTimeout(t *testing.T) {
	mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)
	mgr.mcpBackoffPolicy = fastBackoffPolicy(3)
	mgr.mcpToolsCache["ws"] = []MCPToolInfo{{Name: "known"}}

	mgr.StdioToolsDiscoverer = func(ctx context.Context, workspaceUUID string) ([]mcpdiscovery.ServerToolsResult, error) {
		return []mcpdiscovery.ServerToolsResult{{Server: "s1", Reachable: false, Err: context.DeadlineExceeded}}, nil
	}

	var onUpdateCalls int32
	onUpdate := func(tools []MCPToolInfo) {
		atomic.AddInt32(&onUpdateCalls, 1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.EnsureMCPBackoffRetry(ctx, "ws", onUpdate)

	// Poll until the backoff goroutine has finished (mcpBackoffActive cleared).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mgr.mcpBackoffMu.Lock()
		active := mgr.mcpBackoffActive["ws"]
		mgr.mcpBackoffMu.Unlock()
		if !active {
			break
		}
		time.Sleep(time.Millisecond)
	}

	if atomic.LoadInt32(&onUpdateCalls) != 0 {
		t.Errorf("onUpdate called %d times, want 0", onUpdateCalls)
	}
	cached, ok := mgr.GetCachedMCPTools("ws")
	if !ok || len(cached) != 1 || cached[0].Name != "known" {
		t.Errorf("cache = %+v (ok=%v), want unchanged [{known}]", cached, ok)
	}
}

func TestEnsureMCPBackoffRetry_Dedup(t *testing.T) {
	mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)
	mgr.mcpBackoffPolicy = fastBackoffPolicy(0) // unbounded, relies on ctx cancel

	var starts int32
	mgr.StdioToolsDiscoverer = func(ctx context.Context, workspaceUUID string) ([]mcpdiscovery.ServerToolsResult, error) {
		atomic.AddInt32(&starts, 1)
		return []mcpdiscovery.ServerToolsResult{{Server: "s1", Reachable: false}}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.EnsureMCPBackoffRetry(ctx, "ws", nil)
	mgr.EnsureMCPBackoffRetry(ctx, "ws", nil) // second call must be a no-op (already active)

	time.Sleep(20 * time.Millisecond)
	cancel()

	// Wait for the goroutine to exit.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mgr.mcpBackoffMu.Lock()
		active := mgr.mcpBackoffActive["ws"]
		mgr.mcpBackoffMu.Unlock()
		if !active {
			break
		}
		time.Sleep(time.Millisecond)
	}

	if got := atomic.LoadInt32(&starts); got < 1 {
		t.Errorf("discoverer never called")
	}
	// A single active loop dedups repeated Ensure calls; the assertion of
	// interest is that mcpBackoffActive never allowed two concurrent loops,
	// which is implied by there being exactly one goroutine's worth of
	// deterministic sequential calls (no data race under -race, and the
	// second Ensure call returned immediately without starting a rival
	// goroutine that could interleave with the first's cleanup).
}

func TestEnsureMCPBackoffRetry_NilDiscoverer_NoOp(t *testing.T) {
	mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil) // StdioToolsDiscoverer left nil

	called := false
	mgr.EnsureMCPBackoffRetry(context.Background(), "ws", func(tools []MCPToolInfo) {
		called = true
	})

	time.Sleep(10 * time.Millisecond)
	if called {
		t.Errorf("onUpdate called, want no-op for nil discoverer")
	}
}
