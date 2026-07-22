import { testWithCleanup, expect } from "../fixtures/test-fixtures";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Global Dashboard (mitto-aqo) UI tests.
 *
 * The Dashboard component polls GET /api/dashboard and derives some counts
 * from allSessions client-side. To keep the tests deterministic we mock
 * /api/dashboard with a fixed payload. When a task row is clicked the
 * Beads viewer opens via /api/issues/{id}?working_dir=... — that endpoint
 * shells out to the external `bd` binary, so we mock it too.
 */

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);
const AGENT_NAME = "mock-acp";

const MOCK_DASHBOARD = {
  stats: {
    issues_in_progress: 7,
    conversations_prompting: 2,
    loops_active: 3,
    loops_stopped: 4,
  },
  lists: {
    in_progress: [
      {
        id: "mitto-t1",
        title: "In-progress task 1",
        priority: 1,
        working_dir: WORKSPACE_ALPHA,
        issue_type: "task",
        status: "in_progress",
        updated_at: "2026-07-12T10:00:00Z",
      },
      {
        id: "mitto-t2",
        title: "In-progress task 2",
        priority: 2,
        working_dir: WORKSPACE_ALPHA,
        issue_type: "task",
        status: "in_progress",
        updated_at: "2026-07-12T09:00:00Z",
      },
    ],
    ready: [
      {
        id: "mitto-r1",
        title: "Ready task 1",
        priority: 0,
        working_dir: WORKSPACE_ALPHA,
        issue_type: "task",
        status: "open",
        updated_at: "2026-07-12T08:00:00Z",
      },
    ],
    recently_modified: [
      {
        id: "mitto-e1",
        title: "Recently modified item 1",
        priority: 3,
        working_dir: WORKSPACE_ALPHA,
        issue_type: "epic",
        status: "open",
        updated_at: "2026-07-12T07:00:00Z",
      },
    ],
  },
};

// Mocked single-issue payload for /api/issues/{id} so the Beads viewer can
// render without hitting the external `bd` binary. Mirrors the shape used by
// beads.spec.ts (ISSUE_WITH_LONG_COMMENT).
const MOCK_ISSUE_T1 = {
  id: "mitto-t1",
  title: "In-progress task 1",
  description: "Fixture in-progress task used by the dashboard spec.",
  status: "in_progress",
  priority: 1,
  issue_type: "task",
  assignee: "",
  owner: "",
  created_at: "2026-07-12T09:00:00Z",
  updated_at: "2026-07-12T10:00:00Z",
  dependencies: [],
  labels: [],
  notes: "",
  comments: [],
};

// Selector for the sidebar Dashboard entry (SessionList.js ~L1108).
const DASHBOARD_BUTTON = 'button[title="Dashboard"]';
// Beads detail panel — shared with beads.spec.ts.
const DETAIL_PANEL = "div.properties-panel";
// Mobile viewport — matches the one used across the other mobile specs
// (beads.spec.ts, side-panels-outside-click-mobile.spec.ts, etc.). Below the
// Tailwind `md` breakpoint (768px) so the Dashboard's responsive grid
// (mitto-aqo.5) collapses to a single column while still rendering every list.
const MOBILE_VIEWPORT = { width: 390, height: 844 };

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

