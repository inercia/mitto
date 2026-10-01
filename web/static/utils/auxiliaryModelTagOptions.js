// Builds the option list for the "Auxiliary conversations" model-tag dropdown
// (Settings > Conversations). Pure function — no DOM, no side effects — so it
// can be unit tested with bun without any browser/preact globals.
//
// Rules:
//   - First option is always the default ("" value, "Default (agent model)").
//   - Remaining options are the known model tags, de-duplicated
//     case-insensitively (first occurrence wins for display casing) and
//     sorted case-insensitively alphabetically.
//   - If `savedValue` is non-empty and not already present among the known
//     tags (e.g. the model profile that carried it was removed), it is
//     appended as an extra option so the saved value is never silently
//     dropped from the UI.
export function buildAuxiliaryModelTagOptions(modelTags, savedValue) {
  const tags = Array.isArray(modelTags) ? modelTags : [];
  const seen = new Set();
  const deduped = [];
  for (const tag of tags) {
    if (typeof tag !== "string" || tag === "") continue;
    const key = tag.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    deduped.push(tag);
  }

  deduped.sort((a, b) =>
    a.localeCompare(b, undefined, { sensitivity: "base" }),
  );

  const options = [{ value: "", label: "Default (agent model)" }];
  for (const tag of deduped) {
    options.push({ value: tag, label: tag });
  }

  if (
    typeof savedValue === "string" &&
    savedValue !== "" &&
    !seen.has(savedValue.toLowerCase())
  ) {
    options.push({ value: savedValue, label: savedValue });
  }

  return options;
}
