import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { Fragment, h, render } from "../vendor/preact.js";
import * as hooks from "../vendor/preact-hooks.js";
import htm from "../vendor/htm.js";

if (process.env.MITTO_LOOP_PROMPT_SELECTOR_TEST_CHILD === "1") {
  let LoopPromptSelector;
  let container;
  // Preact passive effects run after paint; allow the real focus/outside-click effects to run.
  const settle = () => new Promise((resolve) => setTimeout(resolve, 100));
  const prompts = [{ name: "Alpha", description: "First description" }, { name: "Beta" }];
  beforeAll(async () => {
    const previous = window.preact;
    window.preact = { ...hooks, Fragment, h, render, html: htm.bind(h) };
    ({ LoopPromptSelector } = await import("./LoopPromptSelector.js"));
    window.preact = previous;
  });
  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
  });
  afterEach(() => {
    render(null, container);
    container.remove();
  });
  const mount = (props = {}) => render(h(LoopPromptSelector, { prompts, ...props }), container);

  test("external focus, description filtering, name selection, close and reopen reset", async () => {
    const calls = [];
    mount({ selectedPromptName: "Beta", onSelect: (...args) => calls.push(args) });
    container.querySelector("button").click();
    await settle();
    let input = container.querySelector("input");
    expect(document.activeElement).toBe(input);
    expect(container.querySelector('[aria-current="true"]').textContent).toContain("Beta");
    input.value = "description";
    input.dispatchEvent(new Event("input", { bubbles: true }));
    await settle();
    expect(container.querySelectorAll(".prompt-item")).toHaveLength(1);
    container.querySelector(".prompt-item").click();
    await settle();
    expect(calls).toEqual([["Alpha"]]);
    expect(container.querySelector("input")).toBeNull();
    container.querySelector("button").click();
    await settle();
    input = container.querySelector("input");
    expect(input.value).toBe("");
    document.body.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
    await settle();
    expect(container.querySelector("input")).toBeNull();
  });

  test("no newly enabled navigation or hover selection; Tab and key propagation stay native", async () => {
    const calls = [];
    mount({ onSelect: (...args) => calls.push(args) });
    container.querySelector("button").click();
    await settle();
    const input = container.querySelector("input");
    const bubbled = [];
    container.addEventListener("keydown", (event) => bubbled.push(event.key));
    for (const key of ["ArrowDown", "ArrowUp", "Enter", "Escape", "Tab"]) {
      const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true });
      input.dispatchEvent(event);
      expect(event.defaultPrevented).toBe(false);
    }
    container.querySelector(".prompt-item").dispatchEvent(new MouseEvent("mouseover", { bubbles: true }));
    await settle();
    expect(bubbled).toEqual(["ArrowDown", "ArrowUp", "Enter", "Escape", "Tab"]);
    expect(calls).toHaveLength(0);
    expect(container.querySelector("input")).toBe(input);
    expect([...container.querySelectorAll(".prompt-item")].every((row) => !row.style.boxShadow)).toBe(true);
  });

  test("multiple instances retain distinct refs, focus and test IDs", async () => {
    render(h(Fragment, null, h(LoopPromptSelector, { prompts, idPrefix: "header" }), h(LoopPromptSelector, { prompts, idPrefix: "mobile" })), container);
    container.querySelector('[data-testid="header-button"]').click();
    await settle();
    const first = container.querySelector('[data-testid="header-search"]');
    expect(document.activeElement).toBe(first);
    container.querySelector('[data-testid="mobile-button"]').click();
    await settle();
    const second = container.querySelector('[data-testid="mobile-search"]');
    expect(document.activeElement).toBe(second);
    expect(first).not.toBe(second);
    expect(container.querySelectorAll('[data-testid$="-list"]')).toHaveLength(2);
  });
} else {
  test("LoopPromptSelector mounted behavior runs in an isolated happy-dom process", () => {
    const result = spawnSync(process.execPath, ["test", fileURLToPath(import.meta.url)], { encoding: "utf8", env: { ...process.env, MITTO_LOOP_PROMPT_SELECTOR_TEST_CHILD: "1" }, timeout: 30_000 });
    if (result.status !== 0) throw new Error(`Isolated LoopPromptSelector tests failed:\n${result.stdout}\n${result.stderr}`);
  });
}
