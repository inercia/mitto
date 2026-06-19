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
// The mobile sidebar is a daisyUI `drawer` (side="start", zClass="z-40"); its
// full-viewport container is `.drawer-side` (position:fixed/inset via daisyUI),
// which becomes visible when the permanently-checked drawer-toggle resolves the
// open state and holds the panel with the "Conversations" heading.
const MOBILE_OVERLAY = ".drawer-side.z-40";
const MOBILE_VIEWPORT = { width: 390, height: 844 };

// The detail panel carries the shared `properties-panel` class; no other panel
// using it is mounted while the Beads view is active, so it is unambiguous.
const DETAIL_PANEL = "div.properties-panel";
// The full-window dimming backdrop (fixed inset-0, like SessionPanel); clicking
// anywhere on it outside the panel closes the panel.
const PANEL_BACKDROP = "div.properties-backdrop";

// Opens the Beads view from the project-alpha folder header (desktop sidebar)
// and waits for the mocked issue list to render. Shared by all Beads specs.
// Clicks the per-folder Tasks/Beads button to open the Beads view. The button
// lives in the folder's expandable content <ul> (a sibling of the <summary>),
// so the folder <details> must be open for it to render — expand it if needed.
async function clickBeadsButton(page, timeouts) {
  const folderHeader = page
    .locator('summary[data-has-context-menu="true"]')
    .filter({ hasText: "project-alpha" })
    .first();
  await expect(folderHeader).toBeVisible({ timeout: timeouts.appReady });

  const folderDetails = folderHeader.locator("xpath=ancestor::details[1]");
  if (!(await folderDetails.evaluate((el: HTMLDetailsElement) => el.open))) {
    await folderHeader.click();
  }
  await folderDetails
    .locator('[role="button"][title^="Beads issues:"]')
    .first()
    .click();
}

