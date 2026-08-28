import {
  addTaskLabelColor,
  mergeTaskLabelColors,
  moveTaskLabelColor,
  removeTaskLabelColor,
  updateTaskLabelColor,
} from "./taskLabelColors.js";

const entries = [
  { label: "needs-human", color: "#ef4444" },
  { label: "blocked", color: "#f59e0b" },
];

describe("task label color editor transforms", () => {
  test("adds a blank row with the default color without mutating input", () => {
    const next = addTaskLabelColor(entries);
    expect(next).toEqual([...entries, { label: "", color: "#ef4444" }]);
    expect(next).not.toBe(entries);
    expect(entries).toHaveLength(2);
  });

  test("updates only the selected row without mutating input", () => {
    const next = updateTaskLabelColor(entries, 1, { color: "#123456" });
    expect(next[0]).toBe(entries[0]);
    expect(next[1]).toEqual({ label: "blocked", color: "#123456" });
    expect(entries[1].color).toBe("#f59e0b");
  });

  test("removes only the selected row", () => {
    expect(removeTaskLabelColor(entries, 0)).toEqual([entries[1]]);
    expect(entries).toHaveLength(2);
  });

  test("reorders adjacent rows and ignores out-of-range moves", () => {
    const moved = moveTaskLabelColor(entries, 0, 1);
    expect(moved).toEqual([entries[1], entries[0]]);
    expect(moved).not.toBe(entries);
    expect(moveTaskLabelColor(entries, 0, -1)).toBe(entries);
    expect(moveTaskLabelColor(entries, 1, 1)).toBe(entries);
  });
});

describe("mergeTaskLabelColors (mitto-m5f.3): folder-first precedence for render-time lookup", () => {
  const folder = [
    { label: "blocked", color: "#111111" },
    { label: "urgent", color: "#222222" },
  ];
  const global = [
    { label: "needs-human", color: "#ef4444" },
    { label: "blocked", color: "#f59e0b" },
  ];

  test("folder entries are kept in full and take precedence over overlapping global labels", () => {
    const merged = mergeTaskLabelColors(folder, global);
    expect(merged).toEqual([
      { label: "blocked", color: "#111111" },
      { label: "urgent", color: "#222222" },
      { label: "needs-human", color: "#ef4444" },
    ]);
  });

  test("first-match-wins consumer (taskTitleBackground) resolves folder color, not global, on overlap", () => {
    const merged = mergeTaskLabelColors(folder, global);
    // "blocked" is defined in both; the merged order must put the folder's
    // #111111 ahead of global's #f59e0b so a first-match-wins lookup by label
    // returns the folder color.
    const firstBlocked = merged.find((e) => e.label === "blocked");
    expect(firstBlocked.color).toBe("#111111");
  });

  test("global-only labels are still present (gap-filled) when the folder omits them", () => {
    const merged = mergeTaskLabelColors(folder, global);
    expect(merged.some((e) => e.label === "needs-human" && e.color === "#ef4444"))
      .toBe(true);
  });

  test("empty/missing folder entries fall back to global entries unchanged", () => {
    expect(mergeTaskLabelColors([], global)).toEqual(global);
    expect(mergeTaskLabelColors(undefined, global)).toEqual(global);
    expect(mergeTaskLabelColors(null, global)).toEqual(global);
  });

  test("empty/missing global entries leave only folder entries", () => {
    expect(mergeTaskLabelColors(folder, [])).toEqual(folder);
    expect(mergeTaskLabelColors(folder, undefined)).toEqual(folder);
  });

  test("both empty/missing returns an empty array", () => {
    expect(mergeTaskLabelColors([], [])).toEqual([]);
    expect(mergeTaskLabelColors(undefined, undefined)).toEqual([]);
  });

  test("does not mutate either input array", () => {
    const folderCopy = [...folder];
    const globalCopy = [...global];
    mergeTaskLabelColors(folder, global);
    expect(folder).toEqual(folderCopy);
    expect(global).toEqual(globalCopy);
  });
});
