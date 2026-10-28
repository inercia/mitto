import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { Fragment, h, render } from "../vendor/preact.js";
import * as hooks from "../vendor/preact-hooks.js";
import htm from "../vendor/htm.js";
import { flattenPrompts } from "../utils/prompts.js";

if (process.env.MITTO_PROMPTS_MENU_TEST_CHILD === "1") {
  let PromptsMenu;
  let container;
  const tick = () => new Promise((resolve) => setTimeout(resolve, 0));
  const prompts = [
    { name: "Zulu", group: "Work", source: "workspace", backgroundColor: "#ff0000", preferredModels: [{ modelName: "Beta" }] },
    { name: "Alpha", group: "Work", source: "file", description: "Find this description", loop: { mode: "optional", default: false } },
    { name: "Always", source: "settings", loop: true },
  ];
  beforeAll(async () => {
    const previous = window.preact;
    window.preact = { ...hooks, Fragment, h, render, html: htm.bind(h) };
    ({ PromptsMenu } = await import("./PromptsMenu.js"));
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
  const mount = (props = {}) => render(h(PromptsMenu, { prompts, ...props }), container);
  const rows = () => [...container.querySelectorAll("button.prompt-item")];

  test("native grouped render order, chosen name and keyboard highlight remain distinct", () => {
    const ref = { current: null };
    mount({ selectedName: "Zulu", selectedIndex: 0, selectedItemRef: ref });
    expect(rows().map((row) => row.title)).toEqual(flattenPrompts(prompts).flat.map((p) => p.description || p.name));
    expect([...container.querySelectorAll(".menu-title")].map((el) => el.textContent.trim())).toEqual(["Work", "Other"]);
    expect(container.querySelectorAll(".menu-title button").length).toBe(0);
    expect(ref.current).toBe(rows()[0]);
    expect(rows()[0].getAttribute("aria-selected")).toBe("false");
    expect(rows()[1].getAttribute("aria-selected")).toBe("true");
    expect(rows()[1].querySelector('[fill-rule="evenodd"]')).not.toBeNull();
    expect(rows()[0].style.boxShadow).toContain("inset");
    expect(container.querySelector('[role="listbox"], [role="option"]')).toBeNull();
  });

  test("shell retains controlled filter, refs, flags, empty state and interactive footer", () => {
    const ref = { current: null };
    const values = [];
    let configured = 0;
    const props = { filterInputRef: ref, filterText: "Alpha", onFilterChange: (value) => values.push(value), filterTestId: "filter", listTestId: "list", placeholder: "Find prompts", footer: h("button", { onClick: () => configured++ }, "Configure") };
    mount(props);
    expect(ref.current).toBe(container.querySelector('[data-testid="filter"]'));
    expect(ref.current.value).toBe("Alpha");
    expect(ref.current.placeholder).toBe("Find prompts");
    for (const attr of ["autocomplete", "autocorrect", "autocapitalize"]) expect(ref.current.getAttribute(attr)).toBe("off");
    // Vendored Preact omits a false lowercase boolean attribute in happy-dom.
    expect(ref.current.getAttribute("spellcheck")).toBeNull();
    expect(container.querySelector('[data-testid="list"]').getAttribute("style")).toContain("scrollbar-gutter: stable");
    ref.current.value = "different";
    ref.current.dispatchEvent(new Event("input", { bubbles: true }));
    expect(values).toEqual(["different"]);
    container.querySelector("button:not(.prompt-item)").click();
    expect(configured).toBe(1);
    mount({ ...props, filterText: "missing" });
    expect(rows()).toHaveLength(0);
    expect(container.textContent).toContain("No matching prompts");
    expect(container.textContent).toContain("Configure");
    mount({ filterText: "missing" });
    expect(container.querySelector(".border-t")).toBeNull();
  });

  test("colors, Shift pencil, W/F/S badges and model override stay prompt-owned", () => {
    mount({ shiftHeld: true, showSourceBadge: true, modelOption: { current_value: "a", options: [{ value: "a", name: "Alpha" }, { value: "b", name: "Beta" }] }, modelProfiles: [{ name: "Beta", criteria: { matchMode: "contains", pattern: "Beta" } }] });
    expect(rows()[1].style.backgroundColor).toBe("#ff0000");
    expect(rows()[1].querySelector('path[d^="M11 5H6"]')).not.toBeNull();
    expect(container.querySelector('[title="Workspace prompt"]').textContent).toBe("W");
    expect(container.querySelector('[title="File-based prompt"]').textContent).toBe("F");
    expect(container.querySelector('[title="Settings prompt"]').textContent).toBe("S");
    expect(container.querySelector('[title^="Runs on Beta"]')).not.toBeNull();
    expect(container.querySelector('[title^="Loop prompt"]')).not.toBeNull();
  });

  test("checkbox actions do not select, override survives filtering, mouse preserves Shift and options", async () => {
    const calls = [];
    const props = { loopToggle: true, onSelect: (...args) => calls.push(args) };
    mount(props);
    const checkbox = rows()[0].querySelector("input");
    expect(checkbox.disabled).toBe(false);
    checkbox.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    checkbox.checked = true;
    checkbox.dispatchEvent(new Event("change", { bubbles: true }));
    await tick();
    expect(calls).toHaveLength(0);
    mount({ ...props, filterText: "Zulu" });
    mount(props);
    expect(rows()[0].querySelector("input").checked).toBe(true);
    const event = new MouseEvent("click", { bubbles: true, shiftKey: true });
    rows()[0].dispatchEvent(event);
    expect(calls[0]).toEqual([prompts[1], event, { asLoop: true }]);
    expect(calls[0][1].shiftKey).toBe(true);
    expect(rows()[2].querySelector("input").checked).toBe(true);
    expect(rows()[2].querySelector("input").disabled).toBe(true);
    rows()[1].click();
    expect(calls[1][2]).toEqual({ asLoop: undefined });
  });

  // mitto-byq: a prompt row must not nest interactive content inside its action
  // button, and both controls must carry an explicit accessible name.
  test("loop control is a sibling of the prompt action and both are named", () => {
    mount({ loopToggle: true, selectedName: "Zulu" });
    for (const row of rows()) {
      expect(row.querySelector("input, button, a, select, textarea")).toBeNull();
    }
    const controls = [...container.querySelectorAll(".prompt-loop-checkbox")];
    expect(controls).toHaveLength(2);
    expect(controls[0].closest("button")).toBeNull();
    expect(controls[0].closest("li")).not.toBeNull();
    expect(controls[0].getAttribute("aria-label")).toBe("Loop: Alpha");
    expect(controls[0].disabled).toBe(false);
    expect(controls[1].getAttribute("aria-label")).toBe("Always loops: Always");
    expect(controls[1].checked).toBe(true);
    expect(controls[1].disabled).toBe(true);
    expect(rows()[0].getAttribute("aria-label")).toBe("Zulu, selected");
    expect(rows()[1].getAttribute("aria-label")).toBe("Alpha, loop prompt");
  });

  // mitto-byq: chosen prompt and keyboard highlight are different states and
  // must be exposed through different, supported mechanisms.
  test("chosen prompt is aria-current while keyboard highlight stays visual", () => {
    mount({ selectedName: "Zulu", selectedIndex: 1 });
    expect(container.querySelectorAll("[aria-selected]")).toHaveLength(0);
    expect(
      rows().filter((row) => row.getAttribute("aria-current") === "true"),
    ).toHaveLength(1);
    expect(rows()[0].getAttribute("aria-current")).toBe("true");
    expect(rows()[1].getAttribute("aria-current")).toBeNull();
    expect(rows()[1].style.boxShadow).toContain("inset");
    expect(container.querySelector('[role="listbox"], [role="option"]')).toBeNull();
  });

  // mitto-byq: navigating the list must announce the highlighted prompt without
  // announcing it as chosen.
  test("highlighted prompt is announced by a polite status region", () => {
    mount({ selectedIndex: 1 });
    const status = container.querySelector('[role="status"]');
    expect(status).not.toBeNull();
    expect(status.getAttribute("aria-live")).toBe("polite");
    expect(status.getAttribute("aria-atomic")).toBe("true");
    expect(status.className).toContain("sr-only");
    expect(status.textContent).toBe("Alpha, 2 of 3");
    mount({ selectedIndex: -1 });
    expect(container.querySelector('[role="status"]').textContent).toBe("");
    mount({});
    expect(container.querySelector('[role="status"]').textContent).toBe("");
    mount({ selectedIndex: 99 });
    expect(container.querySelector('[role="status"]').textContent).toBe(
      "Always, 3 of 3",
    );
    mount({ selectedIndex: 0, filterText: "missing" });
    expect(container.querySelector('[role="status"]').textContent).toBe("");
  });
} else {
  test("PromptsMenu mounted behavior runs in an isolated happy-dom process", () => {
    const result = spawnSync(process.execPath, ["test", fileURLToPath(import.meta.url)], { encoding: "utf8", env: { ...process.env, MITTO_PROMPTS_MENU_TEST_CHILD: "1" }, timeout: 30_000 });
    if (result.status !== 0) throw new Error(`Isolated PromptsMenu tests failed:\n${result.stdout}\n${result.stderr}`);
  });
}