async function openBeads(page, timeouts) {
  await clickBeadsButton(page, timeouts);

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

      // Replace the title and blur with Enter (draft updated, not yet saved).
      await titleInput.fill("Renamed issue");
      await titleInput.press("Enter");

      // Click the unified Save button to persist all dirty fields.
      await panel.locator('button[title="Save changes"]').click();

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

      // Delete moved to the kebab menu (now a ContextMenu portaled to body).
      await panel.locator('button[title="More actions"]').click();
      await page.locator("ul.menu.fixed").getByRole("button", { name: "Delete", exact: true }).click();

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

  testWithCleanup(
    "issue list stays painted when hovering with the detail panel open",
    async ({ page, timeouts }) => {
      // Regression guard for a WKWebView/Safari compositing glitch: with the
      // detail panel open, a translucent fixed backdrop (bg-black/50) and the
      // scoped drawer's transparent overlay stack over the scrollable issue
      // list. Moving the pointer over that overlay dropped the scroll layer's
      // backing store and the list painted blank. The fix promotes
      // .beads-table-scroll to its own GPU backing layer via translateZ(0).
      // Chromium does not reproduce the WebKit repaint bug, so this asserts the
      // layer-promotion is applied (the actual fix) and that the rows remain
      // visible/painted while hovering with the panel open.
      await openBeads(page, timeouts);
      const panel = page.locator(DETAIL_PANEL);
      const scroll = page.locator("div.beads-table-scroll");

      // Open the panel for the short issue (mitto-bbb).
      await page
        .locator('div[data-has-context-menu]:has-text("Short issue")')
        .first()
        .click();
      await expect(panel).toBeVisible({ timeout: timeouts.shortAction });

      // The scroll container is promoted to its own GPU backing layer. A
      // non-"none" transform is the layer-promotion the fix relies on; without
      // it, this regresses to the blank-on-hover WebKit glitch.
      const transform = await scroll.evaluate(
        (el) => getComputedStyle(el).transform,
      );
      expect(transform).not.toBe("none");
      expect(transform).not.toBe("");

      // Move the pointer over the list's left region (which the panel does not
      // cover, but the translucent backdrop and the transparent drawer-overlay
      // do). This is the exact gesture that triggered the WebKit blank-out, so
      // we dispatch a raw mouse move rather than Locator.hover() — the overlays
      // intercept pointer events by design, which would fail hover's
      // actionability check.
      const scrollBox = await scroll.boundingBox();
      expect(scrollBox).not.toBeNull();
      await page.mouse.move(
        scrollBox!.x + 20,
        scrollBox!.y + scrollBox!.height / 2,
      );

      // The rows must remain visible and painted, not blanked out.
      const shortRow = page.getByText("Short issue").first();
      await expect(shortRow).toBeVisible();
      const box = await shortRow.boundingBox();
      expect(box).not.toBeNull();
      expect(box!.width).toBeGreaterThan(0);
      expect(box!.height).toBeGreaterThan(0);
    },
  );

  testWithCleanup(
    "clicking the type badge opens a dropdown and selecting a type marks the draft dirty",
    async ({ page, timeouts }) => {
      // Capture the update request so we can assert the new type is sent.
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

      // Open the panel for the short issue (mitto-bbb, which has issue_type "bug").
      await page
        .locator('div[data-has-context-menu]:has-text("Short issue")')
        .first()
        .click();
      await expect(panel).toBeVisible({ timeout: timeouts.shortAction });

      // Click the type badge button to open the dropdown.
      // mitto-bbb has issue_type "bug"; select a different type to make the draft dirty.
      await panel.locator('button[title="Click to change type"]').click();
      await panel.locator('button:has-text("task")').first().click();

      // Click the unified Save button.
      await panel.locator('button[title="Save changes"]').click();

      await expect
        .poll(() => updateBody, { timeout: timeouts.shortAction })
        .not.toBeNull();
      expect(updateBody.id).toBe("mitto-bbb");
      expect(updateBody.type).toBe("task");
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
    await clickBeadsButton(page, timeouts);
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

    // Delete moved to the kebab menu (now a ContextMenu portaled to body).
    await panel.locator('button[title="More actions"]').click();
    await page.locator("ul.menu.fixed").getByRole("button", { name: "Delete", exact: true }).click();
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

/**
 * Beads epic grouping tests (desktop).
 *
 * The Beads toolbar has an opt-in "Group issues by epic" toggle (LayersIcon, in
 * the "View mode" join group). When enabled, top-level epics render as a
 * collapsible <details> whose children (and grandchildren, attributed to the
 * nearest top-level epic) are shown indented in a `.pl-8` container.
 *
 * These tests pin two behaviors:
 *  1. Enabling grouping immediately reveals the hierarchy — epics are EXPANDED
 *     by default, so the indented children are visible without extra clicks.
 *  2. The toggle state and any explicitly-collapsed epics are persisted, so the
 *     grouped view (with the same epic still collapsed) survives a reload.
 *
 * Reuses the EPIC_ISSUES mock tree (epic + children + grandchild) defined above.
 */
testWithCleanup.describe("Beads view - epic grouping", () => {
  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    await page.route("**/api/beads/list**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(EPIC_ISSUES),
      });
    });

    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: { name: `Beads Seed ${Date.now()}`, working_dir: WORKSPACE_ALPHA },
    });
    expect(createResp.ok()).toBeTruthy();

    await helpers.navigateAndWait(page);
  });

  // The grouping toggle is the single aria-pressed button in the "View mode"
  // join group (status toggles live in a separate group).
  const groupingToggle = (page) =>
    page.locator('[aria-label="View mode"] button[aria-pressed]').first();

  testWithCleanup(
    "grouping is on by default, showing epic children indented and expanded",
    async ({ page, timeouts }) => {
      await clickBeadsButton(page, timeouts);
      await expect(page.getByText(EPIC_TITLE).first()).toBeVisible({
        timeout: timeouts.appReady,
      });

      // Grouping is enabled by default, so the toggle starts pressed and the
      // epic renders as a collapsible group that is OPEN by default — its
      // children are immediately visible (the fix for "I don't see the
      // hierarchy").
      const toggle = groupingToggle(page);
      await expect(toggle).toHaveAttribute("aria-pressed", "true");

      const epicGroup = page.locator("details.beads-epic-group").first();
      await expect(epicGroup).toBeVisible({ timeout: timeouts.shortAction });
      await expect(epicGroup).toHaveJSProperty("open", true);

      // Children, and the grandchild (attributed to the top epic), appear in the
      // indented (pl-8) container under the epic summary.
      const indented = epicGroup.locator(".pl-8");
      await expect(indented.getByText("Child one", { exact: true })).toBeVisible();
      await expect(indented.getByText("Child two", { exact: true })).toBeVisible();
      await expect(indented.getByText("Grandchild one", { exact: true })).toBeVisible();

      // Turning grouping off switches back to a flat list (no epic groups).
      await toggle.click();
      await expect(toggle).toHaveAttribute("aria-pressed", "false");
      await expect(page.locator("details.beads-epic-group")).toHaveCount(0);
    },
  );

  testWithCleanup(
    "collapsing an epic hides its children and persists across reload",
    async ({ page, timeouts, helpers }) => {
      await clickBeadsButton(page, timeouts);
      await expect(page.getByText(EPIC_TITLE).first()).toBeVisible({
        timeout: timeouts.appReady,
      });

      // Grouping is on by default; the epic starts open with children visible.
      const epicGroup = page.locator("details.beads-epic-group").first();
      await expect(epicGroup).toHaveJSProperty("open", true);
      await expect(
        epicGroup.locator(".pl-8").getByText("Child one", { exact: true }),
      ).toBeVisible();

      // Collapse the epic via its summary; the indented children disappear.
      await epicGroup.locator("summary").click();
      await expect(epicGroup).toHaveJSProperty("open", false);
      await expect(
        epicGroup.locator(".pl-8").getByText("Child one", { exact: true }),
      ).toBeHidden();

      // Reload: the grouping toggle (enabled) and the collapsed epic are both
      // persisted, so the view returns grouped with the epic still collapsed.
      await helpers.navigateAndWait(page);
      await clickBeadsButton(page, timeouts);
      await expect(page.getByText(EPIC_TITLE).first()).toBeVisible({
        timeout: timeouts.appReady,
      });
      await expect(groupingToggle(page)).toHaveAttribute(
        "aria-pressed",
        "true",
      );
      const epicGroup2 = page.locator("details.beads-epic-group").first();
      await expect(epicGroup2).toHaveJSProperty("open", false);
      await expect(
        epicGroup2.locator(".pl-8").getByText("Child one", { exact: true }),
      ).toBeHidden();
    },
  );

  testWithCleanup(
    "the epic header shows a chevron that flips between expanded and collapsed",
    async ({ page, timeouts }) => {
      // The native <details> disclosure marker is hidden via .beads-epic-summary,
      // so the epic header renders an explicit chevron: ChevronDown when open
      // (path d="M19 9l-7 7-7-7"), ChevronRight when collapsed (d="M9 5l7 7-7 7").
      await clickBeadsButton(page, timeouts);
      await expect(page.getByText(EPIC_TITLE).first()).toBeVisible({
        timeout: timeouts.appReady,
      });

      const epicGroup = page.locator("details.beads-epic-group").first();
      await expect(epicGroup).toHaveJSProperty("open", true);

      // Expanded by default → the summary chevron is the "down" glyph.
      const chevronPath = epicGroup.locator(
        'summary [data-testid="beads-epic-chevron"] path',
      );
      await expect(chevronPath).toBeVisible();
      await expect(chevronPath).toHaveAttribute("d", "M19 9l-7 7-7-7");

      // Collapsing the epic flips the chevron to the "right" glyph.
      await epicGroup.locator("summary").click();
      await expect(epicGroup).toHaveJSProperty("open", false);
      await expect(chevronPath).toHaveAttribute("d", "M9 5l7 7-7 7");
    },
  );

  testWithCleanup(
    "clicking the chevron toggles the epic without opening the detail panel",
    async ({ page, timeouts }) => {
      // The chevron is its own button that stops propagation: it must toggle
      // collapse/expand only, never select the epic (which would open the
      // detail panel) and never let the native <summary> double-toggle.
      await clickBeadsButton(page, timeouts);
      await expect(page.getByText(EPIC_TITLE).first()).toBeVisible({
        timeout: timeouts.appReady,
      });

      const epicGroup = page.locator("details.beads-epic-group").first();
      await expect(epicGroup).toHaveJSProperty("open", true);
      const panel = page.locator(DETAIL_PANEL);
      const chevron = epicGroup.locator(
        'summary [data-testid="beads-epic-chevron"]',
      );

      // Clicking the chevron collapses the epic and leaves the panel closed.
      await chevron.click();
      await expect(epicGroup).toHaveJSProperty("open", false);
      await expect(panel).toBeHidden();

      // Clicking it again re-expands, still without opening the panel.
      await chevron.click();
      await expect(epicGroup).toHaveJSProperty("open", true);
      await expect(panel).toBeHidden();

      // The rest of the epic header still selects the epic (opens the panel).
      await epicGroup
        .locator("summary")
        .getByText(EPIC_TITLE)
        .first()
        .click();
      await expect(panel).toBeVisible({ timeout: timeouts.shortAction });
    },
  );

  testWithCleanup(
    "the epic '+' button opens the create panel with a read-only, pre-filled Parent field",
    async ({ page, timeouts }) => {
      await clickBeadsButton(page, timeouts);
      await expect(page.getByText(EPIC_TITLE).first()).toBeVisible({
        timeout: timeouts.appReady,
      });

      // The grouped epic header carries a "+" (add-child) button scoped to its
      // summary. Clicking it opens the New Issue panel pre-seeded with the epic
      // as the new issue's parent.
      const epicGroup = page.locator("details.beads-epic-group").first();
      await epicGroup
        .locator('summary [data-testid="beads-issue-add-child"]')
        .click();

      const panel = page.locator(NEW_ISSUE_PANEL);
      await expect(panel).toBeVisible({ timeout: timeouts.shortAction });

      // The Parent field is pre-filled with the epic id and is read-only, so the
      // new issue's parent relationship is fixed to the epic it was opened from.
      const parentField = panel.locator('[data-testid="beads-create-parent"]');
      await expect(parentField).toBeVisible();
      await expect(parentField).toHaveValue("mitto-epic");
      await expect(parentField).toHaveJSProperty("readOnly", true);
    },
  );
});

