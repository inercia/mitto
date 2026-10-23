import { test, expect } from "../fixtures/test-fixtures";
import type { Page } from "@playwright/test";
import path from "node:path";

// All three identities run the test mock. The global default deliberately
// differs from both agents in alpha, so an omitted IsDefault cannot accidentally
// pass by selecting the global agent. Restore config and remove only our sessions.
const alpha = path.resolve("tests/fixtures/workspaces/project-alpha");
const beta = path.resolve("tests/fixtures/workspaces/project-beta");
let originalConfig: any;
let fixtureConfig: any;
let originalSessions: Set<string>;
let originId: string;

async function saveConfig(request, apiUrl, config) {
  // Never round-trip sanitized web/auth values.
  const body: Record<string, unknown> = {};
  for (const key of [
    "acp_servers", "workspaces", "prompts", "ui", "models",
    "conversations", "session", "permissions",
  ]) {
    if (config[key] !== undefined) body[key] = config[key];
  }
  // GET includes expanded file/builtin prompts; those remain on disk and must
  // not be duplicated into settings (or exceed the config request body limit).
  body.prompts = (config.prompts || []).filter((prompt) => prompt.source !== "file");
  const response = await request.post(apiUrl("/api/config"), { data: body });
  expect(response.ok(), await response.text()).toBeTruthy();
}

async function openOrigin(page: Page) {
  await page.addInitScript((id) => {
    localStorage.setItem("mitto_last_session_id", id);
    localStorage.setItem("mitto_sidebar_collapsed", "false");
  }, originId);
  await page.goto("/mitto/");
  await expect(page.locator(".chat-input-textarea")).toBeEnabled();
  await expect.poll(() => selectedSession(page)).toBe(originId);
  await expect.poll(() => page.evaluate(
    () => typeof (window as any).mittoNewConversationHere,
  )).toBe("function");
}

const selectedSession = (page: Page) => page.evaluate(
  () => localStorage.getItem("mitto_last_session_id"),
);
const invokeNative = (page: Page) => page.evaluate(
  () => { void (window as any).mittoNewConversationHere(); },
);
async function changedSession(page: Page) {
  await expect.poll(() => selectedSession(page)).toBeTruthy();
  await expect.poll(() => selectedSession(page)).not.toBe(originId);
  return (await selectedSession(page))!;
}
async function settleUI(page: Page) {
  await page.evaluate(() => new Promise<void>((resolve) => {
    requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
  }));
}

test.beforeEach(async ({ request, apiUrl }) => {
  originalConfig = null;
  originalConfig = await (await request.get(apiUrl("/api/config"))).json();
  originalSessions = new Set((await (
    await request.get(apiUrl("/api/sessions"))
  ).json()).map((session) => session.session_id));
  const mock = originalConfig.acp_servers.find((s) => s.name === "mock-acp");
  expect(mock?.command).toBeTruthy();
  fixtureConfig = {
    ...originalConfig,
    acp_servers: [
      { ...mock, name: "cn9-global" },
      mock,
      { ...mock, name: "cn9-default" },
    ],
    workspaces: [
      { working_dir: beta, acp_server: "cn9-global" },
      { working_dir: alpha, acp_server: "mock-acp" },
      { working_dir: alpha, acp_server: "cn9-default", is_default: true },
    ],
  };
  await saveConfig(request, apiUrl, fixtureConfig);
  const response = await request.post(apiUrl("/api/sessions"), {
    data: { working_dir: alpha, acp_server: "mock-acp", name: "cn9 origin" },
  });
  expect(response.ok()).toBeTruthy();
  originId = (await response.json()).session_id;
});

test.afterEach(async ({ page, request, apiUrl }) => {
  // Unmount before restoring settings so no late retry can mutate the fixture.
  await page.goto("about:blank");
  const sessions = await (await request.get(apiUrl("/api/sessions"))).json();
  for (const session of sessions) {
    if (originalSessions && !originalSessions.has(session.session_id)) {
      const response = await request.delete(
        apiUrl(`/api/sessions/${session.session_id}`),
      );
      expect(response.ok()).toBeTruthy();
    }
  }
  if (originalConfig) await saveConfig(request, apiUrl, originalConfig);
});

