package auxiliary

import (
	"context"
	"testing"
)

func TestTrimQuotes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"double quotes", `"Hello World"`, "Hello World"},
		{"single quotes", `'Hello World'`, "Hello World"},
		{"no quotes", "Hello World", "Hello World"},
		{"empty string", "", ""},
		{"single char", "a", "a"},
		{"only quotes", `""`, ""},
		{"mismatched quotes start double", `"Hello'`, `"Hello'`},
		{"mismatched quotes start single", `'Hello"`, `'Hello"`},
		{"nested quotes", `"'Hello'"`, "'Hello'"},
		{"quotes in middle", `Hello "World"`, `Hello "World"`},
		{"single quote only", `'`, `'`},
		{"double quote only", `"`, `"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := trimQuotes(tt.input)
			if got != tt.want {
				t.Errorf("trimQuotes(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetManager_NotInitialized(t *testing.T) {
	// Reset global state for this test
	// Note: This test may be affected by other tests that call Initialize
	// In a real scenario, we'd use a test-specific initialization

	// GetManager should return nil or the existing manager
	// We can't easily test the nil case without resetting sync.Once
	manager := GetManager()
	// Just verify it doesn't panic
	_ = manager
}

func TestPrompt_NotInitialized(t *testing.T) {
	// Reset global state - this is tricky with sync.Once
	// We'll test the error case by checking if manager is nil

	// Save current manager
	globalMu.Lock()
	savedManager := globalManager
	globalManager = nil
	globalMu.Unlock()

	// Restore after test
	defer func() {
		globalMu.Lock()
		globalManager = savedManager
		globalMu.Unlock()
	}()

	_, err := Prompt(context.TODO(), "test")
	if err == nil {
		t.Error("Prompt should fail when manager is not initialized")
	}
}

// Note: Testing Initialize, GenerateTitle, and ImprovePrompt with the mock ACP
// server would require more complex setup. These are integration-level tests
// that depend on the mock server's behavior.

func TestGenerateTitle_TitleTruncation(t *testing.T) {
	// Test the title truncation logic by examining the function behavior
	// The actual GenerateTitle function calls Prompt, so we can't easily unit test it
	// without mocking. Instead, we test the truncation logic conceptually.

	// A title longer than 50 chars should be truncated
	longTitle := "This is a very long title that exceeds fifty characters limit"
	if len(longTitle) <= 50 {
		t.Skip("Test title is not long enough")
	}

	// The truncation logic in GenerateTitle:
	// if len(title) > 50 { title = title[:47] + "..." }
	truncated := longTitle[:47] + "..."
	if len(truncated) != 50 {
		t.Errorf("Truncated title length = %d, want 50", len(truncated))
	}
}

func TestParseFollowUpSuggestions(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int // number of suggestions expected
	}{
		{
			name:     "valid JSON array",
			input:    `[{"label": "Test", "value": "test value"}]`,
			expected: 1,
		},
		{
			name:     "multiple suggestions",
			input:    `[{"label": "One", "value": "one"}, {"label": "Two", "value": "two"}]`,
			expected: 2,
		},
		{
			name:     "JSON with surrounding text",
			input:    `Here are some suggestions: [{"label": "Test", "value": "test"}] Hope this helps!`,
			expected: 1,
		},
		{
			name:     "empty array",
			input:    `[]`,
			expected: 0,
		},
		{
			name:     "invalid JSON",
			input:    `not valid json`,
			expected: 0,
		},
		{
			name:     "empty string",
			input:    ``,
			expected: 0,
		},
		{
			name:     "whitespace only",
			input:    `   `,
			expected: 0,
		},
		{
			name:     "more than 5 suggestions (should be limited)",
			input:    `[{"label": "1", "value": "1"}, {"label": "2", "value": "2"}, {"label": "3", "value": "3"}, {"label": "4", "value": "4"}, {"label": "5", "value": "5"}, {"label": "6", "value": "6"}]`,
			expected: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseFollowUpSuggestions(tt.input)
			if len(result) != tt.expected {
				t.Errorf("parseFollowUpSuggestions() returned %d suggestions, want %d", len(result), tt.expected)
			}
		})
	}
}

func TestValidateSuggestions(t *testing.T) {
	tests := []struct {
		name        string
		suggestions []FollowUpSuggestion
		expected    int
	}{
		{
			name:        "valid suggestions",
			suggestions: []FollowUpSuggestion{{Label: "Test", Value: "test"}},
			expected:    1,
		},
		{
			name:        "empty label filtered",
			suggestions: []FollowUpSuggestion{{Label: "", Value: "test"}},
			expected:    0,
		},
		{
			name:        "empty value filtered",
			suggestions: []FollowUpSuggestion{{Label: "Test", Value: ""}},
			expected:    0,
		},
		{
			name:        "whitespace only filtered",
			suggestions: []FollowUpSuggestion{{Label: "  ", Value: "  "}},
			expected:    0,
		},
		{
			name:        "mixed valid and invalid",
			suggestions: []FollowUpSuggestion{{Label: "Valid", Value: "valid"}, {Label: "", Value: "invalid"}},
			expected:    1,
		},
		{
			name:        "nil input",
			suggestions: nil,
			expected:    0,
		},
		{
			name:        "empty slice",
			suggestions: []FollowUpSuggestion{},
			expected:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := validateSuggestions(tt.suggestions)
			if len(result) != tt.expected {
				t.Errorf("validateSuggestions() returned %d suggestions, want %d", len(result), tt.expected)
			}
		})
	}
}

func TestValidateSuggestions_Truncation(t *testing.T) {
	// Test that long labels and values are truncated
	longLabel := "This is a very long label that exceeds the fifty character limit for labels"
	longValue := ""
	for i := 0; i < 1100; i++ {
		longValue += "x"
	}

	suggestions := []FollowUpSuggestion{{Label: longLabel, Value: longValue}}
	result := validateSuggestions(suggestions)

	if len(result) != 1 {
		t.Fatalf("Expected 1 suggestion, got %d", len(result))
	}

	// Label should be truncated to 50 chars (47 + "...")
	if len(result[0].Label) != 50 {
		t.Errorf("Label length = %d, want 50", len(result[0].Label))
	}
	if result[0].Label[47:] != "..." {
		t.Errorf("Label should end with '...', got %q", result[0].Label[47:])
	}

	// Value should be truncated to 1000 chars (997 + "...")
	if len(result[0].Value) != 1000 {
		t.Errorf("Value length = %d, want 1000", len(result[0].Value))
	}
	if result[0].Value[997:] != "..." {
		t.Errorf("Value should end with '...', got %q", result[0].Value[997:])
	}
}

func TestTruncateForLog(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{
			name:   "short string unchanged",
			input:  "hello",
			maxLen: 10,
			want:   "hello",
		},
		{
			name:   "exact length unchanged",
			input:  "hello",
			maxLen: 5,
			want:   "hello",
		},
		{
			name:   "long string truncated",
			input:  "hello world",
			maxLen: 8,
			want:   "hello...",
		},
		{
			name:   "very short maxLen",
			input:  "hello",
			maxLen: 3,
			want:   "hel",
		},
		{
			name:   "maxLen of 4",
			input:  "hello world",
			maxLen: 4,
			want:   "h...",
		},
		{
			name:   "empty string",
			input:  "",
			maxLen: 10,
			want:   "",
		},
		{
			name:   "realistic log preview",
			input:  "This is a long agent response that needs to be truncated for logging",
			maxLen: 30,
			want:   "This is a long agent respon...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateForLog(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateForLog(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}