/**
 * Beads view — a CLOSED epic with OPEN children stays visible (grouped).
 *
 * With the default status filter (closed hidden; open + in-progress shown), a
 * closed epic must still appear as a real group header whenever at least one of
 * its children survives the filter — otherwise its open children would be
 * stranded with no parent context. The grouped path renders the epic header
 * from the full issue list (issueById), not the filtered set, so the closed
 * epic remains a labelled header row (with its chevron) rather than collapsing
 * to a "not in current filter" ghost placeholder.
 */
const CLOSED_EPIC_TITLE = "Closed parent epic";
const CLOSED_EPIC_ISSUES = [
  {
    id: "mitto-cep",
    title: CLOSED_EPIC_TITLE,
    description: "A closed epic that still has open children.",
    status: "closed",
    priority: 1,
    issue_type: "epic",
    created_at: "2026-06-01T10:00:00Z",
    updated_at: "2026-06-01T10:00:00Z",
  },
  {
    id: "mitto-oc1",
    title: "Open child of closed epic",
    description: "",
    status: "open",
    priority: 2,
    issue_type: "task",
    parent: "mitto-cep",
    created_at: "2026-06-01T10:00:00Z",
    updated_at: "2026-06-01T10:00:00Z",
  },
];

testWithCleanup.describe("Beads view - closed epic with open children", () => {
  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    await page.route("**/api/beads/list**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(CLOSED_EPIC_ISSUES),
      });
    });

    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: { name: `Beads Seed ${Date.now()}`, working_dir: WORKSPACE_ALPHA },
    });
    expect(createResp.ok()).toBeTruthy();

    await helpers.navigateAndWait(page);
  });

  testWithCleanup(
    "a closed epic stays visible as a header (with chevron) while its open child shows, with closed issues hidden",
    async ({ page, timeouts }) => {
      await clickBeadsButton(page, timeouts);
      await expect(
        page.getByText("Open child of closed epic").first(),
      ).toBeVisible({ timeout: timeouts.appReady });

      // The default status filter hides closed issues.
      const closedToggle = page.locator(
        'button[aria-label="Show closed issues"]',
      );
      await expect(closedToggle).toHaveAttribute("aria-pressed", "false");

      // Yet the closed epic remains a real group header (rendered from the full
      // issue list), not a "not in current filter" ghost placeholder.
      const epicGroup = page.locator("details.beads-epic-group").first();
      await expect(epicGroup).toBeVisible({ timeout: timeouts.shortAction });
      const summary = epicGroup.locator("summary");
      await expect(summary).toContainText(CLOSED_EPIC_TITLE);
      await expect(summary).toContainText("mitto-cep");
      await expect(
        summary.getByText("Epic (not in current filter)"),
      ).toHaveCount(0);

      // The header carries the collapse chevron.
      await expect(
        summary.locator('[data-testid="beads-epic-chevron"]'),
      ).toBeVisible();

      // The open child is shown indented beneath the closed epic.
      await expect(
        epicGroup
          .locator(".pl-8")
          .getByText("Open child of closed epic", { exact: true }),
      ).toBeVisible();
    },
  );
});