for (const modifier of ["Meta", "Control"]) {
  test(`${modifier}+Alt+N resolves the folder default and focuses without a chooser`, async ({
    page, request, apiUrl,
  }) => {
    await openOrigin(page);
    const posted = page.waitForRequest((r) =>
      r.method() === "POST" && r.url().endsWith("/api/sessions"));
    await page.keyboard.press(`${modifier}+Alt+n`);
    const body = (await posted).postDataJSON();
    expect(body.working_dir).toBe(alpha);
    expect(body.acp_server).toBe("");
    expect(body).not.toHaveProperty("onCreated");
    const id = await changedSession(page);
    const session = await (await request.get(apiUrl(`/api/sessions/${id}`))).json();
    expect(session.acp_server).toBe("cn9-default");
    await expect(page.locator(".chat-input-textarea")).toBeFocused();
    await expect(page.getByRole("dialog", { name: "Select Workspace" })).toBeHidden();
  });
}

test("physical KeyN survives Option-transformed key and no default uses first exact-folder agent", async ({
  page, request, apiUrl,
}) => {
  fixtureConfig.workspaces[2].is_default = false;
  await saveConfig(request, apiUrl, fixtureConfig);
  await openOrigin(page);
  await page.evaluate(() => window.dispatchEvent(new KeyboardEvent("keydown", {
    code: "KeyN", key: "ñ", metaKey: true, altKey: true,
    bubbles: true, cancelable: true,
  })));
  const id = await changedSession(page);
  const session = await (await request.get(apiUrl(`/api/sessions/${id}`))).json();
  expect(session.acp_server).toBe("mock-acp");
  await expect(page.locator(".chat-input-textarea")).toBeFocused();
});

test("shared native action and browser shortcut honor modal and keyboard guards", async ({ page }) => {
  await openOrigin(page);
  let posts = 0;
  page.on("request", (r) => {
    if (r.method() === "POST" && r.url().endsWith("/api/sessions")) posts++;
  });
  await page.evaluate(() => {
    for (const extra of [
      { repeat: true }, { shiftKey: true }, { isComposing: true },
      { ctrlKey: true },
    ]) {
      window.dispatchEvent(new KeyboardEvent("keydown", {
        key: "n", code: "KeyN", metaKey: true, altKey: true,
        bubbles: true, cancelable: true, ...extra,
      }));
    }
    const prevented = new KeyboardEvent("keydown", {
      key: "n", code: "KeyN", ctrlKey: true, altKey: true, cancelable: true,
    });
    prevented.preventDefault();
    window.dispatchEvent(prevented);
  });
  await page.getByTestId("settings-btn").first().click();
  await expect(page.getByTestId("settings-dialog")).toBeVisible();
  await invokeNative(page);
  await page.keyboard.press("Control+Alt+n");
  await settleUI(page);
  expect(posts).toBe(0);
  expect(await selectedSession(page)).toBe(originId);
  await expect(page.getByTestId("settings-dialog")).toBeVisible();
});

test("readonly mode blocks the shared native action too", async ({ page }) => {
  await page.route("**/api/config", async (route) => {
    const response = await route.fetch();
    const config = await response.json();
    await route.fulfill({ response, json: { ...config, config_readonly: true } });
  });
  await openOrigin(page);
  let posts = 0;
  page.on("request", (r) => {
    if (r.method() === "POST" && r.url().endsWith("/api/sessions")) posts++;
  });
  await invokeNative(page);
  await page.keyboard.press("Control+Alt+n");
  await settleUI(page);
  expect(posts).toBe(0);
});

test("retry success leaves Dashboard, blocks repeated creation, and focuses", async ({ page }) => {
  await openOrigin(page);
  await page.getByRole("button", { name: "Dashboard", exact: true }).click();
  await page.clock.install();
  let posts = 0;
  await page.route("**/api/sessions", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    posts++;
    if (posts === 1) return route.fulfill({
      status: 503, contentType: "application/json",
      body: JSON.stringify({ error: {
        code: "session_creation_timeout", message: "mock agent busy",
      } }),
    });
    await route.continue();
  });
  await invokeNative(page);
  await expect(page.getByText("Agent is busy — retrying automatically…")).toBeVisible();
  await invokeNative(page);
  await page.keyboard.press("Control+Alt+n");
  expect(posts).toBe(1);
  await page.clock.fastForward(30000);
  await changedSession(page);
  await expect(page.locator(".chat-input-textarea")).toBeVisible();
  await expect(page.locator(".chat-input-textarea")).toBeFocused();
  expect(posts).toBe(2);
});

