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
| `FileLinker` | Detect and linkify file paths in HTML |

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

