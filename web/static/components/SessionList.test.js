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

describe("SessionList.js: sidebar toolbar bottom gap", () => {
  test("sidebar-toolbar wrapper uses pb-4 (halved from pb-8), not the old pb-8", () => {
    const idx = listJs.indexOf('data-testid="sidebar-toolbar"');
    expect(idx).toBeGreaterThan(-1);
    const snippet = listJs.slice(Math.max(0, idx - 200), idx + 50);
    expect(snippet).toMatch(/class="px-3 pb-4"/);
    expect(snippet).not.toMatch(/pb-8/);
  });
});

describe("SessionList.js: category-filter dropdown Loops subsection (mitto-k53.3)", () => {
  test("anyCategoryHidden gates on all six toggles: regular, archived, tasks, loopRunning, loopIdle, loopPaused", () => {
    const idx = listJs.indexOf("const anyCategoryHidden =");
    expect(idx).toBeGreaterThan(-1);
    const snippet = listJs.slice(idx, idx + 250);
    expect(snippet).toMatch(/!categoryFilter\.regular/);
    expect(snippet).toMatch(/!categoryFilter\.archived/);
    expect(snippet).toMatch(/!categoryFilter\.tasks/);
    expect(snippet).toMatch(/!categoryFilter\.loopRunning/);
    expect(snippet).toMatch(/!categoryFilter\.loopIdle/);
    expect(snippet).toMatch(/!categoryFilter\.loopPaused/);
    // The old single "loop" toggle must be gone from this predicate.
    expect(snippet).not.toMatch(/!categoryFilter\.loop[^RIP]/);
  });

  test("dropdown descriptor array replaces the single Loop checkbox with a Loops title + three indented toggles", () => {
    const idx = listJs.indexOf(
      '<li class="menu-title text-xs">Show categories</li>',
    );
    expect(idx).toBeGreaterThan(-1);
    const snippet = listJs.slice(idx, idx + 2000);

    // Top-level toggles unchanged.
    expect(snippet).toMatch(/\{ key: "regular", label: "Regular" \}/);
    expect(snippet).toMatch(/\{ key: "archived", label: "Archived" \}/);
    expect(snippet).toMatch(/\{ key: "tasks", label: "Tasks" \}/);

    // The old single "Loop" checkbox descriptor must be gone.
    expect(snippet).not.toMatch(/\{ key: "loop", label: "Loop" \}/);

    // Section title marker for the Loops subsection.
    expect(snippet).toMatch(/\{ title: "Loops" \}/);

    // Three loop toggles, each with an explicit hyphenated testId and indent flag.
    expect(snippet).toMatch(
      /key: "loopRunning",\s*\n\s*label: "Running",\s*\n\s*testId: "category-filter-loop-running",\s*\n\s*indent: true,/,
    );
    expect(snippet).toMatch(
      /key: "loopIdle",\s*\n\s*label: "Idle",\s*\n\s*testId: "category-filter-loop-idle",\s*\n\s*indent: true,/,
    );
    expect(snippet).toMatch(
      /key: "loopPaused",\s*\n\s*label: "Paused",\s*\n\s*testId: "category-filter-loop-paused",\s*\n\s*indent: true,/,
    );

    // The .map branches on opt.title to render a plain menu-title <li> for
    // section headers (not a toggle), vs a checkbox <li> for everything else.
    expect(snippet).toMatch(/opt\.title\s*\n\s*\? html`/);
    expect(snippet).toMatch(
      /<li key=\$\{opt\.title\} class="menu-title text-xs">/,
    );

    // Toggle rendering: checked/onInput bound generically by key; explicit
    // testId used when present, defaulting to category-filter-${key}.
    expect(snippet).toMatch(/checked=\$\{categoryFilter\[opt\.key\]\}/);
    expect(snippet).toMatch(
      /onInput=\$\{\(\) => handleCategoryToggle\(opt\.key\)\}/,
    );
    expect(snippet).toMatch(
      /data-testid=\$\{opt\.testId \?\?\s*\n\s*`category-filter-\$\{opt\.key\}`\}/,
    );

    // Indented rows get a pl-2 class on the label.
    expect(snippet).toMatch(/opt\.indent\s*\n\s*\? "pl-2"/);
  });
});