test("a late modal retains focus after pending creation completes", async ({ page }) => {
  await openOrigin(page);
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  const posted = page.waitForRequest((r) =>
    r.method() === "POST" && r.url().endsWith("/api/sessions"));
  await page.route("**/api/sessions", async (route) => {
    if (route.request().method() === "POST") await gate;
    await route.continue();
  });
  await invokeNative(page);
  await posted;
  await page.getByTestId("settings-btn").first().click();
  await expect(page.getByTestId("settings-dialog")).toBeVisible();
  release();
  await changedSession(page);
  await expect(page.locator(".chat-input-textarea")).not.toBeFocused();
  await expect(page.getByTestId("settings-dialog")).toBeVisible();
});

test("navigation during creation does not switch back on late success", async ({ page }) => {
  await openOrigin(page);
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  const posted = page.waitForRequest((r) =>
    r.method() === "POST" && r.url().endsWith("/api/sessions"));
  await page.route("**/api/sessions", async (route) => {
    if (route.request().method() === "POST") await gate;
    await route.continue();
  });
  await invokeNative(page);
  await posted;
  await page.getByRole("button", { name: "Dashboard", exact: true }).click();
  const response = page.waitForResponse((r) =>
    r.request().method() === "POST" && r.url().endsWith("/api/sessions"));
  release();
  expect((await response).ok()).toBeTruthy();
  await settleUI(page);
  await expect(page.getByRole("button", { name: "Dashboard", exact: true }))
    .toHaveAttribute("aria-current", "page");
  expect(await selectedSession(page)).toBe(originId);
});

test("mobile creation never summons the keyboard", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openOrigin(page);
  await invokeNative(page);
  await changedSession(page);
  await expect(page.locator(".chat-input-textarea")).toBeEnabled();
  await expect(page.locator(".chat-input-textarea")).not.toBeFocused();
});

async function openDashboardWithoutSession(page: Page) {
  await page.addInitScript(() => {
    localStorage.removeItem("mitto_last_session_id");
    localStorage.setItem("mitto_sidebar_collapsed", "false");
  });
  await page.goto("/mitto/");
  await expect(page.getByRole("button", { name: "Dashboard", exact: true }))
    .toHaveAttribute("aria-current", "page");
  await expect.poll(() => page.evaluate(
    () => typeof (window as any).mittoNewConversationHere,
  )).toBe("function");
}

test("without a current session the sole workspace is used", async ({ page, request, apiUrl }) => {
  fixtureConfig.workspaces = [fixtureConfig.workspaces[2]];
  await saveConfig(request, apiUrl, fixtureConfig);
  await openDashboardWithoutSession(page);
  await invokeNative(page);
  const id = await changedSession(page);
  const session = await (await request.get(apiUrl(`/api/sessions/${id}`))).json();
  expect(session.working_dir).toBe(alpha);
  expect(session.acp_server).toBe("cn9-default");
  await expect(page.locator(".chat-input-textarea")).toBeFocused();
});

test("multiple workspace entries without context retain the existing chooser", async ({ page }) => {
  await openDashboardWithoutSession(page);
  await invokeNative(page);
  await expect(page.getByRole("dialog", { name: "Select Workspace" })).toBeVisible();
});

test("current session folder wins over a different visible Tasks folder", async ({ page, request, apiUrl }) => {
  await request.post(apiUrl("/api/sessions"), {
    data: { working_dir: beta, acp_server: "cn9-global" },
  });
  await openOrigin(page);
  const tasks = page.locator(`[title="Beads issues: ${beta}"]`);
  await tasks.locator("xpath=ancestor::details[1]").evaluate((el: HTMLDetailsElement) => { el.open = true; });
  await tasks.click();
  await invokeNative(page);
  const id = await changedSession(page);
  const session = await (await request.get(apiUrl(`/api/sessions/${id}`))).json();
  expect(session.working_dir).toBe(alpha);
  expect(session.acp_server).toBe("cn9-default");
  await expect(page.locator(".chat-input-textarea")).toBeFocused();
});

