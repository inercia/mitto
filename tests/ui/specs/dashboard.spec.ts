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
// Mobile viewport shared with the other mobile specs. Dashboard list panels
// remain in a horizontal carousel that overflows at this narrow width.
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
      data: {
        name: `Dashboard Seed ${Date.now()}`,
        working_dir: WORKSPACE_ALPHA,
      },
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

      // Both leading panels remain mounted in the horizontal list carousel.
      await expect(page.locator("#dash-slide-prompting")).toBeVisible();
      await expect(page.locator("#dash-slide-in-progress")).toBeVisible();
    },
  );

  testWithCleanup(
    "all four list panel labels are rendered in the horizontal carousel",
    async ({ page, timeouts }) => {
      await openDashboard(page, timeouts);

      // All four panels share the horizontal carousel; each label remains
      // mounted without requiring pagination or separate navigation.
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
    "empty Recent conversations slide shows the empty placeholder",
    async ({ page, timeouts, apiUrl }) => {
      await page.route(`**${apiUrl("/api/sessions")}`, async (route) => {
        if (route.request().method() === "GET") {
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: "[]",
          });
        } else {
          await route.continue();
        }
      });
      await page.evaluate(() => {
        localStorage.removeItem("mitto_last_session_id");
        localStorage.removeItem("mitto_last_session_id_conversations");
        localStorage.removeItem("mitto_last_session_id_loop");
        localStorage.removeItem("mitto_last_session_id_archived");
        localStorage.removeItem("mitto_conversation_filter_tab");
      });
      await page.reload();
      await openDashboard(page, timeouts);

      // The session-list route is empty in this test, so Recent conversations
      // should render its empty placeholder once Dashboard data has loaded.
      await expect(
        page.locator("#dash-slide-prompting").getByText("No items"),
      ).toBeVisible();
    },
  );

  testWithCleanup(
    "mobile viewport keeps all four panels in the horizontal carousel",
    async ({ page, timeouts }) => {
      // Open Dashboard on desktop first, then resize to verify the list carousel
      // remains horizontally scrollable at a narrow viewport.
      await openDashboard(page, timeouts);
      await page.setViewportSize(MOBILE_VIEWPORT);

      // All four slide IDs remain mounted in the horizontal carousel at mobile
      // width; the following checks verify their order and scrollability.
      const promptingSlide = page.locator("#dash-slide-prompting");
      const inProgressSlide = page.locator("#dash-slide-in-progress");
      const readySlide = page.locator("#dash-slide-ready");
      const recentSlide = page.locator("#dash-slide-recent");
      const carousel = promptingSlide.locator("..");

      await expect(promptingSlide).toBeVisible({
        timeout: timeouts.shortAction,
      });
      await expect(inProgressSlide).toBeVisible();
      await expect(readySlide).toBeVisible();
      await expect(recentSlide).toBeVisible();
      await expect(carousel).toHaveClass(/mitto-carousel/);

      // Panels share one carousel row. Their x positions preserve slide order,
      // while the overflow assertion ensures later panels can be reached by scrolling.
      const promptingBox = await promptingSlide.boundingBox();
      const inProgressBox = await inProgressSlide.boundingBox();
      const readyBox = await readySlide.boundingBox();
      const recentBox = await recentSlide.boundingBox();
      expect(promptingBox).not.toBeNull();
      expect(inProgressBox).not.toBeNull();
      expect(readyBox).not.toBeNull();
      expect(recentBox).not.toBeNull();

      const carouselMetrics = await carousel.evaluate((element) => ({
        clientWidth: element.clientWidth,
        scrollWidth: element.scrollWidth,
        overflowX: getComputedStyle(element).overflowX,
      }));
      expect(carouselMetrics.overflowX).toBe("auto");
      expect(carouselMetrics.scrollWidth).toBeGreaterThan(
        carouselMetrics.clientWidth,
      );

      expect(inProgressBox!.y).toBe(promptingBox!.y);
      expect(readyBox!.y).toBe(promptingBox!.y);
      expect(recentBox!.y).toBe(promptingBox!.y);
      expect(inProgressBox!.x).toBeGreaterThan(promptingBox!.x);
      expect(readyBox!.x).toBeGreaterThan(inProgressBox!.x);
      expect(recentBox!.x).toBeGreaterThan(readyBox!.x);
    },
  );
});
