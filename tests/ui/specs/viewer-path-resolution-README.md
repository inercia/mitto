# Viewer Path Resolution Tests

This directory contains comprehensive tests for the `resolveWorkspacePath` function in `web/static/viewer.html`.

## Overview

The `resolveWorkspacePath` function handles URL-encoded markdown links in the file viewer, ensuring that links with special characters (spaces, `&`, `#`, etc.) are properly resolved and navigated to, rather than being opened as native OS apps.

## Test Files

### 1. `viewer-path-resolution.unit.test.js`
**Standalone unit tests** that can be run directly with Node.js.

**Run with:**
```bash
node tests/ui/specs/viewer-path-resolution.unit.test.js
```

**Coverage:** 38 comprehensive test cases covering:
- Basic relative paths
- URL-encoded characters (`%20`, `%26`, `%23`, etc.)
- Double-encoding scenarios
- Mixed encoding and plain text
- Special characters (`&`, `#`, `(`, `)`, etc.)
- Path normalization (`.`, `..`, `//`)
- Fragment identifiers (`#section`)
- Unicode characters (emoji)
- Edge cases (null, empty, malformed input)
- Whitespace handling
- Case sensitivity
- Idempotency

### 2. `viewer-path-resolution.spec.ts`
**Playwright integration tests** that test the function in a real browser context.

**Run with:**
```bash
npm run test:ui -- viewer-path-resolution.spec.ts
```
Or from the `tests/ui` directory:
```bash
npx playwright test viewer-path-resolution.spec.ts
```

## Implementation Details

### Key Features

1. **URL Decoding First**: The function decodes the entire URL BEFORE extracting the fragment. This ensures that `%23` (encoded `#`) is properly treated as a fragment separator.

2. **Single Decoding**: Only decodes once to avoid double-decoding issues (e.g., `%2520` → `%20`, not ` `).

3. **Graceful Error Handling**: Malformed URLs (invalid `%` sequences) don't throw errors - they fall back to the original path.

4. **Path Normalization**: Removes empty components (`//`), resolves `.` and `..`, and preserves case sensitivity.

5. **Whitespace Trimming**: Trims both the initial input and after decoding.

### Edge Cases Handled

| Edge Case | Example | Result |
|-----------|---------|--------|
| URL-encoded spaces | `Vanguard%20U.S.%20500` | `Vanguard U.S. 500` |
| Double-encoded | `%2520` | `%20` (decode once) |
| Mixed encoding | `Vanguard%20U.S. 500` | `Vanguard U.S. 500` |
| Encoded hash | `file.md%23section` | `{ path: 'file.md', fragment: '#section' }` |
| Multiple slashes | `..//dir//file.md` | `dir/file.md` |
| Excessive `..` | `../../../../file.md` | `file.md` (stops at root) |
| Malformed percent | `file%2.md` | `file%2.md` (original) |
| Unicode emoji | `test-%F0%9F%98%80.md` | `test-😀.md` |
| Leading/trailing space | `  ../file.md  ` | `file.md` |
| Null/undefined | `null`, `undefined` | `null` |

### Function Signature

```javascript
function resolveWorkspacePath(href: string): { path: string, fragment: string } | null
```

**Parameters:**
- `href` - The link href from markdown (e.g., `../symbols/Vanguard%20U.S.%20500/file.md#section`)

**Returns:**
- Object with `path` (resolved file path) and `fragment` (hash fragment), or
- `null` if input is invalid or empty

**Global Dependency:**
- Uses global `path` variable (current file path) for relative path resolution

## Test Results

All **38 tests pass** with the current implementation:

```
✅ 38/38 tests passing
```

Key test categories:
- ✅ Basic path resolution (6 tests)
- ✅ URL encoding/decoding (12 tests)
- ✅ Path normalization (8 tests)
- ✅ Fragment handling (4 tests)
- ✅ Edge cases & error handling (8 tests)

## Example Use Cases

### Real-World Scenario
You have a markdown file at:
```
/Users/user/Documents/Investments/recommendations/sell-2026-04-28.md
```

With a link like:
```markdown
[analysis](../symbols/Vanguard%20U.S.%20500%20Stock%20Index%20Fund/analysis-2026-04-28.md)
```

**Before fix:** Browser tries to open `Vanguard%20U.S.%20500%20Stock%20Index%20Fund` as a native app

**After fix:** Resolves to `symbols/Vanguard U.S. 500 Stock Index Fund/analysis-2026-04-28.md` and navigates correctly

## Maintenance

When modifying `resolveWorkspacePath` in `viewer.html`:

1. Update the function implementation in all test files:
   - `viewer-path-resolution.unit.test.js` (standalone)
   - `viewer-path-resolution.spec.ts` (Playwright)

2. Run unit tests first for fast feedback:
   ```bash
   node tests/ui/specs/viewer-path-resolution.unit.test.js
   ```

3. Run Playwright tests for integration verification:
   ```bash
   npx playwright test viewer-path-resolution.spec.ts
   ```

4. Add new test cases for any new edge cases discovered

## References

- Implementation: `web/static/viewer.html` (~line 959)
- Issue: Link navigation with URL-encoded characters
- Related: File link handling, markdown rendering
