/**
 * Standalone unit tests for resolveWorkspacePath function
 * Can be run directly with Node.js: node viewer-path-resolution.unit.test.js
 */

// The resolveWorkspacePath function from viewer.html
function resolveWorkspacePath(href, currentPath) {
  // Handle null/undefined/empty input
  if (!href || typeof href !== 'string') return null;
  
  // Trim leading/trailing whitespace
  href = href.trim();
  if (!href) return null;

  // Decode URL-encoded characters FIRST (e.g., %20 for spaces, %23 for #)
  // This is important because %23 (encoded #) should become a real fragment separator
  // Only decode once to avoid double-decoding issues
  try {
    href = decodeURIComponent(href);
  } catch (e) {
    // If decoding fails (malformed %), use original href
    console.warn("Failed to decode URL:", href, e);
    // Continue with original href
  }

  // Extract fragment (hash) AFTER decoding
  // This way %23 in the original URL becomes # and is properly treated as a fragment
  const hashIdx = href.indexOf("#");
  let linkPath = hashIdx >= 0 ? href.substring(0, hashIdx) : href;
  const fragment = hashIdx >= 0 ? href.substring(hashIdx) : "";

  // Pure fragment (no path)
  if (!linkPath) return null;

  // Trim the path (in case of spaces at edges)
  linkPath = linkPath.trim();
  if (!linkPath) return null;

  // Get current directory from the provided path
  const currentDir = currentPath.includes("/")
    ? currentPath.substring(0, currentPath.lastIndexOf("/"))
    : "";

  // Resolve absolute vs relative paths
  let resolved;
  if (linkPath.startsWith("/")) {
    // Absolute path (relative to workspace root)
    resolved = linkPath.substring(1);
  } else {
    // Relative path (relative to current file's directory)
    resolved = currentDir ? currentDir + "/" + linkPath : linkPath;
  }

  // Normalize path:
  // - Remove empty components (from double slashes //)
  // - Resolve . (current directory)
  // - Resolve .. (parent directory)
  // - Preserve case exactly
  const parts = resolved.split("/");
  const normalized = [];
  
  for (const part of parts) {
    // Skip empty parts (from // or leading/trailing slashes)
    if (part === "" || part === ".") {
      continue;
    }
    
    // Handle parent directory (..)
    if (part === "..") {
      // Only pop if there's something to go back to
      if (normalized.length > 0) {
        normalized.pop();
      }
      // If we're already at the root, .. is a no-op
      continue;
    }
    
    // Normal path component - preserve exactly as-is (case-sensitive)
    normalized.push(part);
  }

  // Join normalized parts
  const finalPath = normalized.join("/");
  
  return { 
    path: finalPath, 
    fragment: fragment 
  };
}

// Test runner
class TestRunner {
  constructor() {
    this.tests = [];
    this.passed = 0;
    this.failed = 0;
    this.currentPath = 'recommendations/sell-2026-04-28.md';
  }

  test(name, fn) {
    this.tests.push({ name, fn });
  }

  assertEqual(actual, expected, message) {
    const actualStr = JSON.stringify(actual);
    const expectedStr = JSON.stringify(expected);
    if (actualStr !== expectedStr) {
      throw new Error(
        `${message || 'Assertion failed'}\n` +
        `  Expected: ${expectedStr}\n` +
        `  Actual:   ${actualStr}`
      );
    }
  }

  assertNull(actual, message) {
    if (actual !== null) {
      throw new Error(
        `${message || 'Expected null'}\n` +
        `  Actual: ${JSON.stringify(actual)}`
      );
    }
  }

  async run() {
    console.log(`\n🧪 Running ${this.tests.length} tests...\n`);

    for (const { name, fn } of this.tests) {
      try {
        await fn.call(this);
        this.passed++;
        console.log(`✅ ${name}`);
      } catch (error) {
        this.failed++;
        console.log(`❌ ${name}`);
        console.log(`   ${error.message}\n`);
      }
    }

    console.log(`\n${'='.repeat(60)}`);
    console.log(`Total: ${this.tests.length} | Passed: ${this.passed} | Failed: ${this.failed}`);
    console.log('='.repeat(60));

    if (this.failed > 0) {
      process.exit(1);
    }
  }

