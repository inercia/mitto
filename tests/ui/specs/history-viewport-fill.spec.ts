import { test, expect } from "../fixtures/test-fixtures";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { execFileSync } from "child_process";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const repoRoot = path.resolve(__dirname, "../../..");
const mittoDir = process.env.MITTO_DIR || "/tmp/mitto-test";
const markerFile = path.join(mittoDir, "perf-history-sessions.json");

test.describe("History viewport fill", () => {
  test.describe.configure({ mode: "serial" });
  test.setTimeout(120_000);

  let seededSessionId = "";

  test.beforeAll(() => {
    const snapshotsDir = fs.mkdtempSync(path.join(os.tmpdir(), "mitto-sparse-history-"));
    const events: Array<{ type: string; data: Record<string, string> }> = [];
    for (let i = 0; i < 25; i++) {
      events.push({ type: "user_prompt", data: { message: `Oldest question ${i}` } });
      events.push({ type: "agent_message", data: { text: `Oldest answer ${i}` } });
    }
    for (let i = 0; i < 25; i++) {
      events.push({ type: "user_prompt", data: { message: `Old question ${i}` } });
      events.push({ type: "agent_message", data: { text: `Old answer ${i}` } });
    }
    for (let i = 0; i < 25; i++) {
      events.push({ type: "user_prompt", data: { message: `Earlier question ${i}` } });
      events.push({ type: "agent_message", data: { text: `Earlier answer ${i}` } });
    }
    for (let i = 0; i < 49; i++) {
      events.push({ type: "agent_thought", data: { text: `Private reasoning ${i}` } });
    }
    events.push({ type: "agent_message", data: { text: "Latest visible answer" } });
    fs.writeFileSync(
      path.join(snapshotsDir, "sparse.json"),
      JSON.stringify({ name: "sparse", events }),
    );

    const binPath = path.join(mittoDir, "seed-sparse-history-bin");
    execFileSync("go", ["build", "-o", binPath, "./tests/ui/helpers/seed-perf-history"], {
      cwd: repoRoot,
      stdio: "pipe",
    });
    execFileSync(
      binPath,
      ["-dir", mittoDir, "-snapshots-dir", snapshotsDir, "-working-dir", path.join(repoRoot, "tests/fixtures/workspaces/project-alpha")],
      { stdio: "pipe" },
    );
    const sessions: Record<string, string> = JSON.parse(fs.readFileSync(markerFile, "utf-8"));
    seededSessionId = sessions.sparse;
    expect(seededSessionId, "sparse fixture should be seeded").toBeTruthy();
  });

  test("fills an initially sparse panel to two rendered panel heights", async ({ page, helpers, selectors }) => {
    await helpers.navigateAndWait(page);
    await page.evaluate((id) => localStorage.setItem("mitto_last_session_id", id), seededSessionId);
    await page.reload();
    await helpers.waitForAppReady(page);
    await helpers.waitForMessagesLoaded(page);

    const rows = page.locator(selectors.allMessages);
    await expect(rows.filter({ hasText: "Latest visible answer" })).toBeVisible();

    const measureCoverage = () => page.locator(selectors.messagesContainer).evaluate((container) => {
      const panel = container as HTMLElement;
      const rowExtent = Array.from(panel.querySelectorAll<HTMLElement>(".mitto-msg-row"))
        .reduce((total, row) => total + row.offsetHeight, 0);
      return { rowExtent, target: panel.clientHeight * 2 };
    });
    await expect.poll(async () => {
      const current = await measureCoverage();
      return current.rowExtent >= current.target;
    }, { timeout: 15_000 }).toBe(true);
    const coverage = await measureCoverage();

    expect(
      coverage.rowExtent,
      `initial rendered history covers ${coverage.rowExtent}px; expected at least two panel heights (${coverage.target}px)`,
    ).toBeGreaterThanOrEqual(coverage.target);

    const priorHeight = coverage.rowExtent;
    await page.locator('[data-testid="load-more-button"]').click();
    await expect(rows.filter({ hasText: "Old answer 0" })).toHaveCount(1);
    const expandedHeight = await page.locator(selectors.messagesContainer).evaluate((container) =>
      Array.from(container.querySelectorAll<HTMLElement>(".mitto-msg-row"))
        .reduce((total, row) => total + row.offsetHeight, 0),
    );
    expect(
      expandedHeight - priorHeight,
      `manual history load added ${expandedHeight - priorHeight}px; expected at least two panel heights (${coverage.target}px)`,
    ).toBeGreaterThanOrEqual(coverage.target);
  });
});
