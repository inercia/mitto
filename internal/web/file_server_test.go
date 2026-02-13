package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/config"
)

func TestFileServer_ServeFile(t *testing.T) {
	// Create a temporary workspace with test files
	tmpDir := t.TempDir()

	// Create a test file
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("Hello, World!"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create a subdirectory with a file
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("Failed to create subdirectory: %v", err)
	}
	subFile := filepath.Join(subDir, "nested.txt")
	if err := os.WriteFile(subFile, []byte("Nested content"), 0644); err != nil {
		t.Fatalf("Failed to create nested file: %v", err)
	}

	// Create an executable file (should be blocked)
	execFile := filepath.Join(tmpDir, "script.sh")
	if err := os.WriteFile(execFile, []byte("#!/bin/bash"), 0755); err != nil {
		t.Fatalf("Failed to create executable file: %v", err)
	}

	// Create a sensitive file (should be blocked)
	envFile := filepath.Join(tmpDir, ".env")
	if err := os.WriteFile(envFile, []byte("SECRET=value"), 0644); err != nil {
		t.Fatalf("Failed to create .env file: %v", err)
	}

	// Create a session manager with the workspace
	workspaceUUID := "test-workspace-uuid-123"
	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces: []config.WorkspaceSettings{{
			UUID:       workspaceUUID,
			WorkingDir: tmpDir,
			ACPServer:  "test",
			ACPCommand: "echo test",
		}},
	})

	fs := NewFileServer(sm, nil)

	tests := []struct {
		name           string
		wsUUID         string
		path           string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "valid file",
			wsUUID:         workspaceUUID,
			path:           "test.txt",
			expectedStatus: http.StatusOK,
			expectedBody:   "Hello, World!",
		},
		{
			name:           "nested file",
			wsUUID:         workspaceUUID,
			path:           "subdir/nested.txt",
			expectedStatus: http.StatusOK,
			expectedBody:   "Nested content",
		},
		{
			name:           "non-existent file",
			wsUUID:         workspaceUUID,
			path:           "nonexistent.txt",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "executable file blocked",
			wsUUID:         workspaceUUID,
			path:           "script.sh",
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "sensitive file blocked",
			wsUUID:         workspaceUUID,
			path:           ".env",
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "path traversal blocked",
			wsUUID:         workspaceUUID,
			path:           "../etc/passwd",
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "absolute path blocked",
			wsUUID:         workspaceUUID,
			path:           "/etc/passwd",
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "invalid workspace",
			wsUUID:         "nonexistent-uuid",
			path:           "test.txt",
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "missing workspace",
			wsUUID:         "",
			path:           "test.txt",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "missing path",
			wsUUID:         workspaceUUID,
			path:           "",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "directory access blocked",
			wsUUID:         workspaceUUID,
			path:           "subdir",
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/files?ws="+tt.wsUUID+"&path="+tt.path, nil)
			w := httptest.NewRecorder()

			fs.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d. Body: %s", tt.expectedStatus, w.Code, w.Body.String())
			}

			if tt.expectedBody != "" && w.Body.String() != tt.expectedBody {
				t.Errorf("Expected body %q, got %q", tt.expectedBody, w.Body.String())
			}
		})
	}
}

func TestFileServer_SymlinkSecurity(t *testing.T) {
	// Create two temporary directories
	workspaceDir := t.TempDir()
	outsideDir := t.TempDir()

	// Create a file outside the workspace
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret data"), 0644); err != nil {
		t.Fatalf("Failed to create outside file: %v", err)
	}

	// Create a symlink inside the workspace pointing outside
	symlinkPath := filepath.Join(workspaceDir, "link.txt")
	if err := os.Symlink(outsideFile, symlinkPath); err != nil {
		t.Skip("Symlinks not supported on this system")
	}

	// Create a session manager with the workspace
	wsUUID := "symlink-test-uuid"
	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces: []config.WorkspaceSettings{{
			UUID:       wsUUID,
			WorkingDir: workspaceDir,
			ACPServer:  "test",
			ACPCommand: "echo test",
		}},
	})

	fs := NewFileServer(sm, nil)

	req := httptest.NewRequest("GET", "/api/files?ws="+wsUUID+"&path=link.txt", nil)
	w := httptest.NewRecorder()

	fs.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Symlink pointing outside workspace should be blocked, got status %d", w.Code)
	}
}

