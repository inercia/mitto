// Mitto Web Interface - Prompt Menu Utilities

import { getSdkClient } from "./sdkClient.js";
import { endpoints } from "./endpoints.js";

/**
 * Returns the list of UI menus a prompt opts INTO (positive tokens only).
 * The `menus` front-matter is a comma-separated list (e.g. "prompts, conversation").
 * Tokens prefixed with `!` (e.g. "!promptsLoop") are exclusions and are
 * stripped from the returned list — use `promptMenuExcludes` to read them.
 * A missing or empty value (after stripping exclusion tokens) defaults to
 * ["prompts"], so prompts that explicitly target other menus (e.g. "conversation")
 * are excluded from the dropup unless they also list "prompts".
 */
export function promptMenus(prompt) {
  const raw = typeof prompt?.menus === "string" ? prompt.menus.trim() : "";
  if (raw === "") return ["prompts"];
  const positive = raw
    .split(",")
    .map((m) => m.trim())
    .filter((m) => m && !m.startsWith("!"));
  return positive.length > 0 ? positive : ["prompts"];
}

/**
 * Returns a Set of menu names that a prompt explicitly opts OUT of (the
 * `!`-prefixed tokens in the `menus` front-matter). For example, for
 * `menus: "prompts, !promptsLoop"` it returns `new Set(["promptsLoop"])`.
 * Robust to null/undefined/empty (returns an empty Set).
 *
 * @param {Object} prompt - Prompt object with optional `menus` string
 * @returns {Set<string>} Set of excluded menu names (without the leading `!`)
 */
export function promptMenuExcludes(prompt) {
  const raw = typeof prompt?.menus === "string" ? prompt.menus.trim() : "";
  if (raw === "") return new Set();
  const excluded = new Set();
  for (const token of raw.split(",")) {
    const t = token.trim();
    if (t.startsWith("!")) {
      const name = t.slice(1).trim();
      if (name) excluded.add(name);
    }
  }
  return excluded;
}

/**
 * Returns true when a prompt is a positive member of `menu`, honoring
 * both inclusions and `!`-prefixed exclusions. Equivalent to:
 *   promptMenus(prompt).includes(menu) && !promptMenuExcludes(prompt).has(menu)
 *
 * This is the canonical membership check to use at every call site instead of a
 * bare `promptMenus(p).includes(menu)`, so that exclusions are always respected.
 *
 * @param {Object} prompt - Prompt object with optional `menus` string
 * @param {string} menu   - Menu name to check (e.g. "prompts", "promptsLoop")
 * @returns {boolean}
 */
export function promptMenuIncludes(prompt, menu) {
  return (
    promptMenus(prompt).includes(menu) && !promptMenuExcludes(prompt).has(menu)
  );
}

/**
 * True when a prompt declares it must not have multiple concurrent
 * conversation instances (singleton). Absent/false → not singleton.
 */
export function isSingletonPrompt(prompt) {
  return prompt?.singleton === true;
}

/**
 * Returns the loop mode of a prompt: "always" | "optional" | "none".
 * - "none"     when prompt.loop is absent/null (never a loop).
 * - "optional" when prompt.loop.mode === "optional".
 * - "always"   otherwise (block present with absent/unknown mode → backend default).
 */
export function promptLoopMode(prompt) {
  const loop = prompt?.loop;
  if (!loop) return "none";
  return loop.mode === "optional" ? "optional" : "always";
}

/** True iff the prompt's loop mode is "optional" (the only toggleable category). */
export function promptLoopIsToggleable(prompt) {
  return promptLoopMode(prompt) === "optional";
}

/**
 * Initial send-as-loop state:
 * - "always"   → true (locked ON)
 * - "optional" → prompt.loop.default !== false (nil/true → true, false → false)
 * - "none"     → false
 */
export function promptLoopDefaultOn(prompt) {
  const mode = promptLoopMode(prompt);
  if (mode === "none") return false;
  if (mode === "optional") return prompt.loop.default !== false;
  return true;
}

/**
 * Resolve whether a given send should be dispatched as a loop.
 * @param {object} prompt - the prompt object (may have prompt.loop with mode/default).
 * @param {boolean} [override] - explicit per-send choice from a UI toggle; only honored for mode "optional".
 * @returns {boolean}
 */
