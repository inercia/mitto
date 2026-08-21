// Pure helpers extracted from components/BeadsView.js (mitto-90f.3, E-1/E-3).
// This module intentionally contains ONLY framework-free helpers and pure-data
// constants (no Preact, no html`` template tag, no icon components) so it can
// be imported directly under jsdom / jest without the window.preact bootstrap
// dance. Icon-carrying constants (e.g. BEADS_STATUS_TOGGLES) stay in
// components/BeadsView.js because they depend on frontend runtime components.

// How often (ms) to surface a progress toast during a bulk closed-issue
// cleanup. Progress events arrive per server-side batch (25 issues each), which
// can be more frequent than is useful as toasts, so we throttle visible updates
// to this rate and keep a single live toast updated in place.
export const CLEANUP_PROGRESS_TOAST_INTERVAL_MS = 3000;

// Display labels for the folder's configured upstream task system.
export const UPSTREAM_LABELS = {
  jira: "Jira",
  github: "GitHub",
  gitlab: "GitLab",
  linear: "Linear",
};

// Dependency edge kinds accepted by "bd dep add -t" (mirrors the backend
// allow-list in beads_api.go). "blocks" is the default/most common kind, so it
// is listed first.
export const DEP_TYPES = [
  "blocks",
  "related",
  "parent-child",
  "discovered-from",
  "until",
  "caused-by",
  "validates",
  "relates-to",
  "supersedes",
  "tracks",
];

export const PRIORITY_LABELS = {
  0: "Critical",
  1: "High",
  2: "Medium",
  3: "Low",
};
export const PRIORITY_COLORS = {
  0: "badge-error",
  1: "badge-warning",
  2: "badge-info",
  3: "badge-ghost",
};

export const STATUS_COLORS = {
  open: "bg-green-700 text-green-100",
  in_progress: "bg-blue-700 text-blue-100 beads-status-inprogress",
  closed: "bg-mitto-surface-4 text-mitto-text-strong",
  blocked: "bg-red-700 text-red-100",
  deferred: "bg-cyan-800 text-cyan-100",
};

export const TYPE_COLORS = {
  epic: "bg-purple-700 text-purple-100",
  feature: "bg-blue-700 text-blue-100 beads-type-feature",
  bug: "bg-red-700 text-red-100",
  task: "bg-mitto-surface-4 text-mitto-text-strong",
  chore: "bg-mitto-surface-4 text-mitto-text-strong",
};

// Issue kinds accepted by the "New issue" form and inline type-change picker.
// Order controls dropdown order in the UI.
export const ISSUE_TYPES = ["task", "feature", "epic", "bug", "chore"];

// Hover-only tooltips are pointless on touch devices (no hover); gate the portal
// toolbar tooltip the same way daisyUI gates its CSS tooltips so taps never
// trigger a stuck bubble.
export const BEADS_SUPPORTS_HOVER =
  typeof window !== "undefined" &&
  typeof window.matchMedia === "function" &&
  window.matchMedia("(hover: hover)").matches;

// Delay before a toolbar tooltip appears on hover (ms).
export const BEADS_TOOLTIP_DELAY_MS = 250;

// Safely read a fetch Response body that is expected to be JSON. If the body is
// not valid JSON (e.g. a plain-text error page from a 403/500), return an object
// with an `error` field instead of throwing. This prevents WebKit/Safari from
// surfacing the cryptic "The string did not match the expected pattern." error
// when res.json() is called on a non-JSON body.
export async function readBeadsResponse(res) {
  const text = await res.text();
  if (text) {
    try {
      const parsed = JSON.parse(text);
      // Normalize the canonical nested error envelope {error:{code,message,details}}
      // down to the flat {error:"<message>", stderr} shape the beads consumers expect.
      // This covers both validation errors (4xx) and bd-failure errors (500, canonical envelope).
      if (parsed && typeof parsed.error === "object" && parsed.error !== null) {
        return {
          error: parsed.error.message || `Request failed (HTTP ${res.status})`,
          code: parsed.error.code,
          stderr:
            (parsed.error.details && parsed.error.details.stderr) || undefined,
          details: parsed.error.details || undefined,
        };
      }
      return parsed;
    } catch (_e) {
      // fall through to error object below
    }
  }
  return {
    error: (text && text.trim()) || `Request failed (HTTP ${res.status})`,
  };
}

// isBeadsSchemaSkew returns true when a flattened beads response (as produced
// by readBeadsResponse) carries the canonical `beads_schema_skew` error code.
// The backend returns HTTP 409 with this code whenever the .beads database is
// behind the bd binary's schema and is remote-backed; every write handler must
// route this branch into SchemaSkewDialog instead of a generic error toast.
export function isBeadsSchemaSkew(data) {
  return !!(data && data.code === "beads_schema_skew");
}

