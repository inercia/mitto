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

## WebSocket and Async Anti-Patterns

### ❌ Don't: Show Timeout Warning for Synchronous Errors

```javascript
// BAD: Backend returns error immediately, but frontend still shows timeout warning
const handleSubmit = async () => {
  const timeoutId = setTimeout(() => {
    showWarning("Message delivery could not be confirmed"); // Wrong!
  }, 5000);

  try {
    await sendPrompt(message); // Backend returns error synchronously
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
    clearTimeout(timeoutId); // Always clear, regardless of success/failure
  }
};
```

### ❌ Don't: Assume WebSocket State from `readyState`

```javascript
// BAD: readyState can be OPEN even for zombie connections
if (ws.readyState === WebSocket.OPEN) {
  ws.send(message); // May silently fail!
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

## M1 Deduplication Anti-Patterns

### ❌ Don't: Keep Stale Seq Tracker on Client Recovery

```javascript
// BAD: Seq tracker not reset when stale client detected
case "events_loaded": {
  const isStaleClient = isStaleClientState(clientLastSeq, serverLastSeq);

  // Process events without resetting tracker
  for (const event of events) {
    if (event.seq) {
      markSeqSeen(sessionId, event.seq);  // Tracker still has stale highestSeq!
    }
  }
  // Fresh events from server are rejected as "very old" duplicates!
}
```

**Problem**: When client has stale state (e.g., `highestSeq = 200` but server now has `lastSeq = 50`), the `isSeqDuplicate()` function rejects any seq < 100 as "very old":

```javascript
// In isSeqDuplicate()
if (seq < tracker.highestSeq - MAX_RECENT_SEQS) {
  return true; // Wrongly marks fresh events as duplicates!
}
```

### ✅ Do: Reset Seq Tracker Before Processing Stale Recovery Events

```javascript
// GOOD: Reset tracker when stale client detected
case "events_loaded": {
  const isStaleClient = isStaleClientState(clientLastSeq, serverLastSeq);

  // CRITICAL: Reset tracker BEFORE processing events
  if (isStaleClient) {
    console.log(`[M1 fix] Resetting seq tracker for stale client`);
    clearSeenSeqs(sessionId);
  }

  // Now process events with fresh tracker
  for (const event of events) {
    if (event.seq) {
      markSeqSeen(sessionId, event.seq);
    }
  }
}
```

**Key insight**: The M1 deduplication tracker (`seenSeqsRef`) must be reset when the client detects stale state, otherwise fresh events from the server will be wrongly rejected.

### ❌ Don't: Reset lastSentSeq on Fallback to Initial Load

```go
// BAD: Resetting lastSentSeq when falling back to initial load
if afterSeq > serverMaxSeq {
    events, err = c.store.ReadEventsLast(c.sessionID, limit, 0)
    isPrepend = false
    // Reset lastSentSeq since we're doing a fresh load
    c.seqMu.Lock()
    c.lastSentSeq = 0  // BUG: This loses track of observer-delivered events!
    c.seqMu.Unlock()
}
```

**Problem**: The observer path may have already delivered events with higher seq numbers than what's in storage (events not yet persisted). Resetting `lastSentSeq` causes `replayBufferedEventsWithDedup` to re-send those events, resulting in duplicate messages in the UI.

**Race condition:**

1. Agent streams message, observer delivers seq=18 to client (`lastSentSeq=18`)
2. Client sends keepalive, server detects mismatch (client has seq=18, storage only has seq=10)
3. Server falls back to initial load, resets `lastSentSeq=0`, then updates to `lastSentSeq=10`
4. `replayBufferedEventsWithDedup` sees seq=18 > `lastSentSeq=10`, sends seq=18 again
5. Client receives seq=18 twice → duplicate message!

### ✅ Do: Preserve lastSentSeq on Fallback

```go
// GOOD: Don't reset lastSentSeq - preserve observer-delivered events
if afterSeq > serverMaxSeq {
    events, err = c.store.ReadEventsLast(c.sessionID, limit, 0)
    isPrepend = false
    // NOTE: We intentionally do NOT reset lastSentSeq here.
    // The observer path may have already delivered events with higher seq numbers
    // than what's in storage. The lastSentSeq will be updated below based on
    // the loaded events, but only if they have higher seq than what was already sent.
}
```

**Key insight**: The `lastSentSeq` tracks what was sent via ANY path (observer or load_events). Resetting it loses track of observer-delivered events that aren't yet persisted.

### ❌ Don't: Append Duplicate HTML During Streaming Coalescing

```javascript
// BAD: Blindly appending HTML without checking for duplicates
if (shouldAppend) {
    const newHtml = (last.html || "") + msg.data.html;
    messages[messages.length - 1] = { ...last, html: newHtml };
}
```

**Problem**: If the backend sends the same complete HTML multiple times with the same seq (edge case), the frontend appends identical content repeatedly, causing duplicate text in the UI.

### ✅ Do: Check for Duplicate Content Before Appending

```javascript
// GOOD: Check if content is already present before appending
if (shouldAppend) {
    const existingHtml = last.html || "";
    const incomingHtml = msg.data.html;

    // Safeguard: Skip if this is duplicate content
    if (existingHtml.endsWith(incomingHtml)) {
        console.log("[DEBUG agent_message] Skipping duplicate append");
        return prev;
    }

    const newHtml = existingHtml + incomingHtml;
    messages[messages.length - 1] = { ...last, html: newHtml };
}
```

**Key insight**: This is a defense-in-depth safeguard. The primary fix is server-side (preserving `lastSentSeq`), but this frontend check provides additional protection.

## WKWebView Anti-Patterns

### ❌ Don't: Store Sync State in localStorage

```javascript
// BAD: Storing lastSeenSeq in localStorage
localStorage.setItem(`mitto_last_seen_seq_${sessionId}`, lastSeq);
// Later...
const lastSeq = localStorage.getItem(`mitto_last_seen_seq_${sessionId}`);
// This value can be stale in WKWebView!
```

**Problem**: WKWebView's localStorage can desynchronize from the actual data store, causing:

- Stale seq values that don't match displayed messages
- Sync requests that return 0 events when messages exist
- Messages appearing to be "lost" until page reload

### ✅ Do: Calculate Sync State from Application State

```javascript
// GOOD: Calculate lastSeenSeq dynamically from messages in state
import { getMaxSeq } from "../lib.js";

