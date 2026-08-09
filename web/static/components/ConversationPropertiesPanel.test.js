/**
 * Regression tests for the mitto-7gta.17 slice S4 SDK migration in
 * ConversationPropertiesPanel.js (sessions domain: authFetch/secureFetch ->
 * getSdkClient()).
 *
 * ConversationPropertiesPanel.js cannot be imported directly under jsdom (it
 * reads `window.preact` at module load time, same limitation documented in
 * SessionItem.test.js / BeadsView.test.js / app.test.js), so these tests
 * read the raw source and assert on the exact wiring rather than executing
 * the component.
 */

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const __dirname = dirname(fileURLToPath(import.meta.url));
const panelJs = readFileSync(
  resolve(__dirname, "ConversationPropertiesPanel.js"),
  "utf8",
);

describe("ConversationPropertiesPanel.js: SDK migration (mitto-7gta.17 slice S4)", () => {
  test("imports getSdkClient/errorMessage; no authFetch/secureFetch remain", () => {
    expect(panelJs).toMatch(
      /import \{ getSdkClient \} from "\.\.\/utils\/sdkClient\.js";/,
    );
    expect(panelJs).toMatch(
      /import \{ errorMessage \} from "\.\.\/utils\/sdkErrors\.js";/,
    );
    expect(panelJs).not.toMatch(/authFetch|secureFetch/);
  });

  test("properties fetch: loop/callback/flags/settings each tolerate their own failure with .catch(() => null)", () => {
    const idx = panelJs.indexOf(
      "const [loopData, callbackData, flagsData, settingsData] =",
    );
    expect(idx).toBeGreaterThan(-1);
    const snippet = panelJs.slice(idx, idx + 700);
    expect(snippet).toMatch(
      /loopConfigured\s*\n\s*\? getSdkClient\(\)\s*\n\s*\.sessions\.loop\.get\(sessionId\)\s*\n\s*\.catch\(\(\) => null\)/,
    );
    expect(snippet).toMatch(
      /loopConfigured\s*\n\s*\? getSdkClient\(\)\s*\n\s*\.sessions\.getCallback\(sessionId\)\s*\n\s*\.catch\(\(\) => null\)/,
    );
    expect(snippet).toMatch(
      /getSdkClient\(\)\s*\n\s*\.misc\.advancedFlags\(\)\s*\n\s*\.catch\(\(\) => null\)/,
    );
    expect(snippet).toMatch(
      /getSdkClient\(\)\s*\n\s*\.sessions\.getSettings\(sessionId\)\s*\n\s*\.catch\(\(\) => null\)/,
    );
  });

  test("handleFreshContextChange calls getSdkClient().sessions.loop.update(sessionId, { fresh_context }) and applies the server-echoed value optimistically", () => {
    const idx = panelJs.indexOf(
      "const handleFreshContextChange = useCallback(",
    );
    expect(idx).toBeGreaterThan(-1);
    const snippet = panelJs.slice(idx, idx + 400);
    expect(snippet).toMatch(
      /const data = await getSdkClient\(\)\.sessions\.loop\.update\(sessionId, \{\s*\n\s*fresh_context: newValue,\s*\n\s*\}\);/,
    );
    expect(snippet).toMatch(/fresh_context: data\.fresh_context \?\? newValue/);
  });

  test("callback CRUD handlers (enable/rotate/revoke) swallow SDK throws as a no-op, matching the old !res.ok no-op", () => {
    expect(panelJs).toMatch(
      /const data = await getSdkClient\(\)\.sessions\.createCallback\(sessionId\);/,
    );
    expect(panelJs).toMatch(
      /await getSdkClient\(\)\.sessions\.revokeCallback\(sessionId\);/,
    );
    const noopComments =
      panelJs.match(/\/\* mirrors the prior !res\.ok no-op \*\//g) || [];
    expect(noopComments.length).toBeGreaterThanOrEqual(2);
  });

  test("handleFlagChange surfaces the real SDK error message via errorMessage()", () => {
    expect(panelJs).toMatch(
      /const data = await getSdkClient\(\)\.sessions\.updateSettings\(sessionId, \{/,
    );
    expect(panelJs).toMatch(
      /setFlagsError\(errorMessage\(err, "Failed to save setting"\)\);/,
    );
  });
});
