import { test, expect } from "../fixtures/test-fixtures";
import type { Locator, Page } from "@playwright/test";

/**
 * Model picker (unified searchable picker) E2E tests for Mitto Web UI.
 *
 * Covers the two .5 acceptance flows end to end against the mock ACP server
 * (tests/mocks/acp-server), which advertises a model config option with
 * exactly three options — Haiku 4.5, Sonnet 4.6 (initially selected), and
 * Opus 4.6 — and confirms every set_config_option with no rollback:
 *
 *   1. Toolbar flow: open the picker from the toolbar summary, verify the
 *      filter is focused, the rows are name-sorted with the current model
 *      highlighted, filter by name, and select with ArrowDown+Enter.
 *   2. Ctrl+M flow: with the textarea focused, Ctrl+M opens the picker with
 *      the filter focused; a selection confirms via Enter; a further Ctrl+M
 *      reopens and another Ctrl+M closes the picker and restores textarea
 *      focus. Cmd+M stays native (macOS Window > Minimize) and is never
 *      intercepted by the web layer.
 *
 * The mock's current model is server-global and persists across sessions,
 * so these tests assert label *deltas* (or read the initial label first)
 * and never hardcode "Sonnet 4.6".
 */

// The toolbar trigger: the model selector's <summary> (aria-label comes from
// the config option name "Model"; mode/effort summaries carry their own).
function modelTrigger(page: Page): Locator {
  return page.locator(
    '.chat-input-model-selector summary[aria-label="Model"]',
  );
}

function modelFilter(page: Page): Locator {
  return page.getByTestId("config-option-model-filter");
}

function modelRows(page: Page): Locator {
  return page
    .getByTestId("config-option-model-list")
    .locator("button.picker-row");
}

test.describe("Model Picker", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndEnsureSession(page);
  });

  test("toolbar click opens the picker focused and sorted; filter + ArrowDown + Enter updates the toolbar label", async ({
    page,
  }) => {
    const trigger = modelTrigger(page);
    const filter = modelFilter(page);
    const rows = modelRows(page);

    // The trigger shows the current model — read it, never hardcode it.
    const initialLabel = (await trigger.innerText()).trim();
    expect(initialLabel.length).toBeGreaterThan(0);

    // Open from the toolbar: the open effect focuses the filter input.
    await trigger.click();
    await expect(filter).toBeVisible();
    await expect(filter).toBeFocused();

    // Rows are name-sorted; the keyboard highlight starts on the current
    // model (the .menu-active row), whatever the server-global current is.
    await expect(rows).toHaveText(["Haiku 4.5", "Opus 4.6", "Sonnet 4.6"]);
    const highlighted = page
      .getByTestId("config-option-model-list")
      .locator("button.picker-row--highlighted");
    const active = page
      .getByTestId("config-option-model-list")
      .locator("button.menu-active");
    await expect(highlighted).toHaveCount(1);
    await expect(active).toHaveCount(1);
    expect((await highlighted.innerText()).trim()).toBe(
      (await active.innerText()).trim(),
    );

    // Filtering narrows to a single row, which becomes the highlight.
    await filter.fill("haiku");
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toHaveText("Haiku 4.5");
    await expect(rows.first()).toHaveClass(/picker-row--highlighted/);

    // ArrowDown clamps on the single row (no wrap, no deselect).
    await page.keyboard.press("ArrowDown");
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toHaveText("Haiku 4.5");
    await expect(rows.first()).toHaveClass(/picker-row--highlighted/);

    // Enter selects the highlighted model: the menu closes and the toolbar
    // label optimistically updates (the mock always confirms, no rollback).
    await page.keyboard.press("Enter");
    await expect(filter).toBeHidden();
    await expect(trigger).toContainText("Haiku 4.5");
  });

  test("Ctrl+M opens the picker with the filter focused, selects, and toggles closed with textarea focus restored", async ({
    page,
    selectors,
  }) => {
    const trigger = modelTrigger(page);
    const filter = modelFilter(page);
    const rows = modelRows(page);
    const textarea = page.locator(selectors.chatInput);

    // Start from the textarea, like a user typing a message.
    await textarea.click();

    // Ctrl+M (literal Ctrl, never Cmd) opens the picker and focuses the
    // filter via the select's open effect.
    await page.keyboard.press("Control+m");
    await expect(filter).toBeVisible();
    await expect(filter).toBeFocused();

    // Filter and select with Enter; the menu closes and the label updates.
    await filter.fill("opus");
    await expect(rows).toHaveCount(1);
    await page.keyboard.press("Enter");
    await expect(filter).toBeHidden();
    await expect(trigger).toContainText("Opus 4.6");

    // Ctrl+M reopens the picker (the shortcut bubbles to the global handler
    // even from the menu's own filter input).
    await page.keyboard.press("Control+m");
    await expect(filter).toBeVisible();
    await expect(filter).toBeFocused();

    // A further Ctrl+M closes it and returns focus to the textarea.
    await page.keyboard.press("Control+m");
    await expect(filter).toBeHidden();
    await expect(textarea).toBeFocused();
  });
});
