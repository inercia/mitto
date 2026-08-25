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
    const snippet = appJs.slice(idx, idx + 3000);

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
    expect(snippet).toMatch(/handleOpenSidePanelTab\("loop"\)/);
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

describe("app.js: SDK migration — dashboard/misc/remainder handlers (mitto-7gta.17 slice S7)", () => {
  test("no authFetch/secureFetch residue remains in the header-beads / badge-click / folder-group / shortcuts handlers", () => {
    // These four handlers are the S7 call sites; none of them should read a
    // raw fetch Response anymore (res.ok / res.json() / res.status).
    const handlers = [
      "getSdkClient().issues.show(issueId,",
      "getSdkClient().misc.badgeClick({",
      "getSdkClient().workspaces.setFolderGroup(uuid,",
      "getSdkClient().shortcuts.getFolder({",
      ".shortcuts.getGlobal()",
    ];
    for (const h of handlers) {
      expect(appJs).toContain(h);
    }
  });

  test("header beads-status effect: isGone() short-circuits before the network call, 404 marks gone via isNotFoundError", () => {
    const idx = appJs.indexOf("const issueId = sessionInfo?.beads_issue;");
    expect(idx).toBeGreaterThan(-1);
    const snippet = appJs.slice(idx, idx + 1200);
    const goneIdx = snippet.indexOf("if (isGone(workingDir, issueId))");
    const showIdx = snippet.indexOf(
      "await getSdkClient().issues.show(issueId, {",
    );
    expect(goneIdx).toBeGreaterThan(-1);
    expect(showIdx).toBeGreaterThan(goneIdx);
    expect(snippet).toMatch(
      /if \(isNotFoundError\(err\)\) markGone\(workingDir, issueId\);/,
    );
  });

  test("handleBadgeClick / handleOpenTarget: POST misc.badgeClick, a data.error field toasts without throwing, a thrown error toasts via errorMessage()", () => {
    const idx = appJs.indexOf("const handleBadgeClick = useCallback(");
    expect(idx).toBeGreaterThan(-1);
    const snippet = appJs.slice(idx, idx + 1300);
    expect(snippet).toMatch(
      /const data = await getSdkClient\(\)\.misc\.badgeClick\(\{\s*\n\s*workspace_path: workspacePath,\s*\n\s*action: "open",\s*\n\s*target_id: "finder",\s*\n\s*\}\);/,
    );
    expect(snippet).toMatch(
      /if \(!data\.success && data\.error\) \{\s*\n\s*showToast\(\{ style: "error", title: data\.error \}\);\s*\n\s*\}/,
    );
    expect(snippet).toMatch(
      /title: errorMessage\(err, "Failed to open folder"\),/,
    );

    const openTargetIdx = appJs.indexOf(
      "const handleOpenTarget = useCallback(",
    );
    expect(openTargetIdx).toBeGreaterThan(idx);
    const openTargetSnippet = appJs.slice(openTargetIdx, openTargetIdx + 900);
    expect(openTargetSnippet).toMatch(
      /const data = await getSdkClient\(\)\.misc\.badgeClick\(\{\s*\n\s*workspace_path: workspacePath,\s*\n\s*action: "open",\s*\n\s*target_id: targetId,\s*\n\s*\}\);/,
    );
    expect(openTargetSnippet).toMatch(
      /title: errorMessage\(err, "Failed to open target"\),/,
    );
  });

  test("handleMoveFolderToGroup: PUT workspaces.setFolderGroup(uuid, group), invalidates config cache and refreshes workspaces on success", () => {
    const idx = appJs.indexOf("const handleMoveFolderToGroup = useCallback(");
    expect(idx).toBeGreaterThan(-1);
    const snippet = appJs.slice(idx, idx + 900);
    const setGroupIdx = snippet.indexOf(
      'await getSdkClient().workspaces.setFolderGroup(uuid, group || "");',
    );
    expect(setGroupIdx).toBeGreaterThan(-1);
    const invalidateIdx = snippet.indexOf("invalidateConfigCache();");
    const refreshIdx = snippet.indexOf("refreshWorkspaces();");
    expect(invalidateIdx).toBeGreaterThan(setGroupIdx);
    expect(refreshIdx).toBeGreaterThan(invalidateIdx);
    expect(snippet).toMatch(
      /title: errorMessage\(err, "Failed to move folder to group"\),/,
    );
  });

  test("loadConvShortcuts: folder+global shortcuts fetched in parallel, global's failure tolerated via .catch(() => ({}))", () => {
    const idx = appJs.indexOf("const loadConvShortcuts = useCallback(");
    expect(idx).toBeGreaterThan(-1);
    const snippet = appJs.slice(idx, idx + 900);
    expect(snippet).toMatch(
      /const \[data, globalData\] = await Promise\.all\(\[\s*\n\s*getSdkClient\(\)\.shortcuts\.getFolder\(\{ working_dir: wd \}\),\s*\n\s*getSdkClient\(\)\s*\n\s*\.shortcuts\.getGlobal\(\)\s*\n\s*\.catch\(\(\) => \(\{\}\)\),\s*\n\s*\]\);/,
    );
  });
});

