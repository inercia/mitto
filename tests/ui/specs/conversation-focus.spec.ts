import { test, expect } from "../fixtures/test-fixtures";
import path from "path";
import { fileURLToPath } from "url";

const projectRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../../..",
);

test.describe("Conversation composer focus", () => {
  test("expanding another project focuses its remembered conversation on desktop", async ({
    page,
    request,
    apiUrl,
    helpers,
  }) => {
    const sessions: string[] = [];
    let createdBeta = false;
    for (const project of ["project-alpha", "project-beta"]) {
      const workingDir = path.join(
        projectRoot,
        "tests/fixtures/workspaces",
        project,
      );
      const workspaceResponse = await request.post(apiUrl("/api/workspaces"), {
        data: { acp_server: "mock-acp", working_dir: workingDir },
      });
      if (project === "project-beta")
        createdBeta = workspaceResponse.status() === 201;
      const response = await request.post(apiUrl("/api/sessions"), {
        data: {
          name: `Focus ${project} ${Date.now()}`,
          working_dir: workingDir,
        },
      });
      expect(response.ok()).toBeTruthy();
      sessions.push((await response.json()).session_id);
    }
    try {
      await helpers.navigateAndWait(page);

      const folder = (project: string) =>
        page
          .locator('summary[data-has-context-menu="true"]')
          .filter({ hasText: project })
          .first();
      const alpha = folder("project-alpha");
      const beta = folder("project-beta");
      const expand = async (header: typeof alpha) => {
        const details = header.locator("xpath=..");
        if (!(await details.evaluate((el: HTMLDetailsElement) => el.open))) {
          await header.click();
        }
      };
      await expand(alpha);
      await alpha
        .locator("xpath=..")
        .locator(`[data-session-id="${sessions[0]}"]`)
        .click();
      await expand(beta);
      await beta
        .locator("xpath=..")
        .locator(`[data-session-id="${sessions[1]}"]`)
        .click();

      await alpha.click(); // Collapse without selecting.
      await alpha.click(); // Expand and auto-select the remembered alpha conversation.
      await expect
        .poll(() =>
          page.evaluate(() => localStorage.getItem("mitto_last_session_id")),
        )
        .toBe(sessions[0]);
      await expect(page.locator(".chat-input-textarea")).toBeFocused();
    } finally {
      for (const id of sessions) {
        await request.delete(apiUrl(`/api/sessions/${id}`));
      }
      if (createdBeta) {
        const response = await request.get(apiUrl("/api/workspaces"));
        const { workspaces } = await response.json();
        const beta = workspaces.find((workspace: { working_dir: string }) =>
          workspace.working_dir.endsWith(
            "/tests/fixtures/workspaces/project-beta",
          ),
        );
        if (beta) {
          await request.delete(apiUrl(`/api/workspaces?uuid=${beta.uuid}`));
        }
      }
    }
  });

  test("sidebar selection focuses the composer and keeps drafts per session", async ({
    page,
    helpers,
  }) => {
    await helpers.navigateAndEnsureSession(page);
    const first = await helpers.createFreshSession(page);
    const second = await helpers.createFreshSession(page);
    const composer = page.locator(".chat-input-textarea");

    await helpers.navigateToSession(page, first);
    await expect(composer).toBeFocused();
    await page.keyboard.type("First session draft");
    await expect(composer).toHaveValue("First session draft");

    await helpers.navigateToSession(page, second);
    await expect(composer).toBeFocused();
    await expect(composer).toHaveValue("");
    await page.keyboard.type("Second session draft");

    await helpers.navigateToSession(page, first);
    await expect(composer).toBeFocused();
    await expect(composer).toHaveValue("First session draft");
  });

  test("native swipe and keyboard navigation focus the composer", async ({
    page,
    helpers,
  }) => {
    await helpers.navigateAndEnsureSession(page);
    const first = await helpers.createFreshSession(page);
    await helpers.createFreshSession(page);
    const composer = page.locator(".chat-input-textarea");
    await helpers.navigateToSession(page, first);

    await page.keyboard.press("Meta+Control+ArrowUp");
    // The current folder may contain older test sessions; if first is already
    // at the top of its visible group, navigate down instead.
    if (
      (await page.evaluate(() =>
        localStorage.getItem("mitto_last_session_id"),
      )) === first
    ) {
      await page.keyboard.press("Meta+Control+ArrowDown");
    }
    await expect
      .poll(() =>
        page.evaluate(() => localStorage.getItem("mitto_last_session_id")),
      )
      .not.toBe(first);
    await expect(composer).toBeFocused();

    const beforeSwipe = await page.evaluate(() =>
      localStorage.getItem("mitto_last_session_id"),
    );
    await page.evaluate(() => (window as any).mittoNextConversation());
    await expect
      .poll(() =>
        page.evaluate(() => localStorage.getItem("mitto_last_session_id")),
      )
      .not.toBe(beforeSwipe);
    await expect(composer).toBeFocused();
  });

  test("does not open the mobile keyboard when switching", async ({
    page,
    helpers,
  }) => {
    await helpers.navigateAndEnsureSession(page);
    const first = await helpers.createFreshSession(page);
    await helpers.createFreshSession(page);
    await page.setViewportSize({ width: 390, height: 844 });
    await page
      .locator(".chat-input-textarea")
      .evaluate((element) => (element as HTMLElement).blur());
    await page.evaluate(
      (sessionId) => (window as any).mittoSwitchToSession(sessionId),
      first,
    );
    await expect
      .poll(() =>
        page.evaluate(() => localStorage.getItem("mitto_last_session_id")),
      )
      .toBe(first);
    await expect(page.locator(".chat-input-textarea")).not.toBeFocused();
  });

  test("does not take focus from an open dialog", async ({ page, helpers }) => {
    await helpers.navigateAndEnsureSession(page);
    const first = await helpers.createFreshSession(page);
    await helpers.createFreshSession(page);
    await page.locator('button[data-testid="settings-btn"]').first().click();
    const dialog = page.locator('[data-testid="settings-dialog"]');
    await expect(dialog).toBeVisible();

    await page.evaluate(
      (sessionId) => (window as any).mittoSwitchToSession(sessionId),
      first,
    );
    await expect
      .poll(() =>
        page.evaluate(() => localStorage.getItem("mitto_last_session_id")),
      )
      .toBe(first);
    await expect(page.locator(".chat-input-textarea")).not.toBeFocused();
    await expect(dialog).toBeVisible();
  });
});