  resolve(href) {
    return resolveWorkspacePath(href, this.currentPath);
  }
}

// Create test runner
const runner = new TestRunner();

// Basic tests
runner.test('basic relative path', function() {
  const result = this.resolve('../test.md');
  this.assertEqual(result, { path: 'test.md', fragment: '' });
});

runner.test('URL-encoded spaces (%20)', function() {
  const result = this.resolve('../symbols/Vanguard%20U.S.%20500%20Stock%20Index%20Fund/analysis-2026-04-28.md');
  this.assertEqual(result, { 
    path: 'symbols/Vanguard U.S. 500 Stock Index Fund/analysis-2026-04-28.md', 
    fragment: '' 
  });
});

runner.test('URL-encoded special characters (%26 for &)', function() {
  const result = this.resolve('../files/Q%26A%20(2024).md');
  this.assertEqual(result, { path: 'files/Q&A (2024).md', fragment: '' });
});

runner.test('double-encoded path (decode once only)', function() {
  const result = this.resolve('../symbols/Vanguard%2520U.S./file.md');
  this.assertEqual(result, { path: 'symbols/Vanguard%20U.S./file.md', fragment: '' });
});

runner.test('mixed encoding and plain spaces', function() {
  const result = this.resolve('../symbols/Vanguard%20U.S. 500/file.md');
  this.assertEqual(result, { path: 'symbols/Vanguard U.S. 500/file.md', fragment: '' });
});

runner.test('special characters: &, (, )', function() {
  const result = this.resolve('../files/Q&A%20(2024).md');
  this.assertEqual(result, { path: 'files/Q&A (2024).md', fragment: '' });
});

runner.test('multiple slashes (//)', function() {
  const result = this.resolve('..//..//test.md');
  this.assertEqual(result, { path: 'test.md', fragment: '' });
});

runner.test('trailing slash', function() {
  const result = this.resolve('../symbols/');
  this.assertEqual(result, { path: 'symbols', fragment: '' });
});

runner.test('dot segments: . and ..', function() {
  const result = this.resolve('../.././dir/../test.md');
  this.assertEqual(result, { path: 'test.md', fragment: '' });
});

runner.test('empty path components', function() {
  const result = this.resolve('../symbols//file.md');
  this.assertEqual(result, { path: 'symbols/file.md', fragment: '' });
});

runner.test('fragment identifier (#section)', function() {
  const result = this.resolve('../test.md#section-1');
  this.assertEqual(result, { path: 'test.md', fragment: '#section-1' });
});

runner.test('fragment with URL-encoded path', function() {
  const result = this.resolve('../symbols/Vanguard%20U.S.%20500/file.md#top');
  this.assertEqual(result, { path: 'symbols/Vanguard U.S. 500/file.md', fragment: '#top' });
});

runner.test('leading whitespace (trimmed)', function() {
  const result = this.resolve('  ../test.md');
  this.assertEqual(result, { path: 'test.md', fragment: '' });
});

runner.test('trailing whitespace (trimmed)', function() {
  const result = this.resolve('../test.md  ');
  this.assertEqual(result, { path: 'test.md', fragment: '' });
});

runner.test('encoded whitespace at edges', function() {
  const result = this.resolve('%20../test.md%20');
  this.assertEqual(result, { path: 'test.md', fragment: '' });
});

runner.test('case sensitivity preserved', function() {
  const result = this.resolve('../Files/Test.MD');
  this.assertEqual(result, { path: 'Files/Test.MD', fragment: '' });
});

runner.test('absolute path (relative to workspace root)', function() {
  const result = this.resolve('/symbols/test.md');
  this.assertEqual(result, { path: 'symbols/test.md', fragment: '' });
});

runner.test('current directory (.)', function() {
  const result = this.resolve('./test.md');
  this.assertEqual(result, { path: 'recommendations/test.md', fragment: '' });
});

runner.test('multiple .. navigations', function() {
  const result = this.resolve('../../symbols/file.md');
  this.assertEqual(result, { path: 'symbols/file.md', fragment: '' });
});

