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
