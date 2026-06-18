// Mitto Web Interface - Prompt Menu Utilities

/**
 * Returns the list of UI menus a prompt opts into. The `menus` front-matter is a
 * comma-separated list (e.g. "prompts, conversation"). A missing or empty value
 * defaults to the "prompts" dropup only, so prompts that explicitly target other
 * menus (e.g. "conversation") are excluded from the dropup unless they also list
 * "prompts".
 */
export function promptMenus(prompt) {
  const raw = typeof prompt?.menus === "string" ? prompt.menus.trim() : "";
  if (raw === "") return ["prompts"];
  return raw
    .split(",")
    .map((m) => m.trim())
    .filter(Boolean);
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
 *   workspaceId    — a Mitto workspace UUID
 *   workspaceFolder — an absolute path to the workspace root directory
 *   text           — generic free-form text (catch-all)
 */
export const KNOWN_PARAM_TYPES = [
  "beadsId",
  "beadsTitle",
  "sessionId",
  "workspaceId",
  "workspaceFolder",
  "text",
];

/**
 * Returns the structured parameters array for a prompt, or [] if absent/empty.
 * Each entry is { name, type, description?, required? }.
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
 * Returns true if `menu` can supply every parameter type that the prompt
 * declares. A prompt with no parameters is satisfied by any menu (including
 * unknown ones). For an unknown menu, its provided types are treated as []
 * (so a prompt WITH params is NOT satisfied — matching old behaviour).
 */
export function menuSatisfies(prompt, menu) {
  const params = promptParameters(prompt);
  if (params.length === 0) return true;
  const provided = MENU_PARAM_TYPES[menu] || [];
  return params.every((p) => provided.includes(p.type));
}

/**
 * Build the arguments map for a prompt from a map of type → value.
 * For each declared parameter { name, type }, if typeValues[type] is defined
 * (not undefined/null), the parameter's name is mapped to that value.
 * Returns a plain object (possibly empty).
 *
 * Example:
 *   collectPromptArguments(prompt, { beadsId: "mitto-42", beadsTitle: "Fix bug" })
 *   // → { ISSUE_ID: "mitto-42" }  (for a prompt with param { name:"ISSUE_ID", type:"beadsId" })
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