test("without a session visible Tasks supplies the folder", async ({ page, request, apiUrl }) => {
  await request.post(apiUrl("/api/sessions"), {
    data: { working_dir: beta, acp_server: "cn9-global" },
  });
  await openDashboardWithoutSession(page);
  const tasks = page.locator(`[title="Beads issues: ${beta}"]`);
  await tasks.locator("xpath=ancestor::details[1]").evaluate((el: HTMLDetailsElement) => { el.open = true; });
  await tasks.click();
  await invokeNative(page);
  const id = await changedSession(page);
  const session = await (await request.get(apiUrl(`/api/sessions/${id}`))).json();
  expect(session.working_dir).toBe(beta);
  expect(session.acp_server).toBe("cn9-global");
  await expect(page.locator(".chat-input-textarea")).toBeFocused();
});

test("disconnected creation waits for an editable composer and focuses only once", async ({
  page, context,
}) => {
  // Chromium's offline toggle does not close an already-open WebSocket.
  // Capture the native events socket so we can close that real transport;
  // offline mode then prevents reconnection until the test explicitly restores it.
  await page.addInitScript(() => {
    const NativeWebSocket = window.WebSocket;
    window.WebSocket = class extends NativeWebSocket {
      constructor(url: string | URL, protocols?: string | string[]) {
        super(url, protocols);
        if (new URL(String(url)).pathname.endsWith("/api/events")) {
          (window as any).cn9EventsSocket = this;
        }
      }
    };
  });
  await openOrigin(page);
  const composer = page.locator(".chat-input-textarea");
  // route.fetch runs in Playwright's request context, so the real mock-server
  // create can complete while browser WebSockets are deliberately offline.
  await page.route("**/api/sessions", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    const response = await route.fetch();
    await route.fulfill({ response });
  });
  try {
    await context.setOffline(true);
    await page.evaluate(() => (window as any).cn9EventsSocket.close());
    await expect(composer).toBeDisabled();
    await invokeNative(page);
    await changedSession(page);
    await settleUI(page); // Let the initial disabled-composer focus attempt run.
    await expect(composer).toBeDisabled();
    await expect(composer).not.toBeFocused();
    await page.evaluate(() => {
      (window as any).cn9ComposerFocusCount = 0;
      document.addEventListener("focusin", (event) => {
        if ((event.target as Element).matches(".chat-input-textarea")) {
          (window as any).cn9ComposerFocusCount++;
        }
      });
    });
    await context.setOffline(false);
    await expect(composer).toBeEditable();
    await expect(composer).toBeFocused();
    expect(await page.evaluate(() => (window as any).cn9ComposerFocusCount)).toBe(1);

    // A subsequent connectivity-driven render must not replay consumed intent.
    const settings = page.getByTestId("settings-btn").first();
    await settings.focus();
    await context.setOffline(true);
    await page.evaluate(() => (window as any).cn9EventsSocket.close());
    await expect(composer).toBeDisabled();
    await context.setOffline(false);
    await expect(composer).toBeEditable();
    await settleUI(page);
    await expect(settings).toBeFocused();
    expect(await page.evaluate(() => (window as any).cn9ComposerFocusCount)).toBe(1);
  } finally {
    await context.setOffline(false);
  }
});

test("terminal busy retry exhaustion releases the latch for a fresh invocation", async ({ page }) => {
  await openOrigin(page);
  await page.clock.install();
  let posts = 0;
  let scheduledRetries = 0;
  page.on("console", (message) => {
    if (message.text().includes("[createNewSession] Agent busy — scheduling retry")) {
      scheduledRetries++;
    }
  });
  await page.route("**/api/sessions", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    posts++;
    // The existing policy is one initial attempt plus four automatic retries.
    if (posts <= 5) return route.fulfill({
      status: 503, contentType: "application/json",
      body: JSON.stringify({ error: {
        code: "session_creation_timeout", message: "mock agent stays busy",
      } }),
    });
    await route.continue();
  });
  const folderButton = page.locator("li.folder-group")
    .filter({ has: page.locator(`summary [title="${alpha}"]`) })
    .getByTestId("new-conversation-btn");
  await invokeNative(page);
  await expect(folderButton).toBeDisabled();
  await expect.poll(() => posts).toBe(1);
  await expect.poll(() => scheduledRetries).toBe(1);
  for (let retry = 1; retry <= 4; retry++) {
    await page.clock.fastForward(30000);
    await expect.poll(() => posts).toBe(retry + 1);
    if (retry < 4) {
      await expect.poll(() => scheduledRetries).toBe(retry + 1);
      await expect(folderButton).toBeDisabled();
      await invokeNative(page);
      expect(posts).toBe(retry + 1);
    }
  }
  await expect(folderButton).toBeEnabled();
  expect(await selectedSession(page)).toBe(originId);
  await page.clock.fastForward(30000);
  expect(posts).toBe(5); // Exhaustion must not leave another scheduled retry.
  await invokeNative(page);
  await changedSession(page);
  await expect(page.locator(".chat-input-textarea")).toBeFocused();
  expect(posts).toBe(6);
});