export function promptResolveAsLoop(prompt, override) {
  const mode = promptLoopMode(prompt);
  if (mode === "none") return false; // never a loop (override ignored)
  if (mode === "always") return true; // locked ON (override ignored)
  // mode === "optional":
  if (typeof override === "boolean") return override;
  return promptLoopDefaultOn(prompt);
}

/**
 * Frontend mirror of the backend parameter-type registry.
 * Canonical source of truth: internal/prompts/param_types.go
 * These two lists MUST be kept in sync — do not add types here without also
 * adding them to the Go registry, and vice versa.
 *
 * Type semantics:
 *   beadsId        — a beads issue ID (e.g. "mitto-42")
 *   beadsTitle     — a beads issue title (free text, typically auto-filled)
 *   sessionId      — a Mitto conversation/session UUID
 *   childSessionId — a child conversation/session UUID (relative to the host conversation)
 *   workspaceId    — a Mitto workspace UUID
 *   workspaceFolder — an absolute path to the workspace root directory,
 *                    rendered as a dropdown of the known workspace folders
 *                    (labelled by display name, valued by absolute path).
 *                    Interactive, dialog-collected (like boolean/prompts): no
 *                    menu auto-supplies it and it never gates menu visibility.
 *   acpServer      — an ACP server (agent) name
 *   text           — generic free-form text (catch-all)
 *   boolean        — a yes/no flag, rendered as a checkbox; supplied as the
 *                    string "true"/"false" (see PromptParameterDialog)
 *   prompts        — the NAME of another workspace prompt, rendered as a picker
 *                    in the parameter dialog. Interactive, dialog-collected (like
 *                    boolean): no menu auto-supplies it and it never gates menu
 *                    visibility. Feeds the {{ PromptText .Args.NAME }} template
 *                    action. multiLine is not supported.
 *   filename       — a workspace-relative file path, rendered as a dropdown of
 *                    files under an optional `dir` (workspace-relative,
 *                    non-recursive), optionally filtered by a `glob`
 *                    (filepath.Match). Interactive, dialog-collected (like
 *                    boolean/prompts): no menu auto-supplies it and it never
 *                    gates menu visibility. Feeds the {{ ReadFile .Args.NAME }}
 *                    template action.
 *   dirname        — a workspace-relative directory path, rendered as a
 *                    dropdown of immediate sub-directories under an optional
 *                    `dir` (workspace-relative, non-recursive), optionally
 *                    filtered by a `glob` (filepath.Match on the base name).
 *                    Interactive, dialog-collected (like filename): no menu
 *                    auto-supplies it and it never gates menu visibility.
 *                    Hidden directories (leading ".") are excluded by default.
 */
export const KNOWN_PARAM_TYPES = [
  "beadsId",
  "beadsTitle",
  "sessionId",
  "childSessionId",
  "workspaceId",
  "workspaceFolder",
  "acpServer",
  "text",
  "boolean",
  "prompts",
  "filename",
  "dirname",
];

/**
 * Returns true if the parameter is a boolean (checkbox) type.
 *
 * Boolean parameters are special: a checkbox always has a definite answer
 * (checked/unchecked), so they never gate menu visibility (menuSatisfies) and
 * they always force the dialog open (shouldOpenPromptDialog), regardless of
 * the menu's auto-supplied types or the `required` flag.
 */
export function isBooleanParam(p) {
  return p?.type === "boolean";
}

/**
 * Returns true if the parameter is a `type: text` parameter that declares a
 * non-empty `options` array — i.e. it renders as a dropdown picker
 * (PromptParameterDialog) rather than a free-text input, even though its
 * declared `type` is "text". Exported (not inlined) so every call site that
 * needs to distinguish "text as picker" from "text as free-text input" uses
 * the identical test — a duplicated inline check across files is exactly how
 * this class of bug (mitto-cwz.1) arose.
 */
export function isOptionsPickerParam(p) {
  return p?.type === "text" && Array.isArray(p.options) && p.options.length > 0;
}

