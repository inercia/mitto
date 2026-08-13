import {
  addTaskLabelColor,
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
