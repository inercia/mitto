// Mitto Web Interface - Nested prompt-args helpers (mitto-47y.6.1)
//
// Pure, framework-free helpers powering the recursive `type: prompts` picker
// rendering in components/PromptParameterDialog.js. Extracted here so they can
// be unit-tested directly under jsdom without pulling in the component (which
// reads `window.preact` at module load — the same jsdom-import gate that keeps
// BeadsView / Message helpers under utils/ instead of the component file).
//
// Wire-format invariant (unchanged from v1): at every nesting level the picker
// emits `<PickerName>` (picked prompt name) + `<PickerName>_Args` (JSON-encoded
// map of that picked prompt's args) into the surrounding args map. Deeper
// levels naturally produce JSON-strings-inside-JSON-strings, and the backend's
// `ArgsMap "<PickerName>_Args"` + `PromptTextWithArgs` sub-render chain decodes
// level-by-level.

// Maximum picker-nesting level the UI will render normally. Mirrors backend
// `promptTextMaxDepth = 3` in internal/cel/templatefuncs.go — each picker level
// N produces `<Picker>_Args` that the backend consumes via a PromptTextWithArgs
// sub-render at depth N+1 (the outer prompt renders at depth 0). A picker at
// level 3 would drive a depth-4 sub-render which the backend fail-closes, so at
// `level === MAX_NESTED_LEVEL` the picker itself renders as a disabled
// placeholder — matching the pre-existing v1 note used at level 1 (Phase B
// `mitto-47y.2`). Keep in sync with promptTextMaxDepth.
export const MAX_NESTED_LEVEL = 3;

// Write `val` into the nested-args tree at `path`.`innerName`. `tree` shape:
// `{ [pickerName]: { values, sub } }` (the "sub" of an implicit root). `path`
// is the picker-name chain from the outermost picker down to the picker that
// owns the field being written (length === level of the field's parent
// picker). Missing intermediate nodes are created on write; existing nodes are
// shallow-cloned so callers can rely on referential-equality checks for cheap
// change detection. Returns a NEW tree — the input is never mutated.
export function updateNestedTree(tree, path, innerName, val) {
  if (!Array.isArray(path) || path.length === 0) return tree;
  const next = { ...(tree || {}) };
  let parent = next;
  for (let i = 0; i < path.length; i++) {
    const pickerName = path[i];
    const existing = parent[pickerName] || { values: {}, sub: {} };
    const clone = {
      values: { ...(existing.values || {}) },
      sub: { ...(existing.sub || {}) },
    };
    parent[pickerName] = clone;
    if (i === path.length - 1) {
      clone.values = { ...clone.values, [innerName]: val };
    } else {
      parent = clone.sub;
    }
  }
  return next;
}

// Prune subtree slots whose picker-value no longer matches a prompt in
// `promptsList`, walking from the root down. `outerParams` is the
// PromptParameter[] at the current level; `outerValues` is the values map at
// this level (for the root, this is the top-level `values` state). Recurses
// into each still-valid picker using the picked prompt's inner parameters.
// Returns a NEW tree (or the same reference when nothing changed). Pure —
// mirrors the v1 stale-clear effect but walks the whole tree.
export function pruneNestedTree(tree, outerParams, outerValues, promptsList) {
  if (!tree || typeof tree !== "object") return tree;
  let mutated = false;
  const next = { ...tree };
  const paramsList = Array.isArray(outerParams) ? outerParams : [];
  const promptByName = new Map(
    (promptsList || [])
      .filter((wp) => wp && wp.name)
      .map((wp) => [wp.name, wp]),
  );
  const pickerParamByName = new Map(
    paramsList.filter((p) => p && p.type === "prompts").map((p) => [p.name, p]),
  );
  for (const key of Object.keys(next)) {
    const pickerParam = pickerParamByName.get(key);
    if (!pickerParam) {
      delete next[key];
      mutated = true;
      continue;
    }
    const pickedName = ((outerValues && outerValues[key]) || "")
      .toString()
      .trim();
    if (!pickedName) {
      delete next[key];
      mutated = true;
      continue;
    }
    const pickedPrompt = promptByName.get(pickedName);
    if (!pickedPrompt) {
      delete next[key];
      mutated = true;
      continue;
    }
    const node = next[key];
    const innerParams = Array.isArray(pickedPrompt.parameters)
      ? pickedPrompt.parameters
      : [];
    const prunedSub = pruneNestedTree(
      node.sub || {},
      innerParams,
      node.values || {},
      promptsList,
    );
    if (prunedSub !== node.sub) {
      next[key] = { ...node, sub: prunedSub };
      mutated = true;
    }
  }
  return mutated ? next : tree;
}