/**
 * Returns true if the parameter is an *interactive picker* type — i.e. one that
 * no menu can auto-supply and that must always be collected via the parameter
 * dialog. Currently: `boolean` (checkbox), `prompts` (workspace-prompt picker),
 * `filename` (workspace-file dropdown), `dirname` (workspace-directory
 * dropdown), `workspaceFolder` (workspace-folder dropdown), and `text` with a
 * declared `options` array (dropdown picker — see isOptionsPickerParam).
 *
 * Rationale: these parameters carry values that no menu context has in scope
 * (a workspace-prompt name, a checkbox answer, a workspace-relative
 * file/directory path, a workspace folder, or a value from a fixed option
 * list). They behave like `boolean` for gating purposes — never gating menu
 * visibility (menuSatisfies) and always forcing the dialog open
 * (shouldOpenPromptDialog) regardless of `required` or the menu's
 * auto-supplied types. The dialog offers the picker unconditionally.
 */
export function isInteractivePickerParam(p) {
  return (
    p?.type === "boolean" ||
    p?.type === "prompts" ||
    p?.type === "filename" ||
    p?.type === "dirname" ||
    p?.type === "workspaceFolder" ||
    isOptionsPickerParam(p)
  );
}

/**
 * Returns true if the parameter declares `show: always` — i.e. its presence
 * forces the parameter dialog open even for an otherwise-satisfied prompt,
 * and it is rendered editable once the dialog is open (even for a
 * menu-supplied param, which would otherwise render read-only — see
 * promptDialogParameters). It stays non-blocking when the parameter is
 * optional: `show: always` never gates menu visibility.
 */
export function isAlwaysShownParam(p) {
  return p?.show === "always";
}

/**
 * Returns true if the parameter declares `show: never` — i.e. it is never
 * rendered in the parameter dialog and never contributes to the open
 * decision. Its value must come from a menu, a declared default, or a
 * cached value.
 */
export function isNeverShownParam(p) {
  return p?.show === "never";
}

/**
 * Returns the structured parameters array for a prompt, or [] if absent/empty.
 * Each entry is { name, type, description?, required?, multiLine?, options? }.
 * multiLine is only meaningful for type "text": when true the dialog renders a
 * resizable multi-line textarea instead of a single-line input. options is also
 * only meaningful for type "text": when non-empty the dialog renders a dropdown
 * of those values instead of a free-text input (mutually exclusive with
 * multiLine — see PromptParameterDialog).
 */
export function promptParameters(prompt) {
  const params = prompt?.parameters;
  if (Array.isArray(params) && params.length > 0) return params;
  return [];
}

/**
 * Parameter types that each menu can auto-supply from its selection context.
 * A prompt is shown in a menu only when every type it declares is in that
 * menu's provided-types list (see menuSatisfies).
 *
 * beadsIssues provides beadsId and beadsTitle because the per-issue context
 * menu always has the selected issue in scope.
 */
export const MENU_PARAM_TYPES = {
  prompts: [],
  promptsLoop: [],
  conversation: [],
  beadsIssues: ["beadsId", "beadsTitle"],
  beadsList: [],
};

/**
 * Returns true if `menu` can supply every *required* parameter type that the
 * prompt declares. A prompt with no parameters is satisfied by any menu
 * (including unknown ones). For an unknown menu, its provided types are treated
 * as [] (so a prompt WITH required params is NOT satisfied — matching old
 * behaviour).
 *
 * Optional parameters (`required === false`) are never gating: a prompt that
 * declares an optional `beadsId` param appears in BOTH `beadsIssues` AND
 * `conversation` menus even though `conversation` cannot auto-supply it. When
 * the menu can supply the type, the value is auto-filled; when it cannot, the
 * param does not force the dialog open (see shouldOpenPromptDialog), though it
 * still renders (read-only or editable) once the dialog opens for another
 * reason (see promptDialogParameters).
 *
 * Unset (`required` absent/null) or `required: true` keeps the current gating
 * behaviour, preserving all existing prompts unchanged.
 *
 * Interactive picker parameters (boolean, prompts, text+options, ...) never
 * gate: a checkbox always has a definite answer, a workspace-prompt picker is
 * always offered by the dialog, and a text+options dropdown always has a
 * fixed value list, so all of them behave like an optional param for
 * visibility purposes (they are collected via the dialog rather than
 * auto-supplied by a menu). See isInteractivePickerParam.
 */
export function menuSatisfies(prompt, menu) {
  const params = promptParameters(prompt);
  if (params.length === 0) return true;
  const provided = MENU_PARAM_TYPES[menu] || [];
  return params.every(
    (p) =>
      isInteractivePickerParam(p) ||
      p.required === false ||
      provided.includes(p.type),
  );
}

