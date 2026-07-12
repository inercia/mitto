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
    epics: [
      {
        id: "mitto-e1",
        title: "Epic task 1",
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

async function openDashboard(page, timeouts) {
  const btn = page.locator(DASHBOARD_BUTTON);
  await expect(btn).toBeVisible({ timeout: timeouts.appReady });
  await btn.click();
  // Wait for the stats header to render (proves the Dashboard component mounted
  // and the mocked /api/dashboard payload was consumed).
  await expect(
    page.locator(".stat-title", { hasText: "Issues in progress" }),
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

      // Three stat titles from the header.
      await expect(
        page.locator(".stat-title", { hasText: "Issues in progress" }),
      ).toBeVisible();
      await expect(
        page.locator(".stat-title", { hasText: "Conversations prompting" }),
      ).toBeVisible();
      await expect(
        page.locator(".stat-title", { hasText: "Loops active / stopped" }),
      ).toBeVisible();

      // Issues-in-progress stat value shows the mocked count (7).
      const inProgressStat = page
        .locator(".stat")
        .filter({ hasText: "Issues in progress" });
      await expect(inProgressStat.locator(".stat-value")).toHaveText(/7/);

      // Paged grid: 4 lists across 2 pages. Page 1 shows the first two list
      // panels (prompting + in-progress); the "Next page" arrow is present
      // for navigation. The page indicator text was removed intentionally.
      await expect(page.locator("#dash-slide-prompting")).toBeVisible();
      await expect(page.locator("#dash-slide-in-progress")).toBeVisible();
      await expect(
        page.getByRole("button", { name: "Next page" }),
      ).toBeVisible();
    },
  );

  testWithCleanup(
    "all list panel labels are reachable via pagination",
    async ({ page, timeouts }) => {
      await openDashboard(page, timeouts);

      // Page 1 shows the first two labels.
      await expect(
        page.getByText("Prompting conversations", { exact: true }),
      ).toBeVisible();
      await expect(
        page.getByText("In-progress tasks", { exact: true }),
      ).toBeVisible();

      // Advance to page 2 via the "Next page" button; assert the remaining
      // two labels appear.
      await page.getByRole("button", { name: "Next page" }).click();
      await expect(
        page.getByText("Ready tasks", { exact: true }),
      ).toBeVisible();
      await expect(
        page.getByText("Epic tasks", { exact: true }),
      ).toBeVisible();
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
});
