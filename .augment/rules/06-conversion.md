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

### `:line[:col]` suffix handling (mitto-3u7)

Paths with a trailing `:<line>` or `:<line>:<col>` suffix are supported across all three link paths (`processAnchorHrefs`, `processInlineCodeTags`, `processPath`/`createLink`). The pattern is:

1. `splitLineSuffix(path)` (regex `^(.+):([0-9]+)(?::[0-9]+)?$`) peels the suffix off before validation.
2. `validatePath` / `os.Stat` runs against the **stripped base path** — `statCache` is also keyed on the stripped form so `file.md:10` and `file.md:42` share one entry.
3. `buildLinkURL(display, real, line)` appends `&line=N` to the viewer URL; the viewer already honors `?line=`.
4. `createLink(displayPath, hrefPath, ...)` keeps `displayPath` (with suffix) distinct from `hrefPath` (stripped) so link text still shows the location while the href is a real file.

**Invariants** — do NOT relax these when extending trailing-metadata handling:

- The suffix regex must be **purely digits anchored at end-of-string**, so Windows drive letters (`C:\...`) and URL scheme colons (`http://`) are never matched. URL scheme collisions are additionally excluded upstream by the `://` check.
- NEVER pass the suffixed string to `validatePath` — `os.Stat` will fail and the link silently drops.
- `displayPath` and `hrefPath` in `createLink` are DISTINCT parameters; collapsing them re-introduces the `os.Stat` failure and drops all `:line` links.

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