/**
 * Returns true if a declared parameter's type is auto-suppliable by `menu`
 * (i.e. the menu's context already has a value for it in scope).
 */
function isMenuSupplied(p, menu) {
  const provided = MENU_PARAM_TYPES[menu] || [];
  return provided.includes(p.type);
}

/**
 * Returns the ordered list of declared parameters to RENDER in the parameter
 * dialog once it is open — the render axis. This is deliberately independent
 * of whether the dialog itself should be open (see shouldOpenPromptDialog):
 * it answers "what fields appear in the form", not "does the form appear".
 *
 * Rules:
 *   - `show: never` is excluded — never rendered, regardless of type/required/menu.
 *   - Every other declared parameter IS included, in declared order — this is
 *     the fix for the historical bug where an optional free-text parameter was
 *     silently dropped even when the dialog was already open for other
 *     parameters (mitto-9rff): `show: auto` (the default) now renders
 *     unconditionally once the dialog opens for any reason.
 *   - A parameter whose type IS auto-suppliable by `menu`, OR whose name is in
 *     `knownNames` (e.g. a childSessionId auto-filled from the host
 *     conversation's context — see autofillConversationMenuArgs), is still
 *     included, but marked `readOnly: true` (unless it declares `show:
 *     always`, which promotes it to editable) so the dialog shows the value
 *     without letting the user override context already resolved elsewhere.
 *
 * @param {Object} prompt - Prompt object with optional `parameters` array
 * @param {string} menu   - Menu key (e.g. "beadsIssues", "prompts")
 * @param {Set|Array} [knownNames] - names already resolved outside the menu
 *   (e.g. host-conversation autofill); rendered read-only like menu-supplied
 * @returns {Array}       - Parameters to render, each possibly annotated with `readOnly: true`
 */
export function promptDialogParameters(prompt, menu, knownNames) {
  const params = promptParameters(prompt);
  if (params.length === 0) return [];
  const known =
    knownNames instanceof Set ? knownNames : new Set(knownNames || []);
  return params
    .filter((p) => !isNeverShownParam(p))
    .map((p) => {
      const readOnly =
        (isMenuSupplied(p, menu) || known.has(p.name)) &&
        !isAlwaysShownParam(p);
      return readOnly ? { ...p, readOnly: true } : p;
    });
}

/**
 * Splits a parameter dialog's rendered parameters (see promptDialogParameters)
 * into tab groups. Purely presentational — never touches parameter values.
 *
 * Gate: `tabbed` is true when AT LEAST ONE parameter declares a non-empty
 * (post-trim) `group`. Deliberately `parameters.some(p => p.group?.trim())`,
 * NOT `distinctGroups.length > 1` — the two differ when every parameter
 * shares a single explicit group name (e.g. all params in "Changes
 * Submission"): in that case a single named tab must still be shown, because
 * the author explicitly asked for one (see mitto-boio requester clarification).
 *
 * When `!tabbed`, returns a single unnamed group holding every parameter in
 * original order — the caller uses this to render today's flat markup
 * byte-identical to before (no tab bar, no "General" header, no wrapper).
 *
 * When `tabbed`, ungrouped parameters (empty/whitespace-only `group`) are
 * collected into a "General" group placed FIRST, followed by one group per
 * distinct trimmed `group` value in first-appearance order. "General" is not
 * a reserved name: an explicit `group: General` merges into the same group
 * as the ungrouped parameters.
 *
 * @param {Array} parameters - dialog-rendered parameters (each may have `group`)
 * @returns {{tabbed: boolean, groups: Array<{name: string, params: Array}>}}
 */
export function groupDialogParameters(parameters) {
  const params = Array.isArray(parameters) ? parameters : [];
  const tabbed = params.some((p) => p?.group && p.group.trim() !== "");
  if (!tabbed) {
    return { tabbed: false, groups: [{ name: "", params }] };
  }
  const general = [];
  const named = []; // [{ name, params }], first-appearance order
  const namedIndex = new Map(); // trimmed name -> index into named
  for (const p of params) {
    const raw = p?.group ? p.group.trim() : "";
    // "General" is not a reserved name: an explicit `group: General` merges
    // into the same tab as ungrouped parameters (see doc comment above).
    if (raw === "" || raw === "General") {
      general.push(p);
      continue;
    }
    if (namedIndex.has(raw)) {
      named[namedIndex.get(raw)].params.push(p);
    } else {
      namedIndex.set(raw, named.length);
      named.push({ name: raw, params: [p] });
    }
  }
  const groups = [];
  if (general.length > 0) groups.push({ name: "General", params: general });
  groups.push(...named);
  return { tabbed: true, groups };
}

