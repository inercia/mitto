---
description: Text and HTML processing anti-patterns, regex patterns, string replacement
globs:
  - "internal/conversion/**/*"
keywords:
  - text processing
  - HTML processing
  - string replacement
  - regex pattern
  - skip region
  - sanitize HTML
  - linkify
---

# Text Processing Anti-Patterns

## String Replacement

### ❌ Don't: Process HTML with String Replacement

```go
// BAD: Simple string replacement breaks with multiple occurrences
func linkifyURL(html string) string {
    return strings.ReplaceAll(html, "https://example.com",
        `<a href="https://example.com">https://example.com</a>`)
}
```

**Problems:**
- Breaks with multiple URLs
- Can't handle variations
- May replace inside existing tags
- Can't skip code blocks

### ✅ Do: Use Regex with Skip Regions

```go
// GOOD: Regex with skip regions and reverse processing
func linkifyURLs(html string) string {
    skipRegions := findSkipRegions(html)
    matches := urlPattern.FindAllStringSubmatchIndex(html, -1)

    result := html
    for i := len(matches) - 1; i >= 0; i-- {
        if !isInSkipRegion(matches[i], skipRegions) {
            // Process match
        }
    }
    return result
}
```

## Match Processing Order

### ❌ Don't: Process Matches Forward

```go
// BAD: Forward processing breaks indices after first replacement
for i := 0; i < len(matches); i++ {
    match := matches[i]
    result = result[:match[0]] + replacement + result[match[1]:]
    // All subsequent indices are now wrong!
}
```

### ✅ Do: Process Matches in Reverse

```go
// GOOD: Reverse processing preserves indices
for i := len(matches) - 1; i >= 0; i-- {
    match := matches[i]
    result = result[:match[0]] + replacement + result[match[1]:]
    // Previous indices are still valid
}
```

## HTML Sanitization

### ❌ Don't: Assume HTML is Safe

```go
// BAD: No sanitization
func convertMarkdown(md string) string {
    html := goldmark.Convert(md)
    return html  // May contain XSS!
}
```

### ✅ Do: Always Sanitize HTML

```go
// GOOD: Sanitize after conversion
func convertMarkdown(md string) string {
    html := goldmark.Convert(md)
    return sanitizer.Sanitize(html)
}
```

## Processing Pipeline Order

### Pattern: More Specific Patterns First

When processing HTML with multiple transformations:

1. Process more specific patterns first (URLs before file paths)
2. Process inline code before regular text
3. Apply sanitization after all transformations

```go
// GOOD: Ordered pipeline
func ProcessHTML(html string) string {
    // 1. Convert markdown to HTML
    html = markdownToHTML(html)
    
    // 2. Process URLs (more specific, includes protocol)
    html = linkifyURLs(html)
    
    // 3. Process file paths (less specific)
    html = linkifyFilePaths(html)
    
    // 4. Sanitize last (preserves legitimate tags)
    return sanitize(html)
}
```

## Skip Region Detection

### Pattern: Find Regions to Skip Before Processing

```go
// Skip regions: inline code, code blocks, existing links
type SkipRegion struct {
    Start, End int
}

func findSkipRegions(html string) []SkipRegion {
    var regions []SkipRegion
    
    // Find <code>...</code>
    for _, match := range codePattern.FindAllStringIndex(html, -1) {
        regions = append(regions, SkipRegion{match[0], match[1]})
    }
    
    // Find <a>...</a>
    for _, match := range linkPattern.FindAllStringIndex(html, -1) {
        regions = append(regions, SkipRegion{match[0], match[1]})
    }
    
    return regions
}

func isInSkipRegion(matchIdx []int, regions []SkipRegion) bool {
    for _, r := range regions {
        if matchIdx[0] >= r.Start && matchIdx[1] <= r.End {
            return true
        }
    }
    return false
}
```

## Lessons Learned

### 1. Order Matters in Processing Pipelines

When processing HTML with multiple transformations:
- Process more specific patterns first (URLs before file paths)
- Process inline code before regular text
- Apply sanitization after all transformations

### 2. Reverse Processing Preserves Indices

When doing multiple string replacements with indices, process in reverse order to avoid invalidating indices after each replacement.

### 3. Skip Regions Prevent Double-Processing

Always identify regions to skip (code blocks, existing links) before processing to avoid nested replacements.

See also: `07-regex-patterns.md` for regex syntax reference

