---
description: Common anti-patterns to avoid, lessons learned, and best practices from past implementations
keywords:
  - anti-pattern
  - best practice
  - lessons learned
  - pitfall
  - mistake
  - wrong
  - don't
  - avoid
alwaysApply: false
---

# Anti-Patterns and Lessons Learned

This file contains testing anti-patterns and general lessons learned. For specific topics, see:

| Topic | File |
|-------|------|
| Text/HTML processing | `35-anti-patterns-text.md` |
| WebSocket/async | `36-anti-patterns-websocket.md` |
| Mobile/WKWebView | `37-anti-patterns-mobile.md` |
| Session lifecycle | `38-anti-patterns-session.md` |
| UI rendering (menus, positioning) | `28-anti-patterns-ui.md` |

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
  URL_PATTERN.lastIndex = 0; // Reset state
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

### 1. Test Coverage Reveals Edge Cases

Aiming for 90%+ coverage forces you to think about:
- Error paths
- Edge cases
- Boundary conditions
- Negative cases

### 2. Examples Are Documentation

Example tests serve multiple purposes:
- Verify functionality
- Document usage
- Provide copy-paste examples
- Catch regressions

### 3. Race Detector Catches Subtle Bugs

Always run tests with `-race` flag:

```bash
go test -race ./...
```

### 4. Auth Page Assets Must Be in Public Paths

When adding assets to authentication pages (auth.html), ensure they're in `publicStaticPaths`:

```go
var publicStaticPaths = map[string]bool{
    "/auth.html":    true,
    "/auth.js":      true,
    "/tailwind.css": true,  // ← Don't forget CSS!
}
```

**Symptom**: Login page shows unstyled HTML, browser console shows MIME type error

### 5. CDN Resources May Be Blocked by Tracking Prevention

When loading libraries from CDN (like Mermaid.js from `cdn.jsdelivr.net`), browsers with tracking prevention may block requests.

**Symptoms**:
- Feature works in Chromium but not Firefox/Safari
- Console shows tracking prevention warnings
- Library fails to load silently

### 6. GitHub MCP Tools May Need Fallback

MCP GitHub tools may fail due to:
- Wrong GitHub instance (corporate vs public)
- Enterprise Managed User restrictions
- Authentication scope limitations

**Pattern**: When MCP GitHub tools fail, fall back to `gh` CLI:

```bash
gh issue comment 23 --repo inercia/mitto --body 'Comment text'
```

## Related Files

For specific anti-patterns, see:
- `28-anti-patterns-ui.md` - UI rendering (context menus, positioning, useState vs useMemo)
- `35-anti-patterns-text.md` - Text/HTML processing
- `36-anti-patterns-websocket.md` - WebSocket/async
- `37-anti-patterns-mobile.md` - Mobile/WKWebView
- `38-anti-patterns-session.md` - Session lifecycle
