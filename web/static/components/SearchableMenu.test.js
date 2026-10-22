import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { Fragment, h, render } from "../vendor/preact.js";
import * as hooks from "../vendor/preact-hooks.js";
import htm from "../vendor/htm.js";

if (process.env.MITTO_SEARCHABLE_MENU_TEST_CHILD === "1") {
  let SearchableMenu;
  let useSearchableMenu;
  let container;
  let apis;
  let accepts;
  let closes;
  let scrolls;
  const settle = () => new Promise((resolve) => setTimeout(resolve, 100));
  const items = [{ label: "Alpha" }, { label: "Beta" }, { label: "Gamma" }];
  beforeAll(async () => {
    const previous = window.preact;
    window.preact = { ...hooks, Fragment, h, render, html: htm.bind(h) };
    ({ SearchableMenu } = await import("./SearchableMenu.js"));
    ({ useSearchableMenu } = await import("../hooks/useSearchableMenu.js"));
    window.preact = previous;
  });
  beforeEach(() => {
    apis = {};
    accepts = [];
    closes = [];
    scrolls = [];
    container = document.createElement("div");
    document.body.appendChild(container);
  });
  afterEach(() => {
    render(null, container);
    container.remove();
  });

  // The consumer owns filtering, render-index clamping, focus and reset policy.
  function Harness({ values = items, id = "one", footer = true }) {
    const [filterText, setFilterText] = hooks.useState("");
    const inputRef = hooks.useRef(null);
    const filtered = values.filter((item) => item.label.toLowerCase().includes(filterText.toLowerCase()));
    const navigation = useSearchableMenu({ items: filtered, onAccept: (...args) => accepts.push(args), onClose: () => closes.push(id) });
    apis[id] = { ...navigation, inputRef };
    const effectiveIndex = filtered.length ? Math.min(navigation.selectedIndex, filtered.length - 1) : -1;
    return h(SearchableMenu, {
      items: filtered,
      filterText,
      onFilterChange: (value) => { setFilterText(value); navigation.resetSelection(); },
      onFilterKeyDown: navigation.handleKeyDown,
      filterInputRef: inputRef,
      placeholder: "Find items",
      emptyText: "Nothing here",
      filterTestId: `${id}-filter`,
      listTestId: `${id}-list`,
      footer: footer ? h("button", { "data-footer": id }, "Footer action") : null,
    }, h("li", { class: "heading" }, "Non-selectable heading"), ...filtered.map((item, index) => h("li", { key: item.label }, h("button", {
      "data-item": item.label,
      ref: (element) => {
        if (element) element.scrollIntoView = (options) => scrolls.push([item, options]);
        if (index === effectiveIndex) navigation.selectedItemRef.current = element;
      },
    }, item.label))));
  }
  const mount = (props = {}) => render(h(Harness, props), container);
  const key = async (value, id = "one", shiftKey = false) => {
    const event = new KeyboardEvent("keydown", { key: value, shiftKey, bubbles: true, cancelable: true });
    apis[id].inputRef.current.dispatchEvent(event);
    await settle();
    return event;
  };

  test("controlled input, accessible name, IDs, native list, ref and exact chrome without autofocus", async () => {
    mount();
    const input = apis.one.inputRef.current;
    expect(input).toBe(container.querySelector('[data-testid="one-filter"]'));
    expect(input.placeholder).toBe("Find items");
    expect(input.getAttribute("aria-label")).toBe("Find items");
    expect(document.activeElement).not.toBe(input);
    expect(input.parentElement.className).toBe("px-2 pt-2 pb-1 shrink-0");
    expect(input.className).toBe("input input-sm w-full");
    for (const attr of ["autocomplete", "autocorrect", "autocapitalize"]) expect(input.getAttribute(attr)).toBe("off");
    expect(input.getAttribute("spellcheck")).toBeNull();
    const list = container.querySelector('[data-testid="one-list"]');
    expect(list.className).toBe("px-2 py-1 overflow-y-auto flex-1 min-h-0");
    expect(list.getAttribute("style")).toContain("scrollbar-gutter: stable");
    expect(list.querySelector("ul").className).toBe("flex flex-col w-full p-0 m-0 list-none");
    expect(container.querySelector('[role="listbox"], [role="option"]')).toBeNull();
    input.value = "bet";
    input.dispatchEvent(new Event("input", { bubbles: true }));
    await settle();
    expect(input.value).toBe("bet");
    expect([...container.querySelectorAll("[data-item]")].map((el) => el.textContent)).toEqual(["Beta"]);
    expect(apis.one.selectedIndex).toBe(-1);
  });

  test("empty detection and optional footer work with or without matches", () => {
    mount();
    expect(container.textContent).not.toContain("Nothing here");
    expect(container.querySelector("[data-footer]")).not.toBeNull();
    mount({ values: [] });
    expect(container.textContent).toContain("Nothing here");
    expect(container.querySelector("[data-footer]")).not.toBeNull();
    mount({ values: [], footer: false });
    expect(container.querySelector(".border-t")).toBeNull();
    mount({ footer: false });
    expect(container.querySelector(".border-t")).toBeNull();
  });

  test("all -1/first/last bounds, no wrap, headings ignored, Enter event and two-argument acceptance", async () => {
    mount();
    expect(apis.one.selectedIndex).toBe(-1);
    await key("Enter");
    expect(accepts).toHaveLength(0);
    await key("ArrowUp");
    expect(apis.one.selectedIndex).toBe(-1);
    await key("ArrowDown");
    expect(apis.one.selectedIndex).toBe(0);
    await key("ArrowUp");
    expect(apis.one.selectedIndex).toBe(-1);
    for (let i = 0; i < 4; i++) await key("ArrowDown");
    expect(apis.one.selectedIndex).toBe(2);
    const event = await key("Enter", "one", true);
    expect(accepts).toEqual([[items[2], event]]);
    expect(accepts[0][1].shiftKey).toBe(true);
    await key("ArrowUp");
    expect(apis.one.selectedIndex).toBe(1);
  });

  test("recognized keys prevent default, all keys stop propagation, Escape closes without focus return", async () => {
    mount();
    const bubbled = [];
    container.addEventListener("keydown", (event) => bubbled.push(event.key));
    const input = apis.one.inputRef.current;
    input.focus();
    for (const value of ["ArrowDown", "ArrowUp", "Enter", "Escape"]) expect((await key(value)).defaultPrevented).toBe(true);
    for (const value of ["Tab", "x", "Shift"]) expect((await key(value)).defaultPrevented).toBe(false);
    expect(bubbled).toEqual([]);
    expect(closes).toEqual(["one"]);
    expect(document.activeElement).toBe(input);
  });

  test("empty navigation never accepts undefined", async () => {
    mount({ values: [] });
    await key("ArrowDown");
    await key("ArrowUp");
    await key("Enter");
    expect(apis.one.selectedIndex).toBe(-1);
    apis.one.highlightIndex(4);
    await settle();
    await key("Enter");
    expect(accepts).toEqual([]);
  });

  test("scroll only follows raw index changes; shrink clamps rendering/acceptance without normalizing state", async () => {
    mount();
    await settle();
    expect(scrolls).toEqual([]);
    await key("ArrowDown");
    expect(scrolls).toEqual([[items[0], { block: "nearest" }]]);
    apis.one.highlightIndex(2);
    await settle();
    expect(scrolls.at(-1)).toEqual([items[2], { block: "nearest" }]);
    scrolls.length = 0;
    mount({ values: items.slice(0, 1) });
    await settle();
    expect(apis.one.selectedIndex).toBe(2);
    expect(apis.one.selectedItemRef.current.textContent).toBe("Alpha");
    expect(scrolls).toEqual([]);
    await key("Enter");
    expect(accepts[0][0]).toBe(items[0]);
    await key("ArrowUp");
    expect(apis.one.selectedIndex).toBe(1);
    await key("ArrowDown");
    expect(apis.one.selectedIndex).toBe(0);
    apis.one.resetSelection();
    await settle();
    expect(apis.one.selectedIndex).toBe(-1);
  });

  test("filter edits reset highlight, remount starts at -1 and multiple instances are independent", async () => {
    render(h(Fragment, null, h(Harness), h(Harness, { id: "two" })), container);
    await key("ArrowDown");
    expect(apis.one.selectedIndex).toBe(0);
    expect(apis.two.selectedIndex).toBe(-1);
    expect(apis.one.inputRef.current).not.toBe(apis.two.inputRef.current);
    apis.one.inputRef.current.value = "Beta";
    apis.one.inputRef.current.dispatchEvent(new Event("input", { bubbles: true }));
    await settle();
    expect(apis.one.selectedIndex).toBe(-1);
    expect(container.querySelectorAll('[data-testid$="-filter"]')).toHaveLength(2);
    render(null, container);
    mount();
    expect(apis.one.selectedIndex).toBe(-1);
    expect(apis.one.inputRef.current.value).toBe("");
  });
} else {
  test("SearchableMenu and actual navigation hook run in an isolated happy-dom process", () => {
    const result = spawnSync(process.execPath, ["test", fileURLToPath(import.meta.url)], { encoding: "utf8", env: { ...process.env, MITTO_SEARCHABLE_MENU_TEST_CHILD: "1" }, timeout: 30_000 });
    if (result.status !== 0) throw new Error(`Isolated SearchableMenu tests failed:\n${result.stdout}\n${result.stderr}`);
  });
}