/**
 * Returns the Set of group names (as produced by groupDialogParameters) that
 * currently hold at least one unmet required parameter — i.e. a required,
 * non-boolean, non-readOnly parameter whose trimmed value is empty. Mirrors
 * the predicate PromptParameterDialog's canSave already applies to the flat
 * parameter list (kept in one place conceptually), used here only to flag
 * which TAB to visually mark so a required field hidden on an inactive tab
 * is discoverable as the reason Save is disabled. Never gates Save itself.
 *
 * @param {Array<{name: string, params: Array}>} groups - from groupDialogParameters
 * @param {Object} values - current { [paramName]: value } map
 * @returns {Set<string>} group names with at least one unmet required field
 */
export function unmetRequiredByGroup(groups, values) {
  const result = new Set();
  const vals = values || {};
  for (const g of groups || []) {
    for (const p of g.params || []) {
      if (!p || !p.required || p.readOnly || isBooleanParam(p)) continue;
      const v = vals[p.name];
      if (typeof v !== "string" || v.trim() === "") {
        result.add(g.name);
        break;
      }
    }
  }
  return result;
}

/**
 * Returns true if the parameter dialog should OPEN for `prompt` under `menu`
 * — the open axis, independent of what gets rendered (see
 * promptDialogParameters). `cachedNames` (a Set or array of parameter names
 * already cached for this conversation, from fetchCachedParamNames) removes
 * their contribution to the open decision so caching keeps saving clicks;
 * cached params still RENDER (prefilled and editable) once the dialog opens
 * for another reason. `knownNames` behaves the same for the open decision
 * (excluded) but renders read-only, like a menu-supplied param — see
 * promptDialogParameters.
 *
 * The dialog opens when any declared parameter is, after excluding cached
 * and known ones:
 *   - an interactive picker (boolean, prompts, text+options, filename,
 *     dirname, workspaceFolder — see isInteractivePickerParam), or
 *   - required (`required !== false`) AND not auto-suppliable by `menu`, or
 *   - declared `show: always`.
 * `show: never` parameters never contribute to the open decision.
 *
 * @param {Object} prompt - Prompt object with optional `parameters` array
 * @param {string} menu   - Menu key (e.g. "beadsIssues", "prompts")
 * @param {Set|Array} [cachedNames] - names already cached for this conversation
 * @param {Set|Array} [knownNames] - names already resolved outside the menu
 * @returns {boolean}
 */
export function shouldOpenPromptDialog(prompt, menu, cachedNames, knownNames) {
  const params = promptParameters(prompt);
  if (params.length === 0) return false;
  const cached =
    cachedNames instanceof Set ? cachedNames : new Set(cachedNames || []);
  const known =
    knownNames instanceof Set ? knownNames : new Set(knownNames || []);
  return params.some((p) => {
    if (isNeverShownParam(p)) return false;
    if (isCacheableParam(p) && cached.has(p.name)) return false;
    if (known.has(p.name)) return false;
    return (
      isInteractivePickerParam(p) ||
      (p.required !== false && !isMenuSupplied(p, menu)) ||
      isAlwaysShownParam(p)
    );
  });
}

/**
 * True when a parameter declares a cache block (per-conversation value caching).
 */
export function isCacheableParam(p) {
  return !!(p && p.cache);
}

/**
 * Fetch the set of parameter names currently cached (fresh) for a prompt in a
 * conversation. Names only — never values. Tolerant of errors: on any failure
 * (network, non-2xx, unknown session) returns an EMPTY Set so callers fall back
 * to today's behavior (ask). `fetchImpl` is injectable for tests (defaults to
 * the SDK client, mitto-7gta.17 S8).
 * @returns {Promise<Set<string>>}
 */