// Build the JSON-string payload for a picker at any level. Mirrors the v1
// outer serializer per-field rules (booleans → "true"/"false", strings →
// trim, drop-if-empty-and-not-required), and recursively emits `<Picker>` /
// `<Picker>_Args` companions for inner `type: prompts` fields. `level` is the
// depth of the BLOCK being processed — the level at which these `innerParams`
// live. Level 0 = the dialog's top-level `parameters`; level 1 = the inner
// block opened by a level-0 picker; and so on. A picker at
// `level >= MAX_NESTED_LEVEL` cannot open another block (its own `_Args`
// would drive a backend sub-render past `promptTextMaxDepth`), so its inner
// `type: prompts` field is skipped entirely (defense-in-depth alongside the
// ParamField disabled render at the same level). Returns a plain object; JSON
// encoding is the caller's job so an empty result can be detected and the
// `_Args` companion omitted from the outer args map (matches v1 wire).
export function buildInnerArgs(innerParams, node, promptsList, level) {
  const out = {};
  const values = (node && node.values) || {};
  const sub = (node && node.sub) || {};
  const paramsList = Array.isArray(innerParams) ? innerParams : [];
  for (const ip of paramsList) {
    if (!ip || !ip.name) continue;
    if (ip.type === "prompts") {
      if (level >= MAX_NESTED_LEVEL) continue;
      const pickedName = (values[ip.name] || "").toString().trim();
      if (pickedName === "") {
        if (ip.required) out[ip.name] = "";
        continue;
      }
      out[ip.name] = pickedName;
      const pickedPrompt = (promptsList || []).find(
        (wp) => wp && wp.name === pickedName,
      );
      const deeperInner =
        pickedPrompt && Array.isArray(pickedPrompt.parameters)
          ? pickedPrompt.parameters
          : [];
      const deeperNode = sub[ip.name];
      const deeperOut = buildInnerArgs(
        deeperInner,
        deeperNode,
        promptsList,
        level + 1,
      );
      if (Object.keys(deeperOut).length > 0) {
        out[`${ip.name}_Args`] = JSON.stringify(deeperOut);
      }
      continue;
    }
    if (ip.type === "boolean") {
      const checked = values[ip.name] === true || values[ip.name] === "true";
      out[ip.name] = checked ? "true" : "false";
      continue;
    }
    const iv = (values[ip.name] || "").toString().trim();
    if (iv !== "" || ip.required) {
      out[ip.name] = iv;
    }
  }
  return out;
}

// Walk the nested tree collecting `{ path, pickedPromptName, prompt }` entries
// for every currently-picked node — used by the remembered-args fetch effect
// to fire one request per non-empty picker at every depth. `outerParams` /
// `outerValues` describe the level whose picker keys are stored in `tree`; for
// the root that's the dialog's `parameters` prop and `values` state.
export function collectPickedPaths(
  tree,
  outerParams,
  outerValues,
  promptsList,
  parentPath = [],
) {
  const out = [];
  if (!tree || typeof tree !== "object") return out;
  const pickerParams = (outerParams || []).filter(
    (p) => p && p.type === "prompts",
  );
  for (const p of pickerParams) {
    const pickedName = ((outerValues && outerValues[p.name]) || "")
      .toString()
      .trim();
    if (!pickedName) continue;
    const pickedPrompt = (promptsList || []).find(
      (wp) => wp && wp.name === pickedName,
    );
    if (!pickedPrompt) continue;
    const path = [...parentPath, p.name];
    out.push({ path, pickedPromptName: pickedName, prompt: pickedPrompt });
    const node = tree[p.name];
    if (!node) continue;
    const innerParams = Array.isArray(pickedPrompt.parameters)
      ? pickedPrompt.parameters
      : [];
    out.push(
      ...collectPickedPaths(
        node.sub || {},
        innerParams,
        node.values || {},
        promptsList,
        path,
      ),
    );
  }
  return out;
}