func TestFileServer_MethodNotAllowed(t *testing.T) {
	wsUUID := "method-test-uuid"
	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces: []config.WorkspaceSettings{{
			UUID:       wsUUID,
			WorkingDir: "/tmp",
			ACPServer:  "test",
			ACPCommand: "echo test",
		}},
	})

	fs := NewFileServer(sm, nil)

	methods := []string{"POST", "PUT", "DELETE", "PATCH"}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/files?ws="+wsUUID+"&path=test.txt", nil)
			w := httptest.NewRecorder()

			fs.ServeHTTP(w, req)

			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("Expected status 405 for %s, got %d", method, w.Code)
			}
		})
	}
}

func TestFileServer_ContentType(t *testing.T) {
	tmpDir := t.TempDir()

	// Create files with different extensions
	files := map[string]string{
		"test.txt":  "text/plain; charset=utf-8",
		"test.html": "text/html; charset=utf-8",
		"test.json": "application/json",
		"test.css":  "text/css; charset=utf-8",
		"test.js":   "text/javascript; charset=utf-8",
	}

	for filename := range files {
		path := filepath.Join(tmpDir, filename)
		if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create %s: %v", filename, err)
		}
	}

	wsUUID := "content-type-test-uuid"
	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces: []config.WorkspaceSettings{{
			UUID:       wsUUID,
			WorkingDir: tmpDir,
			ACPServer:  "test",
			ACPCommand: "echo test",
		}},
	})

	fs := NewFileServer(sm, nil)

	for filename, expectedType := range files {
		t.Run(filename, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/files?ws="+wsUUID+"&path="+filename, nil)
			w := httptest.NewRecorder()

			fs.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Expected status 200, got %d", w.Code)
				return
			}

			contentType := w.Header().Get("Content-Type")
			if contentType != expectedType {
				t.Errorf("Expected Content-Type %q, got %q", expectedType, contentType)
			}
		})
	}
}

func TestFileServer_ActiveSessionWorkspace(t *testing.T) {
	// Create a temporary workspace
	sessionWorkspace := t.TempDir()
	wsUUID := "active-session-test-uuid"

	// Create a test file in the session workspace
	testFile := filepath.Join(sessionWorkspace, "session-file.txt")
	if err := os.WriteFile(testFile, []byte("Session content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create a session manager with the workspace configured
	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces: []config.WorkspaceSettings{{
			UUID:       wsUUID,
			WorkingDir: sessionWorkspace,
			ACPServer:  "test",
			ACPCommand: "echo test",
		}},
	})

	// Add an active session with a working directory
	sm.mu.Lock()
	sm.sessions["test-session"] = &BackgroundSession{
		persistedID:   "test-session",
		workingDir:    sessionWorkspace,
		workspaceUUID: wsUUID,
	}
	sm.mu.Unlock()

	fs := NewFileServer(sm, nil)

	// Test that we can access files from the active session's workspace using UUID
	req := httptest.NewRequest("GET", "/api/files?ws="+wsUUID+"&path=session-file.txt", nil)
	w := httptest.NewRecorder()
	fs.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for active session workspace, got %d. Body: %s", w.Code, w.Body.String())
	}

	if w.Body.String() != "Session content" {
		t.Errorf("Expected body %q, got %q", "Session content", w.Body.String())
	}

	// Test that invalid UUIDs are forbidden
	req = httptest.NewRequest("GET", "/api/files?ws=nonexistent-uuid&path=test.txt", nil)
	w = httptest.NewRecorder()
	fs.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected status 403 for invalid UUID, got %d", w.Code)
	}
}

