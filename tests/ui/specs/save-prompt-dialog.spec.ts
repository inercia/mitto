import { test, expect } from "../fixtures/test-fixtures";
import { selectors, timeouts as timeoutConsts } from "../utils/selectors";
import type { Page, APIRequestContext } from "@playwright/test";
import path from "path";
import fs from "fs/promises";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Regression lock for SavePromptDialog → backend YAML loader round-trip
 * (mitto-rxr).
 *
 * Verifies that a prompt body with YAML-hostile edge cases (backticks, quotes,
 * colons, backslashes, unicode, blank lines, trailing whitespace) written by
 * the frontend's buildFileContent() is parseable by internal/prompts.ParsePromptFile
 * and round-trips byte-for-byte through GET /api/workspace-prompts.
 *
 * The bug this locks: the two halves of SavePromptDialog once drifted, writing
 * markdown-with-frontmatter into a .prompt.yaml file, which the backend then
 * refused to parse. Nothing verified the on-disk output; the fix landed without
 * a test.
 *
 * The trigger button is macOS-native-only (gated on isNativeApp()), so
 * window.mittoPickFolder must be stubbed via addInitScript before navigation.
 * Skips in Docker/CI because /api/save-file-to-path is localhost-only and the
 * test needs host-local filesystem access to assert the written YAML on disk.
 */

const projectRoot = path.resolve(__dirname, "../../..");
const WORKSPACE = path.join(projectRoot, "tests/fixtures/workspaces/empty-project");
const PROMPTS_DIR = path.join(WORKSPACE, ".mitto/prompts");
const PROMPT_NAME = "Round-Trip Edge Cases";
const PROMPT_FILENAME = "round-trip-edge-cases.prompt.yaml";
const PROMPT_DESCRIPTION = 'Edge-case body: "quotes", `backticks`, colons: yes.';

// Every YAML-hostile construct the round-trip must survive intact:
//   - backticks, single + double quotes, colons (YAML-significant), backslashes
//   - unicode (arrow, em-dash)
//   - blank lines mid-body
//   - trailing whitespace + newlines (buildFileContent strips these — the
//     round-trip is byte-for-byte against the *stripped* input).
const PROMPT_BODY_RAW = [
  "Line with `backticks` and \"double\" and 'single' quotes.",
  "Colon in text: value: another.",
  "Backslash: C:\\path\\to\\file and escape \\n literal.",
  "Unicode → em-dash — and arrow.",
  "",
  "After blank line.",
  "  Indented line with trailing spaces.   ",
  "",
  "",
].join("\n") + "   \n\n";

const PROMPT_BODY_EXPECTED = PROMPT_BODY_RAW.replace(/\s+$/, "");

/**
 * Boot the app directly into a conversation view for the empty-project
 * workspace, bypassing the sidebar helper (its `new-conversation-btn` selector
 * is stale — see upload-zero-byte.spec.ts). Also stubs window.mittoPickFolder
 * so isNativeApp() returns true and the Save Prompt trigger renders.
 */
async function bootWithSession(
  page: Page,
  request: APIRequestContext,
  apiUrl: (p: string) => string,
): Promise<string> {
  const resp = await request.post(apiUrl("/api/sessions"), {
    data: { acp_server: "mock-acp", working_dir: WORKSPACE },
  });
  expect(resp.ok(), `POST /api/sessions failed: ${resp.status()}`).toBe(true);
  const id: string = (await resp.json()).session_id;

  await page.addInitScript((sid) => {
    localStorage.setItem("mitto_last_session_id", sid);
    localStorage.removeItem("mitto_conversation_filter_tab");
    // Stub isNativeApp() gate: SavePromptDialog trigger is macOS-native-only.
    Object.defineProperty(window, "mittoPickFolder", {
      configurable: true,
      get: () => () => {},
      set: () => {},
    });
  }, id);

  await page.goto("/");

  // Force the conversation view: cold-start may land on the Dashboard even
  // when a valid last-session id is present (see upload-zero-byte.spec.ts).
  const chatInput = page.locator(selectors.chatInput);
  try {
    await expect(chatInput).toBeVisible({ timeout: 3000 });
  } catch {
    await page.locator(`[data-session-id="${id}"]`).click({ timeout: 5000 });
  }
  await expect(chatInput).toBeEnabled({ timeout: timeoutConsts.agentResponse });
  return id;
}

