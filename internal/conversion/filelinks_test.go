package conversion

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileLinker_LinkFilePaths(t *testing.T) {
	// Create a temporary directory with test files
	tmpDir := t.TempDir()

	// Create test files
	testFile := filepath.Join(tmpDir, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(testFile), 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create a test directory with a file inside (directories need content to be useful)
	testDir := filepath.Join(tmpDir, "docs")
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	// Create an executable file (should be skipped)
	execFile := filepath.Join(tmpDir, "script.sh")
	if err := os.WriteFile(execFile, []byte("#!/bin/bash"), 0755); err != nil {
		t.Fatalf("Failed to create executable file: %v", err)
	}

	t.Logf("Test directory: %s", tmpDir)
	t.Logf("Test file: %s", testFile)

	linker := NewFileLinker(FileLinkerConfig{
		WorkingDir: tmpDir,
		Enabled:    true,
	})

	tests := []struct {
		name     string
		input    string
		contains string // Expected substring in output
		excludes string // Substring that should NOT be in output
	}{
		{
			name:     "relative path that exists",
			input:    "Check the file src/main.go for details",
			contains: `<a href="file://`,
		},
		{
			name:     "relative path with ./",
			input:    "See ./src/main.go",
			contains: `<a href="file://`,
		},
		{
			name:     "directory link",
			input:    "Look in ./docs for more",
			contains: `class="file-link dir-link"`,
		},
		{
			name:     "non-existent file",
			input:    "Check nonexistent/file.go",
			excludes: `<a href="file://`,
		},
		{
			name:     "executable file skipped",
			input:    "Run script.sh to start",
			excludes: `<a href="file://`,
		},
		{
			name:     "path inside inline code tag linked",
			input:    "<code>src/main.go</code>",
			contains: `<a href="file://`,
		},
		{
			name:     "path inside pre tag skipped",
			input:    "<pre>src/main.go</pre>",
			excludes: `<a href="file://`,
		},
		{
			name:     "path inside pre>code tag skipped",
			input:    "<pre><code>src/main.go</code></pre>",
			excludes: `<a href="file://`,
		},
		{
			name:     "path inside anchor tag skipped",
			input:    `<a href="http://example.com">src/main.go</a>`,
			excludes: `file://`,
		},
		{
			name:     "disabled linker returns unchanged",
			input:    "src/main.go",
			excludes: `<a href="file://`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use disabled linker for the "disabled" test
			l := linker
			if tt.name == "disabled linker returns unchanged" {
				l = NewFileLinker(FileLinkerConfig{
					WorkingDir: tmpDir,
					Enabled:    false,
				})
			}

			result := l.LinkFilePaths(tt.input)

			if tt.contains != "" {
				if !containsString(result, tt.contains) {
					t.Errorf("Expected result to contain %q, got: %s", tt.contains, result)
				}
			}

			if tt.excludes != "" {
				if containsString(result, tt.excludes) {
					t.Errorf("Expected result to NOT contain %q, got: %s", tt.excludes, result)
				}
			}
		})
	}
}

func TestFileLinker_SecurityChecks(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a sensitive file
	envFile := filepath.Join(tmpDir, ".env")
	if err := os.WriteFile(envFile, []byte("SECRET=value"), 0644); err != nil {
		t.Fatalf("Failed to create .env file: %v", err)
	}

	// Create a file outside workspace
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0644); err != nil {
		t.Fatalf("Failed to create outside file: %v", err)
	}

	linker := NewFileLinker(FileLinkerConfig{
		WorkingDir:            tmpDir,
		Enabled:               true,
		AllowOutsideWorkspace: false,
	})

	t.Run("sensitive file skipped", func(t *testing.T) {
		result := linker.LinkFilePaths("Check .env for config")
		if containsString(result, `<a href="file://`) {
			t.Errorf("Sensitive file should not be linked: %s", result)
		}
	})

	t.Run("file outside workspace skipped", func(t *testing.T) {
		result := linker.LinkFilePaths("See " + outsideFile)
		if containsString(result, `<a href="file://`) {
			t.Errorf("File outside workspace should not be linked: %s", result)
		}
	})
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestFileLinker_ProcessPath(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a test file
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Verify file exists
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("File doesn't exist: %v", err)
	}
	t.Logf("File mode: %v, isDir: %v", info.Mode(), info.IsDir())
	t.Logf("Execute bits: %v", info.Mode()&0111)

	// Resolve symlinks to see what EvalSymlinks returns
	realPath, err := filepath.EvalSymlinks(testFile)
	if err != nil {
		t.Fatalf("EvalSymlinks failed: %v", err)
	}
	t.Logf("Real path: %s", realPath)

	// Check workspace prefix
	absWorkDir, _ := filepath.Abs(tmpDir)
	t.Logf("Workspace: %s", absWorkDir)
	t.Logf("Has prefix: %v", strings.HasPrefix(realPath, absWorkDir))

	linker := NewFileLinker(FileLinkerConfig{
		WorkingDir: tmpDir,
		Enabled:    true,
	})

	// Test with relative path
	result := linker.processPath("test.txt")
	t.Logf("processPath('test.txt') = %q", result)
	if result == "" {
		t.Errorf("Expected link for test.txt, got empty string")
	}

	// Test with absolute path
	result = linker.processPath(testFile)
	t.Logf("processPath(%q) = %q", testFile, result)
	if result == "" {
		t.Errorf("Expected link for %s, got empty string", testFile)
	}
}

