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

  test("properties-tab parallel fetch: loop/flags/settings each swallow their own failure", () => {
    const idx = panelJs.indexOf(
      "const [loopData, flagsData, settingsData] = await Promise.all([",
    );
    expect(idx).toBeGreaterThan(-1);
    const snippet = panelJs.slice(idx, idx + 700);
    expect(snippet).toMatch(
      /loopConfigured\s*\n\s*\? getSdkClient\(\)\s*\n\s*\.sessions\.loop\.get\(sessionId\)\s*\n\s*\.catch\(\(\) => null\)/,
    );
    expect(snippet).toMatch(
      /getSdkClient\(\)\s*\n\s*\.misc\.advancedFlags\(\)\s*\n\s*\.catch\(\(\) => null\)/,
    );
    expect(snippet).toMatch(
      /getSdkClient\(\)\s*\n\s*\.sessions\.getSettings\(sessionId\)\s*\n\s*\.catch\(\(\) => null\)/,
    );
    // A single Promise.all across all three, not sequential awaits (otherwise
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

  test("Loop tab mounts the full editor and callback section; Advanced owns no callback CRUD", () => {
    expect(panelJs).toMatch(/aria-label="Loop"/);
    expect(panelJs).toMatch(/<\$\{LoopSettingsTab\}/);
    expect(panelJs).toMatch(/<\$\{CallbackTriggerSection\}/);
    expect(panelJs).not.toMatch(/sessions\.(createCallback|revokeCallback)/);
    expect(panelJs).not.toMatch(/Callback URL Section/);
  });

  test("falls back to Properties if Loop is selected after loop removal or conversation switching", () => {
    expect(panelJs).toMatch(
      /currentTab === "loop" && !loopAvailable[\s\S]*?handleTabChange\("properties"\)/,
    );
  });

  test("handleFlagChange / handleSaveAttribute surface the real SDK error message via errorMessage()", () => {
    expect(panelJs).toMatch(
      /setFlagsError\(errorMessage\(err, "Failed to save setting"\)\);/,
    );
    expect(panelJs).toMatch(
      /setUserDataError\(errorMessage\(err, "Failed to save attribute"\)\);/,
    );
  });

  test("dock widens to ~24rem via Drawer rootStyle (mitto-w7hh.2 narrow Loop-tab fix), keeping the phone-safe w-full panel class", () => {
    const idx = panelJs.indexOf("<${Drawer}");
    expect(idx).toBeGreaterThan(-1);
    const snippet = panelJs.slice(idx, idx + 500);
    expect(snippet).toMatch(/dock/);
    expect(snippet).toMatch(/widthClass="w-full"/);
    expect(snippet).toMatch(/rootStyle="--dock-w:24rem"/);
  });
});
