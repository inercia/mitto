---
description: Common anti-patterns to avoid, lessons learned, and best practices from past implementations
globs:
  - "**/*.go"
  - "**/*.js"
alwaysApply: false
---

# Anti-Patterns and Lessons Learned

## Text Processing Anti-Patterns

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

## Testing Anti-Patterns

### ❌ Don't: Test Only Happy Paths

```go
// BAD: Only tests successful case
func TestURLDetection(t *testing.T) {
    result := detectURL("https://example.com")
    if !strings.Contains(result, "<a href=") {
        t.Error("URL not detected")
    }
}
```

### ✅ Do: Test Edge Cases and Negative Cases

```go
// GOOD: Tests both positive and negative cases
func TestURLDetection(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        shouldLink bool
    }{
        {"valid URL", "https://example.com", true},
        {"URL in code block", "<pre>https://example.com</pre>", false},
        {"partial URL", "example.com", false},
        {"URL with text", "see https://example.com here", false},
    }
    // ...
}
```

### ❌ Don't: Use Simple Boolean Checks

```go
// BAD: Doesn't verify what's in the output
if result != "" {
    t.Error("Expected non-empty result")
}
```

### ✅ Do: Use Contains/Excludes Pattern

```go
// GOOD: Verifies specific content
tests := []struct {
    name     string
    input    string
    contains []string
    excludes []string
}{
    {
        name:  "URL linkified",
        input: "<code>https://example.com</code>",
        contains: []string{
            `<a href="https://example.com"`,
            `<code>https://example.com</code></a>`,
        },
        excludes: []string{
            `<code>https://example.com</code>` + "\n", // Not wrapped
        },
    },
}
```

## Regex Anti-Patterns

### ❌ Don't: Recompile Regex in Loops

```go
// BAD: Compiles regex on every call
func processText(text string) string {
    pattern := regexp.MustCompile(`https?://[^\s]+`)  // Expensive!
    return pattern.ReplaceAllString(text, "...")
}
```

### ✅ Do: Compile Once at Package Level

```go
// GOOD: Compile once, reuse many times
var urlPattern = regexp.MustCompile(`https?://[^\s]+`)

func processText(text string) string {
    return urlPattern.ReplaceAllString(text, "...")
}
```

### ❌ Don't: Forget to Reset Global Regex State (JavaScript)

```javascript
// BAD: Global regex state persists between calls
const URL_PATTERN = /https?:\/\/[^\s]+/gi;

function findURLs(text) {
    // If called multiple times, lastIndex is wrong!
    return URL_PATTERN.exec(text);
}
```

### ✅ Do: Reset Regex State

```javascript
// GOOD: Reset state before use
const URL_PATTERN = /https?:\/\/[^\s]+/gi;

function findURLs(text) {
    URL_PATTERN.lastIndex = 0;  // Reset state
    return URL_PATTERN.exec(text);
}
```

## Implementation Anti-Patterns

### ❌ Don't: Add Features Without Tests

```go
// BAD: New feature without tests
func (fl *FileLinker) processURLs(html string) string {
    // Implementation...
    return result
}
// No tests written!
```

### ✅ Do: Write Tests First or Alongside

```go
// GOOD: Tests written alongside implementation
func TestFileLinker_ProcessURLs(t *testing.T) {
    tests := []struct {
        name  string
        input string
        want  string
    }{
        // Test cases...
    }
    // ...
}
```

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

## Lessons Learned

### 1. Order Matters in Processing Pipelines

When processing HTML with multiple transformations:
- Process more specific patterns first (URLs before file paths)
- Process inline code before regular text
- Apply sanitization after all transformations

### 2. Test Coverage Reveals Edge Cases

Aiming for 90%+ coverage forces you to think about:
- Error paths
- Edge cases
- Boundary conditions
- Negative cases

### 3. Examples Are Documentation

Example tests serve multiple purposes:
- Verify functionality
- Document usage
- Provide copy-paste examples
- Catch regressions

### 4. Race Detector Catches Subtle Bugs

Always run tests with `-race` flag:
```bash
go test -race ./...
```

Catches:
- Concurrent map access
- Shared state without locks
- Goroutine leaks

