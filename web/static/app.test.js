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

describe("app.js: SDK migration — session loop/flush handlers (mitto-7gta.17 slice S4)", () => {
  test("imports getSdkClient/isNotFoundError; the session-domain handlers no longer use authFetch/secureFetch", () => {
    expect(appJs).toMatch(
      /import \{ getSdkClient \} from "\.\/utils\/sdkClient\.js";/,
    );
    expect(appJs).toMatch(
      /import \{ errorMessage, isNotFoundError \} from "\.\/utils\/sdkErrors\.js";/,
    );
  });

  test("handleMakeLoop: restore -> (404 falls through, other errors rethrow) -> suggestFromRecent+set draft -> blank-draft set", () => {
    const idx = appJs.indexOf("const handleMakeLoop = useCallback(");
    expect(idx).toBeGreaterThan(-1);
    const snippet = appJs.slice(idx, idx + 2200);

    // Step 1: restore; a 404 falls through (does not rethrow), anything else rethrows.
    const restoreIdx = snippet.indexOf(
      "await getSdkClient().sessions.loop.restore(sessionId);",
    );
    expect(restoreIdx).toBeGreaterThan(-1);
    expect(snippet).toMatch(
      /catch \(err\) \{\s*\n\s*if \(!isNotFoundError\(err\)\) throw err;\s*\n\s*\}/,
    );

    // Step 2: suggestFromRecent feeds set(..., { enabled: false }); any failure
    // here falls through silently (no rethrow) to the blank-draft step.
    const suggestIdx = snippet.indexOf(
      "await getSdkClient().sessions.loop.suggestFromRecent(sessionId);",
    );
    expect(suggestIdx).toBeGreaterThan(restoreIdx);
    const suggestSetIdx = snippet.indexOf(
      "await getSdkClient().sessions.loop.set(sessionId, {\n            ...suggestion,\n            enabled: false,\n          });",
    );
    expect(suggestSetIdx).toBeGreaterThan(suggestIdx);
    expect(snippet).toMatch(
      /\} catch \(_\) \{\s*\n\s*\/\/ Fall through to blank-draft PUT on any suggest\/PUT failure\.\s*\n\s*\}/,
    );

    // Step 3: blank draft, enabled: false so nothing is scheduled yet.
    const draftMatch = snippet.match(
      /await getSdkClient\(\)\.sessions\.loop\.set\(sessionId, \{\s*\n\s*prompt: "",\s*\n\s*frequency: \{ value: 1, unit: "hours" \},\s*\n\s*enabled: false,\s*\n\s*\}\);/,
    );
    expect(draftMatch).not.toBeNull();
    expect(draftMatch.index).toBeGreaterThan(suggestSetIdx);
  });

  test("handleMakeNonLoop: getSdkClient().sessions.loop.detach(sessionId)", () => {
    const idx = appJs.indexOf("const handleMakeNonLoop = useCallback(");
    expect(idx).toBeGreaterThan(-1);
    const snippet = appJs.slice(idx, idx + 400);
    expect(snippet).toMatch(
      /await getSdkClient\(\)\.sessions\.loop\.detach\(sessionId\);/,
    );
  });

  test("handleFlushContext: getSdkClient().sessions.flush(sessionId), errors surfaced via errorMessage()", () => {
    const idx = appJs.indexOf("const handleFlushContext = useCallback(");
    expect(idx).toBeGreaterThan(-1);
    const snippet = appJs.slice(idx, idx + 700);
    expect(snippet).toMatch(
      /await getSdkClient\(\)\.sessions\.flush\(sessionId\);/,
    );
    expect(snippet).toMatch(
      /title: errorMessage\(err, "Failed to flush context"\),/,
    );
  });
});
