// Pure helpers for the per-agent Hide Models row editor. Regex validation is
// authoritative on the backend (Go/RE2); nothing here parses regex.

// Field paths are `<prefix>[i].hidden_models[j]`; prefix is `acp_servers` for
// agents and `workspaces` for workspace rules.
function hiddenModelFieldRe(prefix) {
  return new RegExp(`^${prefix}\\[(\\d+)\\]\\.hidden_models\\[(\\d+)\\]$`);
}

// Parses a backend field path into the owning item index and row index.
export function parseHiddenModelField(field, prefix = "acp_servers") {
  const match = hiddenModelFieldRe(prefix).exec(field ?? "");
  if (!match) return null;
  return { index: Number(match[1]), rowIndex: Number(match[2]) };
}

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

// Maps a backend validation error for `<prefix>[serverIndex].hidden_models[j]`
// back to the draft row. `rowValue` is the rule text that was submitted, so
// callers can drop the error once the row's current value differs.
export function mapHiddenModelError(
  err,
  serverIndex,
  rowIndexes,
  rules,
  prefix = "acp_servers",
) {
  const details = err?.details;
  const parsed = parseHiddenModelField(details?.field, prefix);
  if (!parsed || parsed.index !== serverIndex) return null;
  const rowIndex = rowIndexes?.[parsed.rowIndex];
  if (rowIndex === undefined) return null;
  return {
    rowIndex,
    message: details.message || err.message || "Invalid regular expression",
    rowValue: rules?.[parsed.rowIndex],
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
