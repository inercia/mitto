---
description: Markdown-to-HTML conversion, file/URL link detection, regex patterns, text processing anti-patterns
globs:
  - "internal/conversion/**/*"
keywords:
  - markdown conversion
  - file link detection
  - URL detection
  - linkify
  - regex
  - regexp
  - pattern
  - Mermaid diagram
  - skip region
  - sanitize HTML
  - text processing
---

# Conversion Package

The `internal/conversion` package handles Markdown-to-HTML conversion with security and streaming support.

## Components

| Component    | Purpose                                        |
| ------------ | ---------------------------------------------- |
| `Converter`  | Markdown-to-HTML with goldmark                 |
| `FileLinker` | Detect and linkify file paths and URLs in HTML |

## Converter Usage

```go
converter := conversion.DefaultConverter()
html := converter.ConvertToSafeHTML(markdown)

converter := conversion.NewConverter(
    conversion.WithFileLinks(FileLinkerConfig{
        WorkingDir: "/path/to/project",
        BasePath:   "/api/files",
    }),
)
```

## Link Detection Pipeline

Processing order matters - more specific patterns first:

```go
func ProcessHTML(html string) string {
    html = markdownToHTML(html)
    html = linkifyURLs(html)       // URLs first (includes protocol)
    html = linkifyFilePaths(html)  // File paths second
    return sanitize(html)          // Sanitize last
}
```

**Detection patterns:**
- URLs: `https://`, `http://`, `ftp://`, `mailto:`
- Absolute paths: `/home/user/file.go`
- Relative paths: `./src/main.go`, `../lib/utils.py`
- Line references: `file.go:42`, `file.go:42:10`

**Edge cases handled:** Trailing punctuation stripped, balanced parentheses preserved, URLs in code blocks NOT linked, partial URLs NOT linked.

## Regex Patterns

### HTML Processing with Skip Regions

Always find skip regions first, then process matches in reverse:

```go
func linkifyURLs(html string) string {
    skipRegions := findSkipRegions(html)  // <pre>, <code>, <a> tags
    matches := urlPattern.FindAllStringSubmatchIndex(html, -1)

    result := html
    for i := len(matches) - 1; i >= 0; i-- {  // Reverse order!
        if !isInSkipRegion(matches[i], skipRegions) {
            // Process match - indices are still valid
        }
    }
    return result
}
```

### Anti-Pattern: Forward Processing

```go
// BAD: Forward processing breaks indices after first replacement
for i := 0; i < len(matches); i++ {
    result = result[:match[0]] + replacement + result[match[1]:]
    // All subsequent indices are now wrong!
}
```

### Anti-Pattern: Simple String Replacement for HTML

```go
// BAD: Can't handle variations, replaces inside existing tags
return strings.ReplaceAll(html, "https://example.com",
    `<a href="https://example.com">...</a>`)
```

### JavaScript: Reset Global Regex State

```javascript
const URL_PATTERN = /https?:\/\/[^\s]+/gi;
function findURLs(text) {
  URL_PATTERN.lastIndex = 0;  // Must reset before use!
  return URL_PATTERN.exec(text);
}
```

## Helper Functions for MarkdownBuffer

| Function                             | Purpose                               |
| ------------------------------------ | ------------------------------------- |
| `IsCodeBlockStart(line)`             | Detect ``` fence lines                |
| `IsListItem(line)`                   | Detect list items (`- `, `* `, `1. `) |
| `IsTableRow(line)`                   | Detect table rows                     |
| `HasUnmatchedInlineFormatting(text)` | Check for incomplete `**`, `_`, etc.  |

## Mermaid Diagram Support

Backend renders ` ```mermaid` blocks as `<pre class="mermaid">`. Frontend dynamically loads Mermaid.js from CDN when needed.

**Note**: CDN-hosted Mermaid.js may be blocked by browser tracking protection (Firefox, Safari).

## Security

All HTML output sanitized using bluemonday's UGCPolicy. Allows common formatting tags, strips script tags and event handlers.

## Testing

Golden file testing with `internal/conversion/testdata/`. Coverage target: 90%+.

Three-level testing strategy:
1. **Unit tests** (`TestFileLinker_*`): HTML input/output, edge cases
2. **Integration tests** (`Test*_Integration`): Full markdown-to-HTML pipeline
3. **Example tests** (`Example_*`): Real-world usage as documentation