/**
 * Beads view — fast-open + return-to-origin from a conversation's linked issue.
 *
 * Covers the navigation flow for the properties panel's "Linked beads issue"
 * link:
 *   1. Fast-open: the issue's detail panel appears immediately from a single
 *      `/api/beads/show` fetch, without waiting for the full `/api/beads/list`
 *      to load. The list is deliberately gated (held pending) to prove the
 *      panel opens before any list row renders.
 *   2. Return-to-origin: closing that detail panel returns the user to the
 *      originating conversation with its properties panel re-opened — instead
 *      of leaving them stranded on the beads list.
 *
 * The seed conversation is created with beads_issue=mitto-bbb so the
 * SessionPanel renders the linked-issue link. The external `bd` binary is never
 * invoked: both list and show are mocked.
 */

// Both the SessionPanel and the beads detail panel carry the shared
// `properties-panel` class (added by Drawer). Scope each by a heading unique to
// it so assertions stay unambiguous even during the brief cross-view
// transition: the SessionPanel header is "Conversation"; the detail panel
// header is the issue title ("Short issue").
const CONV_PANEL = 'div.properties-panel:has(h2:has-text("Conversation"))';
const ISSUE_PANEL = 'div.properties-panel:has(h2:has-text("Short issue"))';

// The list-vs-show race test ("opens the linked issue even when the list loads
// before the show fetch") was removed: BeadsIssueView is now a standalone
// component that only calls /api/beads/show — there is no full-list fetch to
// race against. The list is never mounted in this flow.
testWithCleanup.describe("Beads view - return to conversation", () => {
  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    // Mock the single-issue show endpoint so the detail panel resolves
    // immediately. Returns mitto-bbb.
    await page.route("**/api/beads/show**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(MOCK_ISSUES[1]), // mitto-bbb "Short issue"
      });
    });

    // Ensure the project-alpha workspace exists so its folder group renders.
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });

    // Create a conversation linked to mitto-bbb so the properties panel shows
    // the "Linked beads issue" link.
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: {
        name: `Linked ${Date.now()}`,
        working_dir: WORKSPACE_ALPHA,
        beads_issue: "mitto-bbb",
      },
    });
    expect(createResp.ok()).toBeTruthy();
    const linkedSessionId = (await createResp.json()).session_id;
    expect(linkedSessionId).toBeTruthy();

    // Make the app open directly into the linked conversation on load.
    await page.addInitScript((sid) => {
      localStorage.setItem("mitto_last_session_id", sid);
    }, linkedSessionId);

    await helpers.navigateAndWait(page);
  });

  testWithCleanup(
    "opens the linked issue standalone (no list), then returns to the conversation on close",
    async ({ page, timeouts }) => {
      // Wait for the linked conversation to be active (chat input enabled).
      await expect(page.locator("textarea")).toBeEnabled({
        timeout: timeouts.appReady,
      });

      // Open the conversation properties side panel; confirm the linked-issue
      // link is present.
      await page.locator('button[aria-label="Session details"]').click();
      const convPanel = page.locator(CONV_PANEL);
      await expect(convPanel).toBeVisible({ timeout: timeouts.shortAction });
      await expect(page.getByTitle("Open beads issue mitto-bbb")).toBeVisible();

      // Follow the linked-issue link → opens the standalone BeadsIssueView.
      await page.getByTitle("Open beads issue mitto-bbb").click();

      // The issue detail panel opens from the show fetch.
      const issuePanel = page.locator(ISSUE_PANEL);
      await expect(issuePanel).toBeVisible({ timeout: timeouts.shortAction });
      await expect(issuePanel.getByText("mitto-bbb")).toBeVisible();

      // The list IS NOT rendered behind the panel — standalone view has no list.
      await expect(page.locator("div.beads-table-scroll")).toHaveCount(0);
      await expect(page.getByText(LONG_TITLE)).toHaveCount(0);

      // The standalone viewer opens expanded (fullscreen) but exposes a toggle
      // so it can be collapsed to the docked strip. The dock-mode Drawer drives
      // its width via the --dock-w CSS var on the .drawer-dock root: 100% when
      // fullscreen, 40rem when collapsed. getByTitle uses exact:true so
      // "Fullscreen" never substring-matches "Exit fullscreen".
      const drawerRoot = page.locator(
        'div.drawer-dock:has(h2:has-text("Short issue"))',
      );
      await expect(drawerRoot).toHaveAttribute("style", /--dock-w:\s*100%/);
      const collapseBtn = issuePanel.getByTitle("Exit fullscreen", {
        exact: true,
      });
      await expect(collapseBtn).toBeVisible();

      // Collapse: the panel shrinks to the 40rem docked strip and the toggle
      // flips to the expand state ("Fullscreen").
      await collapseBtn.click();
      await expect(drawerRoot).toHaveAttribute("style", /--dock-w:\s*40rem/);
      const expandBtn = issuePanel.getByTitle("Fullscreen", { exact: true });
      await expect(expandBtn).toBeVisible();

      // Expand again: back to fullscreen, toggle returns to "Exit fullscreen".
      await expandBtn.click();
      await expect(drawerRoot).toHaveAttribute("style", /--dock-w:\s*100%/);
      await expect(
        issuePanel.getByTitle("Exit fullscreen", { exact: true }),
      ).toBeVisible();

      // Close the detail panel → returns to the originating conversation with
      // its properties panel re-opened (not left on the beads list).
      await issuePanel.getByTitle("Close", { exact: true }).click();

      // Back in the conversation: the conversation properties panel (with the
      // linked-issue link) is shown again and the beads table remains absent.
      await expect(convPanel).toBeVisible({ timeout: timeouts.shortAction });
      await expect(page.getByTitle("Open beads issue mitto-bbb")).toBeVisible();
      await expect(page.locator("div.beads-table-scroll")).toHaveCount(0);
    },
  );
});

