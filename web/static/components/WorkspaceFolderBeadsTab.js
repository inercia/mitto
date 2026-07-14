// Mitto Web Interface — Folder Editor: Beads tab
//
// Renders the Beads config + upstream (Jira/GitHub/GitLab/Linear/etc.) tab
// body of the folder editor. Extracted verbatim from WorkspaceFolderEditor.js
// (mitto-90f.4 Increment C). Behavior-preserving; all state/handlers still
// live in the shell (WorkspacesDialog.js) and are drilled through as props.
const { html } = window.preact;

import { openExternalURL } from "../utils/index.js";
import { promptParameters } from "../utils/prompts.js";
import { SpinnerIcon } from "./Icons.js";

// Descriptors used by the Beads Config tab to render the label + config-key
// help table for each supported upstream task system.
const BEADS_UPSTREAM_HELP = {
  github: {
    label: "GitHub",
    rows: [
      { key: "github.token", desc: "Personal access token" },
      { key: "github.owner", desc: "Repository owner" },
      { key: "github.repo", desc: "Repository name" },
    ],
  },
  gitlab: {
    label: "GitLab",
    rows: [
      { key: "gitlab.token", desc: "Personal access token" },
      { key: "gitlab.project", desc: "Project path or numeric ID" },
      {
        key: "gitlab.base_url",
        desc: "Optional: custom GitLab instance URL",
      },
    ],
  },
  jira: {
    label: "Jira",
    rows: [
      {
        key: "jira.url",
        desc: "Instance URL, e.g. https://acme.atlassian.net",
      },
      { key: "jira.user", desc: "Account email" },
      { key: "jira.token", desc: "API token" },
      { key: "jira.project", desc: "Project key (e.g. ACME)" },
    ],
  },
  linear: {
    label: "Linear",
    rows: [
      { key: "linear.api_key", desc: "API key (for individual developers)" },
      { key: "linear.team_id", desc: "Team ID (UUID)" },
      {
        key: "linear.team_ids",
        desc: "Multiple team IDs, comma-separated UUIDs",
      },
      { key: "linear.project_id", desc: "Optional: sync only this project" },
      { key: "linear.id_mode", desc: 'ID generation: "hash" (default)' },
      { key: "linear.hash_length", desc: "Hash length 3-8 (default: 6)" },
    ],
  },
};

