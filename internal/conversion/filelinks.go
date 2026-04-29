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

	// WorkspacePath is the filesystem path to the workspace root.
	// Included in viewer URLs as ws_path so the viewer can construct
	// file:// URLs for the "Open in System App" button.
	WorkspacePath string

	// WorkspaceUUID is the unique identifier for the workspace.
	// Used in viewer URLs for secure file links.
	WorkspaceUUID string

	// Enabled controls whether file linking is active.
	Enabled bool

	// MaxPathsPerMessage limits the number of paths processed per message (DoS protection).
	MaxPathsPerMessage int

	// AllowOutsideWorkspace allows linking to files outside the workspace.
	AllowOutsideWorkspace bool

	// APIPrefix is the URL prefix for the web server (e.g., "/mitto").
	// Used when generating viewer URLs.
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
// - Hidden directory paths: .augment/rules/test.md, .github/workflows/ci.yml
// - Absolute paths: /Users/foo/project/file.txt
// - Must have at least one path separator or start with ./ or ../
var filePathPattern = regexp.MustCompile(
	`(?:^|[\s>"\x60])` + // Start of string, whitespace, >, ", or backtick
		`(` +
		`\.{1,2}/[^\s<>"'\x60]+` + // Relative: ./ or ../
		`|` +
		`/[^\s<>"'\x60]+` + // Absolute: /path/to/file
		`|` +
		`\.[a-zA-Z0-9_-]+(?:/[a-zA-Z0-9_.\-]+)+` + // Hidden dir: .augment/rules/file.md
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

// urlPattern matches URLs in text.
// Matches http://, https://, ftp://, and mailto: URLs.
// Similar to the pattern used in web/static/lib.js but adapted for Go.
var urlPattern = regexp.MustCompile(
	`\b((?:https?://|ftp://|mailto:)[^\s<>"\[\]{}|\\^` + "`" + `]+)`,
)

// anchorHrefPattern matches opening <a> tags with an href attribute.
// Group 1: attributes before href, Group 2: href value, Group 3: attributes after href.
var anchorHrefPattern = regexp.MustCompile(`<a\s+([^>]*?)href="([^"]*)"([^>]*)>`)

// relAttrPattern matches a rel="..." attribute in an HTML tag (for removal when rewriting).
var relAttrPattern = regexp.MustCompile(`\s*rel="[^"]*"`)

// classAttrInTagPattern matches a class="..." attribute in an HTML tag (for merging classes).
var classAttrInTagPattern = regexp.MustCompile(`\s*class="([^"]*)"`)

// LinkFilePaths scans HTML content for file path patterns and converts them to file:// links.
// Only paths that exist on the filesystem and pass security checks are converted.
// This also processes inline <code> tags (backtick-enclosed text in markdown) for both
// file paths and URLs.
func (fl *FileLinker) LinkFilePaths(html string) string {
	if !fl.config.Enabled || html == "" {
		return html
	}

	// First pass: rewrite <a> tags whose href is a local file path.
	// Agent-generated markdown links like [text](path/to/file.md) become
	// <a href="path/to/file.md"> after Goldmark. We rewrite them to API links.
	html = fl.processAnchorHrefs(html)

	// Second pass: process inline <code> tags that contain URLs
	// These come from backtick-enclosed text in markdown (e.g., `https://example.com`)
	html = fl.processInlineCodeURLs(html)

	// Third pass: process inline <code> tags that contain file paths
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

// processInlineCodeURLs finds inline <code> tags containing URLs and wraps them in links.
// Only processes <code> tags that are NOT inside <pre> tags (i.e., inline code, not code blocks).
// This handles URLs in backtick-enclosed text like `https://example.com`.
func (fl *FileLinker) processInlineCodeURLs(html string) string {
	// Find all <pre> tag regions to skip
	preRegions := fl.findPreRegions(html)
	// Find all <a> tag regions to skip (prevent double-nesting)
	anchorRegions := fl.findAnchorRegions(html)

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
		// Skip if inside an <a> tag (prevent double-nesting)
		if fl.isInSkipRegion(fullStart, fullEnd, anchorRegions) {
			continue
		}

		content := html[contentStart:contentEnd]

		// Check if content is a URL
		if !urlPattern.MatchString(content) {
			continue
		}

		// Extract the URL (trim any trailing punctuation that might have been captured)
		urlMatch := urlPattern.FindStringSubmatch(content)
		if len(urlMatch) < 2 {
			continue
		}
		urlStr := urlMatch[1]

		// Clean up trailing punctuation
		cleanedURL := cleanURLTrailingPunctuation(urlStr)

		// Only linkify if the entire content is the URL (possibly with trailing punctuation)
		// This prevents linkifying things like "see https://example.com for details"
		// where the URL is part of a larger sentence
		trimmedContent := strings.TrimSpace(content)
		cleanedContent := cleanURLTrailingPunctuation(trimmedContent)
		if cleanedContent != cleanedURL {
			continue
		}

		// Use the cleaned URL for the link
		urlStr = cleanedURL

		// Determine link attributes based on scheme
		isMailto := strings.HasPrefix(strings.ToLower(urlStr), "mailto:")
		var attrs string
		if isMailto {
			attrs = fmt.Sprintf(`href="%s" class="url-link mailto-link"`, urlStr)
		} else {
			attrs = fmt.Sprintf(`href="%s" target="_blank" rel="noopener noreferrer" class="url-link"`, urlStr)
		}

		// Replace <code>URL</code> with <a href="..."><code>URL</code></a>
		codeTag := html[fullStart:fullEnd]
		replacement := fmt.Sprintf(`<a %s>%s</a>`, attrs, codeTag)
		result = result[:fullStart] + replacement + result[fullEnd:]
	}

	return result
}

// processInlineCodeTags finds inline <code> tags containing file paths and wraps them in links.
// Only processes <code> tags that are NOT inside <pre> or <a> tags.
func (fl *FileLinker) processInlineCodeTags(html string) string {
	// Find all <pre> tag regions to skip
	preRegions := fl.findPreRegions(html)
	// Find all <a> tag regions to skip (prevent double-nesting)
	anchorRegions := fl.findAnchorRegions(html)

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
		// Skip if inside an <a> tag (prevent double-nesting)
		if fl.isInSkipRegion(fullStart, fullEnd, anchorRegions) {
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

// findAnchorRegions returns a list of [start, end] pairs for <a> tag regions.
func (fl *FileLinker) findAnchorRegions(html string) [][2]int {
	var regions [][2]int
	for _, match := range anchorTagPattern.FindAllStringIndex(html, -1) {
		regions = append(regions, [2]int{match[0], match[1]})
	}
	return regions
}

// processAnchorHrefs rewrites <a> tags whose href is a local file path.
// Agent-generated markdown links like [text](path/to/file.md) become
// <a href="path/to/file.md"> after Goldmark processing. This method
// detects those and rewrites the href to use buildLinkURL() (which
// generates viewer URLs in the form /viewer.html?ws=...&path=...).
// It also adds a file-link CSS class and removes any rel="nofollow" attribute.
func (fl *FileLinker) processAnchorHrefs(html string) string {
	// Find all <pre> tag regions to skip
	preRegions := fl.findPreRegions(html)

	// Find all <a> tags with an href attribute
	matches := anchorHrefPattern.FindAllStringSubmatchIndex(html, -1)
	if len(matches) == 0 {
		return html
	}

	// Process matches in reverse order to preserve indices
	result := html
	for i := len(matches) - 1; i >= 0; i-- {
		match := matches[i]
		if len(match) < 8 {
			continue
		}

		// Full match: the entire <a ...> opening tag
		fullStart, fullEnd := match[0], match[1]
		// Group 1: attrs before href
		preHrefStart, preHrefEnd := match[2], match[3]
		// Group 2: href value
		hrefStart, hrefEnd := match[4], match[5]
		// Group 3: attrs after href
		postHrefStart, postHrefEnd := match[6], match[7]

		if hrefStart < 0 || hrefEnd < 0 {
			continue
		}

		// Skip if inside a <pre> tag
		if fl.isInSkipRegion(fullStart, fullEnd, preRegions) {
			continue
		}

		href := html[hrefStart:hrefEnd]

		// Skip if href is empty
		if href == "" {
			continue
		}

		// Skip external URLs, fragments, and special schemes
		hrefLower := strings.ToLower(href)
		if strings.HasPrefix(hrefLower, "http://") ||
			strings.HasPrefix(hrefLower, "https://") ||
			strings.HasPrefix(hrefLower, "ftp://") ||
			strings.HasPrefix(hrefLower, "mailto:") ||
			strings.HasPrefix(hrefLower, "file://") ||
			strings.HasPrefix(hrefLower, "#") ||
			strings.HasPrefix(hrefLower, "javascript:") ||
			strings.Contains(hrefLower, "://") {
			continue
		}

		// Validate the path (with caching)
		var info *pathInfo
		if cached, ok := fl.statCache.Load(href); ok {
			info = cached.(*pathInfo)
		} else {
			info = fl.validatePath(href)
			fl.statCache.Store(href, info)
		}
		if !info.safe || !info.exists {
			continue
		}

		// Build the new href URL
		linkURL := fl.buildLinkURL(href, info.realPath)

		// Determine CSS class
		class := "file-link"
		if info.isDir {
			class += " dir-link"
		}

		// Extract attribute strings captured by the regex
		preHrefAttrs := ""
		if preHrefStart >= 0 {
			preHrefAttrs = html[preHrefStart:preHrefEnd]
		}
		postHrefAttrs := ""
		if postHrefStart >= 0 {
			postHrefAttrs = html[postHrefStart:postHrefEnd]
		}

		// Rebuild the <a> opening tag with updated href and class
		newTag := fl.buildNewAnchorTag(preHrefAttrs, postHrefAttrs, linkURL, class)
		result = result[:fullStart] + newTag + result[fullEnd:]
	}

	return result
}

// buildNewAnchorTag constructs a new <a> opening tag with an updated href and class.
// It merges any existing class with additionalClass and removes rel="nofollow".
func (fl *FileLinker) buildNewAnchorTag(preHrefAttrs, postHrefAttrs, newHref, additionalClass string) string {
	// Combine all non-href attributes
	allOtherAttrs := preHrefAttrs + postHrefAttrs

	// Extract existing class value (if any) before stripping it
	existingClass := ""
	if m := classAttrInTagPattern.FindStringSubmatch(allOtherAttrs); m != nil {
		existingClass = m[1]
	}

	// Remove class and rel attributes; we re-add class cleanly below
	allOtherAttrs = classAttrInTagPattern.ReplaceAllString(allOtherAttrs, "")
	allOtherAttrs = relAttrPattern.ReplaceAllString(allOtherAttrs, "")
	allOtherAttrs = strings.TrimSpace(allOtherAttrs)

	// Build the final class value
	finalClass := additionalClass
	if existingClass != "" {
		finalClass = existingClass + " " + additionalClass
	}

	// Reconstruct the opening tag
	if allOtherAttrs != "" {
		return fmt.Sprintf(`<a href="%s" class="%s" %s>`, newHref, finalClass, allOtherAttrs)
	}
	return fmt.Sprintf(`<a href="%s" class="%s">`, newHref, finalClass)
}

// buildLinkURL creates the viewer URL for a file link.
func (fl *FileLinker) buildLinkURL(displayPath, realPath string) string {
	relativePath := strings.TrimPrefix(displayPath, "./")
	u := fl.config.APIPrefix + "/viewer.html?ws=" + url.QueryEscape(fl.config.WorkspaceUUID) + "&path=" + url.QueryEscape(relativePath)
	if fl.config.WorkspacePath != "" {
		u += "&ws_path=" + url.QueryEscape(fl.config.WorkspacePath)
	}
	return u
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

// imgSrcRegex matches the src attribute value inside an <img> tag.
// It captures the src value so it can be targeted for replacement.
// NOTE: An identical regex exists in internal/web/file_server.go (rewriteRelativeImageURLs).
// The duplication is intentional — the web package cannot import the conversion package
// for that specific use, and the conversion package cannot import the web package.
var imgSrcRegex = regexp.MustCompile(`(?i)<img\b[^>]*?\bsrc="([^"]*)"`)

// RewriteImageURLs rewrites relative <img src="..."> URLs in an HTML snippet to use
// the /api/files endpoint before HTML sanitization strips them.
//
// Bluemonday's RequireParseableURLs(true) strips src attributes whose value has no
// recognised URL scheme (http/https/data/file). Relative paths produced by Goldmark
// (e.g. src="charts/portfolio-5d.png") therefore get silently removed. This method
// replaces them with absolute /api/files?ws=...&path=... URLs so they survive
// sanitization and resolve correctly in the browser.
//
// The method is a no-op when the FileLinker is not enabled or WorkspaceUUID is empty.
func (fl *FileLinker) RewriteImageURLs(html string) string {
	if !fl.config.Enabled || fl.config.WorkspaceUUID == "" {
		return html
	}

	return imgSrcRegex.ReplaceAllStringFunc(html, func(match string) string {
		submatches := imgSrcRegex.FindStringSubmatch(match)
		if len(submatches) < 2 {
			return match
		}
		src := submatches[1]

		// Skip absolute URLs, data URIs, protocol-relative URLs, and root-relative paths.
		srcLower := strings.ToLower(src)
		if strings.HasPrefix(srcLower, "http://") ||
			strings.HasPrefix(srcLower, "https://") ||
			strings.HasPrefix(srcLower, "data:") ||
			strings.HasPrefix(srcLower, "//") ||
			strings.HasPrefix(srcLower, "/") {
			return match
		}

		// Resolve the relative path against the working directory.
		var resolved string
		if fl.config.WorkingDir != "" {
			resolved = filepath.Join(fl.config.WorkingDir, src)
		} else {
			resolved = src
		}
		resolved = filepath.Clean(resolved)

		// Security: reject paths that escape the workspace root.
		if strings.HasPrefix(resolved, "..") {
			return match
		}

		// Make the path relative again (strip the working directory prefix) so
		// it can be passed as the ?path= parameter (FileServer expects relative paths).
		if fl.config.WorkingDir != "" {
			rel, err := filepath.Rel(fl.config.WorkingDir, resolved)
			if err == nil {
				resolved = rel
			}
		}

		// Build the API URL. Use &amp; so the value is valid inside an HTML attribute.
		apiURL := fl.config.APIPrefix + "/api/files?ws=" + url.QueryEscape(fl.config.WorkspaceUUID) + "&amp;path=" + url.QueryEscape(resolved)
		return strings.Replace(match, `src="`+src+`"`, `src="`+apiURL+`"`, 1)
	})
}

// cleanURLTrailingPunctuation removes trailing punctuation from URLs.
// This handles cases where punctuation at the end of a sentence gets captured in the URL.
func cleanURLTrailingPunctuation(urlStr string) string {
	// Remove trailing punctuation that's unlikely to be part of the URL
	for len(urlStr) > 0 {
		lastChar := urlStr[len(urlStr)-1]
		if lastChar == '.' || lastChar == ',' || lastChar == ';' || lastChar == ':' ||
			lastChar == '!' || lastChar == '?' || lastChar == ')' || lastChar == ']' ||
			lastChar == '}' || lastChar == '>' {
			// Keep closing brackets/parens if they're balanced
			if lastChar == ')' {
				openCount := strings.Count(urlStr, "(")
				closeCount := strings.Count(urlStr, ")")
				if openCount >= closeCount {
					break
				}
			}
			if lastChar == ']' {
				openCount := strings.Count(urlStr, "[")
				closeCount := strings.Count(urlStr, "]")
				if openCount >= closeCount {
					break
				}
			}
			urlStr = urlStr[:len(urlStr)-1]
		} else {
			break
		}
	}
	return urlStr
}
