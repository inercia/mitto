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
