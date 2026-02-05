// Package web provides the web interface for Mitto.
package web

import (
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// FileServer provides secure file serving from workspace directories.
// It enforces strict security checks to prevent unauthorized file access.
type FileServer struct {
	sessionManager *SessionManager
	logger         *slog.Logger
}

// NewFileServer creates a new FileServer.
func NewFileServer(sessionManager *SessionManager, logger *slog.Logger) *FileServer {
	return &FileServer{
		sessionManager: sessionManager,
		logger:         logger,
	}
}

// ServeHTTP handles file serving requests.
// URL format: /api/files?ws={workspace_uuid}&path={relative_path}
func (fs *FileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get path from query parameters
	relativePath := r.URL.Query().Get("path")
	if relativePath == "" {
		http.Error(w, "Missing path parameter", http.StatusBadRequest)
		return
	}

	// Get workspace - support both UUID (ws) and legacy path (workspace) parameters
	wsUUID := r.URL.Query().Get("ws")
	legacyWorkspace := r.URL.Query().Get("workspace")

	var workspacePath string
	if wsUUID != "" {
		// New format: resolve UUID to workspace path
		var found bool
		workspacePath, found = fs.sessionManager.ResolveWorkspaceIdentifier(wsUUID)
		if !found {
			fs.logSecurityEvent("invalid_workspace_uuid", wsUUID, relativePath, r)
			http.Error(w, "Invalid workspace", http.StatusForbidden)
			return
		}
	} else if legacyWorkspace != "" {
		// Legacy format: use workspace path directly (will be validated in serveFile)
		workspacePath = legacyWorkspace
	} else {
		http.Error(w, "Missing ws or workspace parameter", http.StatusBadRequest)
		return
	}

	// Validate and serve the file
	fs.serveFile(w, r, workspacePath, relativePath)
}

// serveFile validates the request and serves the file if allowed.
func (fs *FileServer) serveFile(w http.ResponseWriter, r *http.Request, workspace, relativePath string) {
	// Security check 1: Validate workspace is registered
	if !fs.isValidWorkspace(workspace) {
		fs.logSecurityEvent("invalid_workspace", workspace, relativePath, r)
		http.Error(w, "Invalid workspace", http.StatusForbidden)
		return
	}

	// Security check 2: Clean and validate the relative path
	cleanPath := filepath.Clean(relativePath)
	if strings.HasPrefix(cleanPath, "..") || filepath.IsAbs(cleanPath) {
		fs.logSecurityEvent("path_traversal_attempt", workspace, relativePath, r)
		http.Error(w, "Invalid path", http.StatusForbidden)
		return
	}

	// Construct the full path
	fullPath := filepath.Join(workspace, cleanPath)

	// Security check 3: Resolve symlinks and verify still within workspace
	realPath, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "File not found", http.StatusNotFound)
		} else {
			fs.logSecurityEvent("symlink_resolution_failed", workspace, relativePath, r)
			http.Error(w, "Invalid path", http.StatusForbidden)
		}
		return
	}

	// Resolve workspace symlinks too for consistent comparison
	realWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		fs.logSecurityEvent("workspace_resolution_failed", workspace, relativePath, r)
		http.Error(w, "Invalid workspace", http.StatusForbidden)
		return
	}

	// Verify the resolved path is within the resolved workspace
	if !strings.HasPrefix(realPath, realWorkspace+string(filepath.Separator)) && realPath != realWorkspace {
		fs.logSecurityEvent("symlink_escape_attempt", workspace, relativePath, r)
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	// Security check 4: Get file info and validate
	info, err := os.Stat(realPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "File not found", http.StatusNotFound)
		} else {
			http.Error(w, "Error accessing file", http.StatusInternalServerError)
		}
		return
	}

	// Security check 5: Don't serve directories
	if info.IsDir() {
		http.Error(w, "Cannot serve directories", http.StatusForbidden)
		return
	}

	// Security check 6: Don't serve executable files
	if info.Mode()&0111 != 0 {
		fs.logSecurityEvent("executable_file_blocked", workspace, relativePath, r)
		http.Error(w, "Cannot serve executable files", http.StatusForbidden)
		return
	}

	// Security check 7: Don't serve sensitive files
	if isSensitiveFile(realPath) {
		fs.logSecurityEvent("sensitive_file_blocked", workspace, relativePath, r)
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	// Open and serve the file
	file, err := os.Open(realPath)
	if err != nil {
		http.Error(w, "Error opening file", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// Set content type based on extension
	contentType := mime.TypeByExtension(filepath.Ext(realPath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)

	// Set security headers
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")

	// Serve the file
	http.ServeContent(w, r, filepath.Base(realPath), info.ModTime(), file)
}

// isValidWorkspace checks if the given path is a registered workspace or
// the working directory of an active session.
func (fs *FileServer) isValidWorkspace(workspace string) bool {
	if fs.sessionManager == nil {
		return false
	}

	// Check configured workspaces
	workspaces := fs.sessionManager.GetWorkspaces()
	for _, ws := range workspaces {
		if ws.WorkingDir == workspace {
			return true
		}
	}

	// Check active session working directories
	// This allows file access for sessions started with working directories
	// that may not be explicitly configured as workspaces
	activeDirs := fs.sessionManager.GetActiveWorkingDirs()
	for _, dir := range activeDirs {
		if dir == workspace {
			return true
		}
	}

	return false
}

// logSecurityEvent logs a security-related event.
func (fs *FileServer) logSecurityEvent(event, workspace, path string, r *http.Request) {
	if fs.logger != nil {
		fs.logger.Warn("File server security event",
			"event", event,
			"workspace", workspace,
			"path", path,
			"client_ip", r.RemoteAddr,
			"user_agent", r.UserAgent(),
		)
	}
}

// isSensitiveFile checks if a file path matches sensitive patterns.
// This is a copy of the logic from filelinks.go to avoid circular imports.
func isSensitiveFile(path string) bool {
	sensitivePatterns := []string{
		".env",
		"id_rsa", "id_ed25519", "id_ecdsa", "id_dsa",
		".pem", ".key", ".p12", ".pfx",
		".aws/credentials",
		".netrc",
		".npmrc",
		".pypirc",
		".docker/config.json",
		".git-credentials",
		".ssh/config",
		"known_hosts",
		"authorized_keys",
		"/etc/shadow",
		"/etc/passwd",
	}

	normalized := strings.ToLower(path)
	for _, pattern := range sensitivePatterns {
		if strings.Contains(normalized, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

// FileURLBuilder builds URLs for file links based on the serving mode.
type FileURLBuilder struct {
	// APIPrefix is the URL prefix for API endpoints (e.g., "/mitto")
	APIPrefix string
	// UseHTTP indicates whether to generate HTTP URLs (true) or file:// URLs (false)
	UseHTTP bool
	// WorkspaceUUID is the UUID of the workspace for HTTP URLs
	WorkspaceUUID string
}

// BuildURL creates a URL for accessing a file.
// For HTTP mode, returns a URL like: /api/files?ws={uuid}&path={path}
// For file:// mode, returns a URL like: file:///absolute/path
func (b *FileURLBuilder) BuildURL(relativePath, absolutePath string) string {
	if b.UseHTTP {
		// URL-encode the parameters using standard library
		return b.APIPrefix + "/api/files?ws=" + url.QueryEscape(b.WorkspaceUUID) + "&path=" + url.QueryEscape(relativePath)
	}
	// Return file:// URL
	return "file://" + absolutePath
}

// ServeFileContent serves a file's content directly (for inline viewing).
// This is a convenience method that reads the entire file into memory.
func (fs *FileServer) ServeFileContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	relativePath := r.URL.Query().Get("path")
	if relativePath == "" {
		http.Error(w, "Missing path parameter", http.StatusBadRequest)
		return
	}

	// Get workspace - support both UUID (ws) and legacy path (workspace) parameters
	wsUUID := r.URL.Query().Get("ws")
	legacyWorkspace := r.URL.Query().Get("workspace")

	var workspace string
	if wsUUID != "" {
		// New format: resolve UUID to workspace path
		var found bool
		workspace, found = fs.sessionManager.ResolveWorkspaceIdentifier(wsUUID)
		if !found {
			http.Error(w, "Invalid workspace", http.StatusForbidden)
			return
		}
	} else if legacyWorkspace != "" {
		// Legacy format: use workspace path directly (will be validated below)
		workspace = legacyWorkspace
	} else {
		http.Error(w, "Missing ws or workspace parameter", http.StatusBadRequest)
		return
	}

	// Reuse the same security checks
	if !fs.isValidWorkspace(workspace) {
		http.Error(w, "Invalid workspace", http.StatusForbidden)
		return
	}

	cleanPath := filepath.Clean(relativePath)
	if strings.HasPrefix(cleanPath, "..") || filepath.IsAbs(cleanPath) {
		http.Error(w, "Invalid path", http.StatusForbidden)
		return
	}

	fullPath := filepath.Join(workspace, cleanPath)
	realPath, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	realWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		http.Error(w, "Invalid workspace", http.StatusForbidden)
		return
	}

	if !strings.HasPrefix(realPath, realWorkspace+string(filepath.Separator)) && realPath != realWorkspace {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	info, err := os.Stat(realPath)
	if err != nil || info.IsDir() || info.Mode()&0111 != 0 || isSensitiveFile(realPath) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	// Read file content
	content, err := io.ReadAll(io.LimitReader(mustOpen(realPath), 10*1024*1024)) // 10MB limit
	if err != nil {
		http.Error(w, "Error reading file", http.StatusInternalServerError)
		return
	}

	contentType := mime.TypeByExtension(filepath.Ext(realPath))
	if contentType == "" {
		contentType = "text/plain; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(content)
}

// mustOpen opens a file and panics on error (for use with LimitReader).
func mustOpen(path string) io.Reader {
	f, err := os.Open(path)
	if err != nil {
		return strings.NewReader("")
	}
	return f
}
