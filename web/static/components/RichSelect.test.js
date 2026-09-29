// Tests for RichSelect.js — real-import-with-stub pattern (mirrors
// CallbackTriggerSection.test.js) since RichSelect.js only needs
// hooks/html available on window.preact at module-load time.
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

import {
  Component,
  Fragment,
  h,
  render as preactRender,
} from "../vendor/preact.js";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "../vendor/preact-hooks.js";
import htmModule from "../vendor/htm.js";

const html = htmModule.bind(h);

let RichSelect;

beforeAll(async () => {
  const previousPreact = window.preact;
  window.preact = {
    Component,
    Fragment,
    h,
    render: preactRender,
    useCallback,
    useEffect,
    useLayoutEffect,
    useMemo,
    useRef,
    useState,
    html,
  };
  // Ensure the file actually exists (sanity check mirroring other suites).
  readFileSync(path.join(__dirname, "RichSelect.js"), "utf8");
  ({ RichSelect } = await import("./RichSelect.js?behavior-test"));
  window.preact = previousPreact;
});

let container;

function mount(props) {
  preactRender(h(RichSelect, props), container);
}

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  preactRender(null, container);
  container.remove();
});

const OPTIONS = [
  { value: "a", label: "Alpha" },
  { value: "b", label: "Beta" },
];

describe("RichSelect: disabled prop", () => {
  test("default (non-disabled) opens on click and has no aria-disabled", () => {
    mount({ value: "a", options: OPTIONS, onChange: () => {} });
    const summary = container.querySelector("summary");
    expect(summary.getAttribute("aria-disabled")).toBeNull();
    expect(summary.getAttribute("tabindex")).toBeNull();
    expect(summary.className).not.toMatch(/cursor-not-allowed/);

    summary.click();
    const details = container.querySelector("details");
    expect(details.open).toBe(true);
    expect(container.querySelector("ul")).not.toBeNull();
  });

  test("disabled prevents opening via click", () => {
    mount({
      value: "a",
      options: OPTIONS,
      onChange: () => {},
      disabled: true,
    });
    const summary = container.querySelector("summary");
    const details = container.querySelector("details");

    expect(summary.getAttribute("aria-disabled")).toBe("true");
    expect(summary.getAttribute("tabindex")).toBe("-1");
    expect(summary.className).toMatch(/cursor-not-allowed/);
    expect(summary.className).toMatch(/opacity-50/);
    expect(summary.className).toMatch(/pointer-events-none/);

    summary.click();
    expect(details.open).toBe(false);
    expect(container.querySelector("ul")).toBeNull();
  });

  test("disabled prevents opening via keyboard (Enter/Space)", () => {
    mount({
      value: "a",
      options: OPTIONS,
      onChange: () => {},
      disabled: true,
    });
    const summary = container.querySelector("summary");

    const enterEvent = new KeyboardEvent("keydown", {
      key: "Enter",
      bubbles: true,
      cancelable: true,
    });
    const preventDefaultSpy = jest.spyOn(enterEvent, "preventDefault");
    summary.dispatchEvent(enterEvent);
    expect(preventDefaultSpy).toHaveBeenCalled();

    const spaceEvent = new KeyboardEvent("keydown", {
      key: " ",
      bubbles: true,
      cancelable: true,
    });
    const spacePreventDefaultSpy = jest.spyOn(spaceEvent, "preventDefault");
    summary.dispatchEvent(spaceEvent);
    expect(spacePreventDefaultSpy).toHaveBeenCalled();

    expect(container.querySelector("details").open).toBe(false);
  });

  test("does not suppress the options list when not disabled", () => {
    mount({ value: "b", options: OPTIONS, onChange: () => {} });
    const ul = container.querySelector("ul");
    expect(ul).not.toBeNull();
    expect(ul.querySelectorAll("li").length).toBe(2);
  });
});
