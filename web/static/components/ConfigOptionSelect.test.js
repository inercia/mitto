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
import { registerExclusivePicker } from "../utils/pickerExclusion.js";

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

  test("gives the toolbar mode selector more room than other selectors", () => {
    const container = document.createElement("div");
    document.body.appendChild(container);

    const mount = (category) =>
      render(
        h(ConfigOptionSelect, {
          configOption: {
            id: category,
            name: category,
            category,
            current_value: "default",
            options: [{ value: "default", name: "Default" }],
          },
          variant: "toolbar",
        }),
        container,
      );

    try {
      mount("mode");
      expect(container.querySelector("summary").classList).toContain(
        "max-w-[264px]",
      );
      expect(
        container.querySelector("summary .truncate-left"),
      ).not.toBeNull();
      const modeOptionButton = container.querySelector("ul button");
      expect(modeOptionButton.classList).toContain("w-full");
      expect(
        modeOptionButton.querySelector("span.truncate-left").classList,
      ).toContain("flex-1");
      expect(container.querySelector("ul").classList).toContain("w-[250px]");

      mount("model");
      expect(container.querySelector("summary").classList).toContain(
        "max-w-[200px]",
      );
      const modelMenu = container.querySelector(".dropdown-content").classList;
      expect(modelMenu).toContain("picker-popover");
      expect(modelMenu).toContain("picker-popover--toolbar");
      expect(
        container
          .querySelector("summary span.truncate-left bdi")
          .getAttribute("dir"),
      ).toBe("ltr");
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
        buttons()
          .find((b) => b.classList.contains("menu-active"))
          ?.textContent.trim(),
      ).toBe("High");

      // Server confirms: re-render with a NEW object whose current_value
      // matches what was optimistically sent.
      mount("high");
      await waitFor(
        () =>
          buttons()
            .find((b) => b.classList.contains("menu-active"))
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
        buttons()
          .find((b) => b.classList.contains("menu-active"))
          ?.textContent.trim(),
      ).toBe("High");
      expect(toasts).toEqual([]);

      // Server rejects: re-render with a NEW object whose current_value is
      // UNCHANGED from before the optimistic click ("low").
      mount();
      await waitFor(
        () =>
          buttons()
            .find((b) => b.classList.contains("menu-active"))
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
  const keydown = (element, key) => {
    element.dispatchEvent(
      new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }),
    );
  };

  const typeFilter = (input, value) => {
    input.value = value;
    input.dispatchEvent(new Event("input", { bubbles: true }));
  };

  const rowButtons = (container) => [
    ...container.querySelectorAll("ul button"),
  ];
  const rowNames = (container) =>
    rowButtons(container).map((button) => button.textContent.trim());
  const highlightedButton = (container) =>
    container.querySelector("ul button.picker-row--highlighted");
  const activeButton = (container) =>
    container.querySelector("ul button.menu-active");

  const openMenu = async (container) => {
    container.querySelector("summary").click();
    await waitFor(() => container.querySelector("details").open === true);
  };

  const openModelMenu = async (container) => {
    await openMenu(container);
    const input = container.querySelector('input[type="text"]');
    await waitFor(() => input && document.activeElement === input);
    return input;
  };

  test("filters model options by name or value and keeps name order and selection state", async () => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const options = [
      { value: "anthropic/claude-opus", name: "Claude Opus" },
      { value: "openai/gpt-5", name: "GPT-5" },
      { value: "openai/gpt-4o-mini", name: "gpt-4o mini" },
      { value: "auto", name: "Auto" },
    ];
    const selections = [];
    const mount = (currentValue) =>
      render(
        h(ConfigOptionSelect, {
          configOption: {
            id: "model",
            name: "Model",
            category: "model",
            current_value: currentValue,
            options,
          },
          onSetConfigOption: (id, value) => selections.push([id, value]),
        }),
        container,
      );

    try {
      mount("openai/gpt-5");
      const input = await openModelMenu(container);

      expect(input.getAttribute("data-testid")).toBe(
        "config-option-model-filter",
      );
      expect(input.getAttribute("aria-label")).toBe("Filter models...");
      expect(
        container.querySelector('[data-testid="config-option-model-list"]'),
      ).not.toBeNull();
      expect(rowNames(container)).toEqual([
        "Auto",
        "Claude Opus",
        "gpt-4o mini",
        "GPT-5",
      ]);
      expect(highlightedButton(container).textContent.trim()).toBe("GPT-5");
      expect(activeButton(container).textContent.trim()).toBe("GPT-5");

      typeFilter(input, "GpT");
      await waitFor(() => rowButtons(container).length === 2);
      expect(rowNames(container)).toEqual(["gpt-4o mini", "GPT-5"]);
      expect(highlightedButton(container).textContent.trim()).toBe(
        "gpt-4o mini",
      );
      expect(activeButton(container).textContent.trim()).toBe("GPT-5");

      typeFilter(input, "anthropic");
      await waitFor(() => rowButtons(container).length === 1);
      expect(rowNames(container)).toEqual(["Claude Opus"]);
      expect(highlightedButton(container).textContent.trim()).toBe(
        "Claude Opus",
      );

      typeFilter(input, "  gpt  ");
      await waitFor(() => rowButtons(container).length === 2);
      expect(rowNames(container)).toEqual(["gpt-4o mini", "GPT-5"]);

      typeFilter(input, "");
      await waitFor(() => rowButtons(container).length === 4);
      expect(highlightedButton(container).textContent.trim()).toBe("GPT-5");
      expect(options.map((option) => option.value)).toEqual([
        "anthropic/claude-opus",
        "openai/gpt-5",
        "openai/gpt-4o-mini",
        "auto",
      ]);
      expect(selections).toEqual([]);
      expect(container.querySelector('[role="listbox"]')).toBeNull();
      expect(container.querySelector('[role="option"]')).toBeNull();
    } finally {
      render(null, container);
      container.remove();
    }
  });

  test("highlights the first model when the current value is not advertised", async () => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const options = [
      { value: "b", name: "B" },
      { value: "a", name: "A" },
    ];
    const mount = () =>
      render(
        h(ConfigOptionSelect, {
          configOption: {
            id: "model",
            name: "Model",
            category: "model",
            current_value: "not-advertised",
            options,
          },
        }),
        container,
      );

    try {
      mount();
      await openModelMenu(container);
      expect(highlightedButton(container).textContent.trim()).toBe("A");
    } finally {
      render(null, container);
      container.remove();
    }
  });

  test("navigates models without wrapping and selects with Enter", async () => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const options = [
      { value: "c", name: "C" },
      { value: "a", name: "A" },
      { value: "b", name: "B" },
    ];
    const selections = [];
    const toasts = [];
    const mount = (currentValue) =>
      render(
        h(ConfigOptionSelect, {
          configOption: {
            id: "model",
            name: "Model",
            category: "model",
            current_value: currentValue,
            options,
          },
          onSetConfigOption: (id, value) => selections.push([id, value]),
          onShowToast: (toast) => toasts.push(toast),
        }),
        container,
      );

    try {
      mount("b");
      const input = await openModelMenu(container);
      const highlightedName = () =>
        highlightedButton(container)?.textContent.trim();

      expect(highlightedName()).toBe("B");

      keydown(input, "ArrowDown");
      await Promise.resolve();
      expect(highlightedName()).toBe("C");

      keydown(input, "ArrowDown");
      await Promise.resolve();
      expect(highlightedName()).toBe("C");

      keydown(input, "ArrowUp");
      await Promise.resolve();
      expect(highlightedName()).toBe("B");

      keydown(input, "ArrowUp");
      await Promise.resolve();
      expect(highlightedName()).toBe("A");

      keydown(input, "ArrowUp");
      await Promise.resolve();
      expect(highlightedName()).toBeUndefined();

      keydown(input, "Enter");
      await Promise.resolve();
      expect(selections).toEqual([]);
      expect(container.querySelector("details").open).toBe(true);

      keydown(input, "ArrowDown");
      await Promise.resolve();
      expect(highlightedName()).toBe("A");

      keydown(input, "Enter");
      await waitFor(() => container.querySelector("details").open === false);
      expect(selections).toEqual([["model", "a"]]);
      expect(activeButton(container).textContent.trim()).toBe("A");
      expect(document.activeElement).toBe(container.querySelector("summary"));

      mount("a");
      await new Promise((resolve) => setTimeout(resolve, 20));
      expect(toasts).toEqual([]);
    } finally {
      render(null, container);
      container.remove();
    }
  });

  test("closes with Escape and restores trigger focus, while outside click does not", async () => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const options = [
      { value: "a", name: "A" },
      { value: "b", name: "B" },
    ];
    const mount = () =>
      render(
        h(ConfigOptionSelect, {
          configOption: {
            id: "model",
            name: "Model",
            category: "model",
            current_value: "b",
            options,
          },
        }),
        container,
      );

    try {
      mount();
      const input = await openModelMenu(container);
      keydown(input, "Escape");
      await waitFor(() => container.querySelector("details").open === false);
      expect(document.activeElement).toBe(container.querySelector("summary"));

      const reopenedInput = await openModelMenu(container);
      await waitFor(() => reopenedInput.value === "");
      document.body.dispatchEvent(
        new MouseEvent("mousedown", { bubbles: true }),
      );
      await waitFor(() => container.querySelector("details").open === false);
      expect(document.activeElement).not.toBe(
        container.querySelector("summary"),
      );

      const clickInput = await openModelMenu(container);
      expect(clickInput.value).toBe("");
      rowButtons(container)[0].click();
      await waitFor(() => container.querySelector("details").open === false);
      expect(document.activeElement).not.toBe(
        container.querySelector("summary"),
      );
    } finally {
      render(null, container);
      container.remove();
    }
  });

  test("shows the empty state and makes Enter a no-op when no model matches", async () => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const selections = [];
    const mount = () =>
      render(
        h(ConfigOptionSelect, {
          configOption: {
            id: "model",
            name: "Model",
            category: "model",
            current_value: "a",
            options: [
              { value: "a", name: "A" },
              { value: "b", name: "B" },
            ],
          },
          onSetConfigOption: (id, value) => selections.push([id, value]),
        }),
        container,
      );

    try {
      mount();
      const input = await openModelMenu(container);
      typeFilter(input, "zzz");
      await waitFor(() => rowButtons(container).length === 0);
      expect(container.textContent).toContain("No matching models");

      keydown(input, "Enter");
      await Promise.resolve();
      expect(selections).toEqual([]);
      expect(container.querySelector("details").open).toBe(true);

      keydown(input, "Escape");
      await waitFor(() => container.querySelector("details").open === false);
      expect(document.activeElement).toBe(container.querySelector("summary"));
    } finally {
      render(null, container);
      container.remove();
    }
  });

  test("scrolls the highlighted model into view", async () => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const originalScrollIntoView = Element.prototype.scrollIntoView;
    const calls = [];
    Element.prototype.scrollIntoView = function (options) {
      calls.push({ element: this, options });
    };

    try {
      render(
        h(ConfigOptionSelect, {
          configOption: {
            id: "model",
            name: "Model",
            category: "model",
            current_value: "c",
            options: [
              { value: "a", name: "A" },
              { value: "b", name: "B" },
              { value: "c", name: "C" },
            ],
          },
        }),
        container,
      );
      await openModelMenu(container);
      await waitFor(() => calls.length > 0);
      const highlightedCall = calls.find(
        (call) => call.element.textContent.trim() === "C",
      );
      expect(highlightedCall).toBeDefined();
      expect(highlightedCall.options).toEqual({ block: "nearest" });
    } finally {
      Element.prototype.scrollIntoView = originalScrollIntoView;
      render(null, container);
      container.remove();
    }
  });

  test("clamps rendering and Enter when the model list shrinks while open", async () => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const selections = [];
    const options = [
      { value: "a", name: "A" },
      { value: "b", name: "B" },
      { value: "c", name: "C" },
      { value: "d", name: "D" },
    ];
    const mount = (nextOptions) =>
      render(
        h(ConfigOptionSelect, {
          configOption: {
            id: "model",
            name: "Model",
            category: "model",
            current_value: "d",
            options: nextOptions,
          },
          onSetConfigOption: (id, value) => selections.push([id, value]),
        }),
        container,
      );

    try {
      mount(options);
      await openModelMenu(container);
      expect(highlightedButton(container).textContent.trim()).toBe("D");

      // Simulate a server snapshot shrinking the list while the menu stays
      // open: the raw selected index is now out of range.
      mount(options.slice(0, 2));
      await waitFor(() => rowButtons(container).length === 2);
      expect(highlightedButton(container).textContent.trim()).toBe("B");

      keydown(container.querySelector('input[type="text"]'), "Enter");
      await waitFor(() => container.querySelector("details").open === false);
      expect(selections).toEqual([["model", "b"]]);
    } finally {
      render(null, container);
      container.remove();
    }
  });

  test("supports filtering and keyboard selection in the block variant", async () => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const selections = [];
    const mount = () =>
      render(
        h(ConfigOptionSelect, {
          configOption: {
            id: "model",
            name: "Model",
            category: "model",
            current_value: "b",
            options: [
              {
                value: "a",
                name: "A",
                description: "Description A",
              },
              {
                value: "b",
                name: "B",
                description: "Description B",
              },
              {
                value: "c",
                name: "C",
                description: "Description C",
              },
            ],
          },
          showDescription: true,
          onSetConfigOption: (id, value) => selections.push([id, value]),
        }),
        container,
      );

    try {
      mount();
      const input = await openModelMenu(container);
      expect(container.querySelector(".dropdown-content").classList).toContain(
        "w-full",
      );
      expect(container.textContent).toContain("Description B");

      typeFilter(input, "c");
      await waitFor(() => rowButtons(container).length === 1);
      keydown(input, "Enter");
      await waitFor(() => container.querySelector("details").open === false);
      expect(selections).toEqual([["model", "c"]]);
      expect(container.querySelector("summary").textContent).toContain("C");
      expect(container.textContent).toContain("Description C");
    } finally {
      render(null, container);
      container.remove();
    }
  });

  test("keeps mode selectors unchanged and without search", async () => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const selections = [];
    const mount = () =>
      render(
        h(ConfigOptionSelect, {
          configOption: {
            id: "mode",
            name: "Mode",
            category: "mode",
            current_value: "a",
            options: [
              { value: "c", name: "C" },
              { value: "a", name: "A" },
              { value: "b", name: "B" },
            ],
          },
          onSetConfigOption: (id, value) => selections.push([id, value]),
        }),
        container,
      );

    try {
      mount();
      await openMenu(container);
      expect(container.querySelector('input[type="text"]')).toBeNull();
      expect(container.querySelector('[data-testid$="-filter"]')).toBeNull();
      const list = container.querySelector("ul");
      expect(list.classList).toContain("overflow-y-auto");
      expect(list.classList).toContain("flex-nowrap");
      expect(rowNames(container)).toEqual(["C", "A", "B"]);

      keydown(container.querySelector("summary"), "ArrowDown");
      await Promise.resolve();
      expect(highlightedButton(container)).toBeNull();

      rowButtons(container)[1].click();
      expect(selections).toEqual([["mode", "a"]]);
    } finally {
      render(null, container);
      container.remove();
    }
  });

  test("confirms a model selected with Enter without a toast", async () => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const selections = [];
    const toasts = [];
    const mount = (currentValue) =>
      render(
        h(ConfigOptionSelect, {
          configOption: {
            id: "model",
            name: "Model",
            category: "model",
            current_value: currentValue,
            options: [
              { value: "a", name: "A" },
              { value: "b", name: "B" },
            ],
          },
          onSetConfigOption: (id, value) => selections.push([id, value]),
          onShowToast: (toast) => toasts.push(toast),
        }),
        container,
      );

    try {
      mount("a");
      const input = await openModelMenu(container);
      keydown(input, "ArrowDown");
      await Promise.resolve();
      keydown(input, "Enter");
      await waitFor(() => container.querySelector("details").open === false);
      expect(selections).toEqual([["model", "b"]]);
      expect(activeButton(container).textContent.trim()).toBe("B");

      mount("b");
      await new Promise((resolve) => setTimeout(resolve, 20));
      expect(toasts).toEqual([]);
      expect(activeButton(container).textContent.trim()).toBe("B");
    } finally {
      render(null, container);
      container.remove();
    }
  });

  test("rolls back a model selected with Enter when the server rejects it", async () => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const selections = [];
    const toasts = [];
    const mount = (currentValue) =>
      render(
        h(ConfigOptionSelect, {
          configOption: {
            id: "model",
            name: "Model",
            category: "model",
            current_value: currentValue,
            options: [
              { value: "a", name: "A" },
              { value: "b", name: "B" },
            ],
          },
          onSetConfigOption: (id, value) => selections.push([id, value]),
          onShowToast: (toast) => toasts.push(toast),
        }),
        container,
      );

    try {
      mount("a");
      const input = await openModelMenu(container);
      keydown(input, "ArrowDown");
      await Promise.resolve();
      keydown(input, "Enter");
      await waitFor(() => container.querySelector("details").open === false);
      expect(selections).toEqual([["model", "b"]]);
      expect(activeButton(container).textContent.trim()).toBe("B");

      mount("a");
      await waitFor(
        () => activeButton(container)?.textContent.trim() === "A",
      );
      expect(toasts).toHaveLength(1);
      expect(toasts[0].style).toBe("error");
      expect(toasts[0].message).toContain("Model");
    } finally {
      render(null, container);
      container.remove();
    }
  });

  test("renders no search or rows while streaming-disabled", async () => {
    const container = document.createElement("div");
    document.body.appendChild(container);

    try {
      render(
        h(ConfigOptionSelect, {
          configOption: {
            id: "model",
            name: "Model",
            category: "model",
            current_value: "a",
            options: [
              { value: "a", name: "A" },
              { value: "b", name: "B" },
            ],
          },
          disableWhileStreaming: true,
          isStreaming: true,
        }),
        container,
      );
      const details = container.querySelector("details");
      details.open = true;
      details.dispatchEvent(new Event("toggle", { bubbles: true }));
      await waitFor(
        () =>
          details.open === true &&
          container
            .querySelector("summary")
            .classList.contains("pointer-events-none"),
      );
      expect(container.querySelector('input[type="text"]')).toBeNull();
      expect(rowButtons(container)).toEqual([]);
    } finally {
      render(null, container);
      container.remove();
    }
  });

  test("controlled open prop drives the menu and every close path notifies onOpenChange(false)", async () => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const options = [
      { value: "a", name: "A" },
      { value: "b", name: "B" },
    ];
    const selections = [];
    const changes = [];
    let parentOpen = false;
    // Simulates a controlling parent (ChatInput): onOpenChange notifications
    // flow back into the open prop on the next render.
    const handleOpenChange = (next) => {
      changes.push(next);
      parentOpen = next;
      mount();
    };
    const mount = () =>
      render(
        h(ConfigOptionSelect, {
          configOption: {
            id: "model",
            name: "Model",
            category: "model",
            current_value: "a",
            options,
          },
          onSetConfigOption: (id, value) => selections.push([id, value]),
          open: parentOpen,
          onOpenChange: handleOpenChange,
        }),
        container,
      );

    try {
      mount();
      expect(container.querySelector("details").open).toBe(false);

      // Reopening helper: waits for the filter to be focused AND the default
      // highlight to commit — both happen inside/after the open effect —
      // guaranteeing all of the commit's effects (including the doc-level
      // mousedown/Escape listeners, the exclusive-picker registration and
      // the searchable-menu selection state) have flushed before we proceed.
      const reopen = async () => {
        parentOpen = true;
        mount();
        const el = container.querySelector('input[type="text"]');
        await waitFor(() => container.querySelector("details").open === true);
        await waitFor(() => el && document.activeElement === el);
        await waitFor(
          () => container.querySelector("ul button.picker-row--highlighted"),
        );
        return el;
      };

      // Parent flipping open to true opens the menu: the open effect resets
      // the filter and focuses it.
      const input = await reopen();
      expect(input.value).toBe("");

      // Escape inside the filter (useSearchableMenu onClose path) notifies
      // the parent, which re-renders closed; the trigger regains focus.
      keydown(input, "Escape");
      await waitFor(() => container.querySelector("details").open === false);
      expect(document.activeElement).toBe(container.querySelector("summary"));
      expect(changes).toEqual([false]);

      // Outside mousedown notifies the parent too.
      await reopen();
      document.body.dispatchEvent(
        new MouseEvent("mousedown", { bubbles: true }),
      );
      await waitFor(() => container.querySelector("details").open === false);
      expect(changes).toEqual([false, false]);

      // Selecting an option notifies the parent and reports the selection.
      const reopenInput = await reopen();
      keydown(reopenInput, "Enter");
      await waitFor(() => container.querySelector("details").open === false);
      expect(selections).toEqual([["model", "a"]]);
      expect(changes).toEqual([false, false, false]);

      // The exclusive-picker registry close also notifies the parent.
      await reopen();
      const unregister = registerExclusivePicker(() => {});
      await waitFor(() => container.querySelector("details").open === false);
      expect(changes).toEqual([false, false, false, false]);
      unregister();

      // Only internal closes notified — programmatic parent-driven open
      // transitions never call onOpenChange with true.
      expect(changes.every((value) => value === false)).toBe(true);
    } finally {
      render(null, container);
      container.remove();
    }
  });

  test("uncontrolled callers without open/onOpenChange props keep the existing behaviour", async () => {
    const container = document.createElement("div");
    document.body.appendChild(container);

    try {
      render(
        h(ConfigOptionSelect, {
          configOption: {
            id: "model",
            name: "Model",
            category: "model",
            current_value: "b",
            options: [
              { value: "a", name: "A" },
              { value: "b", name: "B" },
            ],
          },
        }),
        container,
      );

      // Summary click opens and focuses the filter.
      const input = await openModelMenu(container);
      expect(container.querySelector("details").open).toBe(true);

      // Typing then reopening resets the filter, as before.
      typeFilter(input, "A");
      keydown(input, "Escape");
      await waitFor(() => container.querySelector("details").open === false);
      expect(document.activeElement).toBe(container.querySelector("summary"));

      const reopenedInput = await openModelMenu(container);
      expect(reopenedInput.value).toBe("");
      expect(container.querySelector("details").open).toBe(true);
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