// Edge cases
runner.test('pure fragment (no path)', function() {
  const result = this.resolve('#section');
  this.assertNull(result);
});

runner.test('empty string', function() {
  const result = this.resolve('');
  this.assertNull(result);
});

runner.test('null input', function() {
  const result = this.resolve(null);
  this.assertNull(result);
});

runner.test('undefined input', function() {
  const result = this.resolve(undefined);
  this.assertNull(result);
});

runner.test('only whitespace', function() {
  const result = this.resolve('   ');
  this.assertNull(result);
});

runner.test('malformed URL encoding (invalid %)', function() {
  const result = this.resolve('../file%2.md');
  // Should use original path when decoding fails
  this.assertEqual(result, { path: 'file%2.md', fragment: '' });
});

runner.test('Unicode characters (emoji)', function() {
  const result = this.resolve('../files/test-😀.md');
  this.assertEqual(result, { path: 'files/test-😀.md', fragment: '' });
});

runner.test('Unicode characters (URL-encoded)', function() {
  const result = this.resolve('../files/test-%F0%9F%98%80.md');
  this.assertEqual(result, { path: 'files/test-😀.md', fragment: '' });
});

runner.test('complex: encoded spaces + special chars + fragments', function() {
  const result = this.resolve('../files/Q%26A%20(2024).md#section-1');
  this.assertEqual(result, { path: 'files/Q&A (2024).md', fragment: '#section-1' });
});

runner.test('complex: multiple ../ + encoded + normalization', function() {
  const result = this.resolve('../.././symbols/Vanguard%20U.S.%20500/../test.md');
  this.assertEqual(result, { path: 'symbols/test.md', fragment: '' });
});

runner.test('idempotency: calling twice gives same result', function() {
  const result1 = this.resolve('../symbols/Vanguard%20U.S.%20500/file.md');
  const result2 = this.resolve('../symbols/Vanguard%20U.S.%20500/file.md');
  this.assertEqual(result1, result2);
  this.assertEqual(result1, { path: 'symbols/Vanguard U.S. 500/file.md', fragment: '' });
});

runner.test('edge case: .. at root level', function() {
  // Test with root-level file
  const result = resolveWorkspacePath('../file.md', 'test.md');
  this.assertEqual(result, { path: 'file.md', fragment: '' });
});

runner.test('edge case: excessive .. navigations', function() {
  const result = this.resolve('../../../../test.md');
  this.assertEqual(result, { path: 'test.md', fragment: '' });
});

runner.test('query string in filename', function() {
  const result = this.resolve('../files/test.md?query=param');
  this.assertEqual(result, { path: 'files/test.md?query=param', fragment: '' });
});

runner.test('fragment before query', function() {
  const result = this.resolve('../files/test.md#section?query=param');
  this.assertEqual(result, { path: 'files/test.md', fragment: '#section?query=param' });
});

runner.test('URL-encoded hash (%23) becomes fragment', function() {
  const result = this.resolve('../files/test.md%23section');
  this.assertEqual(result, { path: 'files/test.md', fragment: '#section' });
});

runner.test('all URL-encoded special characters', function() {
  const result = this.resolve('../files/test%21%40%23%24%25%5E%26%2A%28%29.md');
  // Note: %23 decodes to #, which becomes a fragment separator
  this.assertEqual(result, { path: 'files/test!@', fragment: '#$%^&*().md' });
});

runner.test('path with %2F (encoded slash) - stays encoded', function() {
  const result = this.resolve('../files/test%2Fname.md');
  this.assertEqual(result, { path: 'files/test/name.md', fragment: '' });
});

runner.test('complex nested path with multiple encodings', function() {
  const result = this.resolve('../../a%20b/c%26d/e%28f%29/file.md#section%201');
  // Fragment is decoded along with the rest of the URL
  this.assertEqual(result, { path: 'a b/c&d/e(f)/file.md', fragment: '#section 1' });
});

// Run all tests
runner.run().catch(err => {
  console.error('Test runner failed:', err);
  process.exit(1);
});