export async function fetchCachedParamNames(
  sessionId,
  promptName,
  { fetchImpl } = {},
) {
  if (!sessionId || !promptName) return new Set();
  try {
    let data;
    if (fetchImpl) {
      const resp = await fetchImpl(
        endpoints.sessions.promptArgCache(sessionId, promptName),
      );
      if (!resp || !resp.ok) return new Set();
      data = await resp.json();
    } else {
      data = await getSdkClient().sessions.promptArgCache(
        sessionId,
        promptName,
      );
    }
    return new Set(Array.isArray(data && data.cached) ? data.cached : []);
  } catch (_err) {
    return new Set();
  }
}

/**
 * Build the arguments map for a prompt from a map of type → value.
 * For each declared parameter { name, type }, if typeValues[type] is defined
 * (not undefined/null), the parameter's name is mapped to that value.
 * Returns a plain object (possibly empty).
 *
 * Example:
 *   collectPromptArguments(prompt, { beadsId: "mitto-42", beadsTitle: "Fix bug" })
 *   // → { IssueID: "mitto-42" }  (for a prompt with param { name:"IssueID", type:"beadsId" })
 */
export function collectPromptArguments(prompt, typeValues) {
  const result = {};
  for (const { name, type } of promptParameters(prompt)) {
    const val = typeValues[type];
    if (val !== undefined && val !== null) {
      result[name] = val;
    }
  }
  return result;
}

/**
 * Auto-fill prompt arguments from the conversation-menu host context.
 *
 * The conversation menu acts on a specific host conversation, so a
 * `childSessionId` parameter can be filled automatically when that host has
 * exactly one (non-archived) child — otherwise the user picks via the dialog,
 * scoped to the host's children. No other types are auto-supplied here.
 *
 * @param {Object} prompt        - prompt object with optional `parameters`
 * @param {string} hostSessionId - the conversation the menu acts on
 * @param {Array}  sessions      - all known sessions (each may have parent_session_id)
 * @returns {Object}             - arguments map (paramName -> value), possibly empty
 */
export function autofillConversationMenuArgs(prompt, hostSessionId, sessions) {
  const result = {};
  if (!hostSessionId) return result;
  for (const { name, type } of promptParameters(prompt)) {
    if (type === "childSessionId") {
      const children = (sessions || []).filter(
        (s) => s && !s.archived && s.parent_session_id === hostSessionId,
      );
      if (children.length === 1) {
        result[name] = children[0].session_id;
      }
    }
  }
  return result;
}

/**
 * Calculate a contrasting text color (black or white) for a given background.
 * @param {string} hexColor - Hex color string (e.g., "#E8F5E9")
 * @returns {string} - "#000000", "#FFFFFF", or a default gray when no color
 */
export function getContrastColor(hexColor) {
  if (!hexColor || !hexColor.startsWith("#")) return "#E5E7EB"; // Default gray-200
  const hex = hexColor.replace("#", "");
  const r = parseInt(hex.substr(0, 2), 16);
  const g = parseInt(hex.substr(2, 2), 16);
  const b = parseInt(hex.substr(4, 2), 16);
  const luminance = (0.299 * r + 0.587 * g + 0.114 * b) / 255;
  return luminance > 0.5 ? "#000000" : "#FFFFFF";
}

/**
 * Convert hex color to HSL values for sorting.
 * @param {string} hexColor - Hex color string
 * @returns {Object|null} - { h: 0-360, s: 0-100, l: 0-100 } or null if invalid
 */
export function hexToHSL(hexColor) {
  if (!hexColor || !hexColor.startsWith("#")) return null;
  const hex = hexColor.replace("#", "");
  const r = parseInt(hex.substr(0, 2), 16) / 255;
  const g = parseInt(hex.substr(2, 2), 16) / 255;
  const b = parseInt(hex.substr(4, 2), 16) / 255;
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const l = (max + min) / 2;
  if (max === min) {
    return { h: 0, s: 0, l: l * 100 };
  }
  const d = max - min;
  const s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
  let h;
  switch (max) {
    case r:
      h = ((g - b) / d + (g < b ? 6 : 0)) / 6;
      break;
    case g:
      h = ((b - r) / d + 2) / 6;
      break;
    case b:
      h = ((r - g) / d + 4) / 6;
      break;
  }
  return { h: h * 360, s: s * 100, l: l * 100 };
}

/**
 * Compute a single numeric color score for consistent sorting. Groups similar
 * colors via quantized hue buckets. Lower scores sort first; no color = end.
 */