test("same-view Tasks folder navigation cancels activation of a pending create", async ({
  page, request, apiUrl,
}) => {
  const betaResponse = await request.post(apiUrl("/api/sessions"), {
    data: { working_dir: beta, acp_server: "cn9-global" },
  });
  expect(betaResponse.ok()).toBeTruthy();
  await openOrigin(page);
  const tasks = (folder: string) => page.locator(`[title="Beads issues: ${folder}"]`);
  // Expand both folders before entering Tasks, so auto-selection on folder
  // expansion cannot masquerade as the navigation cancellation under test.
  for (const folder of [beta, alpha]) {
    await tasks(folder).locator("xpath=ancestor::details[1]")
      .evaluate((el: HTMLDetailsElement) => { el.open = true; });
  }
  await tasks(alpha).click();
  await expect(tasks(alpha)).toHaveAttribute("aria-current", "page");
  const retainedSession = await selectedSession(page);
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  const posted = page.waitForRequest((r) =>
    r.method() === "POST" && r.url().endsWith("/api/sessions"));
  await page.route("**/api/sessions", async (route) => {
    if (route.request().method() === "POST") await gate;
    await route.continue();
  });
  try {
    await invokeNative(page);
    await posted;
    await tasks(beta).click();
    await expect(tasks(beta)).toHaveAttribute("aria-current", "page");
    expect(await selectedSession(page)).toBe(retainedSession);
    const response = page.waitForResponse((r) =>
      r.request().method() === "POST" && r.url().endsWith("/api/sessions"));
    release();
    const created = await response;
    expect(created.ok()).toBeTruthy();
    const id = (await created.json()).session_id;
    expect(id).not.toBe(retainedSession);
    await created.finished();
    await settleUI(page);
    await expect(tasks(beta)).toHaveAttribute("aria-current", "page");
    await expect(tasks(alpha)).not.toHaveAttribute("aria-current", "page");
    expect(await selectedSession(page)).toBe(retainedSession);
    await expect(page.locator(".chat-input-textarea")).toBeHidden();
  } finally {
    release();
  }
});

for (const interveningTyping of [false, true]) {
  test(`originating sidebar inline editor ${interveningTyping ? "keeps focus after later typing" : "releases focus for explicit creation"}`, async ({ page }) => {
    await openOrigin(page);
    // The current sidebar has no inline text editor (rename uses a modal).
    // Supply only a DOM focus owner, as the existing focus spec does for links;
    // exercise the real App action/effects without any production-only seam.
    await page.locator(".session-item-container").first().evaluate((el) => {
      const input = document.createElement("input");
      input.dataset.testid = "cn9-inline-editor";
      input.setAttribute("aria-label", "Test inline sidebar editor");
      el.append(input);
    });
    const editor = page.getByTestId("cn9-inline-editor");
    await editor.fill("draft");
    await expect(editor).toBeFocused();
    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const posted = page.waitForRequest((r) =>
      r.method() === "POST" && r.url().endsWith("/api/sessions"));
    await page.route("**/api/sessions", async (route) => {
      if (route.request().method() === "POST") await gate;
      await route.continue();
    });
    try {
      // Use the real keyboard path while the editor is the origin focus owner.
      await page.keyboard.press("Control+Alt+n");
      await posted;
      await expect(editor).toBeFocused();
      if (interveningTyping) await page.keyboard.type(" later");
      release();
      await changedSession(page);
      const composer = page.locator(".chat-input-textarea");
      await expect(composer).toBeEditable();
      await settleUI(page);
      if (interveningTyping) {
        await expect(editor).toHaveValue("draft later");
        await expect(editor).toBeFocused();
        await expect(composer).not.toBeFocused();
      } else {
        await expect(composer).toBeFocused();
        await expect(editor).not.toBeFocused();
      }
    } finally {
      release();
    }
  });
}
