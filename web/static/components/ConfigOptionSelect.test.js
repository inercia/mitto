import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { Fragment, h, render } from "../vendor/preact.js";
import {
  useCallback,
  useEffect,
  useRef,
  useState,
} from "../vendor/preact-hooks.js";
import htm from "../vendor/htm.js";

if (process.env.MITTO_CONFIG_OPTION_SELECT_TEST_CHILD === "1") {
  let ConfigOptionSelect;

  beforeAll(async () => {
    window.preact = {
      Fragment,
      html: htm.bind(h),
      useCallback,
      useEffect,
      useRef,
      useState,
    };
    ({ ConfigOptionSelect } = await import("./ConfigOptionSelect.js"));
  });

  test("sorts only the model menu by name without changing option IDs or source order", () => {
    const options = [
      { value: "opus", name: "Claude Opus 5.5" },
      { value: "auto", name: "Auto" },
      { value: "sonnet", name: "claude Sonnet 5" },
    ];
    const container = document.createElement("div");
    document.body.appendChild(container);
    const selections = [];
    const mount = (category) =>
      render(
        h(ConfigOptionSelect, {
          configOption: {
            id: category,
            name: category,
            category,
            current_value: "auto",
            options,
          },
          onSetConfigOption: (id, value) => selections.push([id, value]),
        }),
        container,
      );

    try {
      mount("model");
      const buttons = [...container.querySelectorAll("ul button")];
      expect(buttons.map((button) => button.textContent.trim())).toEqual([
        "Auto",
        "Claude Opus 5.5",
        "claude Sonnet 5",
      ]);
      expect(container.querySelectorAll("ul button.menu-active")).toHaveLength(
        1,
      );
      buttons[1].click();
      expect(selections).toEqual([["model", "opus"]]);
      expect(options.map((option) => option.value)).toEqual([
        "opus",
        "auto",
        "sonnet",
      ]);

      mount("mode");
      expect(
        [...container.querySelectorAll("ul button")].map((button) =>
          button.textContent.trim(),
        ),
      ).toEqual(["Claude Opus 5.5", "Auto", "claude Sonnet 5"]);
    } finally {
      render(null, container);
      container.remove();
    }
  });

  // Preact's plain `useEffect` (not `useLayoutEffect`) is flushed via
  // `requestAnimationFrame`, never on a microtask — a bare
  // `await Promise.resolve()` is not enough to observe its side effects.
  // Under happy-dom, `requestAnimationFrame` schedules its callback via
  // Node's `setImmediate` (see `BrowserWindow.requestAnimationFrame`), and
  // the exact number of event-loop ticks between scheduling the effect and
  // its DOM mutation actually committing is an implementation detail of
  // Preact's fallback scheduler (it races `setTimeout(_, 100)` against
  // `requestAnimationFrame`, then does one more `setTimeout(_, 0)` hop) —
  // not something tests should hardcode a tick-count for. Instead, poll the
  // DOM in a bounded loop, yielding one event-loop turn at a time via
  // `setImmediate`, until the caller-supplied predicate is satisfied (or a
  // wall-clock timeout elapses, at which point we throw so a genuine
  // component bug still fails loudly instead of hanging).
  const waitFor = async (predicate, { timeoutMs = 2000 } = {}) => {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      await new Promise((resolve) => setImmediate(resolve));
      if (predicate()) return;
      if (Date.now() >= deadline) {
        throw new Error(
          "waitFor: timed out waiting for condition to become true",
        );
      }
    }
  };

  test("accepts an optimistic change when the server confirms the new value", async () => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const toasts = [];
    const options = [
      { value: "low", name: "Low" },
      { value: "high", name: "High" },
    ];
    const mount = (currentValue) =>
      render(
        h(ConfigOptionSelect, {
          configOption: {
            id: "reasoning_effort",
            name: "Reasoning effort",
            category: "thought_level",
            current_value: currentValue,
            options,
          },
          onSetConfigOption: () => {},
          onShowToast: (t) => toasts.push(t),
        }),
        container,
      );

    try {
      mount("low");
      const buttons = () => [...container.querySelectorAll("ul button")];
      buttons()[1].click(); // optimistically select "high"
      // useState setters route through Preact's core setState/enqueueRender,
      // which (unlike useEffect) is flushed on a microtask — one
      // `await Promise.resolve()` is enough to observe the re-render.
      await Promise.resolve();
      expect(
        buttons().find((b) => b.classList.contains("menu-active"))
          ?.textContent.trim(),
      ).toBe("High");

      // Server confirms: re-render with a NEW object whose current_value
      // matches what was optimistically sent.
      mount("high");
      await waitFor(
        () =>
          buttons().find((b) => b.classList.contains("menu-active"))
            ?.textContent.trim() === "High",
      );
      expect(toasts).toEqual([]);
    } finally {
      render(null, container);
      container.remove();
    }
  });

  test("rolls back an optimistic change when the server re-broadcasts the unchanged value and surfaces a toast", async () => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const toasts = [];
    const options = [
      { value: "low", name: "Low" },
      { value: "high", name: "High" },
    ];
    const mount = () =>
      render(
        h(ConfigOptionSelect, {
          configOption: {
            id: "reasoning_effort",
            name: "Reasoning effort",
            category: "thought_level",
            current_value: "low",
            options,
          },
          onSetConfigOption: () => {},
          onShowToast: (t) => toasts.push(t),
        }),
        container,
      );

    try {
      mount();
      const buttons = () => [...container.querySelectorAll("ul button")];
      buttons()[1].click(); // optimistically select "high"
      // useState's setter routes through Preact's core setState/enqueueRender,
      // which (unlike useEffect) is flushed on a microtask, not a macrotask —
      // a single awaited microtask tick is enough to observe the DOM update.
      await Promise.resolve();
      expect(
        buttons().find((b) => b.classList.contains("menu-active"))
          ?.textContent.trim(),
      ).toBe("High");
      expect(toasts).toEqual([]);

      // Server rejects: re-render with a NEW object whose current_value is
      // UNCHANGED from before the optimistic click ("low").
      mount();
      await waitFor(
        () =>
          buttons().find((b) => b.classList.contains("menu-active"))
            ?.textContent.trim() === "Low",
      );
      expect(toasts).toHaveLength(1);
      expect(toasts[0].style).toBe("error");
      expect(toasts[0].message).toContain("Reasoning effort");
    } finally {
      render(null, container);
      container.remove();
    }
  });
} else {
  test("ConfigOptionSelect menu ordering in isolated happy-dom process", () => {
    const result = spawnSync(
      process.execPath,
      ["test", fileURLToPath(import.meta.url)],
      {
        encoding: "utf8",
        env: { ...process.env, MITTO_CONFIG_OPTION_SELECT_TEST_CHILD: "1" },
        timeout: 30_000,
      },
    );
    expect(result.status).toBe(0);
  });
}
