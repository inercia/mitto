// Mitto Web Interface - Shared Prompts Menu
// A single searchable, grouped, color-aware prompt picker reused by the
// ChatInput prompts dropup and the periodic-conversation prompt selector.

const { html, Fragment, useState } = window.preact;

import { getPromptIcon, PeriodicIcon } from "./Icons.js";
import {
  getContrastColor,
  flattenPrompts,
  resolvePromptModelOverride,
  currentModelName,
  promptPeriodicMode,
  promptPeriodicDefaultOn,
} from "../utils/prompts.js";

// Source badge (W/F/S) shown on the right of each item when enabled.
function getBadgeInfo(source) {
  if (source === "workspace") {
    return {
      label: "W",
      title: "Workspace prompt",
      bgColor: "bg-green-600/80",
    };
  } else if (source === "file") {
    return {
      label: "F",
      title: "File-based prompt",
      bgColor: "bg-purple-600/80",
    };
  }
  return {
    label: "S",
    title: "Settings prompt",
    bgColor: "bg-mitto-accent-600/80",
  };
}

/**
 * PromptsMenu - shared inner body (filter input + grouped list + optional
 * footer) for prompt pickers. Renders as a flex column; the caller supplies
 * the positioned popover box (with a max-height) around it.
 *
 * @param {Object} props
 * @param {Array} props.prompts - Raw prompt objects
 * @param {string} props.filterText - Current filter value (controlled)
 * @param {Function} props.onFilterChange - (value) => void
 * @param {Function} [props.onFilterKeyDown] - keydown handler for the filter input
 * @param {Object} [props.filterInputRef] - ref for the filter input
 * @param {string} [props.sortMode] - "name" (default) or "color"
 * @param {number} [props.selectedIndex] - flat index highlighted via keyboard (-1 = none)
 * @param {Object} [props.selectedItemRef] - ref attached to the keyboard-highlighted item
 * @param {Function} props.onSelect - (prompt, event, opts?) => void. When
 *   periodicToggle is true, opts is { asPeriodic } for "optional"-mode prompts.
 * @param {string} [props.selectedName] - name of the currently-chosen prompt (shows a check)
 * @param {boolean} [props.showSourceBadge] - show the W/F/S source badge
 * @param {Object} [props.modelOption] - the "model" config option ({ current_value,
 *   options }) used to surface an "overrides model" chip on prompts whose
 *   preferredModels would run them on a different model than the current one
 * @param {Array} [props.modelProfiles] - global model profiles (config.models)
 *   needed to resolve structured preferredModels entries ({modelName}/{modelTag})
 *   into a concrete model. Without this, no override chip can be surfaced.
 * @param {boolean} [props.shiftHeld] - swap the leading icon for an edit pencil
 * @param {*} [props.footer] - optional footer content (rendered below the list)
 * @param {string} [props.placeholder] - filter input placeholder
 * @param {string} [props.emptyText] - empty-state text
 * @param {string} [props.keyPrefix] - key namespace to keep instances distinct
 * @param {string} [props.filterTestId] - data-testid for the filter input
 * @param {string} [props.listTestId] - data-testid for the scrollable list container
 * @param {boolean} [props.periodicToggle] - when true, render a mode-aware periodic
 *   control (toggle for "optional", locked badge for "always") instead of the
 *   static periodic badge; onSelect then receives a 3rd ({ asPeriodic }) arg.
 *   Defaults to false (static badge, unchanged look) for config selectors.
 */
