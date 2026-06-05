import { testWithCleanup, expect } from "../fixtures/test-fixtures";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Beads view mobile behavior tests for the Mitto Web UI.
 *
 * Verifies the two mobile-focused fixes in BeadsView:
 *  1. A hamburger (☰) button in the Beads header (md:hidden) that opens the
 *     conversations sidebar overlay, so users are not trapped in the Beads view
 *     on small screens.
 *  2. The issue table can scroll horizontally instead of clipping long titles
 *     and right-hand columns on narrow viewports.
 *
 * The Beads backend shells out to the external `bd` binary, which is not
 * guaranteed in CI. To keep the table deterministic, /api/beads/list is mocked
 * with a fixed set of issues — including one with a very long title to force
 * horizontal overflow.
 */

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE_ALPHA = path.join(
  projectRoot,
  "tests/fixtures/workspaces/project-alpha",
);
const AGENT_NAME = "mock-acp";

// A deliberately long title so the table grows wider than a mobile viewport.
const LONG_TITLE =
  "This is an intentionally very long Beads issue title used to force the table to overflow horizontally on narrow mobile viewports";

const MOCK_ISSUES = [
  {
    id: "mitto-aaa",
    title: LONG_TITLE,
    description: "Issue with a very long title.",
    status: "open",
    priority: 1,
    issue_type: "feature",
    assignee: "Alice Example",
    owner: "alice@example.com",
    created_at: "2026-06-01T10:00:00Z",
    updated_at: "2026-06-01T10:00:00Z",
  },
  {
    id: "mitto-bbb",
    title: "Short issue",
    description: "",
    status: "in_progress",
    priority: 2,
    issue_type: "bug",
    assignee: "Bob Example",
    owner: "bob@example.com",
    created_at: "2026-06-01T10:00:00Z",
    updated_at: "2026-06-01T10:00:00Z",
  },
];

// Mobile sidebar overlay (z-40); modal dialogs use z-50, so this is unambiguous.
const MOBILE_OVERLAY = ".fixed.inset-0.z-40";
const MOBILE_VIEWPORT = { width: 390, height: 844 };

// The docked detail panel carries the shared `properties-panel` class; no other
// panel using it is mounted while the Beads view is active, so it is unambiguous.
const DETAIL_PANEL = "div.properties-panel";

// Opens the Beads view from the project-alpha folder header (desktop sidebar)
// and waits for the mocked issue list to render. Shared by all Beads specs.
async function openBeads(page, timeouts) {
  const folderHeader = page
    .locator('div.sticky.top-0[data-has-context-menu="true"]')
    .filter({ hasText: "project-alpha" })
    .first();
  await expect(folderHeader).toBeVisible({ timeout: timeouts.appReady });

  const beadsButton = folderHeader
    .locator('button[title^="Beads issues:"]')
    .first();
  await beadsButton.click();

  // A mocked row confirms the BeadsView mounted and loaded the list.
  await expect(page.getByText("Short issue").first()).toBeVisible({
    timeout: timeouts.appReady,
  });
}

testWithCleanup.describe("Beads view - mobile", () => {
  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    // Mock the beads list so the table renders without the external `bd` binary.
    await page.route("**/api/beads/list**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(MOCK_ISSUES),
      });
    });

    // Ensure the project-alpha workspace exists so its folder group renders.
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });

    // Seed a conversation in project-alpha so the folder header (with its Beads
    // button) appears in the sidebar.
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: { name: `Beads Seed ${Date.now()}`, working_dir: WORKSPACE_ALPHA },
    });
    expect(createResp.ok()).toBeTruthy();

    await helpers.navigateAndWait(page);
  });

  testWithCleanup(
    "hamburger opens the conversations sidebar on a mobile viewport",
    async ({ page, timeouts }) => {
      await openBeads(page, timeouts);
      await page.setViewportSize(MOBILE_VIEWPORT);

      const hamburger = page.locator('button[title="Show conversations"]');
      await expect(hamburger).toBeVisible({ timeout: timeouts.shortAction });

      await hamburger.click();

      const overlay = page.locator(MOBILE_OVERLAY);
      await expect(overlay).toBeVisible({ timeout: timeouts.shortAction });
      await expect(
        overlay.locator('h2:has-text("Conversations")'),
      ).toBeVisible();
    },
  );

  testWithCleanup(
    "issue table scrolls horizontally instead of clipping on mobile",
    async ({ page, timeouts }) => {
      await openBeads(page, timeouts);
      await page.setViewportSize(MOBILE_VIEWPORT);

      const container = page.locator(
        "div.overflow-x-auto:has(table.min-w-full)",
      );
      await expect(container).toBeVisible({ timeout: timeouts.shortAction });

      // The scroll container carries the visible-scrollbar class so the
      // horizontal scrollbar is discoverable on macOS (WKWebView / Safari),
      // where overlay scrollbars are otherwise hidden.
      await expect(container).toHaveClass(/beads-table-scroll/);

      // The content is wider than the container: it overflows rather than clips.
      const metrics = await container.evaluate((el) => ({
        scrollWidth: el.scrollWidth,
        clientWidth: el.clientWidth,
      }));
      expect(metrics.scrollWidth).toBeGreaterThan(metrics.clientWidth);

      // And the overflow is actually reachable by horizontal scrolling.
      await container.evaluate((el) => {
        el.scrollLeft = el.scrollWidth;
      });
      const scrollLeft = await container.evaluate((el) => el.scrollLeft);
      expect(scrollLeft).toBeGreaterThan(0);
    },
  );

  testWithCleanup(
    "hamburger is hidden on desktop viewports",
    async ({ page, timeouts }) => {
      await openBeads(page, timeouts);
      // Default Desktop Chrome viewport is >= md, so md:hidden applies.
      await expect(
        page.locator('button[title="Show conversations"]'),
      ).toBeHidden();
    },
  );
});

