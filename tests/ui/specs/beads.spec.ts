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
 *  2. The issue list uses two-line rows so long titles wrap onto their own
 *     line instead of forcing the content to overflow horizontally on narrow
 *     viewports.
 *
 * The Beads backend shells out to the external `bd` binary, which is not
 * guaranteed in CI. To keep the list deterministic, /api/beads/list is mocked
 * with a fixed set of issues — including one with a very long title.
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

// The detail panel carries the shared `properties-panel` class; no other panel
// using it is mounted while the Beads view is active, so it is unambiguous.
const DETAIL_PANEL = "div.properties-panel";
// The full-window dimming backdrop (fixed inset-0, like SessionPanel); clicking
// anywhere on it outside the panel closes the panel.
const PANEL_BACKDROP = "div.properties-backdrop";

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
    "issue list wraps long titles instead of overflowing horizontally on mobile",
    async ({ page, timeouts }) => {
      await openBeads(page, timeouts);
      await page.setViewportSize(MOBILE_VIEWPORT);

      const container = page.locator("div.beads-table-scroll");
      await expect(container).toBeVisible({ timeout: timeouts.shortAction });

      // The long title is shown in full (it wraps onto its own line rather than
      // being clipped behind a horizontal scroll).
      await expect(page.getByText(LONG_TITLE).first()).toBeVisible();

      // The two-line layout keeps content within the viewport width: there is
      // no horizontal overflow (allow a 1px rounding tolerance).
      const metrics = await container.evaluate((el) => ({
        scrollWidth: el.scrollWidth,
        clientWidth: el.clientWidth,
      }));
      expect(metrics.scrollWidth).toBeLessThanOrEqual(metrics.clientWidth + 1);
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
 * The detail panel uses two stacked layers: a full-window `fixed inset-0`
 * dimming backdrop (like SessionPanel, so the conversations sidebar is dimmed
 * too) and a `pointer-events-none` panel layer scoped to the beads view area so
 * `expand` fills only that area and never covers the sidebar. Clicking the
 * backdrop (anywhere outside the panel) dismisses it.
 *
 * These run on the default desktop viewport.
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
    "clicking the backdrop closes the open detail panel",
    async ({ page, timeouts }) => {
      await openBeads(page, timeouts);
      const panel = page.locator(DETAIL_PANEL);

      // Open the panel for the short issue.
      await page
        .locator('div[data-has-context-menu]:has-text("Short issue")')
        .first()
        .click();
      await expect(panel).toBeVisible({ timeout: timeouts.shortAction });
      await expect(panel.getByText("mitto-bbb")).toBeVisible();

      // Clicking the dimming backdrop (outside the panel) dismisses it. The
      // backdrop now spans the whole window and the panel sits above it on the
      // right, so click near the top-left (over the sidebar region) to land on
      // the backdrop rather than the panel.
      await page.locator(PANEL_BACKDROP).click({ position: { x: 5, y: 5 } });
      await expect(panel).toBeHidden({ timeout: timeouts.shortAction });
    },
  );

  testWithCleanup(
    "the fullscreen toggle expands the panel and hides the backdrop",
    async ({ page, timeouts }) => {
      await openBeads(page, timeouts);
      const panel = page.locator(DETAIL_PANEL);

      // Open the panel for the short issue.
      await page
        .locator('div[data-has-context-menu]:has-text("Short issue")')
        .first()
        .click();
      await expect(panel).toBeVisible({ timeout: timeouts.shortAction });

      // Normal mode (desktop): a doubled fixed-width panel (w-[40rem]) capped at
      // 85% of the beads view so the dimming backdrop always retains room. The
      // width is UA-driven (not a viewport breakpoint) so it also applies in a
      // narrow native-app window; the desktop test UA yields the desktop layout.
      await expect(panel).toHaveClass(/w-\[40rem\]/);
      await expect(panel).toHaveClass(/max-w-\[85%\]/);
      await expect(page.locator(PANEL_BACKDROP)).toBeVisible();

      // Toggle fullscreen: the panel fills the beads view width (w-full) and the
      // backdrop is gone.
      await page.getByTitle("Fullscreen").click();
      await expect(panel).toHaveClass(/w-full/);
      await expect(panel).not.toHaveClass(/w-\[40rem\]/);
      await expect(page.locator(PANEL_BACKDROP)).toHaveCount(0);

      // Toggle back: the panel returns to its fixed doubled width and the
      // backdrop reappears.
      await page.getByTitle("Exit fullscreen").click();
      await expect(panel).toHaveClass(/w-\[40rem\]/);
      await expect(page.locator(PANEL_BACKDROP)).toBeVisible();
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
        .locator('div[data-has-context-menu]:has-text("Short issue")')
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
    "delete confirmation dialog renders above the open detail panel",
    async ({ page, timeouts }) => {
      await page.route("**/api/beads/delete", async (route) => {
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
        .locator('div[data-has-context-menu]:has-text("Short issue")')
        .first()
        .click();
      await expect(panel).toBeVisible({ timeout: timeouts.shortAction });

      // Trigger delete from inside the panel footer.
      await panel.locator('button[title="Delete issue"]').click();

      const dialog = page.locator('[data-testid="confirm-dialog"]');
      await expect(dialog).toBeVisible({ timeout: timeouts.shortAction });

      // Ground truth: the element painted at the dialog's center must belong to
      // the dialog, not the detail panel. This catches z-index/stacking-context
      // regressions where the panel covers the confirmation dialog.
      const hit = await dialog.evaluate((el) => {
        const r = el.getBoundingClientRect();
        const cx = r.left + r.width / 2;
        const cy = r.top + r.height / 2;
        const top = document.elementFromPoint(cx, cy);
        return {
          insideDialog: !!top && el.contains(top),
          topTestId: top ? top.getAttribute("data-testid") : null,
          topClass: top ? String(top.className) : null,
        };
      });
      expect(
        hit.insideDialog,
        `Dialog center is covered by: testid=${hit.topTestId} class=${hit.topClass}`,
      ).toBe(true);

      // The Delete button inside the dialog must also be the topmost element at
      // its own center (i.e. actually clickable, not obscured by the panel).
      const delBtn = dialog.getByRole("button", { name: "Delete" });
      const btnClickable = await delBtn.evaluate((el) => {
        const r = el.getBoundingClientRect();
        const top = document.elementFromPoint(
          r.left + r.width / 2,
          r.top + r.height / 2,
        );
        return !!top && (el === top || el.contains(top));
      });
      expect(btnClickable).toBe(true);
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
        .locator('div[data-has-context-menu]:has-text("Short issue")')
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

/**
 * Beads epic deletion tests (desktop).
 *
 * When deleting an epic (an issue with descendants), the confirmation dialog
 * offers a radio group controlling what happens to the whole descendant subtree
 * (recursive): leave them unchanged (default), close the open ones via
 * /api/beads/status (action "close"), or permanently delete all of them via
 * /api/beads/delete. The epic itself is always deleted last.
 *
 * The mock tree is:
 *   mitto-epic (epic, open)
 *   ├── mitto-c1 (open)        ── has a grandchild, to verify recursion
 *   │   └── mitto-g1 (open)    ── grandchild
 *   ├── mitto-c2 (in_progress)
 *   └── mitto-c3 (closed)
 *
 * So the epic has 4 descendants total and 3 OPEN descendants (c1, c2, g1).
 */
const EPIC_TITLE = "Parent epic";
const EPIC_ISSUES = [
  {
    id: "mitto-epic",
    title: EPIC_TITLE,
    description: "An epic with children.",
    status: "open",
    priority: 1,
    issue_type: "epic",
    assignee: "Alice Example",
    owner: "alice@example.com",
    created_at: "2026-06-01T10:00:00Z",
    updated_at: "2026-06-01T10:00:00Z",
  },
  {
    id: "mitto-c1",
    title: "Child one",
    description: "",
    status: "open",
    priority: 2,
    issue_type: "task",
    parent: "mitto-epic",
    created_at: "2026-06-01T10:00:00Z",
    updated_at: "2026-06-01T10:00:00Z",
  },
  {
    id: "mitto-g1",
    title: "Grandchild one",
    description: "",
    status: "open",
    priority: 2,
    issue_type: "task",
    parent: "mitto-c1",
    created_at: "2026-06-01T10:00:00Z",
    updated_at: "2026-06-01T10:00:00Z",
  },
  {
    id: "mitto-c2",
    title: "Child two",
    description: "",
    status: "in_progress",
    priority: 2,
    issue_type: "task",
    parent: "mitto-epic",
    created_at: "2026-06-01T10:00:00Z",
    updated_at: "2026-06-01T10:00:00Z",
  },
  {
    id: "mitto-c3",
    title: "Child closed",
    description: "",
    status: "closed",
    priority: 3,
    issue_type: "task",
    parent: "mitto-epic",
    created_at: "2026-06-01T10:00:00Z",
    updated_at: "2026-06-01T10:00:00Z",
  },
];

testWithCleanup.describe("Beads view - epic deletion", () => {
  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    // Mock the beads list with an epic + children so the table renders without
    // the external `bd` binary.
    await page.route("**/api/beads/list**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(EPIC_ISSUES),
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

  // Open the Beads view and the epic's detail panel, then click Delete so the
  // confirmation dialog is showing. Returns the dialog locator.
  async function openEpicDeleteDialog(page, timeouts) {
    const folderHeader = page
      .locator('div.sticky.top-0[data-has-context-menu="true"]')
      .filter({ hasText: "project-alpha" })
      .first();
    await expect(folderHeader).toBeVisible({ timeout: timeouts.appReady });
    await folderHeader.locator('button[title^="Beads issues:"]').first().click();
    await expect(page.getByText(EPIC_TITLE).first()).toBeVisible({
      timeout: timeouts.appReady,
    });

    const panel = page.locator(DETAIL_PANEL);
    await page
      .locator(`div[data-has-context-menu]:has-text("${EPIC_TITLE}")`)
      .first()
      .click();
    await expect(panel).toBeVisible({ timeout: timeouts.shortAction });
    await expect(panel.getByText("mitto-epic")).toBeVisible();

    await panel.locator('button[title="Delete issue"]').click();
    const dialog = page.locator('[data-testid="confirm-dialog"]');
    await expect(dialog).toBeVisible({ timeout: timeouts.shortAction });
    return dialog;
  }

  testWithCleanup(
    "choosing 'close children' closes the whole open subtree, not the closed one",
    async ({ page, timeouts }) => {
      // Capture the close (status) and delete calls the frontend makes.
      const closedIds: string[] = [];
      await page.route("**/api/beads/status", async (route) => {
        const body = route.request().postDataJSON();
        if (body && body.action === "close") closedIds.push(body.id);
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ ok: true }),
        });
      });
      let deletedId: string | null = null;
      await page.route("**/api/beads/delete", async (route) => {
        const body = route.request().postDataJSON();
        deletedId = body && body.id;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ ok: true }),
        });
      });

      const dialog = await openEpicDeleteDialog(page, timeouts);

      // The dialog announces the epic and offers the recursive radio group. The
      // "close" option counts the 3 OPEN descendants (c1, c2, g1); the closed
      // c3 is excluded. The default selection leaves children unchanged.
      await expect(dialog.locator("h3")).toHaveText("Delete epic");
      await expect(dialog).toContainText("This epic has 4 descendant issues.");
      await expect(dialog).toContainText("Close the 3 open child issues");
      await expect(dialog).toContainText("Delete all 4 child issues (permanent)");

      const noneRadio = dialog.locator('input[type="radio"][value="none"]');
      const closeRadio = dialog.locator('input[type="radio"][value="close"]');
      await expect(noneRadio).toBeChecked();
      await expect(closeRadio).not.toBeChecked();

      // Choose "close children", then confirm the deletion.
      await closeRadio.check();
      await expect(closeRadio).toBeChecked();
      await dialog.getByRole("button", { name: "Delete" }).click();

      // The epic is deleted and exactly the three open descendants are closed
      // (recursively, including the grandchild); the closed child is untouched.
      await expect
        .poll(() => deletedId, { timeout: timeouts.shortAction })
        .toBe("mitto-epic");
      expect([...closedIds].sort()).toEqual(["mitto-c1", "mitto-c2", "mitto-g1"]);
    },
  );

  testWithCleanup(
    "choosing 'delete children' permanently deletes the whole subtree",
    async ({ page, timeouts }) => {
      let statusCalled = false;
      await page.route("**/api/beads/status", async (route) => {
        statusCalled = true;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ ok: true }),
        });
      });
      const deletedIds: string[] = [];
      await page.route("**/api/beads/delete", async (route) => {
        const body = route.request().postDataJSON();
        if (body && body.id) deletedIds.push(body.id);
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ ok: true }),
        });
      });

      const dialog = await openEpicDeleteDialog(page, timeouts);

      // Choose "delete children" and confirm.
      const deleteRadio = dialog.locator('input[type="radio"][value="delete"]');
      await deleteRadio.check();
      await expect(deleteRadio).toBeChecked();
      await dialog.getByRole("button", { name: "Delete" }).click();

      // Every descendant is deleted (including the closed child and the
      // grandchild) plus the epic itself; no child is merely closed.
      await expect
        .poll(() => [...deletedIds].sort(), { timeout: timeouts.shortAction })
        .toEqual(["mitto-c1", "mitto-c2", "mitto-c3", "mitto-epic", "mitto-g1"]);
      // The epic is deleted last (deepest-first child order precedes it).
      expect(deletedIds[deletedIds.length - 1]).toBe("mitto-epic");
      expect(statusCalled).toBe(false);
    },
  );

  testWithCleanup(
    "the default 'leave unchanged' option leaves the subtree untouched",
    async ({ page, timeouts }) => {
      let statusCalled = false;
      await page.route("**/api/beads/status", async (route) => {
        statusCalled = true;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ ok: true }),
        });
      });
      const deletedIds: string[] = [];
      await page.route("**/api/beads/delete", async (route) => {
        const body = route.request().postDataJSON();
        if (body && body.id) deletedIds.push(body.id);
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ ok: true }),
        });
      });

      const dialog = await openEpicDeleteDialog(page, timeouts);

      // Leave the default ("none") selection and confirm: only the epic is
      // deleted; no child is closed and no child is deleted.
      await dialog.getByRole("button", { name: "Delete" }).click();
      await expect
        .poll(() => deletedIds, { timeout: timeouts.shortAction })
        .toEqual(["mitto-epic"]);
      expect(statusCalled).toBe(false);
    },
  );
});
