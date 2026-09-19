/**
 * mitto-sus.8 virtualization-spike prototype A: `content-visibility: auto`.
 *
 * Compares DOM node count and cumulative layout/paint cost (CDP
 * Performance.getMetrics, Chromium-only) while growing a conversation via
 * repeated "Load earlier messages" clicks, with the `.mitto-perf-cv`
 * prototype flag OFF vs ON (see web/static/utils/perfMarks.js
 * applyPerfCVFlag(), styles.css `.mitto-perf-cv .mitto-msg-row`).
 *
 * Gated behind PERF_SPIKE=1 (spike-only, not part of normal CI) per the
 * mitto-sus.8 plan's Sub-part 7. Reuses the same seed-perf-history fixture
 * technique as history-load.perf.spec.ts.
 */
import { test, expect } from "../../../fixtures/test-fixtures";
import * as path from "path";
import * as fs from "fs";
import { fileURLToPath } from "url";
import { execFileSync } from "child_process";
import {
  collectDOMStats,
  collectPaintLayoutStats,
  writePerfSample,
} from "../../../utils/perf";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const repoRoot = path.resolve(__dirname, "../../../../..");
const mittoDir = process.env.MITTO_DIR || "/tmp/mitto-test";
const markerFile = path.join(mittoDir, "perf-history-sessions.json");
const GROWTH_STEPS = 3;

let seedingAvailable = false;

test.describe("Perf spike: content-visibility prototype vs baseline", () => {
  test.describe.configure({ mode: "serial" });
  test.setTimeout(120_000);
  test.skip(
    !process.env.PERF_SPIKE,
    "virtualization-spike prototype — set PERF_SPIKE=1 to run",
  );

  test.beforeAll(() => {
    try {
      const binPath = path.join(mittoDir, "seed-perf-history-bin");
      execFileSync(
        "go",
        ["build", "-o", binPath, "./tests/ui/helpers/seed-perf-history"],
        { cwd: repoRoot, stdio: "pipe" },
      );
      execFileSync(
        binPath,
        [
          "-dir", mittoDir,
          "-snapshots-dir", path.join(repoRoot, "tests/ui/perf/fixtures/histories"),
          "-working-dir", path.join(repoRoot, "tests/fixtures/workspaces/project-alpha"),
        ],
        { stdio: "pipe" },
      );
      seedingAvailable = fs.existsSync(markerFile);
    } catch (err) {
      console.log(`[Test] Could not seed perf histories, will skip: ${err}`);
      seedingAvailable = false;
    }
  });

  /** Opens the "medium" seeded session and pages back GROWTH_STEPS times. */
  async function loadAndGrow(page: import("@playwright/test").Page, sessionId: string) {
    await page.evaluate(
      (id) => localStorage.setItem("mitto_last_session_id", id),
      sessionId,
    );
    await page.reload();
    await page.locator("textarea").waitFor({ state: "visible", timeout: 10_000 });
    // Don't assert an exact initial count: depending on this browser
    // context's localStorage watermark state, the cold-start restore may
    // take either the "last N events" path or the "everything after
    // watermark" path (see useWSConnection.js's stream.on("open", ...)).
    // Either way, the DOM-parity assertion below holds regardless of how
    // much history loaded initially.
    await expect(page.locator(".bg-mitto-user, .bg-blue-600")).not.toHaveCount(0, {
      timeout: 10_000,
    });

    for (let i = 0; i < GROWTH_STEPS; i++) {
      const loadMoreButton = page.locator('[data-testid="load-more-button"]');
      if (!(await loadMoreButton.isVisible().catch(() => false))) break;
      const beforeCount = await page.locator(".bg-mitto-user, .bg-blue-600").count();
      await loadMoreButton.click();
      await expect(page.locator(".bg-mitto-user, .bg-blue-600")).not.toHaveCount(
        beforeCount,
        { timeout: 10_000 },
      );
    }
  }

  test("DOM size is unchanged, paint/layout cost is record-only compared, cv OFF vs ON", async ({
    page,
    helpers,
  }) => {
    if (!seedingAvailable) {
      console.log("[Test] Skipping: seed-perf-history build/run failed (see beforeAll log above)");
      test.skip();
      return;
    }
    const sessionIds: Record<string, string> = JSON.parse(
      fs.readFileSync(markerFile, "utf-8"),
    );
    const sessionId = sessionIds["medium"];

    // --- Prototype OFF (baseline) ---
    await helpers.navigateAndWait(page);
    const beforeOff = await collectPaintLayoutStats(page);
    await loadAndGrow(page, sessionId);
    const domOff = await collectDOMStats(page);
    const afterOff = await collectPaintLayoutStats(page);

    // --- Prototype ON (?perf-cv=1 / window.__mittoPerfCV) ---
    // Clear the per-session seq watermark left by the OFF phase above:
    // otherwise the cold-start restore (useWSConnection.js) takes the
    // "everything after watermark" path instead of the capped
    // INITIAL_EVENTS_LIMIT path, loading a much larger — and no longer
    // comparable — initial window than the OFF phase measured.
    await page.evaluate(
      (id) => localStorage.removeItem(`mitto_last_seen_seq_${id}`),
      sessionId,
    );
    await page.addInitScript(() => {
      (window as unknown as { __mittoPerfCV: boolean }).__mittoPerfCV = true;
    });
    await helpers.navigateAndWait(page);
    const beforeOn = await collectPaintLayoutStats(page);
    await loadAndGrow(page, sessionId);
    const domOn = await collectDOMStats(page);
    const afterOn = await collectPaintLayoutStats(page);

    console.log(
      `[perf] content-visibility: domNodes off=${domOff.domNodes} on=${domOn.domNodes}`,
    );
    writePerfSample("virtualization-spike.content-visibility", "domNodesOff", domOff.domNodes);
    writePerfSample("virtualization-spike.content-visibility", "domNodesOn", domOn.domNodes);

    // Core spike finding: content-visibility does NOT remove DOM nodes — it
    // only skips rendering work for offscreen ones. Any DOM-size reduction
    // must come from real windowing (prototype B), not this prototype.
    expect(domOn.domNodes).toBe(domOff.domNodes);

    if (beforeOff && afterOff && beforeOn && afterOn) {
      const layoutDeltaOff = afterOff.layoutMs - beforeOff.layoutMs;
      const layoutDeltaOn = afterOn.layoutMs - beforeOn.layoutMs;
      console.log(
        `[perf] content-visibility: layoutDeltaMs off=${layoutDeltaOff.toFixed(2)} on=${layoutDeltaOn.toFixed(2)}`,
      );
      writePerfSample("virtualization-spike.content-visibility", "layoutDeltaMsOff", layoutDeltaOff);
      writePerfSample("virtualization-spike.content-visibility", "layoutDeltaMsOn", layoutDeltaOn);
    }
  });
});