testWithCleanup.describe("Global Dashboard", () => {
  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    // Mock /api/dashboard BEFORE navigateAndWait so the first poll sees it.
    await page.route(/\/api\/dashboard(\?|$)/, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(MOCK_DASHBOARD),
      });
    });

    // Mock /api/issues/{id} so clicking a task row does not shell out to `bd`.
    await page.route(/\/api\/issues\/[^/?]+/, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(MOCK_ISSUE_T1),
      });
    });

    // Ensure project-alpha exists and has one seed session so the sidebar has
    // content (matches beads.spec.ts setup).
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: { name: `Dashboard Seed ${Date.now()}`, working_dir: WORKSPACE_ALPHA },
    });
    expect(createResp.ok()).toBeTruthy();

    await helpers.navigateAndWait(page);
  });

  testWithCleanup(
    "sidebar Dashboard entry opens the dashboard view",
    async ({ page, timeouts }) => {
      await openDashboard(page, timeouts);

      // Three stat labels from the header. The stats row uses plain divs
      // (not daisyUI .stats) — see Dashboard.js L252-257.
      await expect(
        page.getByText("Issues in progress", { exact: true }),
      ).toBeVisible();
      await expect(
        page.getByText("Conversations prompting", { exact: true }),
      ).toBeVisible();
      await expect(
        page.getByText("Loops active / stopped", { exact: true }),
      ).toBeVisible();

      // Issues-in-progress stat value shows the mocked count (7). Locate by
      // the stat column (min-w-0 stat cells in the header grid — see
      // Dashboard.js L262) that contains the label; the value is the
      // sibling .text-2xl div.
      const inProgressCol = page
        .locator("div.flex.flex-col.gap-1.min-w-0")
        .filter({ hasText: "Issues in progress" });
      await expect(inProgressCol.locator("div.text-2xl")).toContainText("7");

      // Responsive grid (mitto-aqo.5): every list panel is always visible;
      // there is no carousel/pagination.
      await expect(page.locator("#dash-slide-prompting")).toBeVisible();
      await expect(page.locator("#dash-slide-in-progress")).toBeVisible();
    },
  );

  testWithCleanup(
    "all four list panel labels are visible at once",
    async ({ page, timeouts }) => {
      await openDashboard(page, timeouts);

      // Responsive grid (mitto-aqo.5): all four list panels are always
      // rendered simultaneously; there is no pagination. Assert every label
      // is visible without any navigation.
      await expect(
        page.getByText("Recent conversations", { exact: true }),
      ).toBeVisible();
      await expect(
        page.getByText("In-progress tasks", { exact: true }),
      ).toBeVisible();
      await expect(
        page.getByText("Ready tasks", { exact: true }),
      ).toBeVisible();
      await expect(
        page.getByText("Recently modified", { exact: true }),
      ).toBeVisible();

      // And their slide containers are all mounted at the same time.
      await expect(page.locator("#dash-slide-prompting")).toBeVisible();
      await expect(page.locator("#dash-slide-in-progress")).toBeVisible();
      await expect(page.locator("#dash-slide-ready")).toBeVisible();
      await expect(page.locator("#dash-slide-recent")).toBeVisible();
    },
  );

  testWithCleanup(
    "clicking an in-progress task row opens the Beads viewer",
    async ({ page, timeouts }) => {
      await openDashboard(page, timeouts);

      // The in-progress task row for mitto-t1 lives on the second slide. Scope
      // by slide id to disambiguate from any similar text elsewhere.
      const row = page
        .locator("#dash-slide-in-progress li.list-row")
        .filter({ hasText: "In-progress task 1" })
        .first();
      await expect(row).toBeVisible({ timeout: timeouts.shortAction });
      await row.click();

      // Beads detail panel opens for the clicked issue; disambiguate by
      // scoping the title match to the panel container.
      const panel = page.locator(DETAIL_PANEL);
      await expect(panel).toBeVisible({ timeout: timeouts.appReady });
      await expect(panel.getByText("In-progress task 1").first()).toBeVisible();
    },
  );

  testWithCleanup(
    "empty prompting-conversations slide shows the empty placeholder",
    async ({ page, timeouts }) => {
      await openDashboard(page, timeouts);

      // The mocked dashboard payload has no prompting conversations, and the
      // seeded conversation is idle → the prompting slide renders "No items".
      await expect(
        page.locator("#dash-slide-prompting").getByText("No items"),
      ).toBeVisible();
    },
  );

  testWithCleanup(
    "mobile viewport renders all four panels in a single column",
    async ({ page, timeouts }) => {
      // Open the dashboard on the default (desktop) viewport first — the
      // sidebar Dashboard button is always visible there, so we do not have
      // to open the mobile drawer to reach it. Then shrink the viewport,
      // which fires the (min-width:768px) matchMedia listener wired in
      // Dashboard.js and collapses the responsive grid from 4 columns to 1.
      await openDashboard(page, timeouts);
      await page.setViewportSize(MOBILE_VIEWPORT);

      // Responsive grid (mitto-aqo.5): mobile only changes the column count
      // (1 instead of 2/3/4); every list stays mounted. The four slide ids
      // come from SLIDES in Dashboard.js and are stable.
      const promptingSlide = page.locator("#dash-slide-prompting");
      const inProgressSlide = page.locator("#dash-slide-in-progress");
      const readySlide = page.locator("#dash-slide-ready");
      const recentSlide = page.locator("#dash-slide-recent");

      await expect(promptingSlide).toBeVisible({
        timeout: timeouts.shortAction,
      });
      await expect(inProgressSlide).toBeVisible();
      await expect(readySlide).toBeVisible();
      await expect(recentSlide).toBeVisible();

      // Panels stack vertically: each slide's top edge is strictly below the
      // previous one's, confirming the single-column layout rather than a
      // side-by-side row.
      const promptingBox = await promptingSlide.boundingBox();
      const inProgressBox = await inProgressSlide.boundingBox();
      const readyBox = await readySlide.boundingBox();
      const recentBox = await recentSlide.boundingBox();
      expect(promptingBox).not.toBeNull();
      expect(inProgressBox).not.toBeNull();
      expect(readyBox).not.toBeNull();
      expect(recentBox).not.toBeNull();
      expect(inProgressBox!.y).toBeGreaterThan(promptingBox!.y);
      expect(readyBox!.y).toBeGreaterThan(inProgressBox!.y);
      expect(recentBox!.y).toBeGreaterThan(readyBox!.y);
    },
  );
});
