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

  describe("WorkspaceEditor Models tab", () => {
    test("shows empty-catalog guidance and keeps saved selections available", () => {
      const container = mount(defaultProps());
      try {
        expect(container.textContent).toContain("No model catalog is available yet");
        const modelSelect = container.querySelector(
          '[data-testid="ws-model-override-model-0"]',
        );
        expect(modelSelect.value).toBe("retired-model");
        expect(modelSelect.textContent).toContain("retired-model (unavailable)");
        expect(
          container.querySelector('[data-testid="ws-model-override-tag-0"]')
            .value,
        ).toBe("Coding");
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
