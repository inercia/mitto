// Mitto Web Interface - Agent Discovery Dialog Component
// Supports two modes:
//   'wizard'   — First-run flow. Auto-saves via POST /api/agents/confirm.
//   'settings' — Triggered from SettingsDialog. Returns agents to parent without saving.

const { html, useState, useEffect, useCallback } = window.preact;

import { CloseIcon, SpinnerIcon } from "./Icons.js";
import { apiUrl, secureFetch } from "../utils/index.js";

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
      const resp = await secureFetch(apiUrl("/api/agents/scan"), {
        method: "POST",
      });
      if (!resp.ok) {
        throw new Error("Scan failed: " + resp.statusText);
      }
      const results = await resp.json();

      // In settings mode, exclude agents already configured (matched by command)
      const existingCommands = new Set(
        existingServers.map((s) => s.command).filter(Boolean)
      );

      // Pre-select available agents that aren't already configured
      const selectable = results.filter(
        (a) => a.available && !existingCommands.has(a.status?.command)
      );
      setAgents(results);
      setSelected(new Set(selectable.map((a) => a.dir_name)));
      setPhase(selectable.length === 0 && results.filter((a) => a.available).length === 0
        ? "empty"
        : "results"
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
  }, [isOpen, mode]); // eslint-disable-line react-hooks/exhaustive-deps

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
      .map((a) => ({
        name: a.metadata.display_name || a.dir_name,
        command: a.status.command,
        type: a.dir_name,
        source: "settings",
      }));

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
      const resp = await secureFetch(apiUrl("/api/agents/confirm"), {
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

  if (!isOpen) return null;

  const isLoading = phase === "scanning" || phase === "confirming";
  const isSettingsMode = mode === "settings";
  const dialogTitle = isSettingsMode ? "Discover AI Agents" : "Set Up AI Agent";

  // Build set of already-configured commands for rendering
  const existingCommands = new Set(
    existingServers.map((s) => s.command).filter(Boolean)
  );

  return html`
    <div
      class="fixed inset-0 z-[60] flex items-center justify-center bg-black/50"
      onClick=${(e) => e.target === e.currentTarget && !isLoading && onClose?.()}
      data-testid="agent-discovery-backdrop"
    >
      <div
        class="bg-mitto-sidebar rounded-xl w-[500px] max-w-[90vw] overflow-hidden shadow-2xl flex flex-col"
        onClick=${(e) => e.stopPropagation()}
        data-testid="agent-discovery-dialog"
      >
        <!-- Header -->
        <div class="flex items-center justify-between p-4 border-b border-slate-700">
          <h3 class="text-lg font-semibold">${dialogTitle}</h3>
          <button
            onClick=${() => !isLoading && onClose?.()}
            disabled=${isLoading}
            class="p-1.5 hover:bg-slate-700 rounded-lg transition-colors ${isLoading ? "opacity-50 cursor-not-allowed" : ""}"
          >
            <${CloseIcon} className="w-5 h-5" />
          </button>
        </div>

        <!-- Content -->
        <div class="p-5 flex-1">
          ${phase === "initial" && html`
            <div class="text-center py-4">
              <div class="text-4xl mb-3">🤖</div>
              <p class="text-gray-200 font-medium mb-2">No AI agents configured yet</p>
              <p class="text-gray-400 text-sm mb-5">
                Scan your system to detect installed AI coding agents
                (Claude Code, Auggie, Cursor, etc.)
              </p>
              ${error && html`<p class="text-red-400 text-sm mb-3">${error}</p>`}
            </div>
          `}

          ${phase === "scanning" && html`
            <div class="text-center py-6">
              <${SpinnerIcon} className="w-10 h-10 mx-auto mb-3 text-blue-400" />
              <p class="text-gray-300">Scanning for installed agents...</p>
            </div>
          `}

          ${phase === "confirming" && html`
            <div class="text-center py-6">
              <${SpinnerIcon} className="w-10 h-10 mx-auto mb-3 text-blue-400" />
              <p class="text-gray-300">Saving agent configuration...</p>
            </div>
          `}

          ${phase === "empty" && html`
            <div class="text-center py-4">
              <div class="text-4xl mb-3">🔍</div>
              <p class="text-gray-200 font-medium mb-2">No agents detected</p>
              <p class="text-gray-400 text-sm">
                No installed AI agents were found.
                ${!isSettingsMode && " You can configure one manually in Settings."}
              </p>
              ${error && html`<p class="text-red-400 text-sm mt-2">${error}</p>`}
            </div>
          `}

          ${phase === "results" && html`
            <div>
              <p class="text-gray-300 text-sm mb-3">Select the agents to add:</p>
              <div class="space-y-2 max-h-64 overflow-y-auto">
                ${agents.filter((a) => a.available).map((agent) => {
                  const alreadyConfigured = existingCommands.has(agent.status?.command);
                  return html`
                    <div
                      key=${agent.dir_name}
                      class="flex items-start gap-3 p-3 rounded-lg border transition-colors
                        ${alreadyConfigured
                          ? "border-slate-700 opacity-50 cursor-default"
                          : selected.has(agent.dir_name)
                            ? "border-blue-600 bg-blue-600/10 cursor-pointer hover:border-blue-500"
                            : "border-slate-700 cursor-pointer hover:border-slate-500"
                        }"
                      onClick=${() => !alreadyConfigured && toggleAgent(agent.dir_name)}
                    >
                      ${alreadyConfigured
                        ? html`<div class="mt-0.5 w-4 h-4 flex-shrink-0"></div>`
                        : html`<input
                            type="checkbox"
                            checked=${selected.has(agent.dir_name)}
                            onChange=${() => toggleAgent(agent.dir_name)}
                            onClick=${(e) => e.stopPropagation()}
                            class="mt-0.5 accent-blue-500 flex-shrink-0"
                          />`
                      }
                      <div class="flex-1 min-w-0">
                        <div class="flex items-center gap-2 flex-wrap">
                          <span class="font-medium text-sm">${agent.metadata.display_name || agent.dir_name}</span>
                          ${agent.status?.version && html`
                            <span class="text-xs text-gray-500">${agent.status.version}</span>
                          `}
                          ${alreadyConfigured && html`
                            <span class="text-xs text-gray-500 bg-slate-700 px-1.5 py-0.5 rounded">
                              Already configured
                            </span>
                          `}
                        </div>
                        ${agent.status?.command && html`
                          <div class="text-xs text-gray-500 truncate mt-0.5">${agent.status.command}</div>
                        `}
                      </div>
                    </div>
                  `;
                })}
              </div>
              ${agents.some((a) => !a.available) && html`
                <p class="text-gray-500 text-xs mt-3">
                  ${agents.filter((a) => !a.available).length} agent(s) not installed on this system.
                </p>
              `}
              ${error && html`<p class="text-red-400 text-sm mt-3">${error}</p>`}
            </div>
          `}
        </div>

        <!-- Footer -->
        <div class="flex justify-end gap-3 p-4 border-t border-slate-700">
          ${(phase === "initial" || phase === "empty") && html`
            <button
              onClick=${onClose}
              class="px-4 py-2 text-sm hover:bg-slate-700 rounded-lg transition-colors"
              data-testid="agent-discovery-skip"
            >
              ${isSettingsMode ? "Cancel" : "Configure Manually"}
            </button>
          `}
          ${phase === "results" && html`
            <button
              onClick=${onClose}
              class="px-4 py-2 text-sm hover:bg-slate-700 rounded-lg transition-colors"
            >
              ${isSettingsMode ? "Cancel" : "Skip"}
            </button>
          `}
          ${phase === "initial" && html`
            <button
              onClick=${handleScan}
              class="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-500 text-white rounded-lg transition-colors"
              data-testid="agent-discovery-scan"
            >
              Scan for Agents
            </button>
          `}
          ${phase === "results" && html`
            <button
              onClick=${handleConfirm}
              disabled=${selected.size === 0}
              class="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-500 text-white rounded-lg transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              data-testid="agent-discovery-confirm"
            >
              Add Selected (${selected.size})
            </button>
          `}
        </div>
      </div>
    </div>
  `;
}
