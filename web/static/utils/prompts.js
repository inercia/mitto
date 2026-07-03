// Mitto Web Interface - Prompt Menu Utilities

import { authFetch } from "./csrf.js";
import { endpoints } from "./endpoints.js";

/**
 * Returns the list of UI menus a prompt opts INTO (positive tokens only).
 * The `menus` front-matter is a comma-separated list (e.g. "prompts, conversation").
 * Tokens prefixed with `!` (e.g. "!promptsPeriodic") are exclusions and are
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
 * `menus: "prompts, !promptsPeriodic"` it returns `new Set(["promptsPeriodic"])`.
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
 * @param {string} menu   - Menu name to check (e.g. "prompts", "promptsPeriodic")
 * @returns {boolean}
 */
export function promptMenuIncludes(prompt, menu) {
  return (
    promptMenus(prompt).includes(menu) &&
    !promptMenuExcludes(prompt).has(menu)
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
 * Returns the periodic mode of a prompt: "always" | "optional" | "none".
 * - "none"     when prompt.periodic is absent/null (never periodic).
 * - "optional" when prompt.periodic.mode === "optional".
 * - "always"   otherwise (block present with absent/unknown mode → backend default).
 */
export function promptPeriodicMode(prompt) {
  const periodic = prompt?.periodic;
  if (!periodic) return "none";
  return periodic.mode === "optional" ? "optional" : "always";
}

/** True iff the prompt's periodic mode is "optional" (the only toggleable category). */
export function promptPeriodicIsToggleable(prompt) {
  return promptPeriodicMode(prompt) === "optional";
}

/**
 * Initial send-as-periodic state:
 * - "always"   → true (locked ON)
 * - "optional" → prompt.periodic.default !== false (nil/true → true, false → false)
 * - "none"     → false
 */
export function promptPeriodicDefaultOn(prompt) {
  const mode = promptPeriodicMode(prompt);
  if (mode === "none") return false;
  if (mode === "optional") return prompt.periodic.default !== false;
  return true;
}

/**
 * Resolve whether a given send should be dispatched as periodic.
 * @param {object} prompt - the prompt object (may have prompt.periodic with mode/default).
 * @param {boolean} [override] - explicit per-send choice from a UI toggle; only honored for mode "optional".
 * @returns {boolean}
 */
export function promptResolveAsPeriodic(prompt, override) {
  const mode = promptPeriodicMode(prompt);
  if (mode === "none") return false; // never periodic (override ignored)
  if (mode === "always") return true; // locked ON (override ignored)
  // mode === "optional":
  if (typeof override === "boolean") return override;
  return promptPeriodicDefaultOn(prompt);
}

/**
 * Frontend mirror of the backend parameter-type registry.
 * Canonical source of truth: internal/config/prompt_param_types.go
 * These two lists MUST be kept in sync — do not add types here without also
 * adding them to the Go registry, and vice versa.
 *
 * Type semantics:
 *   beadsId        — a beads issue ID (e.g. "mitto-42")
 *   beadsTitle     — a beads issue title (free text, typically auto-filled)
 *   sessionId      — a Mitto conversation/session UUID
 *   childSessionId — a child conversation/session UUID (relative to the host conversation)
 *   workspaceId    — a Mitto workspace UUID
 *   workspaceFolder — an absolute path to the workspace root directory
 *   acpServer      — an ACP server (agent) name
 *   text           — generic free-form text (catch-all)
 *   boolean        — a yes/no flag, rendered as a checkbox; supplied as the
 *                    string "true"/"false" (see PromptParameterDialog)
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
];

/**
 * Returns true if the parameter is a boolean (checkbox) type.
 *
 * Boolean parameters are special: a checkbox always has a definite answer
 * (checked/unchecked), so they never gate menu visibility (menuSatisfies) and
 * they are always collected via the dialog (getMissingPromptParameters),
 * regardless of the menu's auto-supplied types or the `required` flag.
 */
export function isBooleanParam(p) {
  return p?.type === "boolean";
}

/**
 * Returns the structured parameters array for a prompt, or [] if absent/empty.
 * Each entry is { name, type, description?, required?, multiLine? }. multiLine is
 * only meaningful for type "text": when true the dialog renders a resizable
 * multi-line textarea instead of a single-line input (see PromptParameterDialog).
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
  promptsPeriodic: [],
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
 * param is silently omitted (no blocking form shown — see getMissingPromptParameters).
 *
 * Unset (`required` absent/null) or `required: true` keeps the current gating
 * behaviour, preserving all existing prompts unchanged.
 *
 * Boolean parameters never gate: a checkbox always has a definite answer, so a
 * boolean param behaves like an optional one for visibility purposes (it is
 * collected via the dialog rather than auto-supplied by a menu).
 */
export function menuSatisfies(prompt, menu) {
  const params = promptParameters(prompt);
  if (params.length === 0) return true;
  const provided = MENU_PARAM_TYPES[menu] || [];
  return params.every(
    (p) =>
      isBooleanParam(p) || p.required === false || provided.includes(p.type),
  );
}

/**
 * Returns the ordered list of declared parameters whose `type` is NOT
 * auto-supplied by the given menu AND that are required (i.e. must be
 * collected via the parameter dialog before the prompt can run).
 *
 * A parameter with `required === false` is considered optional: it is never
 * included in the missing list, so no blocking form is shown for it even when
 * the menu cannot auto-supply it. Its value will simply be absent from the
 * arguments map.
 *
 * Rules:
 *   - An unknown or missing `menu` is treated as providing [] (all required params missing).
 *   - A prompt with no parameters always returns [].
 *   - A boolean parameter is ALWAYS included (it is rendered as a checkbox and
 *     collected via the dialog; no menu can auto-supply it).
 *   - A parameter whose type IS in the menu's provided-types list is excluded.
 *   - A parameter with `required === false` is excluded (optional, no form shown).
 *   - Declared order is preserved.
 *
 * @param {Object} prompt - Prompt object with optional `parameters` array
 * @param {string} menu   - Menu key (e.g. "beadsIssues", "prompts")
 * @returns {Array}       - Subset of prompt parameters not auto-filled by menu
 */
export function getMissingPromptParameters(prompt, menu) {
  const params = promptParameters(prompt);
  if (params.length === 0) return [];
  const provided = MENU_PARAM_TYPES[menu] || [];
  return params.filter(
    (p) =>
      isBooleanParam(p) || (p.required !== false && !provided.includes(p.type)),
  );
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
 * to today's behavior (ask). `fetchImpl` is injectable for tests (defaults to authFetch).
 * @returns {Promise<Set<string>>}
 */
export async function fetchCachedParamNames(
  sessionId,
  promptName,
  { fetchImpl } = {},
) {
  if (!sessionId || !promptName) return new Set();
  const fetch_ = fetchImpl || authFetch;
  try {
    const resp = await fetch_(
      endpoints.sessions.promptArgCache(sessionId, promptName),
    );
    if (!resp || !resp.ok) return new Set();
    const data = await resp.json();
    return new Set(Array.isArray(data && data.cached) ? data.cached : []);
  } catch (_err) {
    return new Set();
  }
}

/**
 * Remove from `missing` any parameter that is cacheable AND whose name is in
 * `cachedNames`. Non-cacheable params and cacheable-but-not-cached params are kept.
 * `cachedNames` may be a Set or an array.
 */
export function effectiveMissingParams(missing, cachedNames) {
  const cached =
    cachedNames instanceof Set ? cachedNames : new Set(cachedNames || []);
  return (missing || []).filter(
    (p) => !(isCacheableParam(p) && cached.has(p.name)),
  );
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
  return matched ? { value: matched.value, name: matched.name || matched.value } : null;
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
        taggedProfiles.some((p) => constraintMatchesName(p.criteria, currentName))
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
