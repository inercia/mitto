// Pure helpers for the per-agent Hide Models row editor. Regex validation is
// authoritative on the backend (Go/RE2); nothing here parses regex.

const FIELD_RE = /^acp_servers\[(\d+)\]\.hidden_models\[(\d+)\]$/;

// Absent value → no rows. Rows are copied verbatim (no trimming).
export function hiddenModelsToRows(list) {
  return Array.isArray(list) ? [...list] : [];
}

// Always yields explicit rules (`[]` clears persisted rules). Whitespace-only
// rows are omitted; nonblank rows are preserved exactly. `rowIndexes[k]` is
// the draft-row index of serialized rule k.
export function serializeHiddenModels(rows) {
  const rules = [];
  const rowIndexes = [];
  (Array.isArray(rows) ? rows : []).forEach((row, i) => {
    if (typeof row !== "string" || row.trim() === "") return;
    rules.push(row);
    rowIndexes.push(i);
  });
  return { rules, rowIndexes };
}

// Maps a backend validation error for `acp_servers[serverIndex].hidden_models[j]`
// back to the draft row. `rowValue` is the rule text that was submitted, so
// callers can drop the error once the row's current value differs.
export function mapHiddenModelError(err, serverIndex, rowIndexes, rules) {
  const details = err?.details;
  const match = FIELD_RE.exec(details?.field ?? "");
  if (!match || Number(match[1]) !== serverIndex) return null;
  const rowIndex = rowIndexes?.[Number(match[2])];
  if (rowIndex === undefined) return null;
  return {
    rowIndex,
    message: details.message || err.message || "Invalid regular expression",
    rowValue: rules?.[Number(match[2])],
  };
}

// True when the error still applies to the current draft row text.
export function isHiddenModelErrorCurrent(error, rows) {
  return (
    !!error &&
    error.rowValue !== undefined &&
    rows?.[error.rowIndex] === error.rowValue
  );
}