/**
 * Beads view — context-menu submenu positioning (desktop).
 *
 * Regression guard for the submenu overlap bug: when the per-row "..." action
 * button is near the RIGHT edge of the screen, the context menu is clamped to
 * the right edge, so a hover-submenu (e.g. "Depends On", which lists every other
 * issue) must flip to the LEFT instead of opening off-screen on top of the
 * parent menu. The submenu width is measured at render time (useLayoutEffect in
 * ContextMenu.js), so wide entries (long "mitto-xxx · title" labels) can't defeat
 * the flip math. The issues carry deliberately long titles to make the submenu
 * wide — the exact condition that broke the old hard-coded 180px estimate.
 *
 * This can only be exercised in a real browser: node --check / Jest don't run
 * the htm render path or the layout engine.
 */
const SUBMENU_LONG = (n: number) =>
  `Issue ${n} with an intentionally long title used to widen the dependency submenu well beyond the old estimate`;
const SUBMENU_ISSUES = [0, 1, 2].map((n) => ({
  id: `mitto-s${n}`,
  title: SUBMENU_LONG(n),
  description: "",
  status: "open",
  priority: 2,
  issue_type: "task",
  created_at: "2026-06-01T10:00:00Z",
  updated_at: "2026-06-01T10:00:00Z",
}));

