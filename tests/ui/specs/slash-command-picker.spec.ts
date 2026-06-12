import { test, expect } from "../fixtures/test-fixtures";
import { selectors, timeouts } from "../utils/selectors";

/**
 * SlashCommandPicker internals tests.
 *
 * Regression net: catches a daisyUI restyle breaking keyboard nav inside the
 * slash command picker (active-highlight, arrow-key navigation, Enter selection).
 *
 * Strategy:
 *   The mock-ACP server does not advertise slash commands. We inject a synthetic
 *   `available_commands_updated` WebSocket message via page.routeWebSocket()
 *   immediately after the server's `connected` message arrives. This makes the
 *   frontend believe the agent supports three slash commands, allowing us to
 *   open the picker by typing "/" and exercise keyboard navigation.
 *
 * Commands injected:
 *   /help    Show help information
 *   /clear   Clear conversation history
 *   /test    Run tests
 */

const INJECTED_COMMANDS = [
  { name: "help", description: "Show help information", input_hint: "" },
  { name: "clear", description: "Clear conversation history", input_hint: "" },
  { name: "test", description: "Run tests", input_hint: "" },
];

test.describe("SlashCommandPicker internals", () => {
  test("should open picker on / and navigate with arrow keys", async ({ page, request }) => {
    // Set up WebSocket interception BEFORE navigation so we catch the first
    // `connected` frame and inject available_commands_updated immediately.
    // We inject on EVERY `connected` message to handle page.reload() reconnections.
    await page.routeWebSocket("**/ws", (ws) => {
      const server = ws.connectToServer();

      // Forward server → client; inject commands after each `connected`
      server.onMessage((message) => {
        ws.send(message);
        try {
          const msg = JSON.parse(message.toString());
          if (msg.type === "connected") {
            ws.send(
              JSON.stringify({
                type: "available_commands_updated",
                data: { commands: INJECTED_COMMANDS },
              }),
            );
          }
        } catch (_) {
          /* non-JSON keepalive frames — ignore */
        }
      });

      // Forward client → server
      ws.onMessage((message) => server.send(message));
    });

    // Navigate first, then create session via API
    await page.goto("/");
    await page.waitForLoadState("networkidle");

    // Create session and activate it
    const resp = await request.post("/mitto/api/sessions", { data: {} });
    expect(resp.ok()).toBe(true);
    const data = await resp.json();
    const sessionId = data.session_id;
    await page.evaluate((id) => {
      localStorage.setItem("mitto_last_session_id", id);
    }, sessionId);
    await page.reload();
    // Wait for ACP to be ready: placeholder changes from "Waiting for AI agent to connect..."
    await expect(page.locator(selectors.chatInput)).toHaveAttribute(
      "placeholder",
      /Type your message/,
      { timeout: timeouts.agentResponse },
    );

    // Picker element is always in DOM but hidden when closed
    const picker = page.locator('[data-testid="slash-command-picker"]');
    await expect(picker).toBeAttached();

    // Type "/" to trigger the picker
    const textarea = page.locator(selectors.chatInput);
    await textarea.focus();
    await page.keyboard.type("/");

    // Picker should become visible (opacity-100 class applied)
    await expect(picker).toHaveClass(/opacity-100/, { timeout: timeouts.shortAction });

    // All three injected commands should be listed
    const items = picker.locator(".slash-picker-item");
    await expect(items).toHaveCount(INJECTED_COMMANDS.length, {
      timeout: timeouts.shortAction,
    });

    // Command names should be shown
    await expect(picker.locator(".slash-command-name").first()).toContainText("/help");

    // First item is highlighted (selectedIndex=0)
    const firstItem = items.first();
    await expect(firstItem).toHaveClass(/menu-active/);

    // ArrowDown → second item should become active, first loses highlight
    await page.keyboard.press("ArrowDown");
    const secondItem = items.nth(1);
    await expect(secondItem).toHaveClass(/menu-active/, {
      timeout: timeouts.shortAction,
    });
    await expect(firstItem).not.toHaveClass(/menu-active/);

    // ArrowDown again → third item active
    await page.keyboard.press("ArrowDown");
    const thirdItem = items.nth(2);
    await expect(thirdItem).toHaveClass(/menu-active/, {
      timeout: timeouts.shortAction,
    });

    // ArrowDown at last item → stays at third (no wrap-around)
    await page.keyboard.press("ArrowDown");
    await expect(thirdItem).toHaveClass(/menu-active/);

    // ArrowUp → back to second
    await page.keyboard.press("ArrowUp");
    await expect(secondItem).toHaveClass(/menu-active/, {
      timeout: timeouts.shortAction,
    });

    // Select second item (/clear) with Enter
    await page.keyboard.press("Enter");

    // Picker should close after selection
    await expect(picker).not.toHaveClass(/opacity-100/, { timeout: timeouts.shortAction });

    // Textarea should now contain "/clear "
    await expect(textarea).toHaveValue("/clear ");
  });

  test("should filter commands when typing after /", async ({ page, request }) => {
    // Inject commands on EVERY connected message (handles page.reload() re-connection)
    await page.routeWebSocket("**/ws", (ws) => {
      const server = ws.connectToServer();
      server.onMessage((message) => {
        ws.send(message);
        try {
          const msg = JSON.parse(message.toString());
          if (msg.type === "connected") {
            ws.send(
              JSON.stringify({
                type: "available_commands_updated",
                data: { commands: INJECTED_COMMANDS },
              }),
            );
          }
        } catch (_) {}
      });
      ws.onMessage((message) => server.send(message));
    });

    await page.goto("/");
    await page.waitForLoadState("networkidle");
    const resp = await request.post("/mitto/api/sessions", { data: {} });
    const data = await resp.json();
    await page.evaluate((id) => localStorage.setItem("mitto_last_session_id", id), data.session_id);
    await page.reload();
    await expect(page.locator(selectors.chatInput)).toHaveAttribute(
      "placeholder",
      /Type your message/,
      { timeout: timeouts.agentResponse },
    );

    const textarea = page.locator(selectors.chatInput);
    await textarea.focus();
    await page.keyboard.type("/cl");

    const picker = page.locator('[data-testid="slash-command-picker"]');
    await expect(picker).toHaveClass(/opacity-100/, { timeout: timeouts.shortAction });

    // Only /clear should match the "cl" prefix
    const items = picker.locator(".slash-picker-item");
    await expect(items).toHaveCount(1, { timeout: timeouts.shortAction });
    await expect(picker.locator(".slash-command-name").first()).toContainText("/clear");

    // Escape should close the picker
    await page.keyboard.press("Escape");
    await expect(picker).not.toHaveClass(/opacity-100/, { timeout: timeouts.shortAction });
  });
});
