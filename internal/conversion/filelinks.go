// Package conversion provides markdown-to-HTML conversion utilities.
package conversion

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// FileLinkerConfig holds configuration for file path linking.
type FileLinkerConfig struct {
	// WorkingDir is the workspace root for resolving relative paths.
	WorkingDir string

	// WorkspaceUUID is the unique identifier for the workspace.
	// Used in HTTP URLs instead of the full path for security.
	// Only used when UseHTTPLinks is true.
	WorkspaceUUID string

	// Enabled controls whether file linking is active.
	Enabled bool

	// MaxPathsPerMessage limits the number of paths processed per message (DoS protection).
	MaxPathsPerMessage int

	// AllowOutsideWorkspace allows linking to files outside the workspace.
	AllowOutsideWorkspace bool

	// UseHTTPLinks generates HTTP URLs instead of file:// URLs.
	// This is used for web browser access where file:// URLs are blocked.
	UseHTTPLinks bool

	// APIPrefix is the URL prefix for API endpoints (e.g., "/mitto").
	// Only used when UseHTTPLinks is true.
	APIPrefix string
}

// FileLinker processes HTML to detect file paths and convert them to clickable links.
type FileLinker struct {
	config    FileLinkerConfig
	statCache sync.Map // map[string]*pathInfo for caching stat results
}

// pathInfo holds cached information about a path.
type pathInfo struct {
	exists   bool
	isDir    bool
	realPath string
	safe     bool
}

// NewFileLinker creates a new FileLinker with the given configuration.
func NewFileLinker(config FileLinkerConfig) *FileLinker {
	if config.MaxPathsPerMessage <= 0 {
		config.MaxPathsPerMessage = 50 // Default limit
	}
	return &FileLinker{
		config: config,
	}
}

