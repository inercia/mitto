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

func TestPromptsCache_AdditionalDirs(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Create default prompts directory with one prompt
	defaultDir := filepath.Join(tmpDir, appdir.PromptsDirName)
	if err := os.MkdirAll(defaultDir, 0755); err != nil {
		t.Fatalf("Failed to create default prompts dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(defaultDir, "default.md"), []byte("---\nname: Default\n---\nDefault content"), 0644); err != nil {
		t.Fatalf("Failed to write default.md: %v", err)
	}

	// Create additional directory with another prompt
	additionalDir := filepath.Join(tmpDir, "extra-prompts")
	if err := os.MkdirAll(additionalDir, 0755); err != nil {
		t.Fatalf("Failed to create additional prompts dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(additionalDir, "extra.md"), []byte("---\nname: Extra\n---\nExtra content"), 0644); err != nil {
		t.Fatalf("Failed to write extra.md: %v", err)
	}

	cache := NewPromptsCache()
	cache.SetAdditionalDirs([]string{additionalDir})

	prompts, err := cache.Get()
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}

	// Should have both prompts
	if len(prompts) != 2 {
		t.Errorf("len(prompts) = %d, want 2", len(prompts))
	}

	// Check that both prompts are present
	names := make(map[string]bool)
	for _, p := range prompts {
		names[p.Name] = true
	}
	if !names["Default"] {
		t.Error("Missing 'Default' prompt")
	}
	if !names["Extra"] {
		t.Error("Missing 'Extra' prompt")
	}
}

func TestPromptsCache_AdditionalDirsOverride(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Create default prompts directory with a prompt
	defaultDir := filepath.Join(tmpDir, appdir.PromptsDirName)
	if err := os.MkdirAll(defaultDir, 0755); err != nil {
		t.Fatalf("Failed to create default prompts dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(defaultDir, "shared.md"), []byte("---\nname: Shared\n---\nDefault version"), 0644); err != nil {
		t.Fatalf("Failed to write shared.md: %v", err)
	}

	// Create additional directory with same-named prompt (should override)
	additionalDir := filepath.Join(tmpDir, "extra-prompts")
	if err := os.MkdirAll(additionalDir, 0755); err != nil {
		t.Fatalf("Failed to create additional prompts dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(additionalDir, "shared.md"), []byte("---\nname: Shared\n---\nOverridden version"), 0644); err != nil {
		t.Fatalf("Failed to write shared.md: %v", err)
	}

	cache := NewPromptsCache()
	cache.SetAdditionalDirs([]string{additionalDir})

	prompts, err := cache.Get()
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}

	// Should have only one prompt (overridden)
	if len(prompts) != 1 {
		t.Errorf("len(prompts) = %d, want 1", len(prompts))
	}

	if prompts[0].Content != "Overridden version" {
		t.Errorf("prompts[0].Content = %q, want %q", prompts[0].Content, "Overridden version")
	}
}

func TestPromptsCache_WorkspaceDirs(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Create default prompts directory
	defaultDir := filepath.Join(tmpDir, appdir.PromptsDirName)
	if err := os.MkdirAll(defaultDir, 0755); err != nil {
		t.Fatalf("Failed to create default prompts dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(defaultDir, "global.md"), []byte("---\nname: Global\n---\nGlobal content"), 0644); err != nil {
		t.Fatalf("Failed to write global.md: %v", err)
	}

	// Create workspace directory with relative prompts dir
	workspaceDir := filepath.Join(tmpDir, "my-project")
	workspacePromptsDir := filepath.Join(workspaceDir, ".prompts")
	if err := os.MkdirAll(workspacePromptsDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace prompts dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspacePromptsDir, "local.md"), []byte("---\nname: Local\n---\nLocal content"), 0644); err != nil {
		t.Fatalf("Failed to write local.md: %v", err)
	}

	cache := NewPromptsCache()
	// Set workspace dirs with relative path
	cache.SetWorkspaceDirs(workspaceDir, []string{".prompts"})

	prompts, err := cache.Get()
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}

	// Should have both prompts
	if len(prompts) != 2 {
		t.Errorf("len(prompts) = %d, want 2", len(prompts))
	}

	names := make(map[string]bool)
	for _, p := range prompts {
		names[p.Name] = true
	}
	if !names["Global"] {
		t.Error("Missing 'Global' prompt")
	}
	if !names["Local"] {
		t.Error("Missing 'Local' prompt")
	}
}

func TestPromptsCache_WorkspaceDirsOverrideAll(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Create default prompts directory with a prompt
	defaultDir := filepath.Join(tmpDir, appdir.PromptsDirName)
	if err := os.MkdirAll(defaultDir, 0755); err != nil {
		t.Fatalf("Failed to create default prompts dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(defaultDir, "shared.md"), []byte("---\nname: Shared\n---\nDefault version"), 0644); err != nil {
		t.Fatalf("Failed to write shared.md: %v", err)
	}

	// Create additional directory with same-named prompt
	additionalDir := filepath.Join(tmpDir, "extra-prompts")
	if err := os.MkdirAll(additionalDir, 0755); err != nil {
		t.Fatalf("Failed to create additional prompts dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(additionalDir, "shared.md"), []byte("---\nname: Shared\n---\nAdditional version"), 0644); err != nil {
		t.Fatalf("Failed to write shared.md: %v", err)
	}

	// Create workspace directory with same-named prompt (highest priority)
	workspaceDir := filepath.Join(tmpDir, "my-project")
	workspacePromptsDir := filepath.Join(workspaceDir, ".prompts")
	if err := os.MkdirAll(workspacePromptsDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace prompts dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspacePromptsDir, "shared.md"), []byte("---\nname: Shared\n---\nWorkspace version"), 0644); err != nil {
		t.Fatalf("Failed to write shared.md: %v", err)
	}

	cache := NewPromptsCache()
	cache.SetAdditionalDirs([]string{additionalDir})
	cache.SetWorkspaceDirs(workspaceDir, []string{".prompts"})

	prompts, err := cache.Get()
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}

	// Should have only one prompt (workspace version wins)
	if len(prompts) != 1 {
		t.Errorf("len(prompts) = %d, want 1", len(prompts))
	}

	if prompts[0].Content != "Workspace version" {
		t.Errorf("prompts[0].Content = %q, want %q", prompts[0].Content, "Workspace version")
	}
}

func TestPromptsCache_ClearWorkspaceDirs(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Create default prompts directory
	defaultDir := filepath.Join(tmpDir, appdir.PromptsDirName)
	if err := os.MkdirAll(defaultDir, 0755); err != nil {
		t.Fatalf("Failed to create default prompts dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(defaultDir, "global.md"), []byte("---\nname: Global\n---\nGlobal content"), 0644); err != nil {
		t.Fatalf("Failed to write global.md: %v", err)
	}

	// Create workspace prompts
	workspaceDir := filepath.Join(tmpDir, "my-project")
	workspacePromptsDir := filepath.Join(workspaceDir, ".prompts")
	if err := os.MkdirAll(workspacePromptsDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace prompts dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspacePromptsDir, "local.md"), []byte("---\nname: Local\n---\nLocal content"), 0644); err != nil {
		t.Fatalf("Failed to write local.md: %v", err)
	}

	cache := NewPromptsCache()
	cache.SetWorkspaceDirs(workspaceDir, []string{".prompts"})

	// Should have 2 prompts
	prompts, _ := cache.Get()
	if len(prompts) != 2 {
		t.Errorf("Before clear: len(prompts) = %d, want 2", len(prompts))
	}

	// Clear workspace dirs
	cache.ClearWorkspaceDirs()

	// Should have only 1 prompt now
	prompts, _ = cache.Get()
	if len(prompts) != 1 {
		t.Errorf("After clear: len(prompts) = %d, want 1", len(prompts))
	}
}

func TestPromptsCache_NonExistentDirIgnored(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Create default prompts directory
	defaultDir := filepath.Join(tmpDir, appdir.PromptsDirName)
	if err := os.MkdirAll(defaultDir, 0755); err != nil {
		t.Fatalf("Failed to create default prompts dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(defaultDir, "test.md"), []byte("---\nname: Test\n---\nTest content"), 0644); err != nil {
		t.Fatalf("Failed to write test.md: %v", err)
	}

	cache := NewPromptsCache()
	// Set non-existent additional directory - should be silently ignored
	cache.SetAdditionalDirs([]string{"/nonexistent/path/to/prompts"})

	prompts, err := cache.Get()
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}

	// Should still have the default prompt
	if len(prompts) != 1 {
		t.Errorf("len(prompts) = %d, want 1", len(prompts))
	}
}

func TestPromptsCache_GetDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Create default prompts directory
	defaultDir := filepath.Join(tmpDir, appdir.PromptsDirName)
	if err := os.MkdirAll(defaultDir, 0755); err != nil {
		t.Fatalf("Failed to create default prompts dir: %v", err)
	}

	additionalDir := "/some/additional/dir"
	workspaceDir := filepath.Join(tmpDir, "my-project")

	cache := NewPromptsCache()
	cache.SetAdditionalDirs([]string{additionalDir})
	cache.SetWorkspaceDirs(workspaceDir, []string{".prompts", "/absolute/path"})

	dirs := cache.GetDirectories()

	// Should have: default, additional, workspace relative, workspace absolute
	if len(dirs) != 4 {
		t.Errorf("len(dirs) = %d, want 4", len(dirs))
	}

	// First should be default
	if dirs[0] != defaultDir {
		t.Errorf("dirs[0] = %q, want %q", dirs[0], defaultDir)
	}

	// Second should be additional
	if dirs[1] != additionalDir {
		t.Errorf("dirs[1] = %q, want %q", dirs[1], additionalDir)
	}

	// Third should be resolved relative path
	expectedRelative := filepath.Join(workspaceDir, ".prompts")
	if dirs[2] != expectedRelative {
		t.Errorf("dirs[2] = %q, want %q", dirs[2], expectedRelative)
	}

	// Fourth should be absolute path
	if dirs[3] != "/absolute/path" {
		t.Errorf("dirs[3] = %q, want %q", dirs[3], "/absolute/path")
	}
}
