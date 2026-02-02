package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/appdir"
)

func TestPromptsCache_Get(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Create prompts directory
	promptsDir := filepath.Join(tmpDir, appdir.PromptsDirName)
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		t.Fatalf("Failed to create prompts dir: %v", err)
	}

	// Create a prompt file
	promptContent := `---
name: "Test Prompt"
---

Test content.
`
	if err := os.WriteFile(filepath.Join(promptsDir, "test.md"), []byte(promptContent), 0644); err != nil {
		t.Fatalf("Failed to write test.md: %v", err)
	}

	// Create cache and get prompts
	cache := NewPromptsCache()
	prompts, err := cache.Get()
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}

	if len(prompts) != 1 {
		t.Errorf("len(prompts) = %d, want 1", len(prompts))
	}
	if prompts[0].Name != "Test Prompt" {
		t.Errorf("prompts[0].Name = %q, want %q", prompts[0].Name, "Test Prompt")
	}

	// Second call should return cached data
	prompts2, err := cache.Get()
	if err != nil {
		t.Fatalf("Get() second call failed: %v", err)
	}
	if len(prompts2) != 1 {
		t.Errorf("Second call: len(prompts) = %d, want 1", len(prompts2))
	}
}

func TestPromptsCache_GetWebPrompts(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	promptsDir := filepath.Join(tmpDir, appdir.PromptsDirName)
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		t.Fatalf("Failed to create prompts dir: %v", err)
	}

	promptContent := `---
name: "Web Prompt"
backgroundColor: "#FF0000"
---

Content.
`
	if err := os.WriteFile(filepath.Join(promptsDir, "web.md"), []byte(promptContent), 0644); err != nil {
		t.Fatalf("Failed to write web.md: %v", err)
	}

	cache := NewPromptsCache()
	webPrompts, err := cache.GetWebPrompts()
	if err != nil {
		t.Fatalf("GetWebPrompts() failed: %v", err)
	}

	if len(webPrompts) != 1 {
		t.Fatalf("len(webPrompts) = %d, want 1", len(webPrompts))
	}
	if webPrompts[0].Name != "Web Prompt" {
		t.Errorf("webPrompts[0].Name = %q, want %q", webPrompts[0].Name, "Web Prompt")
	}
	if webPrompts[0].BackgroundColor != "#FF0000" {
		t.Errorf("webPrompts[0].BackgroundColor = %q, want %q", webPrompts[0].BackgroundColor, "#FF0000")
	}
}

func TestPromptsCache_ReloadOnChange(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	promptsDir := filepath.Join(tmpDir, appdir.PromptsDirName)
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		t.Fatalf("Failed to create prompts dir: %v", err)
	}

	// Create initial prompt
	if err := os.WriteFile(filepath.Join(promptsDir, "first.md"), []byte("First prompt"), 0644); err != nil {
		t.Fatalf("Failed to write first.md: %v", err)
	}

	cache := NewPromptsCache()
	prompts, _ := cache.Get()
	if len(prompts) != 1 {
		t.Errorf("Initial: len(prompts) = %d, want 1", len(prompts))
	}

	// Wait a bit to ensure different mod time
	time.Sleep(10 * time.Millisecond)

	// Add another prompt
	if err := os.WriteFile(filepath.Join(promptsDir, "second.md"), []byte("Second prompt"), 0644); err != nil {
		t.Fatalf("Failed to write second.md: %v", err)
	}

	// Get should detect the change and reload
	prompts, _ = cache.Get()
	if len(prompts) != 2 {
		t.Errorf("After add: len(prompts) = %d, want 2", len(prompts))
	}
}

func TestPromptsCache_ForceReload(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	promptsDir := filepath.Join(tmpDir, appdir.PromptsDirName)
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		t.Fatalf("Failed to create prompts dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(promptsDir, "test.md"), []byte("Test"), 0644); err != nil {
		t.Fatalf("Failed to write test.md: %v", err)
	}

	cache := NewPromptsCache()
	cache.Get()

	loadedAt := cache.LoadedAt()

	// Force reload
	time.Sleep(1 * time.Millisecond)
	cache.ForceReload()

	if !cache.LoadedAt().After(loadedAt) {
		t.Error("ForceReload did not update loadedAt")
	}
}

func TestPromptsCache_EmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	promptsDir := filepath.Join(tmpDir, appdir.PromptsDirName)
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		t.Fatalf("Failed to create prompts dir: %v", err)
	}

	cache := NewPromptsCache()
	prompts, err := cache.Get()
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}

	if len(prompts) != 0 {
		t.Errorf("len(prompts) = %d, want 0 for empty directory", len(prompts))
	}
}
