---
description: Markdown-to-HTML conversion, file link detection, and sanitization
globs:
  - "internal/conversion/**/*"
---

# Conversion Package

The `internal/conversion` package handles Markdown-to-HTML conversion with security and streaming support.

## Components

| Component | Purpose |
|-----------|---------|
| `Converter` | Markdown-to-HTML with goldmark |
| `FileLinker` | Detect and linkify file paths and URLs in HTML |

## Converter Usage

```go
// Default converter (no file linking)
converter := conversion.DefaultConverter()
html := converter.ConvertToSafeHTML(markdown)

// With file links
converter := conversion.NewConverter(
    conversion.WithFileLinks(FileLinkerConfig{
        WorkingDir: "/path/to/project",
        BasePath:   "/api/files",
    }),
)
```

## File Link Detection

The converter can detect file paths in agent output and make them clickable:

```go
config := conversion.FileLinkerConfig{
    WorkingDir: sessionWorkDir,
    BasePath:   "/api/files",  // URL prefix for file links
}
```

**Detection patterns:**
- Absolute paths: `/home/user/file.go`
- Relative paths: `./src/main.go`, `../lib/utils.py`
- Line references: `file.go:42`, `file.go:42:10`

## URL Link Detection

The `FileLinker` also detects and linkifies URLs in inline code blocks (backticks):

**Supported URL schemes:**
- `https://` - HTTPS URLs
- `http://` - HTTP URLs
- `ftp://` - FTP URLs
- `mailto:` - Email links (without `target="_blank"`)

**Processing order:**
1. URLs in `<code>` tags are processed first
2. File paths in `<code>` tags are processed second
3. File paths in regular text are processed last

**Example:**
```markdown
Check out `https://example.com` for more info
```

Converts to:
```html
<a href="https://example.com" target="_blank" rel="noopener noreferrer" class="url-link">
  <code>https://example.com</code>
</a>
```

**Edge cases handled:**
- Trailing punctuation is stripped: `https://example.com.` → `https://example.com`
- Balanced parentheses are preserved: `https://example.com/page(1)`
- URLs in code blocks (triple backticks) are NOT linked
- Only complete URLs are linked (not partial matches like `example.com`)
- URLs with surrounding text in backticks are NOT linked

## Helper Functions for MarkdownBuffer

| Function | Purpose |
|----------|---------|
| `IsCodeBlockStart(line)` | Detect ``` fence lines |
| `IsListItem(line)` | Detect list items (`- `, `* `, `1. `) |
| `IsTableRow(line)` | Detect table rows (`|`) |
| `HasUnmatchedInlineFormatting(text)` | Check for incomplete `**`, `_`, etc. |

These helpers are used by `MarkdownBuffer` to avoid flushing mid-structure:

```go
// In MarkdownBuffer.Write()
if conversion.IsCodeBlockStart(line) {
    mb.inCodeBlock = !mb.inCodeBlock
}
if conversion.IsListItem(line) {
    mb.inList = true
}
```

## Testing

The package uses golden file testing with test data in `internal/conversion/testdata/`:

```bash
# Each test case has a .md input and .html expected output
testdata/
├── basic_paragraph.md
├── basic_paragraph.html
├── code_block.md
├── code_block.html
└── ...
```

Run tests:
```bash
go test ./internal/conversion/...
```

## Security

All HTML output is sanitized using bluemonday's UGCPolicy to prevent XSS:
- Allows common formatting tags (p, strong, em, code, pre)
- Allows safe attributes (class, id, href)
- Strips script tags and event handlers
- Allows URL schemes: http, https, mailto, file

## Implementation Patterns

### Adding New Link Detection

When adding new link detection patterns (like URL detection):

1. **Add regex pattern** at package level:
   ```go
   var urlPattern = regexp.MustCompile(`\b((?:https?://|ftp://|mailto:)[^\s<>"\[\]{}|\\^` + "`" + `]+)`)
   ```

2. **Create processing function** that:
   - Finds all `<pre>` regions to skip (code blocks)
   - Finds all inline `<code>` tags
   - Processes matches in reverse order to preserve indices
   - Validates content before linkifying
   - Wraps `<code>` tag in anchor tag (preserves formatting)

3. **Add to LinkFilePaths pipeline** in correct order:
   ```go
   // Process URLs first (more specific)
   html = fl.processInlineCodeURLs(html)
   // Then file paths
   html = fl.processInlineCodeTags(html)
   ```

4. **Handle edge cases**:
   - Skip content inside `<pre>` tags (code blocks)
   - Clean trailing punctuation
   - Preserve balanced brackets/parentheses
   - Only linkify complete matches (not partial)

### Testing Patterns

For link detection features, create three test levels:

1. **Unit tests** (`TestFileLinker_*`):
   - Test HTML input/output directly
   - Cover all edge cases
   - Test security checks

2. **Integration tests** (`Test*_Integration`):
   - Test full markdown-to-HTML pipeline
   - Verify interaction with goldmark
   - Test with sanitization enabled

3. **Example tests** (`Example_*`):
   - Demonstrate real-world usage
   - Serve as documentation
   - Verify output format

**Test coverage target:** 90%+ for conversion package