export function getColorScore(hsl) {
  if (!hsl) return Infinity;
  const hueBucket = Math.floor(hsl.h / 30);
  return hueBucket * 10000 + (100 - hsl.s) * 100 + hsl.l;
}

/**
 * Sort prompts by color (hue bucket), then by name. Prompts without colors are
 * sorted to the end.
 */
export function sortPromptsByColor(prompts) {
  return [...prompts].sort((a, b) => {
    const scoreA = getColorScore(hexToHSL(a.backgroundColor));
    const scoreB = getColorScore(hexToHSL(b.backgroundColor));
    if (scoreA !== scoreB) return scoreA - scoreB;
    return a.name.localeCompare(b.name);
  });
}

/**
 * Filter, group, and sort a list of prompts for rendering in a prompts menu.
 * Returns both the ordered group structure and a flat array (in render order)
 * so callers can drive keyboard navigation off the same ordering.
 *
 * @param {Array} prompts - Raw prompt objects
 * @param {Object} opts
 * @param {string} [opts.filterText] - Case-insensitive name/description filter
 * @param {string} [opts.sortMode] - "name" (default) or "color"
 * @returns {{ groups: Array<{name: string, prompts: Array, isOther?: boolean}>, flat: Array }}
 */
export function flattenPrompts(prompts, opts) {
  const { filterText = "", sortMode = "name" } = opts || {};
  const lower = filterText.toLowerCase().trim();
  const filtered = lower
    ? prompts.filter(
        (p) =>
          (p.name || "").toLowerCase().includes(lower) ||
          (p.description || "").toLowerCase().includes(lower),
      )
    : prompts;

  const grouped = {};
  const ungrouped = [];
  filtered.forEach((p) => {
    if (p.group) {
      if (!grouped[p.group]) grouped[p.group] = [];
      grouped[p.group].push(p);
    } else {
      ungrouped.push(p);
    }
  });

  const sortFn =
    sortMode === "color"
      ? sortPromptsByColor
      : (arr) => [...arr].sort((a, b) => a.name.localeCompare(b.name));

  const groups = [];
  const flat = [];
  Object.keys(grouped)
    .sort()
    .forEach((name) => {
      const arr = sortFn(grouped[name]);
      groups.push({ name, prompts: arr });
      arr.forEach((p) => flat.push(p));
    });
  const ung = sortFn(ungrouped);
  if (ung.length > 0) {
    groups.push({ name: "Other", prompts: ung, isOther: true });
    ung.forEach((p) => flat.push(p));
  }
  return { groups, flat };
}

/**
 * Frontend mirror of backend config.ConstraintMatchesName
 * (internal/config/config.go). Reports whether `name` matches a criteria
 * `{ matchMode, pattern }` case-insensitively. A nil/empty criteria never
 * matches. Keep in sync with the Go implementation.
 */
function constraintMatchesName(criteria, name) {
  if (!criteria) return false;
  const pattern = String(criteria.pattern || "");
  const patternLower = pattern.toLowerCase();
  const nameStr = String(name || "");
  const nameLower = nameStr.toLowerCase();
  switch (criteria.matchMode) {
    case "contains":
      return nameLower.includes(patternLower);
    case "exact":
      return nameLower === patternLower;
    case "startsWith":
      return nameLower.startsWith(patternLower);
    case "regex": {
      if (!pattern) return false;
      try {
        return new RegExp(pattern, "i").test(nameStr);
      } catch (_e) {
        return false;
      }
    }
    case "lookAlike": {
      const words = patternLower.split(/\s+/).filter(Boolean);
      if (words.length === 0) return false;
      return words.every((w) => nameLower.includes(w));
    }
    default:
      return false;
  }
}

/**
 * Frontend mirror of backend ResolveProfileModel + MatchConstraintOption
 * (internal/conversation/constraints.go). Iterates the modelOption's options
 * and returns the LAST option whose display name matches the profile's
 * criteria — so when models are ordered by version, the latest wins. Returns
 * null when profile/criteria is missing or nothing matches.
 */
function resolveProfileModel(profile, modelOption) {
  if (
    !profile ||
    !profile.criteria ||
    !modelOption ||
    !Array.isArray(modelOption.options)
  ) {
    return null;
  }
  let matched = null;
  for (const opt of modelOption.options) {
    if (constraintMatchesName(profile.criteria, opt.name || "")) {
      matched = opt;
    }
  }
  return matched
    ? { value: matched.value, name: matched.name || matched.value }
    : null;
}

