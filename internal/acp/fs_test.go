package acp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOSFileSystem_ReadTextFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")
	content := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	fs := &OSFileSystem{}

	// Read entire file
	result, err := fs.ReadTextFile(path, nil, nil)
	if err != nil {
		t.Fatalf("ReadTextFile failed: %v", err)
	}
	if result != content {
		t.Errorf("Content = %q, want %q", result, content)
	}
}

func TestOSFileSystem_ReadTextFile_WithLine(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")
	content := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	fs := &OSFileSystem{}
	line := 3

	result, err := fs.ReadTextFile(path, &line, nil)
	if err != nil {
		t.Fatalf("ReadTextFile failed: %v", err)
	}

	expected := "Line 3\nLine 4\nLine 5"
	if result != expected {
		t.Errorf("Content = %q, want %q", result, expected)
	}
}

func TestOSFileSystem_ReadTextFile_WithLimit(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")
	content := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	fs := &OSFileSystem{}
	limit := 2

	result, err := fs.ReadTextFile(path, nil, &limit)
	if err != nil {
		t.Fatalf("ReadTextFile failed: %v", err)
	}

	expected := "Line 1\nLine 2"
	if result != expected {
		t.Errorf("Content = %q, want %q", result, expected)
	}
}

func TestOSFileSystem_ReadTextFile_WithLineAndLimit(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")
	content := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	fs := &OSFileSystem{}
	line := 2
	limit := 2

	result, err := fs.ReadTextFile(path, &line, &limit)
	if err != nil {
		t.Fatalf("ReadTextFile failed: %v", err)
	}

	expected := "Line 2\nLine 3"
	if result != expected {
		t.Errorf("Content = %q, want %q", result, expected)
	}
}

func TestOSFileSystem_ReadTextFile_RelativePath(t *testing.T) {
	fs := &OSFileSystem{}

	_, err := fs.ReadTextFile("relative/path.txt", nil, nil)
	if err == nil {
		t.Error("expected error for relative path")
	}
}

func TestOSFileSystem_ReadTextFile_NotFound(t *testing.T) {
	fs := &OSFileSystem{}

	_, err := fs.ReadTextFile("/nonexistent/file.txt", nil, nil)
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestOSFileSystem_WriteTextFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "output.txt")
	content := "Hello, World!"

	fs := &OSFileSystem{}

	err := fs.WriteTextFile(path, content)
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
}

func TestOSFileSystem_WriteTextFile_CreateDirs(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "subdir", "nested", "output.txt")
	content := "Nested content"

	fs := &OSFileSystem{}

	err := fs.WriteTextFile(path, content)
	if err != nil {
		t.Fatalf("WriteTextFile failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("file was not created")
	}
}

func TestOSFileSystem_WriteTextFile_RelativePath(t *testing.T) {
	fs := &OSFileSystem{}

	err := fs.WriteTextFile("relative/path.txt", "content")
	if err == nil {
		t.Error("expected error for relative path")
	}
}

func TestDefaultFileSystem(t *testing.T) {
	// Verify DefaultFileSystem is set
	if DefaultFileSystem == nil {
		t.Error("DefaultFileSystem should not be nil")
	}

	// Verify it's an OSFileSystem
	_, ok := DefaultFileSystem.(*OSFileSystem)
	if !ok {
		t.Errorf("DefaultFileSystem should be *OSFileSystem, got %T", DefaultFileSystem)
	}
}
