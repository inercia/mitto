import { test, expect } from "../fixtures/test-fixtures";

test("thought messages render without breaking conversation navigation", async ({
  page,
  helpers,
}) => {
  await helpers.navigateAndWait(page);

  const thought = await page.evaluate(async () => {
    const modulePath = "./components/Message.js";
    const { Message } = await import(modulePath);
    const mount = document.createElement("div");
    const { h, render } = (window as any).preact;
    try {
      render(
        h(Message, {
          message: { role: "thought", text: "Thinking", complete: true },
          historyKey: "thought-regression",
          isLast: false,
          isStreaming: false,
        }),
        mount,
      );
      return {
        text: mount.textContent,
        historyKey: mount.firstElementChild?.getAttribute("data-history-key"),
      };
    } finally {
      render(null, mount);
    }
  });

  expect(thought.text).toContain("Thinking");
  expect(thought.historyKey).toBe("thought-regression");
});
