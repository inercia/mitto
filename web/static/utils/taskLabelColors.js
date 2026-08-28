export const DEFAULT_TASK_LABEL_COLOR = "#ef4444";

export function addTaskLabelColor(entries) {
  return [...entries, { label: "", color: DEFAULT_TASK_LABEL_COLOR }];
}

export function updateTaskLabelColor(entries, index, patch) {
  return entries.map((entry, i) =>
    i === index ? { ...entry, ...patch } : entry,
  );
}

export function removeTaskLabelColor(entries, index) {
  return entries.filter((_, i) => i !== index);
}

export function moveTaskLabelColor(entries, index, direction) {
  const target = index + direction;
  if (target < 0 || target >= entries.length) return entries;
  const next = [...entries];
  [next[index], next[target]] = [next[target], next[index]];
  return next;
}

// Merge folder-level and global task-label color entries for render-time
// lookup (mitto-m5f.3). Folder-first precedence: folder entries are kept in
// full, then any global entries whose label is not already covered by a
// folder entry are appended, so the ordered, first-match-wins consumer
// (taskTitleBackground in utils/beads.js) resolves folder colors ahead of
// global ones on overlapping labels while still honoring global-only labels.
export function mergeTaskLabelColors(folderEntries, globalEntries) {
  const folder = Array.isArray(folderEntries) ? folderEntries : [];
  const global = Array.isArray(globalEntries) ? globalEntries : [];
  const folderLabels = new Set(
    folder.map((e) => e?.label).filter((l) => l !== undefined && l !== null),
  );
  return [...folder, ...global.filter((e) => !folderLabels.has(e?.label))];
}