// Both the parent menu and any open submenu render as a fixed daisyUI menu.
const CTX_MENU = ".menu.fixed.z-50.shadow-xl";

// The new-issue create panel is opened by clicking the "+" button in the beads toolbar.
const NEW_ISSUE_PANEL = 'div.properties-panel:has(h2:has-text("New Issue"))';

testWithCleanup.describe("Beads view - submenu positioning", () => {
  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    await page.route("**/api/beads/list**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(SUBMENU_ISSUES),
      });
    });

    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: { name: `Beads Seed ${Date.now()}`, working_dir: WORKSPACE_ALPHA },
    });
    expect(createResp.ok()).toBeTruthy();

    await helpers.navigateAndWait(page);
  });

  testWithCleanup(
    "the 'Depends On' submenu flips left and stays on-screen from the right-aligned menu button",
    async ({ page, timeouts }) => {
      await clickBeadsButton(page, timeouts);
      await expect(page.getByText(SUBMENU_LONG(0)).first()).toBeVisible({
        timeout: timeouts.appReady,
      });

      // Open the context menu from the row's right-aligned "..." button (this
      // anchors the menu at the right edge — the condition that broke flipping).
      await page.locator('[data-testid="beads-issue-menu"]').first().click();
      const parentMenu = page.locator(CTX_MENU).first();
      await expect(parentMenu).toBeVisible({ timeout: timeouts.shortAction });

      // Open the "Depends On" submenu. Use dispatchEvent(mouseenter) instead of
      // hover() so the act of opening isn't blocked by the (buggy) submenu
      // intercepting pointer events over the parent item.
      const dependsItem = page
        .locator(`${CTX_MENU} button`)
        .filter({ hasText: "Depends On" })
        .first();
      await dependsItem.dispatchEvent("mouseenter");

      // The submenu is the second fixed menu now on screen.
      const submenu = page.locator(CTX_MENU).nth(1);
      await expect(submenu).toBeVisible({ timeout: timeouts.shortAction });

      const parentBox = await parentMenu.boundingBox();
      const subBox = await submenu.boundingBox();
      expect(parentBox).not.toBeNull();
      expect(subBox).not.toBeNull();
      const vw = page.viewportSize()!.width;

      // 1) The submenu stays fully within the viewport (allow 1px rounding).
      expect(subBox!.x).toBeGreaterThanOrEqual(-1);
      expect(subBox!.x + subBox!.width).toBeLessThanOrEqual(vw + 1);

      // 2) It does not cover the parent menu: the horizontal overlap is at most a
      //    small connecting bridge (the old bug overlapped by the full ~150px
      //    parent-menu width).
      const overlap =
        Math.min(subBox!.x + subBox!.width, parentBox!.x + parentBox!.width) -
        Math.max(subBox!.x, parentBox!.x);
      expect(overlap).toBeLessThanOrEqual(24);

      // 3) Since the parent menu sits at the right edge, the submenu opened to its
      //    left (its left edge is left of the parent menu's left edge).
      expect(subBox!.x).toBeLessThan(parentBox!.x);
    },
  );
});

