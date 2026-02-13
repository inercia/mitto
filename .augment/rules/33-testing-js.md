---
description: JavaScript unit tests with Jest, lib.js testing, mocking browser globals and localStorage
globs:
  - "web/static/lib.test.js"
  - "web/static/lib.js"
  - "web/static/package.json"
---

# JavaScript Unit Tests (lib.js)

Frontend utility functions in `lib.js` are tested with Jest. Tests run in Node.js without a browser.

## Running JavaScript Tests

```bash
cd web/static && npm test
```

## Test File Structure

```javascript
// lib.test.js
import {
  hasMarkdownContent,
  renderUserMarkdown,
  MAX_MARKDOWN_LENGTH,
} from "./lib.js";

describe("hasMarkdownContent", () => {
  test("returns false for plain text", () => {
    expect(hasMarkdownContent("Hello world")).toBe(false);
  });

  test("detects headers", () => {
    expect(hasMarkdownContent("# Header")).toBe(true);
  });
});
```

## Mocking Browser Globals

Functions that depend on browser globals should gracefully handle their absence:

```javascript
// In lib.js
export function renderUserMarkdown(text) {
  if (typeof window === "undefined" || !window.marked || !window.DOMPurify) {
    return null; // Graceful fallback
  }
  // ... render logic
}

// In lib.test.js
test("returns null when window.marked is not available", () => {
  // In Node.js, window is undefined, so this tests the fallback
  expect(renderUserMarkdown("# Header")).toBeNull();
});
```

## Mocking localStorage

```javascript
const localStorageMock = (() => {
  let store = {};
  return {
    getItem: (key) => store[key] || null,
    setItem: (key, value) => {
      store[key] = value;
    },
    removeItem: (key) => {
      delete store[key];
    },
    clear: () => {
      store = {};
    },
  };
})();

Object.defineProperty(global, "localStorage", { value: localStorageMock });

describe("savePendingPrompt", () => {
  beforeEach(() => localStorageMock.clear());

  test("saves and retrieves a pending prompt", () => {
    savePendingPrompt("session1", "prompt1", "Hello", []);
    const pending = getPendingPrompts();
    expect(pending["prompt1"]).toBeDefined();
  });
});
```

## Testing Pure Functions

Keep functions in `lib.js` pure for easy testing:

```javascript
// Good: Pure function, easy to test
export function hasMarkdownContent(text) {
  if (!text || typeof text !== "string") return false;
  return /^#{1,6}\s+\S/m.test(text);
}

// Avoid: Function with side effects
export function renderAndInsert(text, element) {
  element.innerHTML = marked.parse(text); // Hard to test
}
```

## Test Coverage Areas

- **Input validation**: null, undefined, empty string, wrong types
- **Edge cases**: very long strings, special characters
- **Regex patterns**: ensure patterns match expected inputs
- **Error handling**: graceful fallbacks when dependencies unavailable

## Testing Message Merge Functions

```javascript
describe("mergeMessagesWithSync", () => {
  test("preserves existing order and appends new messages", () => {
    const existing = [
      { role: ROLE_AGENT, html: "Third", seq: 3, timestamp: 3000 },
    ];
    const newMessages = [
      { role: ROLE_USER, text: "First", seq: 1, timestamp: 1000 },
    ];
    const result = mergeMessagesWithSync(existing, newMessages);
    expect(result).toHaveLength(2);
  });

  test("deduplicates by content hash", () => {
    const existing = [{ role: ROLE_USER, text: "Hello", timestamp: 1000 }];
    const newMessages = [
      { role: ROLE_USER, text: "Hello", seq: 1 }, // duplicate
      { role: ROLE_AGENT, html: "Response", seq: 2 },
    ];
    const result = mergeMessagesWithSync(existing, newMessages);
    expect(result).toHaveLength(2);
  });
});
```

## Testing Pending Prompt Functions

```javascript
describe("Pending Prompts", () => {
  beforeEach(() => localStorageMock.clear());

  describe("generatePromptId", () => {
    test("generates unique IDs", () => {
      const id1 = generatePromptId();
      const id2 = generatePromptId();
      expect(id1).not.toBe(id2);
    });

    test("includes timestamp prefix", () => {
      const id = generatePromptId();
      expect(id).toMatch(/^prompt_\d+_/);
    });
  });

  describe("cleanupExpiredPrompts", () => {
    test("removes prompts older than 5 minutes", () => {
      const pending = { old: { timestamp: Date.now() - 6 * 60 * 1000 } };
      localStorage.setItem("mitto_pending_prompts", JSON.stringify(pending));
      cleanupExpiredPrompts();
      expect(getPendingPrompts()).toEqual({});
    });
  });
});
```
