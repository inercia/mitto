// Tests for RichSelect.js — real-import-with-stub pattern (mirrors
// CallbackTriggerSection.test.js) since RichSelect.js only needs
// hooks/html available on window.preact at module-load time.
import { readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
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
import { buildAutoChildAgentOptions } from "../utils/autoChildAgentOptions.js";

const html = htmModule.bind(h);

let RichSelect;

if (process.env.MITTO_RICH_SELECT_TEST_CHILD === "1") {
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

    test("agent options select a workspace UUID or the dynamic current-agent sentinel", () => {
      const workspaces = [
        {
          uuid: "current",
          working_dir: "/repo",
          acp_server: "Claude",
          name: "Project",
        },
        {
          uuid: "other",
          working_dir: "/repo",
          acp_server: "Auggie",
          name: "Project",
        },
      ];
      const { options } = buildAutoChildAgentOptions(workspaces, "current", "");
      let selected = "";
      const onChange = (value) => {
        selected = value;
        mount({ value: selected, options, onChange, ariaLabel: "Agent" });
      };
      mount({ value: selected, options, onChange, ariaLabel: "Agent" });
      expect(
        container.querySelector("summary").getAttribute("aria-label"),
      ).toBe("Agent");
      expect(container.querySelector("summary").textContent).toContain(
        "Current agent",
      );
      container.querySelectorAll("ul button")[1].click();
      expect(selected).toBe("other");
      expect(container.querySelector("summary").textContent).toContain(
        "Auggie",
      );
      container.querySelectorAll("ul button")[0].click();
      expect(selected).toBe("");
      expect(container.querySelector("summary").textContent).toContain(
        "Current agent",
      );
    });
  });
} else {
  test("RichSelect mounted behavior runs in an isolated happy-dom process", () => {
    const result = spawnSync(
      process.execPath,
      ["test", fileURLToPath(import.meta.url)],
      {
        encoding: "utf8",
        env: { ...process.env, MITTO_RICH_SELECT_TEST_CHILD: "1" },
        timeout: 30_000,
      },
    );
    if (result.status !== 0) {
      throw new Error(
        `Isolated RichSelect tests failed:\n${result.stdout}\n${result.stderr}`,
      );
    }
  });
}
