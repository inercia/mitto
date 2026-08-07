import { testWithCleanup, expect } from "../fixtures/test-fixtures";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Dashboard Stats Charts (mitto-a86b.10, mitto-5rm6.4) UI tests.
 *
 * Verifies the five uPlot charts on the global Dashboard render (tokens, tool
 * calls, prompts vs turns, beads opened/closed, beads cycle time), react to
 * the range toolbar (24h/7d/30d), show the empty-state placeholder, and show
 * the backfill-in-progress badge.
 *
 * Strategy:
 *   - Mock GET /api/dashboard and /api/dashboard/timeseries via page.route so
 *     the test is deterministic without seeding SQLite. Store-to-handler
 *     coverage lives in internal/web/handlers/dashboard_timeseries_test.go and
 *     internal/stats/sqlite_store_test.go.
 *   - Stub window.uPlot via page.addInitScript before navigateAndWait so tests
 *     do not depend on cdn.jsdelivr.net (blocked by Firefox/WebKit tracking
 *     prevention per .augment/rules/32-testing-playwright.md). The stub is a
 *     minimal constructor that records opts + data and exposes setData/setSize/
 *     destroy hooks — enough for the component's create/update path.
 */

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);
const AGENT_NAME = "mock-acp";

const MOCK_DASHBOARD = {
  stats: {
    issues_in_progress: 0,
    conversations_prompting: 0,
    loops_active: 0,
    loops_stopped: 0,
  },
  lists: {
    in_progress: [],
    ready: [],
    recently_modified: [],
  },
};

const METRIC_KEYS = [
  "input_tokens_est",
  "output_tokens_est",
  "prompts",
  "agent_turns_completed",
  "tool_calls_total",
  "mcp_calls",
  "beads_opened",
  "beads_closed",
  "beads_cycle_seconds_sum",
  "beads_cycle_closed_count",
];

// Build a tsResponse mirroring internal/web/handlers/dashboard_timeseries.go.
// bucketSeconds defaults to hourly; nonZero=true stamps a single non-zero value
// in every series so isEmptySeries() flips false.
function buildTimeseries(opts?: {
  rangeSeconds?: number;
  bucketSeconds?: number;
  nonZero?: boolean;
  backfill?: boolean;
}) {
  const rangeSeconds = opts?.rangeSeconds ?? 24 * 3600;
  const bucketSeconds = opts?.bucketSeconds ?? 3600;
  const backfill = opts?.backfill ?? false;
  const nonZero = opts?.nonZero ?? false;
  const now = Math.floor(Date.now() / 1000);
  const to = now - (now % bucketSeconds);
  const from = to - rangeSeconds;
  const series: Record<string, Array<{ t: number; v: number }>> = {};
  for (const m of METRIC_KEYS) {
    const arr: Array<{ t: number; v: number }> = [];
    for (let t = from; t < to; t += bucketSeconds) {
      arr.push({ t, v: 0 });
    }
    if (nonZero && arr.length > 0) arr[Math.floor(arr.length / 2)].v = 42;
    series[m] = arr;
  }
  return {
    range: { from, to, bucket_seconds: bucketSeconds },
    series,
    meta: {
      estimator_version: "1",
      backfill_in_progress: backfill,
      note: "Token counts are length-based estimates; not billed usage.",
    },
  };
}

// Install a minimal window.uPlot stub before navigation. Firefox/WebKit block
// cdn.jsdelivr.net (see rule 32); Chromium works with the real CDN but the
// stub keeps behaviour uniform across projects. The stub creates a <canvas>
// child inside the container so the DOM shape roughly matches uPlot's.
async function stubUplot(page) {
  await page.addInitScript(() => {
    class UplotStub {
      opts: any;
      data: any;
      _canvas: HTMLCanvasElement;
      static paths = { bars: () => () => undefined };
      constructor(opts: any, data: any, target: HTMLElement) {
        const c = document.createElement("canvas");
        c.setAttribute("data-mitto-uplot-stub", "1");
        c.width = (opts && opts.width) || 300;
        c.height = (opts && opts.height) || 220;
        if (target && target.appendChild) target.appendChild(c);
        this.opts = opts;
        this.data = data;
        this._canvas = c;
      }
      setData(d: any) {
        this.data = d;
      }
      setSize(sz: any) {
        if (sz && sz.width) this._canvas.width = sz.width;
        if (sz && sz.height) this._canvas.height = sz.height;
      }
      destroy() {
        if (this._canvas.parentNode)
          this._canvas.parentNode.removeChild(this._canvas);
      }
    }
    (window as any).uPlot = UplotStub;
  });
}

// Selector for the sidebar Dashboard entry (SessionList.js).
const DASHBOARD_BUTTON = 'button[title="Dashboard"]';

async function openDashboard(page, timeouts) {
  const btn = page.locator(DASHBOARD_BUTTON);
  await expect(btn).toBeVisible({ timeout: timeouts.appReady });
  await btn.click();
  // Wait for the Dashboard heading to prove the component mounted. The stats
  // row uses plain divs (not .stat-title) — see Dashboard.js L250,L263.
  await expect(
    page.locator("span.font-semibold", { hasText: "Dashboard" }).first(),
  ).toBeVisible({ timeout: timeouts.shortAction });
}