test.describe("SavePromptDialog round-trip (mitto-rxr)", () => {
  test.beforeEach(() => {
    test.skip(
      !!process.env.MITTO_EXTERNAL_SERVER,
      "Requires host-local filesystem access (save-file-to-path is localhost-only)",
    );
  });

  test.beforeAll(async ({ request, apiUrl }) => {
    await request.post(apiUrl("/api/workspaces"), {
      data: { acp_server: "mock-acp", working_dir: WORKSPACE },
    });
  });

  test.afterEach(async ({ request, apiUrl }) => {
    // Delete via API first (idempotent — 404 is fine if the test failed early),
    // then rm the directory to leave the fixture as we found it (.gitkeep only).
    await request
      .delete(
        apiUrl(
          `/api/workspace-prompts?working_dir=${encodeURIComponent(WORKSPACE)}&name=${encodeURIComponent(PROMPT_NAME)}`,
        ),
      )
      .catch(() => {});
    await fs.rm(PROMPTS_DIR, { recursive: true, force: true });
  });

  test("saves edge-case body as valid YAML and round-trips through the backend loader", async ({
    page,
    request,
    apiUrl,
    timeouts,
  }) => {
    await bootWithSession(page, request, apiUrl);

    // Fill the composition textarea with the edge-case body. The Save Prompt
    // trigger is disabled while text is empty, so this must precede the click.
    const textarea = page.locator("textarea").first();
    await expect(textarea).toBeEnabled({ timeout: timeouts.appReady });
    await textarea.fill(PROMPT_BODY_RAW);

    // Open the dialog. The trigger button has no data-testid — target by
    // aria-label (see ChatInput.js:3061).
    const saveBtn = page.locator('button[aria-label="Save prompt as file"]');
    await expect(saveBtn).toBeVisible({ timeout: timeouts.shortAction });
    await expect(saveBtn).toBeEnabled({ timeout: timeouts.shortAction });
    await saveBtn.click();

    const dialog = page.locator('[data-testid="save-prompt-dialog"]');
    await expect(dialog).toBeVisible({ timeout: timeouts.shortAction });

    // Fill Name / Description; override Save-to directory to point into the
    // fixture workspace's .mitto/prompts/ (default resolves to workingDir which
    // for the mount-active session may not equal the fixture path).
    await page.locator('[data-testid="save-prompt-name-input"]').fill(PROMPT_NAME);
    await page
      .locator('[data-testid="save-prompt-description-input"]')
      .fill(PROMPT_DESCRIPTION);
    const dirInput = page.locator('[data-testid="save-prompt-directory-input"]');
    await dirInput.fill(PROMPTS_DIR);

    await page.locator('[data-testid="save-prompt-save-btn"]').click();
    await expect(dialog).toBeHidden({ timeout: timeouts.appReady });

    // 1) On-disk assertion: file exists with .prompt.yaml extension and
    // valid YAML shape (name:, description:, prompt: |- block scalar).
    const filePath = path.join(PROMPTS_DIR, PROMPT_FILENAME);
    const onDisk = await fs.readFile(filePath, "utf-8");
    expect(onDisk).toMatch(/^name:\s+"Round-Trip Edge Cases"/m);
    expect(onDisk).toMatch(/^description:\s+"/m);
    expect(onDisk).toMatch(/^prompt:\s+\|-\s*$/m);

    // 2) Backend loader round-trip via GET /api/workspace-prompts. If the file
    // is not parseable by internal/prompts.ParsePromptFile, it will not appear
    // in the response.
    const resp = await request.get(
      apiUrl(`/api/workspace-prompts?working_dir=${encodeURIComponent(WORKSPACE)}`),
    );
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    const prompts: Array<{ name: string; description?: string; prompt?: string }> =
      body.prompts || [];
    const found = prompts.find((p) => p.name === PROMPT_NAME);
    expect(found, `prompt "${PROMPT_NAME}" must round-trip through the backend loader`).toBeDefined();
    expect(found!.description).toBe(PROMPT_DESCRIPTION);
    expect(found!.prompt).toBe(PROMPT_BODY_EXPECTED);
  });
});
