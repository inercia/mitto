// Invoked by TestLoopRestartIntegratedPlay against its real local Go server.
// No synthetic loop responses: only CSRF discovery is outside this handler seam.
import { test, expect } from "../utils/testing/testGlobals.js";

if (process.env.MITTO_LOOP_INTEGRATION_URL) {
  window.happyDOM.setURL(process.env.MITTO_LOOP_INTEGRATION_URL);
  const preact = await import("../vendor/preact.js");
  const hooks = await import("../vendor/preact-hooks.js");
  const { default: htm } = await import("../vendor/htm.js");
  window.preact = {
    ...preact,
    ...hooks,
    html: htm.bind(preact.h),
    memo: (c) => c,
  };
  preact.options.debounceRendering = queueMicrotask;
  preact.options.requestAnimationFrame = queueMicrotask;
  const { LoopControlBar } = await import("./LoopControlBar.js");
  const { LoopFrequencyPanel } = await import("./LoopFrequencyPanel.js");

  test("real Play traverses authoritative HTTP restart and busy runner", async () => {
    const nativeFetch = globalThis.fetch;
    const requests = [];
    globalThis.fetch = async (url, options = {}) => {
      if (String(url).endsWith("csrf-token"))
        return new Response(JSON.stringify({ token: "test" }), {
          headers: { "Content-Type": "application/json" },
        });
      const response = await nativeFetch(
        new URL(String(url), process.env.MITTO_LOOP_INTEGRATION_URL).href,
        options,
      );
      requests.push({
        method: options.method,
        body: options.body && JSON.parse(options.body),
        status: response.status,
      });
      return response;
    };
    const kind = process.env.MITTO_LOOP_INTEGRATION_KIND;
    const compact = process.env.MITTO_LOOP_INTEGRATION_SURFACE === "compact";
    const container = document.createElement("div");
    document.body.appendChild(container);
    const changes = [];
    const wait = async (ready) => {
      const deadline = Date.now() + 5000;
      while (!ready()) {
        if (Date.now() > deadline)
          throw new Error(`UI boundary timeout: ${container.textContent}`);
        await new Promise((resolve) => setImmediate(resolve));
      }
    };
    const confirm = () =>
      container.querySelector('[data-testid="confirm-dialog-confirm"]');
    try {
      preact.render(
        preact.h(compact ? LoopControlBar : LoopFrequencyPanel, {
          sessionId: "integrated-play",
          isOpen: true,
          enabled: false,
          disabled: false,
          onLoopEnabledChange: (value) => changes.push(value),
        }),
        container,
      );
      container
        .querySelector(
          `[data-testid="${compact ? "loop-pause-resume-button" : "loop-run-now-button"}"]`,
        )
        .click();
      await wait(() => confirm());
      expect(confirm().textContent.trim()).toBe(
        ["remaining", "stale"].includes(kind)
          ? "Resume remaining allowance"
          : "Restart with fresh limits",
      );
      expect(requests.filter((r) => r.method === "PATCH")).toHaveLength(0);
      confirm().click();
      if (kind === "stale") {
        await wait(
          () =>
            confirm()?.textContent.trim() === "Restart with fresh limits" &&
            !confirm().disabled,
        );
        expect(changes).toEqual([]);
        expect(requests.filter((r) => r.method === "POST")).toHaveLength(0);
        expect(
          requests
            .filter((r) => r.method === "PATCH")
            .map((r) => [r.body, r.status]),
        ).toEqual([[{ enabled: true }, 409]]);
        confirm().click();
      }
      await wait(() => requests.some((r) => r.method === "POST") && !confirm());
      expect(requests.filter((r) => r.method === "PATCH").at(-1).body).toEqual(
        kind === "remaining"
          ? { enabled: true }
          : { enabled: true, reset_counters: true },
      );
      expect(
        requests.filter((r) => r.method === "POST").map((r) => r.status),
      ).toEqual([409]);
      expect(changes).toEqual([true]);
      expect(container.textContent).not.toContain("Error");
    } finally {
      preact.render(null, container);
      container.remove();
      globalThis.fetch = nativeFetch;
    }
  });
}