export function WorkspaceFolderBeadsTab({
  beads,
  beadsSetters,
  beadsHandlers,
  onOpenPromptParamDialog,
}) {
  const {
    beadsConfig,
    beadsConfigLoading,
    beadsConfigError,
    beadsConfigSaving,
    newBeadsKey,
    newBeadsValue,
    beadsUpstream,
    beadsUpstreamSaving,
    beadsUpstreamPrompts,
    beadsUpstreamPromptsLoading,
    beadsPullPrompt,
    beadsPushPrompt,
    beadsSyncPrompt,
    beadsPullPromptArgs,
    beadsPushPromptArgs,
    beadsSyncPromptArgs,
  } = beads;
  const { setNewBeadsKey, setNewBeadsValue } = beadsSetters;
  const {
    setBeadsConfigKey,
    unsetBeadsConfigKey,
    saveBeadsUpstream,
    saveBeadsPromptName,
    saveBeadsPromptArgs,
  } = beadsHandlers;
  return html`
    <div class="space-y-4">
      <p class="text-sm text-mitto-text-muted">
        Mitto uses${" "}
        <a
          href="https://github.com/steveyegge/beads"
          onClick=${(e) => {
            e.preventDefault();
            openExternalURL("https://github.com/steveyegge/beads");
          }}
          class="text-mitto-accent hover:text-mitto-accent-300 underline cursor-pointer"
          >beads</a
        >${" "}(the <code>bd</code> tool) for managing tasks.
      </p>
      <!-- Upstream task system selector (persisted in folders.json) -->
      <fieldset class="fieldset pt-2">
        <legend class="fieldset-legend">Upstream Tasks</legend>
        <p class="text-xs text-mitto-text-muted">
          Select the external task system beads syncs with. When set,
          Pull/Push/Sync actions appear in the Tasks view for this folder.
        </p>
        <select
          value=${beadsUpstream}
          onInput=${(e) => saveBeadsUpstream(e.target.value)}
          disabled=${beadsUpstreamSaving}
          class="select select-sm w-full max-w-md disabled:opacity-50"
        >
          <option value="none">None</option>
          <option value="jira">Jira</option>
          <option value="github">GitHub</option>
          <option value="gitlab">GitLab</option>
          <option value="linear">Linear</option>
          <option value="prompts">Prompts</option>
        </select>
      </fieldset>

      ${beadsUpstream !== "none" &&
      BEADS_UPSTREAM_HELP[beadsUpstream] &&
      html`
        <div
          class="p-3 bg-mitto-input-box border border-mitto-border rounded-md"
        >
          <p class="text-xs text-mitto-text-muted mb-2">
            Recommended ${BEADS_UPSTREAM_HELP[beadsUpstream].label} keys${" "}
            (click a key to fill the add-key field below):
          </p>
          <div class="space-y-1">
            ${BEADS_UPSTREAM_HELP[beadsUpstream].rows.map(
              (row) => html`
                <div key=${row.key} class="flex items-baseline gap-2 text-xs">
                  <button
                    type="button"
                    onClick=${() => setNewBeadsKey(row.key)}
                    class="font-mono text-mitto-accent hover:text-mitto-accent-300 hover:underline whitespace-nowrap tooltip tooltip-bottom"
                    data-tip="Use this key in the add-key field below"
                  >
                    ${row.key}
                  </button>
                  <span class="text-mitto-text-muted">— ${row.desc}</span>
                </div>
              `,
            )}
          </div>
        </div>
      `}
      ${beadsUpstream === "prompts" &&
      html`
        <fieldset class="fieldset pt-2">
          <legend class="fieldset-legend">Prompt Actions</legend>
          <p class="label">
            Choose an enabled prompt for each button. Use the sliders button to
            configure arguments for parametrized prompts.
          </p>
          ${beadsUpstreamPromptsLoading
            ? html`<div
                class="flex items-center gap-2 text-sm text-mitto-text-muted"
              >
                <${SpinnerIcon} className="w-4 h-4 animate-spin" />
                Loading prompts…
              </div>`
            : html`
                <div class="space-y-2 pt-1">
                  ${[
                    {
                      label: "Pull",
                      field: "pull_prompt",
                      value: beadsPullPrompt,
                      args: beadsPullPromptArgs,
                    },
                    {
                      label: "Push",
                      field: "push_prompt",
                      value: beadsPushPrompt,
                      args: beadsPushPromptArgs,
                    },
                    {
                      label: "Sync",
                      field: "sync_prompt",
                      value: beadsSyncPrompt,
                      args: beadsSyncPromptArgs,
                    },
                  ].map(({ label, field, value, args }) => {
                    const selectedPrompt = value
                      ? beadsUpstreamPrompts.find((p) => p.name === value)
                      : null;
                    const params = selectedPrompt
                      ? promptParameters(selectedPrompt)
                      : [];
                    const canEditArgs = !!value && params.length > 0;
                    const argsDisabled = !canEditArgs || beadsUpstreamSaving;
                    return html`
                      <div
                        key=${field}
                        class="flex items-center gap-2 max-w-md"
                      >
                        <span
                          class="text-xs text-mitto-text-secondary"
                          style="min-width: 2.5rem"
                          >${label}</span
                        >
                        <select
                          value=${beadsUpstreamPrompts.some(
                            (p) => p.name === value,
                          )
                            ? value
                            : ""}
                          onInput=${(e) =>
                            saveBeadsPromptName(field, e.target.value)}
                          disabled=${beadsUpstreamSaving}
                          class="select select-sm flex-1 disabled:opacity-50"
                        >
                          <option value="">— none —</option>
                          ${beadsUpstreamPrompts.map(
                            (p) => html`
                              <option key=${p.name} value=${p.name}>
                                ${p.name}
                              </option>
                            `,
                          )}
                        </select>
                        <button
                          type="button"
                          onClick=${() => {
                            if (
                              !canEditArgs ||
                              !onOpenPromptParamDialog ||
                              !selectedPrompt
                            )
                              return;
                            onOpenPromptParamDialog(
                              selectedPrompt,
                              params,
                              async (userArgs) => {
                                await saveBeadsPromptArgs(field, userArgs);
                              },
                              { initialValues: args || {} },
                            );
                          }}
                          disabled=${argsDisabled}
                          class="shrink-0 p-1.5 rounded border border-mitto-border dark:border-mitto-border-2 bg-white dark:bg-mitto-surface-2 transition-colors ${argsDisabled
                            ? "opacity-50 cursor-not-allowed"
                            : "cursor-pointer hover:bg-mitto-surface-hover dark:hover:bg-mitto-surface-3"}"
                          aria-label=${`Set ${label.toLowerCase()} prompt arguments`}
                          data-testid=${`beads-${field}-args-btn`}
                        >
                          <${SlidersIcon}
                            className="w-4 h-4 text-mitto-text-secondary"
                          />
                        </button>
                      </div>
                    `;
                  })}
                </div>
              `}
        </fieldset>
      `}

      <div class="pt-2 border-t border-mitto-border"></div>

      <p class="text-xs text-mitto-text-muted">
        Integration settings stored in this folder's beads database via${" "}
        <span class="font-mono text-mitto-text-muted">bd config</span>. Use
        namespaced keys such as${" "}
        <span class="font-mono text-mitto-text-muted">jira.url</span>,${" "}
        <span class="font-mono text-mitto-text-muted">github.repo</span>,
        or${" "}
        <span class="font-mono text-mitto-text-muted">${"custom.<key>"}</span>.
      </p>

      ${beadsConfigError &&
      html`
        <div role="alert" class="alert alert-warning alert-soft text-xs">
          ${beadsConfigError}
        </div>
      `}
      ${beadsConfigLoading
        ? html`<div
            class="flex items-center gap-2 text-sm text-mitto-text-muted"
          >
            <${SpinnerIcon} className="w-4 h-4 animate-spin" />
            Loading…
          </div>`
        : beadsConfig &&
          html`
            ${(() => {
              const editable = Object.entries(beadsConfig).filter(([k]) =>
                k.includes("."),
              );
              const system = Object.entries(beadsConfig).filter(
                ([k]) => !k.includes("."),
              );
              return html`
                <div class="space-y-2">
                  ${editable.length === 0
                    ? html`<p class="text-xs text-mitto-text-muted italic">
                        No integration keys set yet.
                      </p>`
                    : editable.map(
                        ([k, v]) => html`
                          <div key=${k} class="flex gap-2 items-center">
                            <input
                              type="text"
                              value=${k}
                              readonly
                              class="input input-sm font-mono cursor-default"
                              style="width: 38%; height: 38px; box-sizing: border-box"
                            />
                            <input
                              key=${k + ":" + v}
                              type="text"
                              defaultValue=${v}
                              disabled=${beadsConfigSaving}
                              onBlur=${(e) => {
                                if (e.target.value !== v)
                                  setBeadsConfigKey(k, e.target.value);
                              }}
                              class="input input-sm flex-1 font-mono"
                              style="height: 38px; box-sizing: border-box"
                            />
                            <button
                              onClick=${() => {
                                if (beadsConfigSaving) return;
                                unsetBeadsConfigKey(k);
                              }}
                              aria-disabled=${beadsConfigSaving
                                ? "true"
                                : "false"}
                              class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom ${beadsConfigSaving
                                ? "opacity-40 pointer-events-none"
                                : ""}"
                              data-tip="Delete this key"
                              aria-label="Delete this key"
                              style="height: 38px; box-sizing: border-box"
                            >
                              <${TrashIcon} className="w-4 h-4" />
                            </button>
                          </div>
                        `,
                      )}

                  <!-- Add a new key -->
                  <div class="flex gap-2 items-center">
                    <input
                      type="text"
                      value=${newBeadsKey}
                      onInput=${(e) => setNewBeadsKey(e.target.value)}
                      placeholder="jira.url"
                      class="input input-sm font-mono"
                      style="width: 38%; height: 38px; box-sizing: border-box"
                    />
                    <input
                      type="text"
                      value=${newBeadsValue}
                      onInput=${(e) => setNewBeadsValue(e.target.value)}
                      placeholder="value"
                      class="input input-sm flex-1 font-mono"
                      style="height: 38px; box-sizing: border-box"
                    />
                    <button
                      onClick=${async () => {
                        const key = newBeadsKey.trim();
                        if (!key) return;
                        if (beadsConfigSaving) return;
                        await setBeadsConfigKey(key, newBeadsValue);
                        setNewBeadsKey("");
                        setNewBeadsValue("");
                      }}
                      aria-disabled=${beadsConfigSaving || !newBeadsKey.trim()
                        ? "true"
                        : "false"}
                      class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom ${beadsConfigSaving ||
                      !newBeadsKey.trim()
                        ? "opacity-40 pointer-events-none"
                        : ""}"
                      data-tip="Add key"
                      aria-label="Add key"
                      style="height: 38px; box-sizing: border-box"
                    >
                      <${PlusIcon} className="w-4 h-4" />
                    </button>
                  </div>
                </div>

                ${system.length > 0 &&
                html`
                  <fieldset class="fieldset pt-2 mt-4">
                    <legend class="fieldset-legend">System</legend>
                    <p class="label">
                      Operational beads settings (read-only here; edit via the
                      bd CLI).
                    </p>
                    <div class="space-y-1">
                      ${system.map(
                        ([k, v]) => html`
                          <div
                            key=${k}
                            class="flex gap-2 text-xs font-mono text-mitto-text-muted"
                          >
                            <span class="truncate" style="width: 38%"
                              >${k}</span
                            >
                            <span class="flex-1 truncate">${String(v)}</span>
                          </div>
                        `,
                      )}
                    </div>
                  </fieldset>
                `}
              `;
            })()}
          `}
    </div>
  `;
}
