// Mitto Web Interface - Agent Discovery Dialog Component
// Supports two modes:
//   'wizard'   — First-run flow. Auto-saves via POST /api/agents/confirm.
//   'settings' — Triggered from SettingsDialog. Returns agents to parent without saving.

const { html, useState, useEffect, useCallback } = window.preact;

import { Modal } from "./Modal.js";
import { apiUrl, secureFetch, endpoints } from "../utils/index.js";

/**
 * AgentDiscoveryDialog - Discover and configure AI agents.
 *
 * Phases:
 * 1. 'initial'    — (wizard only) Prompt user to scan
 * 2. 'scanning'   — Spinner while running status.sh scripts
 * 3. 'results'    — Checkboxes for available agents + confirm button
 * 4. 'empty'      — No agents found
 * 5. 'confirming' — (wizard only) Spinner while saving
 *
 * @param {Object}   props
 * @param {boolean}  props.isOpen            - Whether the dialog is visible
 * @param {Function} props.onClose           - Called when dismissed
 * @param {Function} props.onAgentsConfirmed - (wizard) Called after API save succeeds
 * @param {Function} props.onAgentsSelected  - (settings) Called with agent array, no API call
 * @param {string}   props.mode              - 'wizard' (default) or 'settings'
 * @param {Array}    props.existingServers   - Existing ACP servers (for duplicate detection)
 */
