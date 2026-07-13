// Pure helpers extracted from components/BeadsView.js (mitto-90f.3, E-1).
// This module intentionally contains ONLY framework-free helpers (no Preact,
// no html`` template tag, no window.preact access) so it can be imported
// directly under jsdom / jest without the window.preact bootstrap dance.

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
