import { describe, expect, jest, test } from "../utils/testing/testGlobals.js";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const childRun = process.env.MITTO_BEADS_TAB_TEST_CHILD === "1";

if (childRun) {
  const preact = await import("../vendor/preact.js");
  const hooks = await import("../vendor/preact-hooks.js");
  const htm = (await import("../vendor/htm.js")).default;
  const html = htm.bind(preact.h);
  const previousPreact = window.preact;
  window.preact = { ...preact, ...hooks, html };
  const { WorkspaceFolderBeadsTab } =
    await import("./WorkspaceFolderBeadsTab.js?mitto-wx5t-3-mounted-tests");
  window.preact = previousPreact;

  function props(overrides = {}) {
    return {
      beads: {
        beadsConfig: {},
        beadsConfigLoading: false,
        beadsConfigError: "",
        beadsConfigSaving: false,
        newBeadsKey: "",
        newBeadsValue: "",
        beadsUpstream: "none",
        beadsUpstreamSaving: false,
        beadsUpstreamPrompts: [],
        beadsUpstreamPromptsLoading: false,
        beadsPullPrompt: "",
        beadsPushPrompt: "",
        beadsSyncPrompt: "",
        beadsPullPromptArgs: {},
        beadsPushPromptArgs: {},
        beadsSyncPromptArgs: {},
        beadsDatabaseMode: "local",
        beadsDatabaseModeHasRemote: false,
        beadsDatabaseModeLoading: false,
        beadsDatabaseModeSaving: false,
        beadsDatabaseModeError: "",
        beadsDatabaseModeErrorStderr: "",
        ...overrides,
      },
      beadsSetters: {
        setNewBeadsKey: jest.fn(),
        setNewBeadsValue: jest.fn(),
      },
      beadsHandlers: {
        setBeadsConfigKey: jest.fn(),
        unsetBeadsConfigKey: jest.fn(),
        saveBeadsDatabaseMode: jest.fn(),
        saveBeadsUpstream: jest.fn(),
        saveBeadsPromptName: jest.fn(),
        saveBeadsPromptArgs: jest.fn(),
      },
      taskLabelColors: { entries: [], loading: false, error: "" },
      taskLabelColorsHandlers: {
        onAdd: jest.fn(),
        onUpdate: jest.fn(),
        onRemove: jest.fn(),
        onMove: jest.fn(),
      },
    };
  }

  function mount(componentProps) {
    const container = document.createElement("div");
    document.body.appendChild(container);
    preact.render(
      html`<${WorkspaceFolderBeadsTab} ...${componentProps} />`,
      container,
    );
    return container;
  }

  function unmount(container) {
    preact.render(null, container);
    container.remove();
  }

  const normalizedText = (node) => node.textContent.replace(/\s+/g, " ").trim();

  describe("WorkspaceFolderBeadsTab database sharing", () => {
    test("defaults to local and disables unavailable shared mode", () => {
      const p = props({ beadsDatabaseMode: undefined });
      const container = mount(p);
      try {
        const select = container.querySelector(
          '[data-testid="beads-database-mode-select"]',
        );
        expect(select.value).toBe("local");
        expect(select.options[0].textContent).toContain("Local only");
        expect(select.options[1].textContent).toContain("Dolt remote");
        expect(select.options[1].disabled).toBe(true);
        const effective = normalizedText(
          container.querySelector(
            '[data-testid="beads-database-mode-effective"]',
          ),
        );
        expect(effective).toContain("Effective:");
        expect(effective).toContain("Local only");
      } finally {
        unmount(container);
      }
    });

    test("switches independently between shared and local", () => {
      const p = props({ beadsDatabaseModeHasRemote: true });
      const container = mount(p);
      try {
        const select = container.querySelector(
          '[data-testid="beads-database-mode-select"]',
        );
        select.value = "shared";
        select.dispatchEvent(new Event("input", { bubbles: true }));
        select.value = "local";
        select.dispatchEvent(new Event("input", { bubbles: true }));
        expect(p.beadsHandlers.saveBeadsDatabaseMode.mock.calls).toEqual([
          ["shared"],
          ["local"],
        ]);
        expect(p.beadsHandlers.saveBeadsUpstream).not.toHaveBeenCalled();
      } finally {
        unmount(container);
      }
    });

    test("shows saving and reconciliation errors", () => {
      const p = props({
        beadsDatabaseModeSaving: true,
        beadsDatabaseModeError: "bd reconciliation failed",
      });
      const container = mount(p);
      try {
        expect(
          container.querySelector('[data-testid="beads-database-mode-select"]')
            .disabled,
        ).toBe(true);
        expect(
          container.querySelector('[data-testid="beads-database-mode-saving"]'),
        ).not.toBeNull();
        expect(
          container.querySelector('[data-testid="beads-database-mode-error"]')
            .textContent,
        ).toContain("bd reconciliation failed");
      } finally {
        unmount(container);
      }
    });

    test("reports a configured remote as dormant without exposing details", () => {
      const p = props({ beadsDatabaseModeHasRemote: true });
      const container = mount(p);
      try {
        const notice = container.querySelector(
          '[data-testid="beads-database-mode-dormant-remote"]',
        );
        expect(normalizedText(notice)).toContain("configured but dormant");
        expect(normalizedText(notice)).toContain("without pushing, pulling");
        expect(normalizedText(notice)).not.toMatch(/https?:\/\//);
      } finally {
        unmount(container);
      }
    });

    test("keeps Upstream Tasks independently selectable", () => {
      const p = props({ beadsUpstream: "github" });
      const container = mount(p);
      try {
        const selects = container.querySelectorAll("select");
        expect(selects[0].value).toBe("local");
        expect(selects[1].value).toBe("github");
        selects[1].value = "jira";
        selects[1].dispatchEvent(new Event("input", { bubbles: true }));
        expect(p.beadsHandlers.saveBeadsUpstream).toHaveBeenCalledWith("jira");
        expect(p.beadsHandlers.saveBeadsDatabaseMode).not.toHaveBeenCalled();
        expect(normalizedText(container)).toContain(
          "Upstream Tasks below independently controls",
        );
      } finally {
        unmount(container);
      }
    });
  });
} else {
  describe("WorkspaceFolderBeadsTab", () => {
    test("passes mounted database-sharing acceptance tests", () => {
      const result = spawnSync(
        process.execPath,
        ["test", fileURLToPath(import.meta.url)],
        {
          encoding: "utf8",
          env: { ...process.env, MITTO_BEADS_TAB_TEST_CHILD: "1" },
          timeout: 30_000,
        },
      );
      if (result.status !== 0) {
        throw new Error(
          `Isolated Beads tab tests failed:\n${result.stdout}\n${result.stderr}`,
        );
      }
    });
  });
}
