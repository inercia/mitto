/**
 * Regression tests for the mitto-7gta.17 slice S4 SDK migration in
 * SessionList.js (sessions domain: authFetch/secureFetch -> getSdkClient()).
 *
 * SessionList.js cannot be imported directly under jsdom (it reads
 * `window.preact` at module load time, same limitation documented in
 * SessionItem.test.js / BeadsView.test.js / app.test.js), so these tests
 * read the raw source and assert on the exact wiring rather than executing
 * the component.
 */

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const __dirname = dirname(fileURLToPath(import.meta.url));
const listJs = readFileSync(resolve(__dirname, "SessionList.js"), "utf8");

describe("SessionList.js: SDK migration (mitto-7gta.17 slice S4)", () => {
  test("imports getSdkClient; no authFetch/secureFetch/endpoints.sessions|issues remain", () => {
    expect(listJs).toMatch(
      /import \{ getSdkClient \} from "\.\.\/utils\/sdkClient\.js";/,
    );
    expect(listJs).not.toMatch(/authFetch|secureFetch/);
    expect(listJs).not.toMatch(/endpoints\.(sessions|issues)\./);
  });

  test("fetchGitChanges: swallows any failure to null via getSdkClient().sessions.changes(sessionId)", () => {
    const idx = listJs.indexOf("async function fetchGitChanges(sessionId) {");
    expect(idx).toBeGreaterThan(-1);
    const snippet = listJs.slice(idx, idx + 200);
    expect(snippet).toMatch(
      /try \{\s*\n\s*return await getSdkClient\(\)\.sessions\.changes\(sessionId\);\s*\n\s*\} catch \{\s*\n\s*return null;\s*\n\s*\}/,
    );
  });

  test("fetchBeadsStats: uses getSdkClient().issues.stats({ working_dir }) and returns data.summary", () => {
    const idx = listJs.indexOf("async function fetchBeadsStats(workingDir) {");
    expect(idx).toBeGreaterThan(-1);
    const snippet = listJs.slice(idx, idx + 300);
    expect(snippet).toMatch(
      /const data = await getSdkClient\(\)\.issues\.stats\(\{\s*\n\s*working_dir: workingDir,\s*\n\s*\}\);/,
    );
    expect(snippet).toMatch(/if \(!data \|\| data\.error\) return null;/);
    expect(snippet).toMatch(/return data\.summary \|\| null;/);
    expect(snippet).toMatch(/\} catch \{\s*\n\s*return null;\s*\n\s*\}/);
  });

  test("UI-prompt ack: fire-and-forget getSdkClient().sessions.acknowledgeUIPrompt(...) rolls back the optimistic ack ref on failure", () => {
    const idx = listJs.indexOf(
      "acknowledgedUIPromptRef.current.set(activeSessionId, activeUIRequestId);",
    );
    expect(idx).toBeGreaterThan(-1);
    const snippet = listJs.slice(idx, idx + 320);
    expect(snippet).toMatch(
      /getSdkClient\(\)\s*\n\s*\.sessions\.acknowledgeUIPrompt\(activeSessionId, activeUIRequestId\)\s*\n\s*\.catch\(\(\) => \{\s*\n\s*acknowledgedUIPromptRef\.current\.delete\(activeSessionId\);\s*\n\s*\}\);/,
    );
  });

  test("loop-error ack: fire-and-forget getSdkClient().sessions.loop.acknowledgeStoppedReason(...) rolls back the optimistic ack ref on failure", () => {
    const idx = listJs.indexOf("acknowledgedLoopErrorRef.current.add(key);");
    expect(idx).toBeGreaterThan(-1);
    const snippet = listJs.slice(idx, idx + 250);
    expect(snippet).toMatch(
      /getSdkClient\(\)\s*\n\s*\.sessions\.loop\.acknowledgeStoppedReason\(activeSessionId\)\s*\n\s*\.catch\(\(\) => \{\s*\n\s*acknowledgedLoopErrorRef\.current\.delete\(key\);\s*\n\s*\}\);/,
    );
  });
});
