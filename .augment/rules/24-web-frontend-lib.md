---
description: lib.js utility functions, markdown rendering, message processing, and pure function patterns
globs:
  - "web/static/lib.js"
  - "web/static/lib.test.js"
---

# lib.js Utility Functions

The library provides pure functions for state manipulation.

## Core Functions

| Function                       | Purpose                                                |
| ------------------------------ | ------------------------------------------------------ |
| `computeAllSessions()`         | Merge active + stored sessions, sort by time           |
| `convertEventsToMessages()`    | Transform stored events to display messages            |
| `createSessionState()`         | Create new session state object                        |
| `addMessageToSessionState()`   | Add message with automatic trimming                    |
| `updateLastMessageInSession()` | Immutably update last message                          |
| `removeSessionFromState()`     | Remove session and determine next active               |
| `limitMessages()`              | Enforce MAX_MESSAGES limit                             |
| `getMinSeq(events)`            | Get minimum sequence number from events array          |
| `getMaxSeq(events)`            | Get maximum sequence number from events/messages array |
| `generatePromptId()`           | Generate unique prompt ID for delivery tracking        |
| `savePendingPrompt()`          | Save prompt to localStorage before sending             |
| `removePendingPrompt()`        | Remove prompt after ACK received                       |
| `getPendingPrompts()`          | Get all pending prompts from localStorage              |
| `mergeMessagesWithSync()`      | Deduplicate and append messages when syncing           |
| `getMessageHash()`             | Create content hash for message deduplication          |

## User Message Markdown Rendering

User messages support Markdown rendering with performance safeguards:

```
User Message Text
       ↓
hasMarkdownContent() → false → Plain text display (<pre>)
       ↓ true
Length > MAX_MARKDOWN_LENGTH → Plain text display
       ↓ within limit
window.marked.parse() → DOMPurify.sanitize() → HTML display
```

### Performance Safeguards

1. **Heuristic detection**: `hasMarkdownContent()` checks for patterns before rendering
2. **Length limit**: Messages > 10,000 chars skip Markdown processing
3. **Memoization**: `useMemo()` prevents re-rendering on every component update
4. **Graceful fallback**: Any error returns `null` → plain text display

### Markdown Detection Patterns

The `hasMarkdownContent()` function detects:

- Headers (`#`, `##`, etc.)
- Bold (`**text**`, `__text__`)
- Italic (`*text*`, `_text_`)
- Code (`` `code` ``, ` `blocks` `)
- Links (`[text](url)`)
- Lists (`- item`, `1. item`)
- Blockquotes (`> text`)
- Tables, horizontal rules, strikethrough

### Usage in Message Component

```javascript
import { renderUserMarkdown } from "../lib.js";

const renderedHtml = useMemo(
  () => renderUserMarkdown(message.text),
  [message.text],
);
const useMarkdown = renderedHtml !== null;

return useMarkdown
  ? html`<div
      class="markdown-content markdown-content-user"
      dangerouslySetInnerHTML=${{ __html: renderedHtml }}
    />`
  : html`<pre class="whitespace-pre-wrap font-sans text-sm m-0">
${message.text}</pre
    >`;
```

### Styling User Message Markdown

User messages use `.markdown-content-user` class:

```css
.markdown-content-user pre {
  background: rgba(0, 0, 0, 0.15);
}
.markdown-content-user :not(pre) > code {
  background: rgba(0, 0, 0, 0.15);
}
.markdown-content-user a {
  color: #1d4ed8;
}
```

## Dynamic Sequence Calculation

The `getMaxSeq` function is used to calculate the last seen sequence number dynamically from messages in state, avoiding stale localStorage issues:

```javascript
import { getMaxSeq } from "../lib.js";

// Calculate lastSeenSeq from messages in state (not localStorage)
const sessionMessages = sessionsRef.current[sessionId]?.messages || [];
const lastSeq = getMaxSeq(sessionMessages);

// Use for sync requests
if (lastSeq > 0) {
  ws.send(
    JSON.stringify({
      type: "load_events",
      data: { after_seq: lastSeq },
    }),
  );
}
```

This approach eliminates the stale localStorage problem in WKWebView because the seq is always calculated from the actual messages being displayed.

## Pure Function Design

Keep functions in `lib.js` pure (no side effects, no DOM access) for easy testing:

```javascript
// Good: Pure function, easy to test
export function hasMarkdownContent(text) {
  if (!text || typeof text !== "string") return false;
  return /^#{1,6}\s+\S/m.test(text); // Check for headers
}

// Avoid: Function with side effects
export function renderAndInsert(text, element) {
  element.innerHTML = marked.parse(text); // Hard to test
}
```

## Browser Environment Handling

Functions that depend on browser globals should gracefully handle their absence:

```javascript
export function renderUserMarkdown(text) {
  // Check if marked and DOMPurify are available
  if (typeof window === "undefined" || !window.marked || !window.DOMPurify) {
    return null; // Graceful fallback
  }
  // ... render logic
}
```