/**
 * Beads view — create form with dependencies, assignee, and notes.
 *
 * Opens the "New Issue" panel via the "+" toolbar button, fills in a
 * description, adds a dependency, sets an assignee and notes, then clicks
 * Save. The POST /api/beads/create request is intercepted and the body is
 * asserted to contain the `dependencies`, `assignee`, and `notes` fields.
 */
testWithCleanup.describe("Beads view - create form fields", () => {
  testWithCleanup.beforeEach(async ({ page, request, apiUrl, helpers }) => {
    await page.route("**/api/beads/list**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(MOCK_ISSUES),
      });
    });

    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: AGENT_NAME, working_dir: WORKSPACE_ALPHA },
    });
    const createResp = await request.post(apiUrl("/api/sessions"), {
      data: { name: `Beads Seed ${Date.now()}`, working_dir: WORKSPACE_ALPHA },
    });
    expect(createResp.ok()).toBeTruthy();

    await helpers.navigateAndWait(page);
  });

  testWithCleanup(
    "create form sends dependencies, assignee, and notes in POST body",
    async ({ page, timeouts }) => {
      await openBeads(page, timeouts);

      // Capture the create request body before clicking Save.
      let capturedBody: Record<string, unknown> | null = null;
      await page.route("**/api/beads/create", async (route) => {
        capturedBody = JSON.parse(route.request().postData() ?? "{}");
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ id: "mitto-new", title: "Test" }),
        });
      });

      // Open the create panel via the "+" button in the beads toolbar.
      await page.locator('button[title="New issue"]').first().click();
      const panel = page.locator(NEW_ISSUE_PANEL);
      await expect(panel).toBeVisible({ timeout: timeouts.shortAction });

      // Fill in description (required).
      const descEditor = panel.locator(".cm-editor").first();
      await descEditor.click();
      await page.keyboard.type("Test description for create form");

      // Add a dependency: type an issue id in the dep input and click "+".
      const depInput = panel.locator('input[list="beads-create-dep-options"]');
      await depInput.fill("mitto-aaa");
      await panel.locator('button[title="Add dependency"]').click();

      // Fill assignee.
      await panel.locator("#new-issue-assignee").fill("alice");

      // Fill notes.
      await panel.locator("#new-issue-notes").fill("some notes");

      // Click Save.
      await panel.locator('button:has-text("Save")').click();

      // Wait for the intercepted request to be captured.
      await expect
        .poll(() => capturedBody, { timeout: timeouts.shortAction })
        .not.toBeNull();

      expect(capturedBody!.assignee).toBe("alice");
      expect(capturedBody!.notes).toBe("some notes");
      expect(Array.isArray(capturedBody!.dependencies)).toBeTruthy();
      const deps = capturedBody!.dependencies as Array<{ id: string; type: string }>;
      expect(deps.length).toBeGreaterThan(0);
      expect(deps[0].id).toBe("mitto-aaa");
      expect(deps[0].type).toBe("blocks"); // default type
    },
  );
});