ws.onopen = () => {
  // Calculate from actual messages being displayed
  const sessionMessages = sessionsRef.current[sessionId]?.messages || [];
  const lastSeq = getMaxSeq(sessionMessages);

  if (lastSeq > 0) {
    ws.send(
      JSON.stringify({
        type: "load_events",
        data: { after_seq: lastSeq },
      }),
    );
  } else {
    // Initial load
    ws.send(
      JSON.stringify({
        type: "load_events",
        data: { limit: INITIAL_EVENTS_LIMIT },
      }),
    );
  }
};
```

**Benefits**:

- Always reflects actual displayed messages
- No stale localStorage issues
- Works correctly in WKWebView
- Simpler code (no localStorage read/write)

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

### 7. Calculate Sync State from Application State, Not localStorage

For sync state (like `lastSeenSeq`), calculate dynamically from application state:

- localStorage can become stale, especially in WKWebView
- Application state (messages in React/Preact state) is always current
- Use `getMaxSeq(messages)` instead of `localStorage.getItem('lastSeenSeq')`
- This eliminates an entire class of "missing messages" bugs

### 8. Auth Page Assets Must Be in Public Paths

When adding assets to authentication pages (auth.html), ensure they're in `publicStaticPaths`:

```go
// internal/web/auth.go
var publicStaticPaths = map[string]bool{
    "/auth.html":    true,
    "/auth.js":      true,
    "/tailwind.css": true,  // ← Don't forget CSS!
    // ...
}
```

**Symptom**: Login page shows unstyled HTML, browser console shows MIME type error
**Cause**: CSS file not in public paths → redirected to auth.html → wrong MIME type
**Fix**: Add the CSS file to `publicStaticPaths`

See `14-web-backend-auth.md` for complete authentication patterns.

### 9. Reset Deduplication State on Stale Client Recovery

When a client reconnects with stale state (detected by `clientLastSeq > serverLastSeq`), any deduplication tracking state must be reset:

- The M1 seq tracker (`seenSeqsRef`) tracks `highestSeq` from previously seen events
- If not reset, fresh events from the server are wrongly rejected as "very old" duplicates
- The `isSeqDuplicate()` function uses `seq < highestSeq - MAX_RECENT_SEQS` to detect old events
- With stale `highestSeq = 200` and `MAX_RECENT_SEQS = 100`, any `seq < 100` is rejected

**Pattern**: Always reset deduplication state when transitioning from stale to fresh state:

```javascript
if (isStaleClient) {
  clearSeenSeqs(sessionId); // Reset M1 tracker
  // Then process fresh events from server
}
```

### 10. Preserve lastSentSeq on Fallback to Initial Load

When `handleLoadEvents` falls back to initial load (due to client/server seq mismatch), do NOT reset `lastSentSeq` to 0:

- The observer path may have already delivered events with higher seq numbers than what's in storage
- Resetting `lastSentSeq` causes `replayBufferedEventsWithDedup` to re-send those events
- This results in duplicate messages appearing in the UI

**Pattern**: Preserve `lastSentSeq` and only update it if loaded events have higher seq:

```go
if afterSeq > serverMaxSeq {
    events, err = c.store.ReadEventsLast(c.sessionID, limit, 0)
    // NOTE: Do NOT reset lastSentSeq here - observer may have delivered higher seqs
}
// Later: update lastSentSeq only if lastSeq > c.lastSentSeq
```

### 11. Frontend HTML Duplicate Safeguard

When coalescing streaming chunks (same seq), check if the incoming HTML is already present:

- The `isSeqDuplicate` function allows same-seq events for coalescing
- If backend sends the same complete HTML multiple times, it would be appended repeatedly
- Check `existingHtml.endsWith(incomingHtml)` before appending

**Pattern**: Defense-in-depth check before appending:

```javascript
if (shouldAppend) {
    if (existingHtml.endsWith(incomingHtml)) {
        return prev; // Skip duplicate content
    }
    const newHtml = existingHtml + incomingHtml;
    // ...
}
```

## Session Lifecycle Anti-Patterns

### ❌ Don't: Resume ACP for Archived Sessions

```go
// BAD: Resumes ACP without checking archived state
if bs == nil && store != nil {
    meta, err := store.GetMetadata(sessionID)
    if err == nil {
        // Missing archived check!
        bs, err = s.sessionManager.ResumeSession(sessionID, meta.Name, cwd)
    }
}
```

**Problem**: Archived sessions should be read-only with no ACP connection. Resuming ACP for archived sessions:
- Wastes resources (ACP process running for read-only session)
- Confuses users (green "active" dot on archived session)
- Violates the archive contract (archived = no active agent)

### ✅ Do: Check Archived State Before Resuming

```go
// GOOD: Skip resume for archived sessions
if bs == nil && store != nil {
    meta, err := store.GetMetadata(sessionID)
    if err == nil {
        if meta.Archived {
            // Don't resume - archived sessions are read-only
            if clientLogger != nil {
                clientLogger.Debug("Session is archived, not resuming ACP")
            }
        } else {
            bs, err = s.sessionManager.ResumeSession(sessionID, meta.Name, cwd)
        }
    }
}
```

### ❌ Don't: Show Active Indicator for Archived Sessions

```javascript
// BAD: All sessions in state are marked active
const isActiveSession = session.isActive || session.status === "active";
// Archived sessions incorrectly show green dot!
```

### ✅ Do: Check Archived State for UI Indicators

```javascript
// GOOD: Archived sessions are never "active"
const isActiveSession =
    !isArchived && (session.isActive || session.status === "active");