// sensitivePatterns are file patterns that should never be linked for security.
var sensitivePatterns = []string{
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

// filePathPattern matches potential file paths in text.
// Matches:
// - Relative paths: src/main.go, ./config.yaml, ../utils/helper.js
// - Absolute paths: /Users/foo/project/file.txt
// - Must have at least one path separator or start with ./ or ../
var filePathPattern = regexp.MustCompile(
	`(?:^|[\s>"\x60])` + // Start of string, whitespace, >, ", or backtick
		`(` +
		`\.{1,2}/[^\s<>"'\x60]+` + // Relative: ./ or ../
		`|` +
		`/[^\s<>"'\x60]+` + // Absolute: /path/to/file
		`|` +
		`[a-zA-Z0-9_-]+(?:/[a-zA-Z0-9_.\-]+)+` + // Relative without ./: src/main.go
		`)` +
		`(?:[\s<>"'\x60]|$)`, // End of string, whitespace, <, >, ", ', or backtick
)

// codeTagPattern matches content inside <code> tags (for skipping in regular text).
var codeTagPattern = regexp.MustCompile(`(?s)<code[^>]*>.*?</code>`)

// inlineCodePattern matches inline <code> tags and captures the content.
// Used to detect file paths inside backtick-enclosed text.
var inlineCodePattern = regexp.MustCompile(`<code>([^<]+)</code>`)

// preTagPattern matches content inside <pre> tags.
var preTagPattern = regexp.MustCompile(`(?s)<pre[^>]*>.*?</pre>`)

// anchorTagPattern matches content inside <a> tags.
var anchorTagPattern = regexp.MustCompile(`(?s)<a[^>]*>.*?</a>`)

// LinkFilePaths scans HTML content for file path patterns and converts them to file:// links.
// Only paths that exist on the filesystem and pass security checks are converted.
// This also processes inline <code> tags (backtick-enclosed text in markdown).
func (fl *FileLinker) LinkFilePaths(html string) string {
	if !fl.config.Enabled || html == "" {
		return html
	}

	// First pass: process inline <code> tags that contain file paths
	// These come from backtick-enclosed text in markdown (e.g., `src/main.go`)
	html = fl.processInlineCodeTags(html)

	// Find all code, pre, and anchor tag regions to skip for regular text processing
	skipRegions := fl.findSkipRegions(html)

	// Find all potential file paths in regular text
	matches := filePathPattern.FindAllStringSubmatchIndex(html, -1)
	if len(matches) == 0 {
		return html
	}

	// Limit number of paths processed
	if len(matches) > fl.config.MaxPathsPerMessage {
		matches = matches[:fl.config.MaxPathsPerMessage]
	}

	// Process matches in reverse order to preserve indices
	result := html
	for i := len(matches) - 1; i >= 0; i-- {
		match := matches[i]
		if len(match) < 4 {
			continue
		}

		// Get the captured group (the path itself)
		pathStart, pathEnd := match[2], match[3]
		if pathStart < 0 || pathEnd < 0 {
			continue
		}

		// Check if this match is inside a skip region
		if fl.isInSkipRegion(pathStart, pathEnd, skipRegions) {
			continue
		}

		path := html[pathStart:pathEnd]
		replacement := fl.processPath(path)
		if replacement != "" {
			result = result[:pathStart] + replacement + result[pathEnd:]
		}
	}

	return result
}

// processInlineCodeTags finds inline <code> tags containing file paths and wraps them in links.
// Only processes <code> tags that are NOT inside <pre> tags (i.e., inline code, not code blocks).
func (fl *FileLinker) processInlineCodeTags(html string) string {
	// Find all <pre> tag regions to skip
	preRegions := fl.findPreRegions(html)

	// Find all inline <code> tags
	matches := inlineCodePattern.FindAllStringSubmatchIndex(html, -1)
	if len(matches) == 0 {
		return html
	}

	// Process matches in reverse order to preserve indices
	result := html
	for i := len(matches) - 1; i >= 0; i-- {
		match := matches[i]
		if len(match) < 4 {
			continue
		}

		// Full match indices
		fullStart, fullEnd := match[0], match[1]
		// Captured content indices
		contentStart, contentEnd := match[2], match[3]

		// Skip if inside a <pre> tag (code block)
		if fl.isInSkipRegion(fullStart, fullEnd, preRegions) {
			continue
		}

		content := html[contentStart:contentEnd]

		// Validate the path
		info := fl.validatePath(content)
		if !info.safe || !info.exists {
			continue
		}

		// Create link that wraps the entire <code> tag
		linkURL := fl.buildLinkURL(content, info.realPath)
		class := "file-link"
		if info.isDir {
			class += " dir-link"
		}

		// Replace <code>path</code> with <a href="..."><code>path</code></a>
		codeTag := html[fullStart:fullEnd]
		replacement := fmt.Sprintf(`<a href="%s" class="%s">%s</a>`, linkURL, class, codeTag)
		result = result[:fullStart] + replacement + result[fullEnd:]
	}

	return result
}

// findPreRegions returns a list of [start, end] pairs for <pre> tag regions.
func (fl *FileLinker) findPreRegions(html string) [][2]int {
	var regions [][2]int
	for _, match := range preTagPattern.FindAllStringIndex(html, -1) {
		regions = append(regions, [2]int{match[0], match[1]})
	}
	return regions
}

// buildLinkURL creates the URL for a file link.
func (fl *FileLinker) buildLinkURL(displayPath, realPath string) string {
	if fl.config.UseHTTPLinks {
		// Generate HTTP URL for web browser access
		relativePath := strings.TrimPrefix(displayPath, "./")
		// Use workspace UUID for secure file links
		return fl.config.APIPrefix + "/api/files?ws=" + url.QueryEscape(fl.config.WorkspaceUUID) + "&path=" + url.QueryEscape(relativePath)
	}
	// Create file:// URL for native app
	linkURL := "file://" + url.PathEscape(realPath)
	// PathEscape encodes slashes, but we want to keep them for file URLs
	return strings.ReplaceAll(linkURL, "%2F", "/")
}

// findSkipRegions returns a list of [start, end] pairs for regions to skip.
func (fl *FileLinker) findSkipRegions(html string) [][2]int {
	var regions [][2]int

	// Find code tags
	for _, match := range codeTagPattern.FindAllStringIndex(html, -1) {
		regions = append(regions, [2]int{match[0], match[1]})
	}

	// Find pre tags
	for _, match := range preTagPattern.FindAllStringIndex(html, -1) {
		regions = append(regions, [2]int{match[0], match[1]})
	}

	// Find anchor tags
	for _, match := range anchorTagPattern.FindAllStringIndex(html, -1) {
		regions = append(regions, [2]int{match[0], match[1]})
	}

	return regions
}

// isInSkipRegion checks if a position is inside any skip region.
func (fl *FileLinker) isInSkipRegion(start, end int, regions [][2]int) bool {
	for _, region := range regions {
		if start >= region[0] && end <= region[1] {
			return true
		}
	}
	return false
}

// processPath validates a path and returns an HTML link if valid, or empty string if not.
func (fl *FileLinker) processPath(path string) string {
	// Clean up the path
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	// Check cache first
	if cached, ok := fl.statCache.Load(path); ok {
		info := cached.(*pathInfo)
		if !info.safe || !info.exists {
			return ""
		}
		return fl.createLink(path, info.realPath, info.isDir)
	}

	// Validate and get path info
	info := fl.validatePath(path)
	fl.statCache.Store(path, info)

	if !info.safe || !info.exists {
		return ""
	}

	return fl.createLink(path, info.realPath, info.isDir)
}

// ClearCache clears the stat cache. Useful for testing.
func (fl *FileLinker) ClearCache() {
	fl.statCache = sync.Map{}
}

// validatePath checks if a path is safe to link and returns its info.
func (fl *FileLinker) validatePath(path string) *pathInfo {
	info := &pathInfo{}

	// Resolve to absolute path
	var absPath string
	if filepath.IsAbs(path) {
		absPath = filepath.Clean(path)
	} else if fl.config.WorkingDir != "" {
		absPath = filepath.Join(fl.config.WorkingDir, path)
	} else {
		// No working directory and relative path - can't resolve
		return info
	}

	// Resolve symlinks to get the real path
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// File doesn't exist or broken symlink
		return info
	}

	// Get file info
	fileInfo, err := os.Stat(realPath)
	if err != nil {
		return info
	}

	info.exists = true
	info.realPath = realPath
	info.isDir = fileInfo.IsDir()

	// Security check 1: Must be regular file or directory
	mode := fileInfo.Mode()
	if !mode.IsRegular() && !mode.IsDir() {
		// Skip special files (devices, sockets, pipes, etc.)
		return info
	}

	// Security check 2: Skip executable files (any execute bit set)
	if mode.IsRegular() && mode&0111 != 0 {
		return info
	}

	// Security check 3: Path must be within workspace (unless allowed)
	if !fl.config.AllowOutsideWorkspace && fl.config.WorkingDir != "" {
		// Resolve symlinks on workspace too (e.g., /var -> /private/var on macOS)
		absWorkDir, err := filepath.Abs(fl.config.WorkingDir)
		if err == nil {
			realWorkDir, err := filepath.EvalSymlinks(absWorkDir)
			if err == nil {
				absWorkDir = realWorkDir
			}
			// Ensure both paths end without trailing slash for consistent comparison
			absWorkDir = strings.TrimSuffix(absWorkDir, string(filepath.Separator))
			if !strings.HasPrefix(realPath, absWorkDir+string(filepath.Separator)) && realPath != absWorkDir {
				return info
			}
		}
	}

	// Security check 4: Skip sensitive files
	if fl.isSensitivePath(realPath) {
		return info
	}

	info.safe = true
	return info
}

// isSensitivePath checks if a path matches any sensitive pattern.
func (fl *FileLinker) isSensitivePath(path string) bool {
	normalized := strings.ToLower(path)
	for _, pattern := range sensitivePatterns {
		if strings.Contains(normalized, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

// createLink generates an HTML anchor tag for a file path.
func (fl *FileLinker) createLink(displayPath, realPath string, isDir bool) string {
	linkURL := fl.buildLinkURL(displayPath, realPath)

	// Escape display path for HTML
	escapedDisplay := EscapeHTML(displayPath)

	// Add appropriate class for styling
	class := "file-link"
	if isDir {
		class += " dir-link"
	}

	return fmt.Sprintf(`<a href="%s" class="%s">%s</a>`, linkURL, class, escapedDisplay)
}
