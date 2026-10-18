// Settings-only drafts. Runtime key handling and tag resolution live elsewhere.
export const MODEL_SHORTCUT_NUMBERS = Object.freeze([1, 2, 3, 4, 5]);

export function loadModelShortcuts(value) {
  return Array.isArray(value)
    ? value.map((row) => ({ number: row?.number, model_tag: row?.model_tag }))
    : [];
}

export function availableModelShortcutNumbers(rows, ownIndex) {
  const occupied = new Set(
    rows.filter((_, index) => index !== ownIndex).map((row) => row.number),
  );
  return MODEL_SHORTCUT_NUMBERS.filter((number) => !occupied.has(number));
}

export function addModelShortcut(rows) {
  const number = availableModelShortcutNumbers(rows)[0];
  if (rows.length >= MODEL_SHORTCUT_NUMBERS.length || number === undefined) {
    return rows;
  }
  return [...rows, { number, model_tag: "" }];
}

const validIndex = (rows, index) =>
  Number.isInteger(index) && index >= 0 && index < rows.length;

export function updateModelShortcut(rows, index, patch) {
  if (!validIndex(rows, index)) return rows;
  const next = { ...rows[index] };
  if (Object.hasOwn(patch, "number")) {
    if (typeof patch.number !== "number" && typeof patch.number !== "string") {
      return rows;
    }
    const number = Number(patch.number);
    if (
      !Number.isInteger(number) ||
      !availableModelShortcutNumbers(rows, index).includes(number)
    ) {
      return rows;
    }
    next.number = number;
  }
  if (Object.hasOwn(patch, "model_tag")) {
    if (typeof patch.model_tag !== "string") return rows;
    next.model_tag = patch.model_tag;
  }
  return rows.map((row, i) => (i === index ? next : row));
}

export function removeModelShortcut(rows, index) {
  return validIndex(rows, index) ? rows.filter((_, i) => i !== index) : rows;
}

export function validateModelShortcuts(rows) {
  const occupied = new Set();
  for (const [index, row] of rows.entries()) {
    const prefix = `Model shortcut row ${index + 1}:`;
    if (
      !Number.isInteger(row.number) ||
      !MODEL_SHORTCUT_NUMBERS.includes(row.number)
    ) {
      return `${prefix} choose a number from 1 to 5.`;
    }
    if (occupied.has(row.number)) {
      return `${prefix} number ${row.number} is already assigned.`;
    }
    occupied.add(row.number);
    if (typeof row.model_tag !== "string" || !row.model_tag.trim()) {
      return `${prefix} enter a model tag or delete the row to unassign it.`;
    }
  }
  return null;
}

export function serializeModelShortcuts(rows) {
  const error = validateModelShortcuts(rows);
  if (error) throw new Error(error);
  return rows.map(({ number, model_tag }) => ({
    number,
    model_tag: model_tag.trim(),
  }));
}

export function modelShortcutLabel(number, platform) {
  return `${platform?.includes("Mac") ? "Command" : "Ctrl"}+Shift+${number}`;
}