/**
 * Frontend mirror of backend SelectPreferredModel
 * (internal/conversation/constraints.go). The Go function is the canonical
 * source of truth — keep this in sync.
 *
 * Resolves a prompt's ordered `preferredModels` — structured references to
 * global model profiles (Settings → Models) — against the live "model" config
 * option to decide which model the prompt would transiently run on. Each
 * entry is `{ modelName }` (single named profile) or `{ modelTag }` (any
 * profile carrying that tag, first-yielding wins by profile order). For each
 * entry the CURRENT model is checked first: if it already satisfies the
 * entry, the prompt keeps the current model and no override chip is shown.
 *
 * Priority axis is profile-list order (mitto-ex7 "list order = priority"
 * contract, mirrors backend config.ProfilesByTag): both the modelName path
 * (via `modelProfiles.find`) and the modelTag path (via `modelProfiles.filter`
 * + linear scan) walk `modelProfiles` in-order, so reordering the global
 * `models:` list flips which profile wins for the same name/tag.
 *
 * @param {Array<{modelName?: string, modelTag?: string}>} preferredModels
 *   ordered preference entries.
 * @param {Object} modelOption the "model" category config option
 *   ({ current_value, options: [{ value, name }] }).
 * @param {Array<{name: string, criteria: {matchMode: string, pattern: string},
 *   tags?: string[]}>} modelProfiles the global model profiles from
 *   config.models.
 * @returns {{ value: string, name: string } | null} the override model when
 *   it DIFFERS from the current conversation model; null when there is no
 *   override (no entries, no model option, no profiles, nothing matches, or
 *   the current model already satisfies an entry).
 */
export function resolvePromptModelOverride(
  preferredModels,
  modelOption,
  modelProfiles,
) {
  if (
    !Array.isArray(preferredModels) ||
    preferredModels.length === 0 ||
    !modelOption ||
    !Array.isArray(modelOption.options) ||
    modelOption.options.length === 0 ||
    !Array.isArray(modelProfiles) ||
    modelProfiles.length === 0
  ) {
    return null;
  }
  const currentId = modelOption.current_value || "";
  const currentOpt = modelOption.options.find((o) => o.value === currentId);
  const currentName = currentOpt ? currentOpt.name || "" : "";

  for (const entry of preferredModels) {
    if (!entry || typeof entry !== "object") continue;
    const modelName = entry.modelName ? String(entry.modelName) : "";
    const modelTag = entry.modelTag ? String(entry.modelTag) : "";

    if (modelName) {
      const profile = modelProfiles.find(
        (p) => p && p.name && p.name.toLowerCase() === modelName.toLowerCase(),
      );
      if (!profile) continue;
      const resolved = resolveProfileModel(profile, modelOption);
      if (!resolved) continue;
      // Current-satisfies short-circuit: if the current model is already the
      // resolved target, no override chip to show.
      if (currentId && resolved.value === currentId) return null;
      return resolved;
    }

    if (modelTag) {
      const tagLower = modelTag.toLowerCase();
      const taggedProfiles = modelProfiles.filter(
        (p) =>
          p &&
          Array.isArray(p.tags) &&
          p.tags.some((t) => String(t).toLowerCase() === tagLower),
      );
      if (taggedProfiles.length === 0) continue;
      // Current-satisfies short-circuit: if the current model's name matches
      // ANY tagged profile's criteria, keep the current model (no override).
      if (
        currentName &&
        taggedProfiles.some((p) =>
          constraintMatchesName(p.criteria, currentName),
        )
      ) {
        return null;
      }
      // Deterministic by profile order: first profile that yields an
      // available model wins.
      for (const profile of taggedProfiles) {
        const resolved = resolveProfileModel(profile, modelOption);
        if (resolved) {
          if (currentId && resolved.value === currentId) return null;
          return resolved;
        }
      }
    }
  }
  return null;
}

/**
 * Returns the display name of the current model from a "model" config option,
 * falling back to the raw value, or "" when unavailable.
 */
export function currentModelName(modelOption) {
  if (!modelOption || !Array.isArray(modelOption.options)) return "";
  const cur = modelOption.options.find(
    (o) => o.value === modelOption.current_value,
  );
  return cur ? cur.name || cur.value : "";
}
