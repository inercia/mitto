// Real Preact controls + SDK transport, isolated from other tests' window mocks.
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { test, expect } from "../utils/testing/testGlobals.js";

if (process.env.MITTO_LOOP_PLAY_TEST_CHILD === "1") {
  const preact = await import("../vendor/preact.js");
  const hooks = await import("../vendor/preact-hooks.js");
  const { default: htm } = await import("../vendor/htm.js");
  window.preact = {
    ...preact,
    ...hooks,
    html: htm.bind(preact.h),
    memo: (component) => component,
  };
  preact.options.debounceRendering = queueMicrotask;
  preact.options.requestAnimationFrame = queueMicrotask;
  const { LoopControlBar } = await import("./LoopControlBar.js");
  const { LoopFrequencyPanel } = await import("./LoopFrequencyPanel.js");
  const { _resetSdkClientForTests } = await import("../utils/sdkClient.js");
  const flush = async () => {
    for (let i = 0; i < 80; i++) await Promise.resolve();
  };

  for (const [name, Component, buttonId] of [
    ["compact", LoopControlBar, "loop-pause-resume-button"],
    ["panel", LoopFrequencyPanel, "loop-run-now-button"],
  ]) {
    for (const scenario of ["exhausted", "remaining", "stale"]) {
      test(`${name} actual Play: ${scenario} confirmation, explicit intent, one run-now`, async () => {
        _resetSdkClientForTests();
        const previousFetch = globalThis.fetch;
        const requests = [];
        let exhausted = scenario === "exhausted";
        let rejectResume = scenario === "stale";
        const json = (body, status = 200) =>
          new Response(JSON.stringify(body), {
            status,
            headers: { "Content-Type": "application/json" },
          });
        globalThis.fetch = async (url, options = {}) => {
          if (String(url).endsWith("csrf-token"))
            return json({ token: "test" });
          requests.push({
            method: options.method,
            body: options.body && JSON.parse(options.body),
          });
          if (options.method === "GET")
            return json({
              enabled: false,
              stopped_reason: exhausted ? "disabledByAgent" : "maxDuration",
              budget: { exhausted, resumable: !exhausted },
            });
          if (options.method === "PATCH") {
            if (rejectResume) {
              rejectResume = false;
              exhausted = true;
              return json(
                {
                  error: {
                    code: "loop_restart_required",
                    message: "Confirm a fresh restart",
                  },
                },
                409,
              );
            }
            return json({ enabled: true });
          }
          // Simulate the existing automatic bootstrap winning admission.
          return json(
            {
              error: {
                code: "conflict",
                message: "Session is currently processing a prompt",
              },
            },
            409,
          );
        };
        const container = document.createElement("div");
        document.body.appendChild(container);
        const enabledChanges = [];
        try {
          preact.render(
            preact.h(Component, {
              sessionId: "rendered-play",
              isOpen: true,
              enabled: false,
              disabled: false,
              onLoopEnabledChange: (value) => enabledChanges.push(value),
            }),
            container,
          );
          await flush();
          container.querySelector(`[data-testid="${buttonId}"]`).click();
          await flush();
          const confirm = () =>
            container.querySelector('[data-testid="confirm-dialog-confirm"]');
          expect(confirm()?.textContent.trim()).toBe(
            exhausted
              ? "Restart with fresh limits"
              : "Resume remaining allowance",
          );
          expect(
            requests.filter((request) => request.method === "PATCH"),
          ).toHaveLength(0);
          confirm().click();
          await flush();
          if (scenario === "stale") {
            expect(confirm()?.textContent.trim()).toBe(
              "Restart with fresh limits",
            );
            expect(container.textContent).toContain("Review and confirm again");
            expect(enabledChanges).toEqual([]);
            expect(
              requests.filter((request) => request.method === "POST"),
            ).toHaveLength(0);
            expect(
              requests
                .filter((request) => request.method === "PATCH")
                .map((request) => request.body),
            ).toEqual([{ enabled: true }]);
            confirm().click();
            await flush();
          }
          const patches = requests.filter(
            (request) => request.method === "PATCH",
          );
          expect(patches.at(-1).body).toEqual(
            scenario === "remaining"
              ? { enabled: true }
              : { enabled: true, reset_counters: true },
          );
          expect(
            requests.filter((request) => request.method === "POST"),
          ).toHaveLength(1);
          expect(enabledChanges).toEqual([true]);
          expect(confirm()).toBeNull();
          expect(container.textContent).not.toContain("Error");
        } finally {
          preact.render(null, container);
          container.remove();
          globalThis.fetch = previousFetch;
        }
      });
    }
  }
} else {
  test("both actual Play controls in isolated happy-dom process", () => {
    const result = spawnSync(
      process.execPath,
      ["test", fileURLToPath(import.meta.url)],
      {
        encoding: "utf8",
        env: { ...process.env, MITTO_LOOP_PLAY_TEST_CHILD: "1" },
        timeout: 30_000,
      },
    );
    if (result.status !== 0)
      throw new Error(
        `Isolated Play tests failed:\n${result.stdout}\n${result.stderr}`,
      );
  });
}