testWithCleanup.describe("Dashboard Stats Charts", () => {
  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    // Stub uPlot BEFORE navigateAndWait so the loader resolves synchronously.
    await stubUplot(page);

    // Mock /api/dashboard so the Dashboard component mounts with a known,
    // empty shape (charts are what we're testing, not the lists).
    await page.route(/\/api\/dashboard(\?|$)/, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(MOCK_DASHBOARD),
      });
    });

    // Ensure workspace exists and seed a session so the sidebar has content
    // (matches dashboard.spec.ts setup so the Dashboard button is reachable).
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: {
        name: `Charts Seed ${Date.now()}`,
        working_dir: WORKSPACE_ALPHA,
      },
    });
    expect(createResp.ok()).toBeTruthy();
  });

  testWithCleanup(
    "renders five chart cards with titles and range toolbar",
    async ({ page, timeouts, helpers }) => {
      // Populated 24h response so charts are not empty.
      await page.route(/\/api\/dashboard\/timeseries/, async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(buildTimeseries({ nonZero: true })),
        });
      });
      await helpers.navigateAndWait(page);
      await openDashboard(page, timeouts);

      // Activity section header appears once uPlot has loaded (stubbed).
      const activity = page.locator("text=Activity").first();
      await expect(activity).toBeVisible({ timeout: timeouts.shortAction });

      // Five chart card titles.
      await expect(
        page.locator("text=Tokens (input + output)"),
      ).toBeVisible();
      await expect(page.locator("text=Tool calls")).toBeVisible();
      await expect(
        page.locator("text=Prompts vs agent turns"),
      ).toBeVisible();
      await expect(
        page.locator("text=Beads opened vs closed"),
      ).toBeVisible();
      await expect(
        page.locator("text=Beads: cycle time (claim → close)"),
      ).toBeVisible();

      // Range toolbar buttons via data-testid (stable per StatsCharts.js).
      await expect(
        page.locator('[data-testid="stats-range-24h"]'),
      ).toBeVisible();
      await expect(
        page.locator('[data-testid="stats-range-7d"]'),
      ).toBeVisible();
      await expect(
        page.locator('[data-testid="stats-range-30d"]'),
      ).toBeVisible();

      // Stub inserted a canvas per card (five cards → five canvases).
      await expect(page.locator("canvas[data-mitto-uplot-stub]")).toHaveCount(
        5,
      );

      // Note about length-based estimates rendered under the charts.
      await expect(page.locator("text=length-based estimates")).toBeVisible();
    },
  );

  testWithCleanup(
    "clicking the 7d range triggers a new /api/dashboard/timeseries request",
    async ({ page, timeouts, helpers }) => {
      await page.route(/\/api\/dashboard\/timeseries/, async (route) => {
        const url = new URL(route.request().url());
        const range = url.searchParams.get("range") || "24h";
        const rangeSeconds =
          range === "7d"
            ? 7 * 24 * 3600
            : range === "30d"
              ? 30 * 24 * 3600
              : 24 * 3600;
        const bucketSeconds = range === "30d" ? 24 * 3600 : 3600;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(
            buildTimeseries({ rangeSeconds, bucketSeconds, nonZero: true }),
          ),
        });
      });
      await helpers.navigateAndWait(page);
      await openDashboard(page, timeouts);

      // Wait for the initial 24h request to resolve so the request buffer is
      // "primed" — anything after this is a range-change refetch.
      await expect(
        page.locator("canvas[data-mitto-uplot-stub]"),
      ).toHaveCount(5, { timeout: timeouts.shortAction });

      // Set up the waitForRequest BEFORE clicking so we do not miss it.
      const req = page.waitForRequest(
        (r) =>
          r.url().includes("/api/dashboard/timeseries") &&
          r.url().includes("range=7d"),
        { timeout: timeouts.shortAction },
      );
      await page.locator('[data-testid="stats-range-7d"]').click();
      await req;
    },
  );

  testWithCleanup(
    "empty series shows the 'No activity in this range' placeholder",
    async ({ page, timeouts, helpers }) => {
      // All-zero response → isEmptySeries() → placeholder renders.
      await page.route(/\/api\/dashboard\/timeseries/, async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(buildTimeseries({ nonZero: false })),
        });
      });
      await helpers.navigateAndWait(page);
      await openDashboard(page, timeouts);

      // The placeholder is rendered inside every chart card when empty=true.
      // Five cards → at least one visible instance; assert on the first.
      await expect(
        page.locator("text=No activity in this range").first(),
      ).toBeVisible({ timeout: timeouts.shortAction });
    },
  );

  testWithCleanup(
    "backfill_in_progress=true renders the 'Backfilling history…' badge",
    async ({ page, timeouts, helpers }) => {
      await page.route(/\/api\/dashboard\/timeseries/, async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(
            buildTimeseries({ nonZero: true, backfill: true }),
          ),
        });
      });
      await helpers.navigateAndWait(page);
      await openDashboard(page, timeouts);

      await expect(
        page.locator("text=Backfilling history").first(),
      ).toBeVisible({ timeout: timeouts.shortAction });
    },
  );
});