// toSchemaSkewState maps a flattened beads response into the shape the
// SchemaSkewDialog state (`schemaSkew`) expects: { message, dbPath, hint,
// options, allowMigrate, databaseMode }. Tolerates missing details by falling back to empty
// strings/options and migration allowed, preserving compatibility with older
// backends that did not send allow_migrate_from_ui.
export function toSchemaSkewState(data) {
  const details = (data && data.details) || {};
  return {
    message: (data && data.error) || "",
    dbPath: details.db_path || "",
    hint: details.hint || "",
    options: Array.isArray(details.options) ? details.options : [],
    allowMigrate: details.allow_migrate_from_ui !== false,
    databaseMode: details.database_mode || "shared",
  };
}

// matchesSearch returns true when `issue` matches the user's search query.
// The query is whitespace-tokenized (case-insensitive) and every token must
// appear as a substring of one of the searchable fields: id, title, owner,
// or description (body). An empty / whitespace-only query matches everything.
// The exact-ID case (e.g. "mitto-3bx") is naturally covered because the full
// id substring-matches itself.
export function matchesSearch(issue, search) {
  if (!search) return true;
  const tokens = search.toLowerCase().split(/\s+/).filter(Boolean);
  if (tokens.length === 0) return true;
  const id = (issue.id || "").toLowerCase();
  const title = (issue.title || "").toLowerCase();
  const owner = (issue.owner || "").toLowerCase();
  const description = (issue.description || "").toLowerCase();
  for (const t of tokens) {
    if (
      !(
        id.includes(t) ||
        title.includes(t) ||
        owner.includes(t) ||
        description.includes(t)
      )
    ) {
      return false;
    }
  }
  return true;
}

// Return the first configured color whose label is present on the issue. The
// mapping is ordered and matching is exact; no color state is stored per issue.
export function taskTitleBackground(issue, entries) {
  const labels = Array.isArray(issue?.labels)
    ? new Set(issue.labels)
    : new Set();
  for (const entry of entries || []) {
    if (entry?.label && labels.has(entry.label)) return entry.color || "";
  }
  return "";
}

// Sort menu options. `field` is the persisted key; `key` is the issue property
// holding the value to compare on (priority is numeric, the dates are RFC3339
// strings).
export const SORT_FIELD_OPTIONS = [
  { field: "created", label: "Creation date", key: "created_at" },
  { field: "updated", label: "Modification date", key: "updated_at" },
  { field: "priority", label: "Priority", key: "priority" },
];

export const SORT_FIELD_LABELS = Object.fromEntries(
  SORT_FIELD_OPTIONS.map((o) => [o.field, o.label]),
);

// Compare two issues for the chosen sort field and direction. Priority is a
// number (0 = highest) so ascending = most important first; the dates compare
// by parsed timestamp. A stable id tiebreaker keeps ordering deterministic and
// is intentionally independent of direction.
export function cmpBySort(a, b, sort) {
  const dir = sort.direction === "asc" ? 1 : -1;
  let primary = 0;
  if (sort.field === "priority") {
    const pa = typeof a.priority === "number" ? a.priority : 3;
    const pb = typeof b.priority === "number" ? b.priority : 3;
    primary = pa - pb;
  } else {
    const key = sort.field === "updated" ? "updated_at" : "created_at";
    primary =
      (Date.parse(a?.[key] || "") || 0) - (Date.parse(b?.[key] || "") || 0);
  }
  if (primary !== 0) return primary * dir;
  return (a.id || "").localeCompare(b.id || "");
}

// Extend a set of "streaming" (actively-prompting) issue ids to also include
// every transitive ancestor reached by walking `issue.parent` upward from any
// id in the base set. Used by BeadsView to tint ancestor epic rows blue when
// any of their descendants is currently prompting, so the active work is
// visible even when the containing epic group is collapsed (mitto-0qn).
//
// The walk follows the raw `issue.parent` link — a strict superset of the
// epic-only walk — so the helper covers both grouped and flat render modes
// from a single source of truth. A visited set guards against cycles in
// malformed data (same idiom as `directEpicParentOf` in BeadsView.js). The
// incoming set is never mutated; a new Set is always returned.
export function computeEffectiveStreamingSet(issues, streamingSet) {
  if (!streamingSet || streamingSet.size === 0) return new Set();
  const issueById = new Map();
  for (const i of issues || []) {
    if (i && i.id) issueById.set(i.id, i);
  }
  const result = new Set(streamingSet);
  for (const seedId of streamingSet) {
    let cur = issueById.get(seedId);
    const visited = new Set();
    while (cur && cur.parent && !visited.has(cur.parent)) {
      visited.add(cur.parent);
      result.add(cur.parent);
      cur = issueById.get(cur.parent);
    }
  }
  return result;
}