func TestFileLinker_AllowOutsideWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	outsideDir := t.TempDir()

	// Create a file outside workspace
	outsideFile := filepath.Join(outsideDir, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0644); err != nil {
		t.Fatalf("Failed to create outside file: %v", err)
	}

	linker := NewFileLinker(FileLinkerConfig{
		WorkingDir:            tmpDir,
		Enabled:               true,
		AllowOutsideWorkspace: true, // Allow outside workspace
	})

	result := linker.LinkFilePaths("See " + outsideFile)
	if !containsString(result, `<a href="file://`) {
		t.Errorf("File outside workspace should be linked when allowed: %s", result)
	}
}

func TestFileLinker_MaxPathsLimit(t *testing.T) {
	tmpDir := t.TempDir()

	// Create multiple test files
	for i := 0; i < 10; i++ {
		file := filepath.Join(tmpDir, "file"+string(rune('0'+i))+".txt")
		if err := os.WriteFile(file, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	linker := NewFileLinker(FileLinkerConfig{
		WorkingDir:         tmpDir,
		Enabled:            true,
		MaxPathsPerMessage: 3, // Only process 3 paths
	})

	// Input with many paths
	input := "file0.txt file1.txt file2.txt file3.txt file4.txt"
	result := linker.LinkFilePaths(input)

	// Count how many links were created
	linkCount := countOccurrences(result, `<a href="file://`)
	if linkCount > 3 {
		t.Errorf("Expected at most 3 links, got %d", linkCount)
	}
}

func TestFileLinker_SymlinkSecurity(t *testing.T) {
	tmpDir := t.TempDir()
	outsideDir := t.TempDir()

	// Create a file outside workspace
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0644); err != nil {
		t.Fatalf("Failed to create outside file: %v", err)
	}

	// Create a symlink inside workspace pointing outside
	symlinkPath := filepath.Join(tmpDir, "link.txt")
	if err := os.Symlink(outsideFile, symlinkPath); err != nil {
		t.Skip("Symlinks not supported on this system")
	}

	linker := NewFileLinker(FileLinkerConfig{
		WorkingDir:            tmpDir,
		Enabled:               true,
		AllowOutsideWorkspace: false,
	})

	result := linker.LinkFilePaths("See link.txt")
	if containsString(result, `<a href="file://`) {
		t.Errorf("Symlink pointing outside workspace should not be linked: %s", result)
	}
}

func TestFileLinker_AbsolutePath(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a test file
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	linker := NewFileLinker(FileLinkerConfig{
		WorkingDir: tmpDir,
		Enabled:    true,
	})

	result := linker.LinkFilePaths("See " + testFile)
	if !containsString(result, `<a href="file://`) {
		t.Errorf("Absolute path within workspace should be linked: %s", result)
	}
}

func TestFileLinker_MultiplePaths(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test files in subdirectory (paths need / to be detected)
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("Failed to create src directory: %v", err)
	}
	file1 := filepath.Join(srcDir, "file1.txt")
	file2 := filepath.Join(srcDir, "file2.txt")
	if err := os.WriteFile(file1, []byte("content1"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	if err := os.WriteFile(file2, []byte("content2"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	linker := NewFileLinker(FileLinkerConfig{
		WorkingDir: tmpDir,
		Enabled:    true,
	})

	result := linker.LinkFilePaths("Check src/file1.txt and src/file2.txt")
	linkCount := countOccurrences(result, `<a href="file://`)
	if linkCount != 2 {
		t.Errorf("Expected 2 links, got %d: %s", linkCount, result)
	}
}

func countOccurrences(s, substr string) int {
	count := 0
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			count++
		}
	}
	return count
}

func TestFileLinker_HTTPLinks(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test file in subdirectory (paths need / to be detected)
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("Failed to create src directory: %v", err)
	}
	testFile := filepath.Join(srcDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	linker := NewFileLinker(FileLinkerConfig{
		WorkingDir:    tmpDir,
		WorkspaceUUID: "test-uuid-123",
		Enabled:       true,
		UseHTTPLinks:  true,
		APIPrefix:     "/mitto",
	})

	result := linker.LinkFilePaths("See src/main.go for details")

	// Should generate HTTP link, not file:// link
	if containsString(result, "file://") {
		t.Errorf("Should not contain file:// links: %s", result)
	}
	if !containsString(result, `/mitto/api/files?ws=test-uuid-123`) {
		t.Errorf("Should contain HTTP link with workspace UUID: %s", result)
	}
	if !containsString(result, `path=src%2Fmain.go`) {
		t.Errorf("Should contain URL-encoded path: %s", result)
	}
}

func TestFileLinker_InlineCodeHTTPLinks(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test file in subdirectory (paths need / to be detected)
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("Failed to create src directory: %v", err)
	}
	testFile := filepath.Join(srcDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	linker := NewFileLinker(FileLinkerConfig{
		WorkingDir:    tmpDir,
		WorkspaceUUID: "test-uuid-456",
		Enabled:       true,
		UseHTTPLinks:  true,
		APIPrefix:     "/mitto",
	})

	// Test inline code tag (backtick-enclosed in markdown)
	result := linker.LinkFilePaths("Check <code>src/main.go</code> for details")

	// Should wrap the code tag in a link
	if !containsString(result, `<a href="/mitto/api/files?ws=test-uuid-456`) {
		t.Errorf("Should contain HTTP link with workspace UUID: %s", result)
	}
	if !containsString(result, `<code>src/main.go</code></a>`) {
		t.Errorf("Should wrap code tag in link: %s", result)
	}
	if containsString(result, "file://") {
		t.Errorf("Should not contain file:// links: %s", result)
	}
}