func TestFileURLBuilder(t *testing.T) {
	tests := []struct {
		name          string
		apiPrefix     string
		useHTTP       bool
		workspaceUUID string
		relativePath  string
		absolutePath  string
		expected      string
	}{
		{
			name:          "HTTP mode",
			apiPrefix:     "/mitto",
			useHTTP:       true,
			workspaceUUID: "test-uuid-123",
			relativePath:  "src/main.go",
			absolutePath:  "/home/user/project/src/main.go",
			expected:      "/mitto/api/files?ws=test-uuid-123&path=src%2Fmain.go",
		},
		{
			name:          "file:// mode",
			apiPrefix:     "/mitto",
			useHTTP:       false,
			workspaceUUID: "test-uuid-123",
			relativePath:  "src/main.go",
			absolutePath:  "/home/user/project/src/main.go",
			expected:      "file:///home/user/project/src/main.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := &FileURLBuilder{
				APIPrefix:     tt.apiPrefix,
				UseHTTP:       tt.useHTTP,
				WorkspaceUUID: tt.workspaceUUID,
			}

			result := builder.BuildURL(tt.relativePath, tt.absolutePath)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestFileServer_MarkdownRendering(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a markdown file with various features
	mdContent := `# Test Document

This is a **bold** and *italic* test.

## Code Example

` + "```go" + `
func main() {
    fmt.Println("Hello, World!")
}
` + "```" + `

## List

- Item 1
- Item 2
- Item 3

## Table

| Name | Value |
|------|-------|
| Foo  | Bar   |
`
	mdFile := filepath.Join(tmpDir, "test.md")
	if err := os.WriteFile(mdFile, []byte(mdContent), 0644); err != nil {
		t.Fatalf("Failed to create markdown file: %v", err)
	}

	// Create a .markdown file (alternative extension)
	markdownFile := filepath.Join(tmpDir, "readme.markdown")
	if err := os.WriteFile(markdownFile, []byte("# README\n\nThis is a readme."), 0644); err != nil {
		t.Fatalf("Failed to create .markdown file: %v", err)
	}

	// Create a non-markdown file
	txtFile := filepath.Join(tmpDir, "plain.txt")
	if err := os.WriteFile(txtFile, []byte("Plain text content"), 0644); err != nil {
		t.Fatalf("Failed to create text file: %v", err)
	}

	wsUUID := "markdown-test-uuid"
	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces: []config.WorkspaceSettings{{
			UUID:       wsUUID,
			WorkingDir: tmpDir,
			ACPServer:  "test",
			ACPCommand: "echo test",
		}},
	})

	fs := NewFileServer(sm, nil)

	tests := []struct {
		name           string
		path           string
		render         string
		expectedStatus int
		checkHTML      bool
		checkContains  []string
	}{
		{
			name:           "render markdown as HTML",
			path:           "test.md",
			render:         "html",
			expectedStatus: http.StatusOK,
			checkHTML:      true,
			checkContains:  []string{"<!DOCTYPE html>", "<h1", "Test Document", "<strong>bold</strong>", "<em>italic</em>"},
		},
		{
			name:           "render .markdown extension",
			path:           "readme.markdown",
			render:         "html",
			expectedStatus: http.StatusOK,
			checkHTML:      true,
			checkContains:  []string{"<!DOCTYPE html>", "<h1", "README"},
		},
		{
			name:           "raw markdown without render param",
			path:           "test.md",
			render:         "",
			expectedStatus: http.StatusOK,
			checkHTML:      false,
			checkContains:  []string{"# Test Document"},
		},
		{
			name:           "render non-markdown file fails",
			path:           "plain.txt",
			render:         "html",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "render non-existent file",
			path:           "nonexistent.md",
			render:         "html",
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/api/files?ws=" + wsUUID + "&path=" + tt.path
			if tt.render != "" {
				url += "&render=" + tt.render
			}
			req := httptest.NewRequest("GET", url, nil)
			w := httptest.NewRecorder()

			fs.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d. Body: %s", tt.expectedStatus, w.Code, w.Body.String())
				return
			}

			if tt.expectedStatus != http.StatusOK {
				return
			}

			// Check content type
			contentType := w.Header().Get("Content-Type")
			if tt.checkHTML {
				if contentType != "text/html; charset=utf-8" {
					t.Errorf("Expected Content-Type 'text/html; charset=utf-8', got %q", contentType)
				}
			}

			// Check for expected content
			body := w.Body.String()
			for _, expected := range tt.checkContains {
				if !contains(body, expected) {
					t.Errorf("Expected body to contain %q, but it didn't. Body length: %d", expected, len(body))
				}
			}
		})
	}
}