/**
 * Beads detail panel behavior tests (desktop).
 *
 * The detail panel is docked inline beside the issue list (no backdrop), so a
 * click-outside handler in BeadsDetailPanel dismisses it when clicking anywhere
 * that is not the panel — except issue rows (which manage their own open/toggle)
 * and floating z-50 layers (context menu / confirm dialogs).
 *
 * These run on the default desktop viewport so the list and the docked panel are
 * visible side by side.
 */
testWithCleanup.describe("Beads view - detail panel", () => {
  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    // Mock the beads list so the table renders without the external `bd` binary.
    await page.route("**/api/beads/list**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(MOCK_ISSUES),
      });
    });

    // Ensure the project-alpha workspace exists so its folder group renders.
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });

    // Seed a conversation so the folder header (with its Beads button) appears.
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: { name: `Beads Seed ${Date.now()}`, working_dir: WORKSPACE_ALPHA },
    });
    expect(createResp.ok()).toBeTruthy();

    await helpers.navigateAndWait(page);
  });

  testWithCleanup(
    "clicking the list area closes the open detail panel",
    async ({ page, timeouts }) => {
      await openBeads(page, timeouts);
      const panel = page.locator(DETAIL_PANEL);

      // Open the panel for the short issue.
      await page
        .locator('tr[data-has-context-menu]:has-text("Short issue")')
        .first()
        .click();
      await expect(panel).toBeVisible({ timeout: timeouts.shortAction });
      await expect(panel.getByText("mitto-bbb")).toBeVisible();

      // Clicking the view header (outside the panel, not a row, not a z-50
      // layer) dismisses the panel.
      await page.locator('span.text-lg:has-text("Beads")').first().click();
      await expect(panel).toBeHidden({ timeout: timeouts.shortAction });
    },
  );

  testWithCleanup(
    "clicking another row switches the detail panel instead of closing it",
    async ({ page, timeouts }) => {
      await openBeads(page, timeouts);
      const panel = page.locator(DETAIL_PANEL);

      // Open the panel for the short issue (mitto-bbb).
      await page
        .locator('tr[data-has-context-menu]:has-text("Short issue")')
        .first()
        .click();
      await expect(panel).toBeVisible({ timeout: timeouts.shortAction });
      await expect(panel.getByText("mitto-bbb")).toBeVisible();

      // Clicking the other issue's row keeps the panel open but swaps content.
      await page
        .locator('tr[data-has-context-menu]:has-text("mitto-aaa")')
        .first()
        .click();
      await expect(panel).toBeVisible();
      await expect(panel.getByText("mitto-aaa")).toBeVisible();
      await expect(panel.getByText("mitto-bbb")).toHaveCount(0);
    },
  );

  testWithCleanup(
    "clicking the title edits it inline and saves the new value",
    async ({ page, timeouts }) => {
      // Capture the update request so we can assert the new title is sent.
      let updateBody: any = null;
      await page.route("**/api/beads/update", async (route) => {
        updateBody = route.request().postDataJSON();
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ ok: true }),
        });
      });

      await openBeads(page, timeouts);
      const panel = page.locator(DETAIL_PANEL);

      // Open the panel for the short issue (mitto-bbb).
      await page
        .locator('tr[data-has-context-menu]:has-text("Short issue")')
        .first()
        .click();
      await expect(panel).toBeVisible({ timeout: timeouts.shortAction });

      // Click the title heading to enter inline edit mode.
      await panel.locator('h2:has-text("Short issue")').click();
      const titleInput = panel.locator('input.font-semibold');
      await expect(titleInput).toBeVisible({ timeout: timeouts.shortAction });
      await expect(titleInput).toHaveValue("Short issue");

      // Replace the title and save with Enter.
      await titleInput.fill("Renamed issue");
      await titleInput.press("Enter");

      await expect
        .poll(() => updateBody, { timeout: timeouts.shortAction })
        .not.toBeNull();
      expect(updateBody.id).toBe("mitto-bbb");
      expect(updateBody.title).toBe("Renamed issue");
    },
  );

  testWithCleanup(
    "pressing Escape cancels the title edit without saving",
    async ({ page, timeouts }) => {
      let updateCalled = false;
      await page.route("**/api/beads/update", async (route) => {
        updateCalled = true;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ ok: true }),
        });
      });

      await openBeads(page, timeouts);
      const panel = page.locator(DETAIL_PANEL);

      await page
        .locator('tr[data-has-context-menu]:has-text("Short issue")')
        .first()
        .click();
      await expect(panel).toBeVisible({ timeout: timeouts.shortAction });

      await panel.locator('h2:has-text("Short issue")').click();
      const titleInput = panel.locator('input.font-semibold');
      await expect(titleInput).toBeVisible({ timeout: timeouts.shortAction });

      // Edit then abort with Escape: no save, title heading restored.
      await titleInput.fill("Discarded title");
      await titleInput.press("Escape");

      await expect(
        panel.locator('h2:has-text("Short issue")'),
      ).toBeVisible({ timeout: timeouts.shortAction });
      expect(updateCalled).toBe(false);
    },
  );
});