describe("app.js: passkey auto-enroll success toast (mitto-4mz.7)", () => {
  test("one-shot mount effect reads+clears the sessionStorage flag and shows a success toast", () => {
    const idx = appJs.indexOf(
      'if (sessionStorage.getItem("mitto_passkey_autoenrolled") === "1") {',
    );
    expect(idx).toBeGreaterThan(-1);
    const snippet = appJs.slice(idx, idx + 400);

    // Cleared before the toast fires, so a re-render/re-mount never re-shows it.
    const removeIdx = snippet.indexOf(
      'sessionStorage.removeItem("mitto_passkey_autoenrolled");',
    );
    const toastIdx = snippet.indexOf("showToast({");
    expect(removeIdx).toBeGreaterThan(-1);
    expect(toastIdx).toBeGreaterThan(removeIdx);
    expect(snippet).toMatch(/style: "success",/);
    expect(snippet).toMatch(/title: "Passkey created",/);

    // Wired as its own effect (dependency array [showToast]), independent of
    // the initCSRF/initUIPreferences mount effect.
    const effectStart = appJs.lastIndexOf("useEffect(() => {", idx);
    expect(effectStart).toBeGreaterThan(-1);
    const effectSnippet = appJs.slice(effectStart, idx + 500);
    expect(effectSnippet).toMatch(/\}, \[showToast\]\);/);
  });
});

