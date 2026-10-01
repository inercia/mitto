import { buildAuxiliaryModelTagOptions } from "./auxiliaryModelTagOptions.js";

describe("buildAuxiliaryModelTagOptions", () => {
  test("always includes the default option first", () => {
    const options = buildAuxiliaryModelTagOptions([], "");
    expect(options[0]).toEqual({ value: "", label: "Default (agent model)" });
    expect(options).toHaveLength(1);
  });

  test("sorts tags case-insensitively alphabetically", () => {
    const options = buildAuxiliaryModelTagOptions(
      ["Reasoning", "coding", "Smartest", "auto"],
      "",
    );
    expect(options.map((o) => o.value)).toEqual([
      "",
      "auto",
      "coding",
      "Reasoning",
      "Smartest",
    ]);
  });

  test("de-duplicates tags case-insensitively, keeping first occurrence casing", () => {
    const options = buildAuxiliaryModelTagOptions(
      ["Coding", "coding", "CODING", "Reasoning"],
      "",
    );
    expect(options.map((o) => o.value)).toEqual(["", "Coding", "Reasoning"]);
  });

  test("preserves a saved value that is unknown/not in modelTags", () => {
    const options = buildAuxiliaryModelTagOptions(
      ["Coding", "Reasoning"],
      "Legacy-Tag",
    );
    expect(options.map((o) => o.value)).toEqual([
      "",
      "Coding",
      "Reasoning",
      "Legacy-Tag",
    ]);
  });

  test("does not duplicate the saved value when it already matches a known tag case-insensitively", () => {
    const options = buildAuxiliaryModelTagOptions(
      ["Coding", "Reasoning"],
      "coding",
    );
    expect(options.map((o) => o.value)).toEqual(["", "Coding", "Reasoning"]);
  });

  test("ignores an empty saved value", () => {
    const options = buildAuxiliaryModelTagOptions(["Coding"], "");
    expect(options.map((o) => o.value)).toEqual(["", "Coding"]);
  });

  test("handles a non-array modelTags gracefully", () => {
    const options = buildAuxiliaryModelTagOptions(null, "");
    expect(options).toEqual([{ value: "", label: "Default (agent model)" }]);
  });

  test("filters out non-string and empty-string entries", () => {
    const options = buildAuxiliaryModelTagOptions(
      ["Coding", "", null, undefined, 42, "Reasoning"],
      "",
    );
    expect(options.map((o) => o.value)).toEqual(["", "Coding", "Reasoning"]);
  });

  test("does not mutate the input modelTags array", () => {
    const input = ["Reasoning", "coding", "Smartest"];
    const snapshot = [...input];
    buildAuxiliaryModelTagOptions(input, "");
    expect(input).toEqual(snapshot);
  });
});
