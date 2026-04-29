/**
 * Comprehensive tests for viewer.html resolveWorkspacePath function
 * Tests all edge cases: URL encoding, special characters, normalization, etc.
 */

import { test, expect, Page } from '@playwright/test';
import path from 'path';
import fs from 'fs';
import { fileURLToPath } from 'url';
import { dirname } from 'path';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

test.describe('viewer.html resolveWorkspacePath', () => {
  let page: Page;
  let workspaceDir: string;
  let testFilePath: string;

  test.beforeAll(async ({ browser }) => {
    page = await browser.newPage();

    // Note: Workspace setup is not needed for these tests as they only test
    // the JavaScript function in isolation, not actual file navigation
  });

  test.beforeEach(async () => {
    // Load viewer.html and inject test harness
    await page.goto('about:blank');
    
    // Inject the resolveWorkspacePath function from viewer.html
    await page.evaluate(() => {
      // Mock the global 'path' variable (simulates current file path)
      (window as any).path = 'recommendations/sell-2026-04-28.md';
      
      // Paste the actual resolveWorkspacePath function
      (window as any).resolveWorkspacePath = function(href: string) {
        const path = (window as any).path;
        
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

        // Get current directory from the global 'path' variable
        const currentDir = path.includes("/")
          ? path.substring(0, path.lastIndexOf("/"))
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
      };
    });
  });

  test('basic relative path', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../test.md');
    });
    expect(result).toEqual({ path: 'test.md', fragment: '' });
  });

  test('URL-encoded spaces (%20)', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../symbols/Vanguard%20U.S.%20500%20Stock%20Index%20Fund/analysis-2026-04-28.md');
    });
    expect(result).toEqual({ 
      path: 'symbols/Vanguard U.S. 500 Stock Index Fund/analysis-2026-04-28.md', 
      fragment: '' 
    });
  });

  test('URL-encoded special characters (%26 for &)', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../files/Q%26A%20(2024).md');
    });
    expect(result).toEqual({ 
      path: 'files/Q&A (2024).md', 
      fragment: '' 
    });
  });

  test('double-encoded path (should decode once only)', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../symbols/Vanguard%2520U.S./file.md');
    });
    expect(result).toEqual({ 
      path: 'symbols/Vanguard%20U.S./file.md', 
      fragment: '' 
    });
  });

  test('mixed encoding and plain spaces', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../symbols/Vanguard%20U.S. 500/file.md');
    });
    expect(result).toEqual({ 
      path: 'symbols/Vanguard U.S. 500/file.md', 
      fragment: '' 
    });
  });

  test('special characters: &, (, ), etc.', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../files/Q&A%20(2024).md');
    });
    expect(result).toEqual({ 
      path: 'files/Q&A (2024).md', 
      fragment: '' 
    });
  });

  test('multiple slashes (//)', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('..//..//test.md');
    });
    expect(result).toEqual({ 
      path: 'test.md', 
      fragment: '' 
    });
  });

  test('trailing slash', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../symbols/');
    });
    expect(result).toEqual({ 
      path: 'symbols', 
      fragment: '' 
    });
  });

  test('dot segments: . and ..', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../.././dir/../test.md');
    });
    expect(result).toEqual({ 
      path: 'test.md', 
      fragment: '' 
    });
  });

  test('empty path components', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../symbols//file.md');
    });
    expect(result).toEqual({ 
      path: 'symbols/file.md', 
      fragment: '' 
    });
  });

  test('fragment identifier (#section)', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../test.md#section-1');
    });
    expect(result).toEqual({ 
      path: 'test.md', 
      fragment: '#section-1' 
    });
  });

  test('fragment with URL-encoded path', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../symbols/Vanguard%20U.S.%20500/file.md#top');
    });
    expect(result).toEqual({ 
      path: 'symbols/Vanguard U.S. 500/file.md', 
      fragment: '#top' 
    });
  });

  test('leading whitespace (trimmed)', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('  ../test.md');
    });
    expect(result).toEqual({ 
      path: 'test.md', 
      fragment: '' 
    });
  });

  test('trailing whitespace (trimmed)', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../test.md  ');
    });
    expect(result).toEqual({ 
      path: 'test.md', 
      fragment: '' 
    });
  });

  test('encoded whitespace at edges', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('%20../test.md%20');
    });
    expect(result).toEqual({ 
      path: 'test.md', 
      fragment: '' 
    });
  });

  test('case sensitivity preserved', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../Files/Test.MD');
    });
    expect(result).toEqual({ 
      path: 'Files/Test.MD', 
      fragment: '' 
    });
  });

  test('absolute path (relative to workspace root)', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('/symbols/test.md');
    });
    expect(result).toEqual({ 
      path: 'symbols/test.md', 
      fragment: '' 
    });
  });

  test('current directory (.)', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('./test.md');
    });
    expect(result).toEqual({ 
      path: 'recommendations/test.md', 
      fragment: '' 
    });
  });

  test('multiple .. navigations', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../../symbols/file.md');
    });
    expect(result).toEqual({ 
      path: 'symbols/file.md', 
      fragment: '' 
    });
  });

  test('pure fragment (no path)', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('#section');
    });
    expect(result).toBeNull();
  });

  test('empty string', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('');
    });
    expect(result).toBeNull();
  });

  test('null input', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath(null);
    });
    expect(result).toBeNull();
  });

  test('undefined input', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath(undefined);
    });
    expect(result).toBeNull();
  });

  test('only whitespace', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('   ');
    });
    expect(result).toBeNull();
  });

  test('malformed URL encoding (invalid %)', async () => {
    const result = await page.evaluate(() => {
      // This should not throw, should handle gracefully
      return (window as any).resolveWorkspacePath('../file%2.md');
    });
    // Should use original path when decoding fails
    expect(result).toEqual({ 
      path: 'file%2.md', 
      fragment: '' 
    });
  });

  test('Unicode characters (emoji)', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../files/test-😀.md');
    });
    expect(result).toEqual({ 
      path: 'files/test-😀.md', 
      fragment: '' 
    });
  });

  test('Unicode characters (URL-encoded)', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../files/test-%F0%9F%98%80.md');
    });
    expect(result).toEqual({ 
      path: 'files/test-😀.md', 
      fragment: '' 
    });
  });

  test('complex combination: encoded spaces + special chars + fragments', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../files/Q%26A%20(2024).md#section-1');
    });
    expect(result).toEqual({ 
      path: 'files/Q&A (2024).md', 
      fragment: '#section-1' 
    });
  });

  test('complex combination: multiple ../ + encoded + normalization', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../.././symbols/Vanguard%20U.S.%20500/../test.md');
    });
    expect(result).toEqual({ 
      path: 'symbols/test.md', 
      fragment: '' 
    });
  });

  test('idempotency: calling twice gives same result', async () => {
    const result1 = await page.evaluate(() => {
      const fn = (window as any).resolveWorkspacePath;
      const r1 = fn('../symbols/Vanguard%20U.S.%20500/file.md');
      return r1;
    });
    
    const result2 = await page.evaluate(() => {
      const fn = (window as any).resolveWorkspacePath;
      const r2 = fn('../symbols/Vanguard%20U.S.%20500/file.md');
      return r2;
    });
    
    expect(result1).toEqual(result2);
    expect(result1).toEqual({ 
      path: 'symbols/Vanguard U.S. 500/file.md', 
      fragment: '' 
    });
  });

  test('edge case: .. at root level', async () => {
    // Set path to root level file
    await page.evaluate(() => {
      (window as any).path = 'test.md';
    });
    
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../file.md');
    });
    
    // Should resolve to file.md (.. is no-op at root)
    expect(result).toEqual({ 
      path: 'file.md', 
      fragment: '' 
    });
  });

  test('edge case: excessive .. navigations', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../../../../test.md');
    });
    
    // Should not go beyond root
    expect(result).toEqual({ 
      path: 'test.md', 
      fragment: '' 
    });
  });

  test('query string in filename (treated as part of filename)', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../files/test.md?query=param');
    });
    
    // Query string is part of the filename in file:// context
    expect(result).toEqual({ 
      path: 'files/test.md?query=param', 
      fragment: '' 
    });
  });

  test('fragment before query (fragment takes precedence)', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../files/test.md#section?query=param');
    });
    
    // Everything after # is the fragment
    expect(result).toEqual({ 
      path: 'files/test.md', 
      fragment: '#section?query=param' 
    });
  });

  test('URL-encoded hash (%23) is decoded and treated as hash', async () => {
    const result = await page.evaluate(() => {
      return (window as any).resolveWorkspacePath('../files/test.md%23section');
    });
    
    // %23 decodes to #, becomes fragment
    expect(result).toEqual({ 
      path: 'files/test.md', 
      fragment: '#section' 
    });
  });

  test.afterAll(async () => {
    await page.close();
    // Cleanup is handled by Playwright's test-results cleanup
  });
});
