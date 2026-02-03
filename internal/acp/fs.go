// Package acp provides ACP (Agent Communication Protocol) client implementation.
package acp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileSystem defines the interface for file operations used by ACP clients.
// This interface enables testing without actual file I/O.
type FileSystem interface {
	// ReadTextFile reads a text file and returns its content.
	// If line and limit are provided, only the specified range of lines is returned.
	ReadTextFile(path string, line, limit *int) (string, error)

	// WriteTextFile writes content to a text file, creating parent directories if needed.
	WriteTextFile(path, content string) error
}

// OSFileSystem implements FileSystem using the real operating system.
type OSFileSystem struct{}

// Ensure OSFileSystem implements FileSystem at compile time.
var _ FileSystem = (*OSFileSystem)(nil)

// ReadTextFile reads a text file from the filesystem.
// If line is provided, reading starts from that line (1-based).
// If limit is provided, at most that many lines are returned.
func (fs *OSFileSystem) ReadTextFile(path string, line, limit *int) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be absolute: %s", path)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	content := string(b)

	// Apply line/limit filtering if specified
	if line != nil || limit != nil {
		lines := strings.Split(content, "\n")
		start := 0
		if line != nil && *line > 0 {
			start = min(max(*line-1, 0), len(lines))
		}
		end := len(lines)
		if limit != nil && *limit > 0 {
			if start+*limit < end {
				end = start + *limit
			}
		}
		content = strings.Join(lines[start:end], "\n")
	}

	return content, nil
}

// WriteTextFile writes content to a text file.
// Parent directories are created if they don't exist.
func (fs *OSFileSystem) WriteTextFile(path, content string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path must be absolute: %s", path)
	}

	dir := filepath.Dir(path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}

// DefaultFileSystem is the default FileSystem implementation using the real OS.
var DefaultFileSystem FileSystem = &OSFileSystem{}
