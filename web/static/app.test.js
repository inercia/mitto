/**
 * Regression tests for the mitto-yvel.4 protected-conversation archive
 * suppression wiring in app.js (header toolbar gate + native
 * window.mittoArchiveConversation shortcut guard).
 *
 * app.js cannot be imported directly under jsdom (it reads `window.preact`
 * at module load time, same limitation documented in SessionItem.test.js /
 * BeadsView.test.js), so these tests read the raw source and assert on the
 * exact wiring rather than executing the component.
 */

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const __dirname = dirname(fileURLToPath(import.meta.url));
const appJs = readFileSync(resolve(__dirname, "app.js"), "utf8");

describe("app.js: protected-conversation archive suppression (mitto-yvel.4)", () => {
  test("window.mittoArchiveConversation guards the native Cmd+Shift+A shortcut, allowing unarchive through", () => {
    const idx = appJs.indexOf("window.mittoArchiveConversation = ");
    expect(idx).toBeGreaterThan(-1);
    const snippet = appJs.slice(idx, idx + 1200);

    // isProtected is derived before the guard and checked alongside isArchived
    // so only the archive direction is blocked.
    expect(snippet).toMatch(
      /const isProtected =\s*\n\s*currentSession\.no_archive \|\| currentSession\.info\?\.no_archive;/,
    );
    expect(snippet).toMatch(/if \(!isArchived && isProtected\) \{/);
    expect(snippet).toMatch(
      /title: "This conversation is protected from archiving",/,
    );

    // The guard must return before the archiveSession toggle call, otherwise
    // the shortcut would archive anyway after showing the toast.
    const guardIdx = snippet.indexOf("if (!isArchived && isProtected)");
    const toggleIdx = snippet.indexOf("await archiveSession(");
    expect(guardIdx).toBeGreaterThan(-1);
    expect(toggleIdx).toBeGreaterThan(guardIdx);
  });

  test("header toolbar gate: headerIsProtected suppresses archive only, unarchive stays available", () => {
    expect(appJs).toMatch(
      /const headerIsProtected = !!\(\s*\n\s*activeSession\?\.no_archive \|\| sessionInfo\?\.no_archive\s*\n\s*\);/,
    );
    expect(appJs).toMatch(
      /const headerCanArchive =\s*\n\s*headerIsArchived \|\|\s*\n\s*\(!headerIsProtected && !headerHasQueued && !isStreaming\);/,
    );
  });

  test("headerArchiveBlockedReason surfaces the protected reason first, only when not archived", () => {
    const idx = appJs.indexOf("const headerArchiveBlockedReason =");
    expect(idx).toBeGreaterThan(-1);
    const snippet = appJs.slice(idx, idx + 400);
    expect(snippet).toMatch(
      /\? headerIsProtected\s*\n\s*\? "This conversation is protected from archiving"/,
    );
    // The protected check must precede the queued/streaming checks so its
    // reason wins when a conversation is both protected and queued/streaming.
    const protectedIdx = snippet.indexOf("headerIsProtected");
    const queuedIdx = snippet.indexOf("headerHasQueued");
    expect(protectedIdx).toBeGreaterThan(-1);
    expect(queuedIdx).toBeGreaterThan(protectedIdx);
  });
});