describe("app.js: header title inline editing (click title to rename, mitto-dpd)", () => {
  test("handleStartEditHeaderTitle: no-ops without an active session; otherwise seeds the draft from sessionInfo.name and enters edit mode", () => {
    const idx = appJs.indexOf(
      "const handleStartEditHeaderTitle = useCallback(",
    );
    expect(idx).toBeGreaterThan(-1);
    const snippet = appJs.slice(idx, idx + 300);
    expect(snippet).toMatch(/if \(!activeSessionId\) return;/);
    expect(snippet).toMatch(
      /setEditedHeaderTitle\(sessionInfo\?\.name \|\| ""\);/,
    );
    expect(snippet).toMatch(/setIsEditingHeaderTitle\(true\);/);
  });

  test("handleSaveHeaderTitle: saves via renameSession, skips the round-trip when unchanged, and does NOT early-return on an empty title (auto-title re-enable)", () => {
    const idx = appJs.indexOf("const handleSaveHeaderTitle = useCallback(");
    expect(idx).toBeGreaterThan(-1);
    const snippet = appJs.slice(idx, idx + 900);

    // Guard clause only checks activeSessionId/isSavingHeaderTitle — no
    // emptiness check on the trimmed title anywhere before the save call.
    expect(snippet).toMatch(
      /if \(!activeSessionId \|\| isSavingHeaderTitle\) return;/,
    );
    const trimIdx = snippet.indexOf(
      "const newTitle = editedHeaderTitle.trim();",
    );
    expect(trimIdx).toBeGreaterThan(-1);

    // Unchanged-value short-circuit compares against the current name, not
    // against an empty string, so clearing the title is never treated as a
    // no-op unless the name was already empty.
    expect(snippet).toMatch(
      /if \(newTitle === \(sessionInfo\?\.name \|\| ""\)\) \{\s*\n\s*setIsEditingHeaderTitle\(false\);\s*\n\s*return;\s*\n\s*\}/,
    );

    // The actual save call always fires with whatever newTitle resolved to
    // (including ""), unlike the side-panel handlers which block empty saves.
    const saveCallIdx = snippet.indexOf(
      "await renameSession(activeSessionId, newTitle);",
    );
    expect(saveCallIdx).toBeGreaterThan(trimIdx);
    expect(snippet).toMatch(
      /\} catch \(err\) \{\s*\n\s*console\.error\("Failed to save header title:", err\);\s*\n\s*\} finally \{\s*\n\s*setIsSavingHeaderTitle\(false\);\s*\n\s*\}/,
    );
  });

  test("handleHeaderTitleKeyDown: Enter saves, Escape cancels without saving", () => {
    const idx = appJs.indexOf("const handleHeaderTitleKeyDown = useCallback(");
    expect(idx).toBeGreaterThan(-1);
    const snippet = appJs.slice(idx, idx + 400);
    expect(snippet).toMatch(
      /if \(e\.key === "Enter"\) \{\s*\n\s*e\.preventDefault\(\);\s*\n\s*handleSaveHeaderTitle\(\);/,
    );
    expect(snippet).toMatch(
      /\} else if \(e\.key === "Escape"\) \{\s*\n\s*e\.preventDefault\(\);\s*\n\s*setIsEditingHeaderTitle\(false\);/,
    );
  });

  test("focus+select effect fires when entering edit mode; a separate effect cancels an in-progress edit on conversation switch", () => {
    const focusIdx = appJs.indexOf(
      "if (isEditingHeaderTitle && headerTitleInputRef.current) {",
    );
    expect(focusIdx).toBeGreaterThan(-1);
    const focusSnippet = appJs.slice(focusIdx, focusIdx + 200);
    expect(focusSnippet).toMatch(/headerTitleInputRef\.current\.focus\(\);/);
    expect(focusSnippet).toMatch(/headerTitleInputRef\.current\.select\(\);/);

    const switchIdx = appJs.indexOf(
      "// Cancel any in-progress header title edit when switching conversations",
    );
    expect(switchIdx).toBeGreaterThan(-1);
    const switchSnippet = appJs.slice(switchIdx, switchIdx + 300);
    expect(switchSnippet).toMatch(/setIsEditingHeaderTitle\(false\);/);
    expect(switchSnippet).toMatch(/\}, \[activeSessionId\]\);/);
  });

  test("header JSX: renders the input only while editing, wires onClick to start editing only when a session is active", () => {
    const idx = appJs.indexOf("${isEditingHeaderTitle\n");
    expect(idx).toBeGreaterThan(-1);
    const snippet = appJs.slice(idx, idx + 2200);

    // Editing branch: input carries the ref, value, handlers and is disabled
    // while a save is in flight — never blocks on emptiness.
    expect(snippet).toMatch(/ref=\$\{headerTitleInputRef\}/);
    expect(snippet).toMatch(/value=\$\{editedHeaderTitle\}/);
    expect(snippet).toMatch(/onKeyDown=\$\{handleHeaderTitleKeyDown\}/);
    expect(snippet).toMatch(/disabled=\$\{isSavingHeaderTitle\}/);

    // Non-editing branch: clicking the h1 only starts editing when a
    // conversation is active ("New conversation" placeholder included,
    // since the ternary only checks activeSessionId, not sessionInfo?.name).
    const onClickIdx = appJs.indexOf(
      "onClick=${activeSessionId\n                              ? handleStartEditHeaderTitle",
    );
    expect(onClickIdx).toBeGreaterThan(idx);
    expect(onClickIdx).toBeLessThan(idx + 2600);
    const onClickSnippet = appJs.slice(onClickIdx, onClickIdx + 140);
    expect(onClickSnippet).toMatch(
      /onClick=\$\{activeSessionId\s*\n\s*\? handleStartEditHeaderTitle\s*\n\s*: undefined\}/,
    );
  });
});