func TestFileServer_MarkdownRenderingSecurityHeaders(t *testing.T) {
	tmpDir := t.TempDir()

	mdFile := filepath.Join(tmpDir, "test.md")
	if err := os.WriteFile(mdFile, []byte("# Test"), 0644); err != nil {
		t.Fatalf("Failed to create markdown file: %v", err)
	}

	wsUUID := "md-security-test-uuid"
	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces: []config.WorkspaceSettings{{
			UUID:       wsUUID,
			WorkingDir: tmpDir,
			ACPServer:  "test",
			ACPCommand: "echo test",
		}},
	})

	fs := NewFileServer(sm, nil)

	req := httptest.NewRequest("GET", "/api/files?ws="+wsUUID+"&path=test.md&render=html", nil)
	w := httptest.NewRecorder()

	fs.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	// Check security headers
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("Expected X-Content-Type-Options: nosniff")
	}

	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Error("Expected Content-Security-Policy header to be set")
	}
}

func TestFileServer_MarkdownRenderingCodeHighlighting(t *testing.T) {
	tmpDir := t.TempDir()

	// Create markdown with code block
	mdContent := "# Code\n\n```go\nfunc main() {}\n```"
	mdFile := filepath.Join(tmpDir, "code.md")
	if err := os.WriteFile(mdFile, []byte(mdContent), 0644); err != nil {
		t.Fatalf("Failed to create markdown file: %v", err)
	}

	wsUUID := "md-code-test-uuid"
	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces: []config.WorkspaceSettings{{
			UUID:       wsUUID,
			WorkingDir: tmpDir,
			ACPServer:  "test",
			ACPCommand: "echo test",
		}},
	})

	fs := NewFileServer(sm, nil)

	req := httptest.NewRequest("GET", "/api/files?ws="+wsUUID+"&path=code.md&render=html", nil)
	w := httptest.NewRecorder()

	fs.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Check that code is wrapped in pre/code tags
	if !contains(body, "<pre") || !contains(body, "<code") {
		t.Errorf("Expected code block to be wrapped in pre/code tags. Body: %s", body)
	}

	// Check that the code content is present (may be HTML-escaped or syntax highlighted)
	// The function keyword should be present in some form
	if !contains(body, "func") {
		t.Errorf("Expected code content 'func' to be present. Body: %s", body)
	}
}

func TestFileServer_MarkdownRenderingDarkLightMode(t *testing.T) {
	tmpDir := t.TempDir()

	mdFile := filepath.Join(tmpDir, "theme.md")
	if err := os.WriteFile(mdFile, []byte("# Theme Test"), 0644); err != nil {
		t.Fatalf("Failed to create markdown file: %v", err)
	}

	wsUUID := "md-theme-test-uuid"
	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces: []config.WorkspaceSettings{{
			UUID:       wsUUID,
			WorkingDir: tmpDir,
			ACPServer:  "test",
			ACPCommand: "echo test",
		}},
	})

	fs := NewFileServer(sm, nil)

	req := httptest.NewRequest("GET", "/api/files?ws="+wsUUID+"&path=theme.md&render=html", nil)
	w := httptest.NewRecorder()

	fs.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Check that CSS variables are defined for theming
	if !contains(body, ":root") {
		t.Error("Expected :root CSS variables for theming")
	}

	// Check that prefers-color-scheme media query is present for light mode
	if !contains(body, "prefers-color-scheme: light") {
		t.Error("Expected prefers-color-scheme media query for light mode support")
	}
}

// contains checks if s contains substr (case-sensitive)
func contains(s, substr string) bool {
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