export function PromptsMenu({
  prompts = [],
  filterText = "",
  onFilterChange,
  onFilterKeyDown,
  filterInputRef,
  sortMode = "name",
  selectedIndex = -1,
  selectedItemRef,
  onSelect,
  selectedName = "",
  showSourceBadge = false,
  shiftHeld = false,
  modelOption = null,
  modelProfiles = [],
  footer = null,
  placeholder = "Search prompts...",
  emptyText = "No matching prompts",
  keyPrefix = "pm",
  filterTestId,
  listTestId,
  periodicToggle = false,
}) {
  const { groups, flat } = flattenPrompts(prompts, { filterText, sortMode });
  const clampedIndex =
    flat.length === 0 ? -1 : Math.min(selectedIndex, flat.length - 1);
  const curModelName = currentModelName(modelOption);
  // Per-item periodic override (mode "optional" only), keyed by prompt.name.
  const [periodicOverrides, setPeriodicOverrides] = useState({});

  const renderItem = (prompt) => {
    const fi = flat.indexOf(prompt);
    const isKbSelected = fi >= 0 && fi === clampedIndex;
    const isChosen = selectedName && prompt.name === selectedName;
    const baseStyle = prompt.backgroundColor
      ? {
          backgroundColor: prompt.backgroundColor,
          color: getContrastColor(prompt.backgroundColor),
        }
      : {};
    const style = isKbSelected
      ? {
          ...baseStyle,
          backgroundColor:
            baseStyle.backgroundColor || "rgba(220, 38, 38, 0.15)",
          boxShadow: "inset 3px 0 0 0 var(--accent)",
        }
      : baseStyle;
    const PromptIcon = getPromptIcon(prompt.icon);
    const overrideModel = resolvePromptModelOverride(
      prompt.preferredModels,
      modelOption,
      modelProfiles,
    );
    return html`
      <li key=${keyPrefix + "-item-" + prompt.name}>
        <button
          type="button"
          onClick=${(e) => {
            const asPeriodic =
              promptPeriodicMode(prompt) === "optional"
                ? periodicOverrides[prompt.name] !== undefined
                  ? periodicOverrides[prompt.name]
                  : promptPeriodicDefaultOn(prompt)
                : undefined;
            onSelect && onSelect(prompt, e, { asPeriodic });
          }}
          title=${prompt.description || prompt.name}
          class="prompt-item w-full text-left px-4 py-2.5 text-sm text-mitto-text hover:brightness-110 transition-all flex items-center gap-2 rounded-none"
          style=${style}
          aria-selected=${isChosen ? "true" : "false"}
          ref=${isKbSelected ? selectedItemRef : null}
        >
          ${shiftHeld
            ? html`<svg
                class="w-4 h-4 shrink-0 opacity-60"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z"
                />
              </svg>`
            : PromptIcon
              ? html`<${PromptIcon} className="w-4 h-4 shrink-0 opacity-60" />`
              : html`<svg
                  class="w-4 h-4 shrink-0 opacity-60"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    stroke-width="2"
                    d="M13 10V3L4 14h7v7l9-11h-7z"
                  />
                </svg>`}
          <span class="truncate flex-1 min-w-0">${prompt.name}</span>
          ${!periodicToggle &&
          prompt.periodic &&
          html`<span
            class="shrink-0 text-success opacity-80"
            title="Periodic prompt — sets the conversation to recurring mode"
            ><${PeriodicIcon} className="w-3.5 h-3.5"
          /></span>`}
          ${periodicToggle &&
          (() => {
            const mode = promptPeriodicMode(prompt);
            if (mode === "none") return null;
            if (mode === "optional") {
              const on =
                periodicOverrides[prompt.name] !== undefined
                  ? periodicOverrides[prompt.name]
                  : promptPeriodicDefaultOn(prompt);
              return html`<input
                type="checkbox"
                class="checkbox checkbox-sm shrink-0"
                style="background-color: transparent"
                checked=${on}
                title=${on
                  ? "Periodic: ON — click to disable recurring runs"
                  : "Periodic: OFF — click to run as recurring conversation"}
                onClick=${(e) => e.stopPropagation()}
                onChange=${(e) => {
                  e.stopPropagation();
                  setPeriodicOverrides((m) => ({
                    ...m,
                    [prompt.name]: e.target.checked,
                  }));
                }}
              />`;
            }
            // mode === "always": checked, locked checkbox (checked + disabled)
            // so it reads coherently next to the optional toggle above —
            // same control, but permanently on and not changeable.
            return html`<input
              type="checkbox"
              class="checkbox checkbox-sm shrink-0"
              style="background-color: transparent"
              checked=${true}
              disabled
              title="Always periodic — this prompt always runs as a recurring conversation (cannot be changed)"
              onClick=${(e) => e.stopPropagation()}
            />`;
          })()}
          ${overrideModel &&
          html`<span
            class="text-[10px] font-bold px-1.5 py-0.5 rounded bg-mitto-accent-600/80 text-white/90 shrink-0"
            title=${"Runs on " +
            overrideModel.name +
            " for this prompt" +
            (curModelName
              ? " — your conversation model stays " + curModelName
              : "")}
            >⚡</span
          >`}
          ${showSourceBadge &&
          html`<span
            class="text-[10px] font-bold px-1.5 py-0.5 rounded ${getBadgeInfo(
              prompt.source,
            ).bgColor} text-white/90 shrink-0"
            title=${getBadgeInfo(prompt.source).title}
            >${getBadgeInfo(prompt.source).label}</span
          >`}
          ${isChosen &&
          html`<svg
            class="w-4 h-4 shrink-0 text-mitto-accent"
            fill="currentColor"
            viewBox="0 0 20 20"
          >
            <path
              fill-rule="evenodd"
              d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z"
              clip-rule="evenodd"
            />
          </svg>`}
        </button>
      </li>
    `;
  };

  return html`
    <${Fragment}>
      <div class="px-2 pt-2 pb-1 shrink-0">
        <input
          ref=${filterInputRef}
          type="text"
          value=${filterText}
          onInput=${(e) => onFilterChange && onFilterChange(e.target.value)}
          onKeyDown=${onFilterKeyDown}
          placeholder=${placeholder}
          autocomplete="off"
          autocorrect="off"
          autocapitalize="off"
          spellcheck=${false}
          data-testid=${filterTestId}
          class="input input-sm w-full"
        />
      </div>
      <div
        class="py-1 overflow-y-auto flex-1 min-h-0"
        style="scrollbar-gutter: stable;"
        data-testid=${listTestId}
      >
        <ul class="flex flex-col w-full p-0 m-0 list-none">
          ${groups.map(
            (g) => html`
              <${Fragment} key=${keyPrefix + "-group-" + g.name}>
                <li class="menu-title px-4 py-2 text-xs font-semibold text-mitto-text-muted uppercase tracking-wider bg-mitto-surface-3/30">
                  ${g.name}
                </li>
                ${g.prompts.map(renderItem)}
              </${Fragment}>
            `,
          )}
        </ul>
        ${
          flat.length === 0 &&
          html`<div class="px-4 py-3 text-xs text-mitto-text-muted text-center">
            ${emptyText}
          </div>`
        }
      </div>
      ${
        footer &&
        html`<div class="px-3 py-1.5 border-t border-mitto-border-1 shrink-0">
          ${footer}
        </div>`
      }
    </${Fragment}>
  `;
}
