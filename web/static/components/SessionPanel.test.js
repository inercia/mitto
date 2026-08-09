/**
 * Regression tests for the mitto-7gta.17 slice S4 SDK migration in
 * SessionPanel.js (sessions domain: authFetch/secureFetch -> getSdkClient()).
 *
 * SessionPanel.js cannot be imported directly under jsdom (it reads
 * `window.preact` at module load time, same limitation documented in
 * SessionItem.test.js / BeadsView.test.js / app.test.js), so these tests
 * read the raw source and assert on the exact wiring rather than executing
 * the component.
 */

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const __dirname = dirname(fileURLToPath(import.meta.url));
const panelJs = readFileSync(resolve(__dirname, "SessionPanel.js"), "utf8");

describe("SessionPanel.js: SDK migration (mitto-7gta.17 slice S4)", () => {
  test("imports getSdkClient/sdkErrors/withIssueCaches; no authFetch/secureFetch remain", () => {
    expect(panelJs).toMatch(
      /import \{ getSdkClient \} from "\.\.\/utils\/sdkClient\.js";/,
    );
    expect(panelJs).toMatch(
      /import \{ errorMessage, isNotFoundError \} from "\.\.\/utils\/sdkErrors\.js";/,
    );
    expect(panelJs).toMatch(
      /import \{ withIssueCaches \} from "\.\.\/sdk\/index\.js";/,
    );
    expect(panelJs).not.toMatch(/authFetch|secureFetch/);
  });

  test("properties-tab parallel fetch: each of loop/callback/flags/settings swallows its own failure with .catch(() => null)", () => {
    const idx = panelJs.indexOf(
      "const [loopData, callbackData, flagsData, settingsData] =",
    );
    expect(idx).toBeGreaterThan(-1);
    const snippet = panelJs.slice(idx, idx + 700);
    // loop and callback are gated on loopConfigured and each independently
    // tolerant; flags/settings are unconditional but still tolerant.
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
    // A single Promise.all across all four, not sequential awaits (otherwise
    // one slow/failing endpoint would serialize the others).
    expect(snippet.indexOf("await Promise.all([")).toBeGreaterThan(-1);
  });

  test("beads-issue status effect: withIssueCaches(getSdkClient().issues, { markGone }) wraps .show(), isGone() guard precedes the network call", () => {
    const idx = panelJs.indexOf(
      "// --- Effects: fetch linked beads issue status when open ---",
    );
    expect(idx).toBeGreaterThan(-1);
    const snippet = panelJs.slice(idx, idx + 1300);
    const isGoneIdx = snippet.indexOf(
      "if (isGone(sessionInfo.working_dir, sessionInfo.beads_issue))",
    );
    const wrapIdx = snippet.indexOf(
      "const issues = withIssueCaches(getSdkClient().issues, { markGone });",
    );
    const showIdx = snippet.indexOf(
      "await issues.show(sessionInfo.beads_issue,",
    );
    expect(isGoneIdx).toBeGreaterThan(-1);
    expect(wrapIdx).toBeGreaterThan(isGoneIdx);
    expect(showIdx).toBeGreaterThan(wrapIdx);
  });

  test("user-data effect: schema fetch treats isNotFoundError as { fields: [] }, not a hard failure", () => {
    const idx = panelJs.indexOf(
      "const [userData, schema] = await Promise.all([",
    );
    expect(idx).toBeGreaterThan(-1);
    const snippet = panelJs.slice(idx, idx + 400);
    expect(snippet).toMatch(
      /getSdkClient\(\)\s*\n\s*\.sessions\.getUserData\(sessionId\)\s*\n\s*\.catch\(\(\) => null\)/,
    );
    expect(snippet).toMatch(
      /getSdkClient\(\)\s*\n\s*\.workspaces\.getUserDataSchema\(wsUuid\)\s*\n\s*\.catch\(\(err\) => \(isNotFoundError\(err\) \? \{ fields: \[\] \} : null\)\)/,
    );
  });

  test("changes tab: both the effect and the manual refresh button call getSdkClient().sessions.changes(sessionId)", () => {
    const matches = [
      ...panelJs.matchAll(
        /const data = await getSdkClient\(\)\.sessions\.changes\(sessionId\);/g,
      ),
    ];
    expect(matches.length).toBe(2);
  });

  test("callback CRUD handlers (enable/rotate/revoke) swallow SDK throws as a no-op, matching the old !res.ok no-op", () => {
    for (const method of [
      "createCallback",
      "createCallback",
      "revokeCallback",
    ]) {
      expect(panelJs).toMatch(
        new RegExp(`getSdkClient\\(\\)\\.sessions\\.${method}\\(sessionId\\)`),
      );
    }
    // Each callback mutation site is followed by a catch that is a documented no-op.
    const noopComments =
      panelJs.match(/\/\* mirrors the prior !res\.ok no-op \*\//g) || [];
    expect(noopComments.length).toBeGreaterThanOrEqual(3);
  });

  test("handleFlagChange / handleSaveAttribute surface the real SDK error message via errorMessage()", () => {
    expect(panelJs).toMatch(
      /setFlagsError\(errorMessage\(err, "Failed to save setting"\)\);/,
    );
    expect(panelJs).toMatch(
      /setUserDataError\(errorMessage\(err, "Failed to save attribute"\)\);/,
    );
  });
});
