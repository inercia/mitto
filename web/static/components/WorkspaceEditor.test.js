import { describe, expect, jest, test } from "../utils/testing/testGlobals.js";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

if (process.env.MITTO_WORKSPACE_EDITOR_TEST_CHILD === "1") {
  const preact = await import("../vendor/preact.js");
  const hooks = await import("../vendor/preact-hooks.js");
  const htm = (await import("../vendor/htm.js")).default;
  const html = htm.bind(preact.h);
  const previousPreact = window.preact;
  window.preact = { ...preact, ...hooks, html };
  const { WorkspaceEditor } =
    await import("./WorkspaceEditor.js?mitto-models-mounted-tests");
  window.preact = previousPreact;

  const defaultProps = (overrides = {}) => ({
    activeTab: "models",
    setActiveTab: jest.fn(),
    workspaceTabs: [{ id: "models", label: "Models" }],
    selectedWorkspace: { uuid: "workspace-1" },
    getWorkspaceKey: () => "workspace-1",
    sortedAcpServers: [],
    acpServers: [],
    supportedRunners: [],
    modelProfiles: [{ tags: ["Coding"] }],
    effectiveConfig: {},
    editAcpServer: "opencode",
    setEditAcpServer: jest.fn(),
    editModelOverrides: [
      { acp_server: "opencode", tag: "Coding", model_id: "retired-model" },
    ],
    setEditModelOverrides: jest.fn(),
    modelsData: { acp_server: "opencode", models: [], live: false },
    modelsLoading: false,
    modelsError: "",
    loadModels: jest.fn(),
    editInitialModelProfile: "",
    setEditInitialModelProfile: jest.fn(),
    editInitialModelTag: "",
    setEditInitialModelTag: jest.fn(),
    editAuxModelProfile: "",
    setEditAuxModelProfile: jest.fn(),
    editAuxModelTag: "",
    setEditAuxModelTag: jest.fn(),
    setEditAuxModelConstraintCleared: jest.fn(),
    editAutoApprove: false,
    setEditAutoApprove: jest.fn(),
    editIsDefault: false,
    handleToggleIsDefault: jest.fn(),
    editRunner: "exec",
    handleRunnerChange: jest.fn(),
    editRunnerConfig: null,
    setEditRunnerConfig: jest.fn(),
    ...overrides,
  });

  function mount(props) {
    const container = document.createElement("div");
    document.body.appendChild(container);
    preact.render(html`<${WorkspaceEditor} ...${props} />`, container);
    return container;
  }

  function unmount(container) {
    preact.render(null, container);
    container.remove();
  }

  const settle = () => new Promise((resolve) => setTimeout(resolve, 120));
  const control = (container, kind, index = 0) =>
    container.querySelector(
      `[data-testid="ws-model-override-${kind}-${index}"]`,
    );
  const change = (element, value) => {
    if (element.type === "checkbox") element.checked = value;
    else element.value = value;
    element.dispatchEvent(new Event("change", { bubbles: true }));
  };
  const checkbox = (container, tag) =>
    [...container.querySelectorAll('input[type="checkbox"]')].find(
      (input) => input.parentElement.querySelector("span").textContent === tag,
    );

  function mountDraft(overrides = {}) {
    let currentRows;
    let updateProps;
    const props = defaultProps({
      modelProfiles: [
        { tags: ["Cheap", "Coding", "Fast", "Smart", "Smartest"] },
      ],
      modelsData: {
        acp_server: "opencode",
        models: [
          { id: "luna", name: "Same display name" },
          { id: "sonnet", name: "Same display name" },
        ],
      },
      onModelOverridesValidityChange: jest.fn(),
      ...overrides,
    });
    function Harness() {
      const [rows, setRows] = hooks.useState(props.editModelOverrides);
      const [extra, setExtra] = hooks.useState({});
      currentRows = rows;
      updateProps = setExtra;
      return html`<${WorkspaceEditor}
        ...${props}
        ...${extra}
        editModelOverrides=${rows}
        setEditModelOverrides=${setRows}
      />`;
    }
    const container = document.createElement("div");
    document.body.appendChild(container);
    preact.render(html`<${Harness} />`, container);
    return {
      container,
      rows: () => currentRows,
      updateProps: (extra) => updateProps(extra),
      props,
    };
  }

  describe("WorkspaceEditor Models tab", () => {
    test("groups by exact model ID and shows a readable summary with all selected tags accessible", async () => {
      const draft = mountDraft({
        editModelOverrides: [
          ...["Cheap", "Coding", "Fast"].map((tag) => ({
            acp_server: "opencode",
            tag,
            model_id: "luna",
          })),
          ...["Smart", "Smartest"].map((tag) => ({
            acp_server: "opencode",
            tag,
            model_id: "sonnet",
          })),
        ],
      });
      try {
        await settle();
        expect(
          draft.container.querySelectorAll(
            '[data-testid^="ws-model-override-model-"]',
          ),
        ).toHaveLength(2);
        const trigger = control(draft.container, "tag");
        expect(trigger.textContent).toContain("Cheap, Coding and 1 more");
        expect(trigger.getAttribute("aria-label")).toBe(
          "Capability tags: Cheap, Coding, Fast",
        );
        trigger.click();
        await settle();
        for (const tag of ["Cheap", "Coding", "Fast"])
          expect(checkbox(draft.container, tag).checked).toBe(true);
        expect(checkbox(draft.container, "Smart").disabled).toBe(true);
      } finally {
        unmount(draft.container);
      }
    });

    test("multi-select stays open, removes one tag, changes shared model, and preserves other agents", async () => {
      const other = {
        acp_server: "other",
        tag: "Fast",
        model_id: "other-model",
      };
      const draft = mountDraft({
        editModelOverrides: [
          other,
          { acp_server: "opencode", tag: "Coding", model_id: "luna" },
        ],
      });
      try {
        await settle();
        control(draft.container, "tag").click();
        await settle();
        change(checkbox(draft.container, "Fast"), true);
        await settle();
        expect(
          control(draft.container, "tag").getAttribute("aria-expanded"),
        ).toBe("true");
        change(control(draft.container, "model"), "sonnet");
        await settle();
        expect(draft.rows()).toEqual([
          other,
          { acp_server: "opencode", tag: "Coding", model_id: "sonnet" },
          { acp_server: "opencode", tag: "Fast", model_id: "sonnet" },
        ]);
        change(checkbox(draft.container, "Coding"), false);
        await settle();
        change(control(draft.container, "model"), "sonnet");
        await settle();
        expect(draft.rows()).toEqual([
          other,
          { acp_server: "opencode", tag: "Fast", model_id: "sonnet" },
        ]);
        control(draft.container, "remove").click();
        await settle();
        expect(draft.rows()).toEqual([other]);
      } finally {
        unmount(draft.container);
      }
    });

    test("keeps incomplete drafts stable and reports invalid until completed or removed", async () => {
      const draft = mountDraft({
        editModelOverrides: [
          { acp_server: "opencode", tag: "Coding", model_id: "luna" },
        ],
      });
      try {
        await settle();
        draft.container
          .querySelector('[data-testid="ws-model-override-add"]')
          .click();
        await settle();
        expect(
          draft.props.onModelOverridesValidityChange,
        ).toHaveBeenLastCalledWith(false);
        change(control(draft.container, "model", 1), "luna");
        await settle();
        expect(
          draft.container.querySelectorAll(
            '[data-testid^="ws-model-override-model-"]',
          ),
        ).toHaveLength(2);
        control(draft.container, "tag", 1).click();
        await settle();
        expect(checkbox(draft.container, "Coding").disabled).toBe(true);
        change(checkbox(draft.container, "Fast"), true);
        await settle();
        expect(
          draft.props.onModelOverridesValidityChange,
        ).toHaveBeenLastCalledWith(true);
        expect(
          draft.container.querySelectorAll(
            '[data-testid^="ws-model-override-model-"]',
          ),
        ).toHaveLength(2);
        const saved = draft.rows();
        unmount(draft.container);
        const reopened = mountDraft({ editModelOverrides: saved });
        try {
          expect(
            reopened.container.querySelectorAll(
              '[data-testid^="ws-model-override-model-"]',
            ),
          ).toHaveLength(1);
          expect(reopened.rows()).toEqual(saved);
        } finally {
          unmount(reopened.container);
        }
      } finally {
        if (draft.container.isConnected) unmount(draft.container);
      }
    });

    test("retains conflicting evidence and requires an explicit model choice", async () => {
      const rows = [
        { acp_server: "opencode", tag: "Fast", model_id: "sonnet" },
        { acp_server: "opencode", tag: "Fast", model_id: "luna" },
      ];
      const draft = mountDraft({ editModelOverrides: rows });
      try {
        await settle();
        expect(draft.rows()).toEqual(rows);
        expect(
          draft.props.onModelOverridesValidityChange,
        ).toHaveBeenLastCalledWith(false);
        expect(control(draft.container, "model").disabled).toBe(true);
        change(
          draft.container.querySelector(
            '[aria-label="Choose model to keep for conflicting tag Fast"]',
          ),
          "luna",
        );
        await settle();
        expect(draft.rows()).toEqual([rows[1]]);
        expect(
          draft.props.onModelOverridesValidityChange,
        ).toHaveBeenLastCalledWith(true);
        expect(draft.container.querySelector('[role="alert"]')).toBeNull();
      } finally {
        unmount(draft.container);
      }
    });

    test("Escape restores trigger focus and outside click dismisses without trapping focus", async () => {
      const draft = mountDraft();
      try {
        await settle();
        const trigger = control(draft.container, "tag");
        trigger.click();
        await settle();
        const input = checkbox(draft.container, "Coding");
        input.focus();
        // A surrounding Modal listens for Escape on window and would close the
        // whole dialog (discarding drafts) if the dropdown let the event through.
        const windowEscapes = [];
        const onWindowKeyDown = (event) => {
          if (event.key === "Escape") windowEscapes.push(event.target);
        };
        window.addEventListener("keydown", onWindowKeyDown);
        try {
          input.dispatchEvent(
            new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
          );
          await settle();
          expect(windowEscapes).toHaveLength(0);
        } finally {
          window.removeEventListener("keydown", onWindowKeyDown);
        }
        expect(trigger.getAttribute("aria-expanded")).toBe("false");
        expect(document.activeElement).toBe(trigger);
        trigger.click();
        await settle();
        document.body.dispatchEvent(
          new MouseEvent("mousedown", { bubbles: true }),
        );
        await settle();
        expect(trigger.getAttribute("aria-expanded")).toBe("false");
      } finally {
        unmount(draft.container);
      }
    });

    test("preserves unavailable tags and switches agent drafts without leaking catalog or mappings", async () => {
      const rows = [
        {
          acp_server: "opencode",
          tag: "Retired Tag",
          model_id: "retired-model",
        },
        { acp_server: "other", tag: "Other Tag", model_id: "other-model" },
      ];
      const draft = mountDraft({ editModelOverrides: rows });
      try {
        await settle();
        control(draft.container, "tag").click();
        await settle();
        expect(draft.container.textContent).toContain(
          "Retired Tag (unavailable)",
        );
        draft.updateProps({ editAcpServer: "other" });
        await settle();
        expect(control(draft.container, "tag").getAttribute("aria-label")).toBe(
          "Capability tags: Other Tag",
        );
        expect(control(draft.container, "model").value).toBe("other-model");
        expect(control(draft.container, "model").textContent).not.toContain(
          "Same display name",
        );
        expect(draft.rows()).toEqual(rows);
      } finally {
        unmount(draft.container);
      }
    });

    test("keeps tags, model and remove controls on one row with a left-aligned add action below", () => {
      const props = defaultProps();
      const container = mount(props);
      try {
        const tag = container.querySelector(
          '[data-testid="ws-model-override-tag-0"]',
        );
        const model = container.querySelector(
          '[data-testid="ws-model-override-model-0"]',
        );
        const remove = container.querySelector(
          '[data-testid="ws-model-override-remove-0"]',
        );
        const add = container.querySelector(
          '[data-testid="ws-model-override-add"]',
        );
        const refresh = container.querySelector(
          '[data-testid="ws-model-catalog-refresh"]',
        );
        const row = tag.parentElement.parentElement;
        expect(row.classList.contains("flex-row")).toBe(true);
        expect(row.classList.contains("flex-col")).toBe(false);
        expect(model.parentElement).toBe(row);
        expect(remove.parentElement).toBe(row);
        expect(remove.classList.contains("text-mitto-danger")).toBe(true);
        expect(add.textContent.trim()).toBe("+ Add override group");
        expect(add.className).toBe("btn btn-sm btn-ghost");
        const action = add.parentElement;
        expect(action.className).toBe("mt-3 flex items-center gap-2");
        expect(action.previousElementSibling.contains(row)).toBe(true);
        expect(refresh.parentElement.contains(add)).toBe(false);
        add.click();
        expect(props.setEditModelOverrides).toHaveBeenCalledTimes(1);
        const update = props.setEditModelOverrides.mock.calls[0][0];
        expect(update(props.editModelOverrides)).toEqual([
          ...props.editModelOverrides,
          { acp_server: "opencode", tag: "", model_id: "" },
        ]);
        refresh.click();
        expect(props.loadModels).toHaveBeenCalledWith("workspace-1");
      } finally {
        unmount(container);
      }
    });

    test("keeps the bottom add action available when empty and disabled without an agent", () => {
      const container = mount(
        defaultProps({ editModelOverrides: [], editAcpServer: "" }),
      );
      try {
        const add = container.querySelector(
          '[data-testid="ws-model-override-add"]',
        );
        expect(add.disabled).toBe(true);
        expect(container.textContent).toContain(
          "No model overrides configured",
        );
      } finally {
        unmount(container);
      }
    });

    test("shows empty-catalog guidance and keeps saved selections available", () => {
      const container = mount(defaultProps());
      try {
        expect(container.textContent).toContain(
          "No model catalog is available yet",
        );
        const modelSelect = container.querySelector(
          '[data-testid="ws-model-override-model-0"]',
        );
        expect(modelSelect.value).toBe("retired-model");
        expect(modelSelect.textContent).toContain(
          "retired-model (unavailable)",
        );
        expect(
          container
            .querySelector('[data-testid="ws-model-override-tag-0"]')
            .getAttribute("aria-label"),
        ).toBe("Capability tags: Coding");
      } finally {
        unmount(container);
      }
    });

    test("does not show the saved workspace catalog for another agent", () => {
      const container = mount(
        defaultProps({
          editAcpServer: "other-agent",
          modelsData: {
            acp_server: "opencode",
            models: [{ id: "model-1", name: "Model One" }],
            live: false,
          },
          editModelOverrides: [],
        }),
      );
      try {
        expect(container.textContent).toContain("Save the agent selection");
        expect(container.querySelectorAll("option")).toHaveLength(0);
      } finally {
        unmount(container);
      }
    });

    const scoped = (dir) =>
      defaultProps({
        selectedWorkspace: { uuid: "workspace-1", working_dir: "/proj" },
        editModelOverrides: [
          { acp_server: "opencode", tag: "smart", model_id: "" },
        ],
        modelsData: {
          acp_server: "opencode",
          working_dir: dir,
          models: [{ id: "model-1", name: "Model One" }],
          live: false,
        },
      });

    test("shows models when both server and folder match", () => {
      const container = mount(scoped("/proj/"));
      try {
        expect(container.textContent).toContain("Model One");
      } finally {
        unmount(container);
      }
    });

    test("hides models when the folder differs", () => {
      const container = mount(scoped("/other"));
      try {
        expect(container.textContent).not.toContain("Model One");
      } finally {
        unmount(container);
      }
    });

    test("shows exact cache-only refresh wording", () => {
      const container = mount(scoped("/proj"));
      try {
        const note = container.querySelector(
          '[data-testid="ws-model-catalog-refresh-note"]',
        );
        expect(note.textContent).toBe(
          "Refresh reloads the last-observed catalog. It does not contact the agent.",
        );
      } finally {
        unmount(container);
      }
    });
  });
} else {
  describe("WorkspaceEditor", () => {
    test("passes mounted Models-tab tests", () => {
      const result = spawnSync(
        process.execPath,
        ["test", fileURLToPath(import.meta.url)],
        {
          encoding: "utf8",
          env: {
            ...process.env,
            MITTO_WORKSPACE_EDITOR_TEST_CHILD: "1",
          },
          timeout: 30_000,
        },
      );
      if (result.status !== 0) {
        throw new Error(
          `Workspace Models tab tests failed:\n${result.stdout}\n${result.stderr}`,
        );
      }
    });
  });
}
