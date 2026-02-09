// Package web provides the web interface for Mitto.
package web

import (
	"fmt"
	"html"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/inercia/mitto/internal/conversion"
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

	// Check for render parameter - render markdown as HTML
	renderMode := r.URL.Query().Get("render")
	if renderMode == "html" {
		ext := strings.ToLower(filepath.Ext(realPath))
		if ext != ".md" && ext != ".markdown" {
			http.Error(w, "Render not supported for this file type", http.StatusBadRequest)
			return
		}
		fs.serveRenderedMarkdown(w, realPath, relativePath)
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
	ext := strings.ToLower(filepath.Ext(realPath))
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)

	// Set security headers
	w.Header().Set("X-Content-Type-Options", "nosniff")

	// For HTML files, use a more permissive CSP to allow the content to render properly
	// with its own styles and scripts. For other files, use strict CSP.
	if ext == ".html" || ext == ".htm" {
		// Allow inline styles/scripts and same-origin resources for HTML files
		w.Header().Set("Content-Security-Policy", "default-src 'self' 'unsafe-inline' 'unsafe-eval'; img-src 'self' data: https:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline' 'unsafe-eval'")
	} else {
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
	}

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

// maxMarkdownRenderSize is the maximum file size for markdown rendering (10MB).
const maxMarkdownRenderSize = 10 * 1024 * 1024

// serveRenderedMarkdown reads a markdown file, converts it to HTML, and serves
// it as a complete HTML document with styling.
func (fs *FileServer) serveRenderedMarkdown(w http.ResponseWriter, realPath, displayPath string) {
	// Read markdown content
	content, err := os.ReadFile(realPath)
	if err != nil {
		http.Error(w, "Error reading file", http.StatusInternalServerError)
		return
	}

	// Apply size limit
	if len(content) > maxMarkdownRenderSize {
		http.Error(w, "File too large for rendering", http.StatusRequestEntityTooLarge)
		return
	}

	// Convert markdown to HTML using existing converter
	converter := conversion.NewConverter(
		conversion.WithHighlighting("monokai"),
		conversion.WithSanitization(conversion.CreateSanitizer()),
	)
	htmlContent, err := converter.Convert(string(content))
	if err != nil {
		http.Error(w, "Error rendering markdown", http.StatusInternalServerError)
		return
	}

	// Wrap in complete HTML document
	fullHTML := wrapMarkdownHTML(htmlContent, displayPath)

	// Set headers
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Permissive CSP for styled HTML with syntax highlighting and Mermaid.js CDN
	w.Header().Set("Content-Security-Policy",
		"default-src 'self' 'unsafe-inline'; img-src 'self' data: https:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net")

	w.Write([]byte(fullHTML))
}

// wrapMarkdownHTML wraps rendered markdown content in a complete HTML document
// with styling that supports both light and dark modes.
func wrapMarkdownHTML(content, filename string) string {
	title := html.EscapeString(filepath.Base(filename))
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>%s - Mitto</title>
    <style>
        :root {
            --bg: #0d1117;
            --text: #c9d1d9;
            --text-muted: #8b949e;
            --border: #30363d;
            --code-bg: #161b22;
            --link: #58a6ff;
            --header-border: #21262d;
        }
        @media (prefers-color-scheme: light) {
            :root {
                --bg: #ffffff;
                --text: #24292f;
                --text-muted: #57606a;
                --border: #d0d7de;
                --code-bg: #f6f8fa;
                --link: #0969da;
                --header-border: #d0d7de;
            }
        }
        * { box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
            background: var(--bg);
            color: var(--text);
            line-height: 1.6;
            max-width: 900px;
            margin: 0 auto;
            padding: 2rem 2rem 4rem;
        }
        h1, h2, h3, h4, h5, h6 { margin: 1.5em 0 0.5em; font-weight: 600; color: var(--text); }
        h1 { font-size: 2em; border-bottom: 1px solid var(--header-border); padding-bottom: 0.3em; }
        h2 { font-size: 1.5em; border-bottom: 1px solid var(--header-border); padding-bottom: 0.3em; }
        h3 { font-size: 1.25em; }
        p { margin: 1em 0; }
        a { color: var(--link); text-decoration: none; }
        a:hover { text-decoration: underline; }
        code {
            font-family: "SF Mono", SFMono-Regular, Consolas, "Liberation Mono", Menlo, monospace;
            font-size: 0.875em;
            background: var(--code-bg);
            padding: 0.2em 0.4em;
            border-radius: 6px;
        }
        pre {
            background: var(--code-bg);
            padding: 1rem;
            overflow-x: auto;
            border-radius: 6px;
            border: 1px solid var(--border);
            line-height: 1.5;
        }
        pre code { padding: 0; background: none; font-size: 0.85em; }
        blockquote {
            margin: 1em 0;
            padding: 0.5em 1em;
            border-left: 4px solid var(--border);
            color: var(--text-muted);
            background: var(--code-bg);
        }
        ul, ol { margin: 1em 0; padding-left: 2em; }
        li { margin: 0.25em 0; }
        table { border-collapse: collapse; width: 100%%; margin: 1em 0; }
        th, td { border: 1px solid var(--border); padding: 0.5em 1em; text-align: left; }
        th { background: var(--code-bg); font-weight: 600; }
        hr { border: none; border-top: 1px solid var(--border); margin: 2em 0; }
        img { max-width: 100%%; }
        /* Syntax highlighting - monokai-inspired for dark, github-inspired for light */
        .chroma { background: var(--code-bg); }
        .chroma .k, .chroma .kc, .chroma .kd, .chroma .kn, .chroma .kp, .chroma .kr { color: #ff79c6; }
        .chroma .n, .chroma .na, .chroma .nb, .chroma .nc, .chroma .no, .chroma .nd { color: #50fa7b; }
        .chroma .s, .chroma .sa, .chroma .sb, .chroma .sc, .chroma .dl, .chroma .sd, .chroma .s2, .chroma .se, .chroma .sh, .chroma .si, .chroma .sx, .chroma .sr, .chroma .s1, .chroma .ss { color: #f1fa8c; }
        .chroma .m, .chroma .mb, .chroma .mf, .chroma .mh, .chroma .mi, .chroma .il, .chroma .mo { color: #bd93f9; }
        .chroma .c, .chroma .ch, .chroma .cm, .chroma .c1, .chroma .cs, .chroma .cp, .chroma .cpf { color: #6272a4; font-style: italic; }
        @media (prefers-color-scheme: light) {
            .chroma .k, .chroma .kc, .chroma .kd, .chroma .kn, .chroma .kp, .chroma .kr { color: #d73a49; }
            .chroma .n, .chroma .na, .chroma .nb, .chroma .nc, .chroma .no, .chroma .nd { color: #6f42c1; }
            .chroma .s, .chroma .sa, .chroma .sb, .chroma .sc, .chroma .dl, .chroma .sd, .chroma .s2, .chroma .se, .chroma .sh, .chroma .si, .chroma .sx, .chroma .sr, .chroma .s1, .chroma .ss { color: #032f62; }
            .chroma .m, .chroma .mb, .chroma .mf, .chroma .mh, .chroma .mi, .chroma .il, .chroma .mo { color: #005cc5; }
            .chroma .c, .chroma .ch, .chroma .cm, .chroma .c1, .chroma .cs, .chroma .cp, .chroma .cpf { color: #6a737d; }
        }
        /* Mermaid diagram styling */
        .mermaid-diagram {
            display: flex;
            justify-content: center;
            margin: 1em 0;
            overflow-x: auto;
        }
        .mermaid-diagram svg {
            max-width: 100%%;
        }
        pre.mermaid {
            text-align: center;
            background: transparent;
            border: none;
            padding: 0;
        }
        .mermaid-error {
            color: var(--text-muted);
            font-style: italic;
        }
    </style>
</head>
<body>
    <article>%s</article>
    <script type="module">
        // Check if there are any mermaid blocks to render
        const mermaidBlocks = document.querySelectorAll('pre.mermaid');
        if (mermaidBlocks.length > 0) {
            // Dynamically load and initialize Mermaid.js
            const script = document.createElement('script');
            script.src = 'https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js';
            script.onload = function() {
                // Detect theme
                const isDark = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches;
                const theme = isDark ? 'dark' : 'default';

                // Initialize Mermaid
                mermaid.initialize({
                    startOnLoad: false,
                    theme: theme,
                    securityLevel: 'strict',
                    fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif'
                });

                // Render each mermaid block
                mermaidBlocks.forEach(async (block, index) => {
                    try {
                        const diagramDef = block.textContent || '';
                        if (!diagramDef.trim()) return;

                        const { svg } = await mermaid.render('mermaid-' + index, diagramDef);
                        const wrapper = document.createElement('div');
                        wrapper.className = 'mermaid-diagram';
                        wrapper.innerHTML = svg;
                        block.replaceWith(wrapper);
                    } catch (err) {
                        console.error('Failed to render mermaid diagram:', err);
                        block.classList.add('mermaid-error');
                        block.textContent = '⚠️ Failed to render diagram: ' + err.message;
                    }
                });
            };
            script.onerror = function(err) {
                console.error('Failed to load Mermaid.js:', err);
            };
            document.head.appendChild(script);
        }
    </script>
</body>
</html>`, title, content)
}
