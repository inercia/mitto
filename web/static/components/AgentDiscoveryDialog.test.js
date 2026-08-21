/**
 * Unit tests for AgentDiscoveryDialog.js (mitto-7gta.17 slice S6 Test phase).
 *
 * Covers the 2 authFetch/secureFetch->getSdkClient() call sites migrated in
 * the Implementation phase: handleScan (agents.scan) and handleConfirm
 * (agents.confirm). No test file existed for this component before. The
 * component destructures `window.preact` at module top level and is not
 * imported anywhere else under jsdom (grep-confirmed), so — mirroring the
 * SessionPanel.test.js / ChatInput.test.js precedent from slices S4/S5 —
 * the two handlers' logic is duplicated here with the SDK client injected,
 * rather than introducing a new component-rendering harness.
 */

import { describe, test, expect, jest } from "../utils/testing/testGlobals.js";

/**
 * Duplicated from AgentDiscoveryDialog.js's handleScan (source: `const
 * results = await getSdkClient().agents.scan(); ... setPhase(...)`). Takes
 * the SDK client and the setters as arguments instead of closing over
 * useState.
 */
async function handleScan(client, { existingServers, mode }, setters) {
  const { setPhase, setAgents, setSelected, setError } = setters;
  setPhase("scanning");
  setError("");
  try {
    const results = await client.agents.scan();
    const existingCommands = new Set(
      existingServers.map((s) => s.command).filter(Boolean),
    );
    const selectable = results.filter(
      (a) => a.available && !existingCommands.has(a.status?.command),
    );
    setAgents(results);
    setSelected(new Set(selectable.map((a) => a.dir_name)));
    setPhase(
      selectable.length === 0 && results.filter((a) => a.available).length === 0
        ? "empty"
        : "results",
    );
  } catch (err) {
    setError("Failed to scan for agents: " + err.message);
    setPhase(mode === "settings" ? "empty" : "initial");
  }
}

/**
 * Duplicated from AgentDiscoveryDialog.js's handleConfirm (agents.confirm
 * branch only — the settings-mode/no-op branches are pure and untouched by
 * this slice).
 */
async function handleConfirm(client, toAdd, setters, callbacks) {
  const { setPhase, setError } = setters;
  const { onAgentsConfirmed } = callbacks;
  setPhase("confirming");
  setError("");
  try {
    await client.agents.confirm(toAdd);
    onAgentsConfirmed?.();
  } catch (err) {
    setError("Failed to save agents: " + err.message);
    setPhase("results");
  }
}

describe("AgentDiscoveryDialog — handleScan", () => {
  test("success: calls agents.scan(), pre-selects available+unconfigured agents, phase -> results", async () => {
    const client = {
      agents: {
        scan: jest.fn(() =>
          Promise.resolve([
            {
              dir_name: "auggie",
              available: true,
              status: { command: "auggie" },
            },
            {
              dir_name: "claude",
              available: true,
              status: { command: "claude" },
            },
            { dir_name: "cursor", available: false },
          ]),
        ),
      },
    };
    const setPhase = jest.fn();
    const setAgents = jest.fn();
    const setSelected = jest.fn();
    const setError = jest.fn();
    await handleScan(
      client,
      { existingServers: [{ command: "claude" }], mode: "wizard" },
      { setPhase, setAgents, setSelected, setError },
    );

    expect(client.agents.scan).toHaveBeenCalledTimes(1);
    // "claude" is already configured, so only "auggie" is pre-selected.
    expect(setSelected).toHaveBeenCalledWith(new Set(["auggie"]));
    expect(setPhase).toHaveBeenLastCalledWith("results");
  });

  test("no available/unconfigured agents: phase -> empty", async () => {
    const client = {
      agents: {
        scan: jest.fn(() =>
          Promise.resolve([{ dir_name: "x", available: false }]),
        ),
      },
    };
    const setPhase = jest.fn();
    await handleScan(
      client,
      { existingServers: [], mode: "settings" },
      {
        setPhase,
        setAgents: jest.fn(),
        setSelected: jest.fn(),
        setError: jest.fn(),
      },
    );
    expect(setPhase).toHaveBeenLastCalledWith("empty");
  });

  test("a rejected scan surfaces err.message and reverts phase (settings mode -> empty)", async () => {
    const client = {
      agents: { scan: jest.fn(() => Promise.reject(new Error("scan failed"))) },
    };
    const setPhase = jest.fn();
    const setError = jest.fn();
    await handleScan(
      client,
      { existingServers: [], mode: "settings" },
      { setPhase, setAgents: jest.fn(), setSelected: jest.fn(), setError },
    );
    expect(setError).toHaveBeenCalledWith(
      "Failed to scan for agents: scan failed",
    );
    expect(setPhase).toHaveBeenLastCalledWith("empty");
  });

  test("a rejected scan in wizard mode reverts phase to initial", async () => {
    const client = {
      agents: { scan: jest.fn(() => Promise.reject(new Error("boom"))) },
    };
    const setPhase = jest.fn();
    await handleScan(
      client,
      { existingServers: [], mode: "wizard" },
      {
        setPhase,
        setAgents: jest.fn(),
        setSelected: jest.fn(),
        setError: jest.fn(),
      },
    );
    expect(setPhase).toHaveBeenLastCalledWith("initial");
  });
});

describe("AgentDiscoveryDialog — handleConfirm (wizard mode, agents.confirm)", () => {
  test("success: POSTs the selected agents and calls onAgentsConfirmed", async () => {
    const client = { agents: { confirm: jest.fn(() => Promise.resolve()) } };
    const setPhase = jest.fn();
    const setError = jest.fn();
    const onAgentsConfirmed = jest.fn();
    const toAdd = [{ name: "Auggie", command: "auggie", type: "auggie" }];
    await handleConfirm(
      client,
      toAdd,
      { setPhase, setError },
      { onAgentsConfirmed },
    );
    expect(client.agents.confirm).toHaveBeenCalledWith(toAdd);
    expect(onAgentsConfirmed).toHaveBeenCalledTimes(1);
    expect(setPhase).toHaveBeenCalledWith("confirming");
  });

  test("a rejected confirm surfaces err.message and reverts phase to results", async () => {
    const client = {
      agents: {
        confirm: jest.fn(() => Promise.reject(new Error("403 read-only"))),
      },
    };
    const setPhase = jest.fn();
    const setError = jest.fn();
    const onAgentsConfirmed = jest.fn();
    await handleConfirm(
      client,
      [{ name: "Auggie" }],
      { setPhase, setError },
      { onAgentsConfirmed },
    );
    expect(setError).toHaveBeenCalledWith(
      "Failed to save agents: 403 read-only",
    );
    expect(setPhase).toHaveBeenLastCalledWith("results");
    expect(onAgentsConfirmed).not.toHaveBeenCalled();
  });
});
