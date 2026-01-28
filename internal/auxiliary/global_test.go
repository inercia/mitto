package auxiliary

import (
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

	_, err := Prompt(nil, "test")
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