export function AgentDiscoveryDialog({
  isOpen,
  onClose,
  onAgentsConfirmed,
  onAgentsSelected,
  mode = "wizard",
  existingServers = [],
}) {
  const [phase, setPhase] = useState("initial");
  const [agents, setAgents] = useState([]);
  const [selected, setSelected] = useState(new Set());
  const [error, setError] = useState("");

  // Reset state whenever the dialog opens
  useEffect(() => {
    if (isOpen) {
      setPhase(mode === "settings" ? "scanning" : "initial");
      setAgents([]);
      setSelected(new Set());
      setError("");
    }
  }, [isOpen, mode]);

  const handleScan = useCallback(async () => {
    setPhase("scanning");
    setError("");
    try {
      const resp = await secureFetch(endpoints.agents.scan(), {
        method: "POST",
      });
      if (!resp.ok) {
        throw new Error("Scan failed: " + resp.statusText);
      }
      const results = await resp.json();

      // In settings mode, exclude agents already configured (matched by command)
      const existingCommands = new Set(
        existingServers.map((s) => s.command).filter(Boolean),
      );

      // Pre-select available agents that aren't already configured
      const selectable = results.filter(
        (a) => a.available && !existingCommands.has(a.status?.command),
      );
      setAgents(results);
      setSelected(new Set(selectable.map((a) => a.dir_name)));
      setPhase(
        selectable.length === 0 &&
          results.filter((a) => a.available).length === 0
          ? "empty"
          : "results",
      );
    } catch (err) {
      setError("Failed to scan for agents: " + err.message);
      setPhase(mode === "settings" ? "empty" : "initial");
    }
  }, [existingServers, mode]);

  // Auto-scan when opened in settings mode
  useEffect(() => {
    if (isOpen && mode === "settings") {
      handleScan();
    }
  }, [isOpen, mode]); // eslint-disable-line

  const toggleAgent = useCallback((dirName) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(dirName)) {
        next.delete(dirName);
      } else {
        next.add(dirName);
      }
      return next;
    });
  }, []);

  const handleConfirm = useCallback(async () => {
    const toAdd = agents
      .filter((a) => selected.has(a.dir_name) && a.available && a.status)
      .map((a) => {
        const d = a.metadata?.defaults;
        const entry = {
          name: a.metadata.display_name || a.dir_name,
          command: a.status.command,
          type: a.dir_name,
          dir_name: a.dir_name,
          source: "settings",
        };
        if (d) {
          if (d.env && Object.keys(d.env).length > 0) entry.env = { ...d.env };
          if (Array.isArray(d.tags) && d.tags.length > 0)
            entry.tags = [...d.tags];
          if (d.constraints && Object.keys(d.constraints).length > 0)
            entry.constraints = d.constraints;
          if (d.autoApprove) entry.auto_approve = true;
        }
        return entry;
      });

    if (toAdd.length === 0) {
      onClose?.();
      return;
    }

    if (mode === "settings") {
      // Settings mode: return agents to parent component, no API call
      onAgentsSelected?.(toAdd);
      return;
    }

    // Wizard mode: save via API then notify parent
    setPhase("confirming");
    setError("");
    try {
      const resp = await secureFetch(endpoints.agents.confirm(), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ agents: toAdd }),
      });
      if (!resp.ok) {
        throw new Error("Confirm failed: " + resp.statusText);
      }
      onAgentsConfirmed?.();
    } catch (err) {
      setError("Failed to save agents: " + err.message);
      setPhase("results");
    }
  }, [agents, selected, mode, onClose, onAgentsConfirmed, onAgentsSelected]);

  const isLoading = phase === "scanning" || phase === "confirming";
  const isSettingsMode = mode === "settings";
  const dialogTitle = isSettingsMode ? "Discover AI Agents" : "Set Up AI Agent";

  // Build set of already-configured commands for rendering
  const existingCommands = new Set(
    existingServers.map((s) => s.command).filter(Boolean),
  );

  const footer =
    phase === "initial" || phase === "empty" || phase === "results"
      ? html`
          ${(phase === "initial" || phase === "empty") &&
          html`
            <button
              onClick=${onClose}
              class="btn btn-sm btn-ghost"
              data-testid="agent-discovery-skip"
            >
              ${isSettingsMode ? "Cancel" : "Configure Manually"}
            </button>
          `}
          ${phase === "results" &&
          html`
            <button onClick=${onClose} class="btn btn-sm btn-ghost">
              ${isSettingsMode ? "Cancel" : "Skip"}
            </button>
          `}
          ${phase === "initial" &&
          html`
            <button
              onClick=${handleScan}
              class="btn btn-sm btn-primary"
              data-testid="agent-discovery-scan"
            >
              Scan for Agents
            </button>
          `}
          ${phase === "results" &&
          html`
            <button
              onClick=${handleConfirm}
              disabled=${selected.size === 0}
              class="btn btn-sm btn-primary"
              data-testid="agent-discovery-confirm"
            >
              Add Selected (${selected.size})
            </button>
          `}
        `
      : null;

  return html`
    <${Modal}
      isOpen=${isOpen}
      onClose=${() => !isLoading && onClose?.()}
      title=${dialogTitle}
      testid="agent-discovery-dialog"
      backdropTestid="agent-discovery-backdrop"
      footer=${footer}
    >
      ${
        phase === "initial" &&
        html`
          <div class="text-center py-4">
            <div class="text-4xl mb-3">🤖</div>
            <p class="text-mitto-text font-medium mb-2">
              No AI agents configured yet
            </p>
            <p class="text-mitto-text-muted text-sm mb-5">
              Scan your system to detect installed AI coding agents (Claude
              Code, Auggie, Cursor, etc.)
            </p>
            ${error &&
            html`<p class="text-mitto-danger text-sm mb-3">${error}</p>`}
          </div>
        `
      }

      ${
        phase === "scanning" &&
        html`
          <div class="text-center py-6">
            <span
              class="loading loading-spinner loading-lg mb-3 text-mitto-accent"
            ></span>
            <p class="text-mitto-text-secondary">
              Scanning for installed agents...
            </p>
          </div>
        `
      }

      ${
        phase === "confirming" &&
        html`
          <div class="text-center py-6">
            <span
              class="loading loading-spinner loading-lg mb-3 text-mitto-accent"
            ></span>
            <p class="text-mitto-text-secondary">
              Saving agent configuration...
            </p>
          </div>
        `
      }

      ${
        phase === "empty" &&
        html`
          <div class="text-center py-4">
            <div class="text-4xl mb-3">🔍</div>
            <p class="text-mitto-text font-medium mb-2">No agents detected</p>
            <p class="text-mitto-text-muted text-sm">
              No installed AI agents were found.
              ${!isSettingsMode &&
              " You can configure one manually in Settings."}
            </p>
            ${error &&
            html`<p class="text-mitto-danger text-sm mt-2">${error}</p>`}
          </div>
        `
      }

      ${
        phase === "results" &&
        html`
          <div>
            <p class="text-mitto-text-secondary text-sm mb-3">
              Select the agents to add:
            </p>
            <ul class="list max-h-64 overflow-y-auto">
              ${agents
                .filter((a) => a.available)
                .map((agent) => {
                  const alreadyConfigured = existingCommands.has(
                    agent.status?.command,
                  );
                  // Selectable cards on the daisyUI list: the list owns row radius +
                  // dividers, so only the two distinctive states carry their own
                  // treatment — a full accent border + tint when selected, and a
                  // dimmed/non-interactive look when already configured. Selection
                  // stays Preact-driven (selected Set + toggleAgent).
                  const stateTone = alreadyConfigured
                    ? "opacity-50 cursor-default"
                    : selected.has(agent.dir_name)
                      ? "border border-mitto-accent-600 bg-mitto-accent-600/10 cursor-pointer hover:border-mitto-accent"
                      : "cursor-pointer hover:bg-mitto-input-box";
                  return html`
                    <li
                      key=${agent.dir_name}
                      class="list-row items-start transition-colors ${stateTone}"
                      onClick=${() =>
                        !alreadyConfigured && toggleAgent(agent.dir_name)}
                    >
                      ${alreadyConfigured
                        ? html`<div class="mt-0.5 w-4 h-4 shrink-0"></div>`
                        : html`<input
                            type="checkbox"
                            checked=${selected.has(agent.dir_name)}
                            onChange=${() => toggleAgent(agent.dir_name)}
                            onClick=${(e) => e.stopPropagation()}
                            class="checkbox checkbox-sm checkbox-accent mt-0.5 shrink-0"
                          />`}
                      <div class="list-col-grow min-w-0">
                        <div class="flex items-center gap-2 flex-wrap">
                          <span class="font-medium text-sm"
                            >${agent.metadata.display_name ||
                            agent.dir_name}</span
                          >
                          ${agent.status?.version &&
                          html`
                            <span class="text-xs text-mitto-text-muted"
                              >${agent.status.version}</span
                            >
                          `}
                          ${alreadyConfigured &&
                          html`
                            <span class="badge badge-ghost badge-sm">
                              Already configured
                            </span>
                          `}
                        </div>
                        ${agent.status?.command &&
                        html`
                          <div
                            class="text-xs text-mitto-text-muted truncate mt-0.5"
                          >
                            ${agent.status.command}
                          </div>
                        `}
                        ${(() => {
                          const d = agent.metadata?.defaults;
                          const hasDefaults =
                            d &&
                            ((d.env && Object.keys(d.env).length) ||
                              (d.tags && d.tags.length) ||
                              (d.constraints &&
                                Object.keys(d.constraints).length) ||
                              d.autoApprove);
                          if (!hasDefaults) return null;
                          return html`
                            <div class="mt-1 flex flex-col gap-1">
                              <div
                                class="text-xs text-mitto-text-muted font-medium"
                              >
                                Defaults
                              </div>
                              ${d.tags &&
                              d.tags.length > 0 &&
                              html`
                                <div class="flex items-center gap-1 flex-wrap">
                                  ${d.tags.map(
                                    (tag) => html`
                                      <span class="badge badge-ghost badge-sm"
                                        >${tag}</span
                                      >
                                    `,
                                  )}
                                </div>
                              `}
                              ${d.constraints?.model?.pattern &&
                              html`
                                <div class="text-xs text-mitto-text-muted">
                                  Model: ${d.constraints.model.matchMode}
                                  "${d.constraints.model.pattern}"
                                </div>
                              `}
                              ${d.env &&
                              Object.keys(d.env).length > 0 &&
                              html`
                                <div class="text-xs text-mitto-text-muted">
                                  Env: ${Object.keys(d.env).join(", ")}
                                </div>
                              `}
                              ${d.autoApprove &&
                              html`
                                <div class="text-xs text-mitto-text-muted">
                                  Auto-approve enabled
                                </div>
                              `}
                            </div>
                          `;
                        })()}
                      </div>
                    </li>
                  `;
                })}
            </ul>
            ${agents.some((a) => !a.available) &&
            html`
              <p class="text-mitto-text-muted text-xs mt-3">
                ${agents.filter((a) => !a.available).length} agent(s) not
                installed on this system.
              </p>
            `}
            ${error &&
            html`<p class="text-mitto-danger text-sm mt-3">${error}</p>`}
          </div>
        `
      }
    </${Modal}>
  `;
}