const isStreaming = !isArchived && (session.isStreaming || false);
```

### 12. Archived Sessions Must Not Have Active ACP

When archiving a session:
1. Wait for any active response to complete (graceful shutdown)
2. Close the ACP connection
3. Mark session as archived in metadata
4. Broadcast state change to all clients

When viewing an archived session:
1. Load history from storage (read-only)
2. Do NOT start ACP connection
3. Do NOT show active indicator

See `15-web-backend-session-lifecycle.md` for complete lifecycle patterns.

## GitHub API/MCP Anti-Patterns

### ❌ Don't: Assume MCP GitHub Tools Always Work

```
// BAD: Assuming MCP tool will succeed
Tool: add_issue_comment_github-adobe-corp
Error: 404 Not Found - repo doesn't exist on corporate GitHub

Tool: add_issue_comment_github-ghec
Error: 403 Unauthorized - Enterprise Managed User can't access public repos
```

**Problem**: MCP GitHub tools may fail due to:
- Wrong GitHub instance (corporate vs public)
- Enterprise Managed User restrictions
- Authentication scope limitations

### ✅ Do: Use `gh` CLI as Fallback

```bash
# GOOD: Use gh CLI when MCP tools fail
# Note: Replace 'inercia/mitto' with your actual repository (e.g., 'youruser/mitto' for forks)
gh issue comment 23 --repo inercia/mitto --body 'Comment text'
# Returns: https://github.com/inercia/mitto/issues/23#issuecomment-123456789
```

**Pattern**: When MCP GitHub tools fail, fall back to `gh` CLI which uses local authentication.

### 13. CDN Resources May Be Blocked by Tracking Prevention

When loading libraries from CDN (like Mermaid.js from `cdn.jsdelivr.net`), browsers with tracking prevention enabled may block the requests:

```javascript
// Console warning in Firefox
// "Tracking Prevention blocked access to storage for https://cdn.jsdelivr.net/..."
```

**Symptoms**:
- Feature works in Chromium but not Firefox/Safari
- Console shows tracking prevention warnings
- Library fails to load silently

**Considerations**:
- Test in multiple browsers with different privacy settings
- Consider bundling critical libraries instead of CDN loading
- Document browser-specific limitations in issue responses
