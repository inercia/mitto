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
  - race condition
  - zombie connection
  - timeout
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

## WebSocket and Async Anti-Patterns

### ❌ Don't: Show Timeout Warning for Synchronous Errors

```javascript
// BAD: Backend returns error immediately, but frontend still shows timeout warning
const handleSubmit = async () => {
    const timeoutId = setTimeout(() => {
        showWarning("Message delivery could not be confirmed");  // Wrong!
    }, 5000);

    try {
        await sendPrompt(message);  // Backend returns error synchronously
        clearTimeout(timeoutId);
    } catch (err) {
        // Error is shown, but timeout warning ALSO shows because clearTimeout
        // wasn't called before the error handler runs
        showError(err.message);
    }
};
```

**Problem**: User sees BOTH the error AND a confusing timeout warning.

### ✅ Do: Clear Timeout on ANY Promise Settlement

```javascript
// GOOD: Clear timeout before handling result
const handleSubmit = async () => {
    const timeoutId = setTimeout(() => {
        showWarning("Message delivery could not be confirmed");
    }, 5000);

    try {
        await sendPrompt(message);
    } catch (err) {
        showError(err.message);
    } finally {
        clearTimeout(timeoutId);  // Always clear, regardless of success/failure
    }
};
```

### ❌ Don't: Assume WebSocket State from `readyState`

```javascript
// BAD: readyState can be OPEN even for zombie connections
if (ws.readyState === WebSocket.OPEN) {
    ws.send(message);  // May silently fail!
}
```

### ✅ Do: Use Application-Level Keepalive

```javascript
// GOOD: Track actual message delivery
if (ws.readyState === WebSocket.OPEN && isConnectionHealthy(sessionId)) {
    ws.send(message);
}

// isConnectionHealthy checks keepalive_ack responses
const isConnectionHealthy = (sessionId) => {
    const keepalive = keepaliveRef.current[sessionId];
    return keepalive && keepalive.missedCount === 0;
};
```

## WKWebView Anti-Patterns

### ❌ Don't: Assume localStorage Consistency

```javascript
// BAD: Assuming localStorage in WKWebView matches browser
const lastSeq = localStorage.getItem(`mitto_last_seen_seq_${sessionId}`);
// This value can be stale in WKWebView!
```

**Problem**: WKWebView's localStorage can desynchronize from the actual data store.

### ✅ Do: Validate State on Reconnect

```javascript
// GOOD: Request fresh state from server on reconnect
ws.onopen = () => {
    // Don't trust localStorage lastSeenSeq completely
    // Request events and merge with deduplication
    ws.send(JSON.stringify({
        type: 'load_events',
        data: { after_seq: getLastSeenSeq(sessionId) || 0 }
    }));
};

// Use mergeMessagesWithSync to handle duplicates
case "events_loaded":
    messages = mergeMessagesWithSync(session.messages, newMessages);
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

### 5. Mobile/WKWebView Requires Extra Validation

Don't trust browser state in mobile contexts:
- Connections can be "zombie" (OPEN but dead)
- localStorage can be stale in WKWebView
- Always validate with server on reconnect
- Use keepalive to detect unhealthy connections

### 6. Synchronous Errors Need Different Handling Than Timeouts

When an operation can fail either synchronously (error) or asynchronously (timeout):
- Use `finally` to clean up timeout handlers
- Don't show timeout warnings for synchronous errors
- Clear pending state on both success AND failure

