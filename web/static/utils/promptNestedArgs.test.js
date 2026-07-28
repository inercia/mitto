/**
 * Unit tests for the nested prompt-args helpers (mitto-47y.6.1).
 *
 * Covers acceptance criteria of `mitto-47y.6.1`:
 *   - Outer prompt with an inner `type: prompts` param produces a nested
 *     picker (verified via `buildInnerArgs` output shape).
 *   - Dispatched `.Args` matches the flat + JSON-encoded shape the backend
 *     expects at each nesting level (recursive `<Picker>` / `<Picker>_Args`).
 *   - Depth cap (3) enforced client-side to match backend
 *     `promptTextMaxDepth` — a picker at level MAX_NESTED_LEVEL is skipped
 *     entirely by `buildInnerArgs`.
 *   - Existing single-level flows unchanged (byte-compatible with v1).
 */

import {
  MAX_NESTED_LEVEL,
  updateNestedTree,
  pruneNestedTree,
  buildInnerArgs,
  collectPickedPaths,
} from "./promptNestedArgs.js";

// -----------------------------------------------------------------------------
// Fixtures — a 3-deep prompt catalog covering every branch used by the helpers.
// Levels used in tests:
//   Outer (level 0):  outerPrompt with "picker" (type: prompts)
//   Level 1 target:   midPrompt with "innerPicker" (type: prompts) + regular
//   Level 2 target:   leafPrompt with "answer" (type: text, required)
//                     + "deepest" (type: prompts) — used to exercise the cap
//   Level 3 target:   sentinelPrompt (has any type: prompts inside to verify
//                     buildInnerArgs skips it at level >= MAX_NESTED_LEVEL)
// -----------------------------------------------------------------------------
const leafPrompt = {
  name: "leaf",
  parameters: [
    { name: "answer", type: "text", required: true },
    { name: "flag", type: "boolean" },
    { name: "deepest", type: "prompts" }, // becomes level-3 picker
  ],
};
const sentinelPrompt = {
  name: "sentinel",
  parameters: [{ name: "wouldBeDeeper", type: "prompts" }],
};
const midPrompt = {
  name: "mid",
  parameters: [
    { name: "innerPicker", type: "prompts" },
    { name: "note", type: "text" },
  ],
};
const promptsList = [leafPrompt, sentinelPrompt, midPrompt];

// -----------------------------------------------------------------------------
// updateNestedTree
// -----------------------------------------------------------------------------
describe("updateNestedTree", () => {
  test("writes into the empty tree at a level-1 path", () => {
    const t = updateNestedTree(null, ["picker"], "field", "v");
    expect(t).toEqual({ picker: { values: { field: "v" }, sub: {} } });
  });

  test("creates missing intermediate nodes for a deep path", () => {
    const t = updateNestedTree({}, ["outer", "mid"], "answer", "42");
    expect(t).toEqual({
      outer: {
        values: {},
        sub: { mid: { values: { answer: "42" }, sub: {} } },
      },
    });
  });

  test("does not mutate the input tree", () => {
    const before = { picker: { values: { a: "1" }, sub: {} } };
    const snapshot = JSON.parse(JSON.stringify(before));
    updateNestedTree(before, ["picker"], "b", "2");
    expect(before).toEqual(snapshot);
  });

  test("returns a NEW tree reference on write (referential change)", () => {
    const before = { picker: { values: {}, sub: {} } };
    const after = updateNestedTree(before, ["picker"], "x", "y");
    expect(after).not.toBe(before);
    expect(after.picker).not.toBe(before.picker);
  });

  test("preserves sibling subtree slots untouched", () => {
    const before = {
      keep: { values: { k: "v" }, sub: {} },
      change: { values: {}, sub: {} },
    };
    const after = updateNestedTree(before, ["change"], "x", "y");
    expect(after.keep).toBe(before.keep);
    expect(after.change.values).toEqual({ x: "y" });
  });

  test("returns input untouched when path is empty or non-array", () => {
    const tree = { a: { values: {}, sub: {} } };
    expect(updateNestedTree(tree, [], "x", "y")).toBe(tree);
    expect(updateNestedTree(tree, null, "x", "y")).toBe(tree);
    expect(updateNestedTree(tree, undefined, "x", "y")).toBe(tree);
  });

  test("overwrites existing leaf values without dropping siblings", () => {
    const before = updateNestedTree({}, ["p"], "a", "1");
    const after = updateNestedTree(before, ["p"], "a", "2");
    expect(after.p.values).toEqual({ a: "2" });

    const withSibling = updateNestedTree(after, ["p"], "b", "3");
    expect(withSibling.p.values).toEqual({ a: "2", b: "3" });
  });
});

// -----------------------------------------------------------------------------
// MAX_NESTED_LEVEL constant — pinned to backend promptTextMaxDepth.
// -----------------------------------------------------------------------------
describe("MAX_NESTED_LEVEL", () => {
  test("equals 3 (mirrors backend promptTextMaxDepth)", () => {
    // If this fails, both this constant AND
    // internal/cel/templatefuncs.go::promptTextMaxDepth must move together.
    expect(MAX_NESTED_LEVEL).toBe(3);
  });
});

// -----------------------------------------------------------------------------
// pruneNestedTree — walks the whole tree and drops stale slots.
// -----------------------------------------------------------------------------
describe("pruneNestedTree", () => {
  const outerParams = [{ name: "picker", type: "prompts" }];

  test("returns the same reference when the tree is already clean", () => {
    const tree = {
      picker: { values: { note: "hi" }, sub: {} },
    };
    const result = pruneNestedTree(
      tree,
      outerParams,
      { picker: "mid" },
      promptsList,
    );
    expect(result).toBe(tree);
  });

  test("drops a picker slot whose value is empty", () => {
    const tree = { picker: { values: { note: "hi" }, sub: {} } };
    const result = pruneNestedTree(
      tree,
      outerParams,
      { picker: "" },
      promptsList,
    );
    expect(result).toEqual({});
    expect(result).not.toBe(tree);
  });

  test("drops a picker slot whose value no longer matches a known prompt", () => {
    const tree = { picker: { values: { note: "hi" }, sub: {} } };
    const result = pruneNestedTree(
      tree,
      outerParams,
      { picker: "does-not-exist" },
      promptsList,
    );
    expect(result).toEqual({});
  });

  test("drops slots keyed under a non-picker outer param name", () => {
    const tree = { junk: { values: {}, sub: {} } };
    const result = pruneNestedTree(
      tree,
      [{ name: "junk", type: "text" }],
      { junk: "anything" },
      promptsList,
    );
    expect(result).toEqual({});
  });

  test("recursively drops stale nested-inner slots", () => {
    // outer.picker = "mid" (valid), inner.innerPicker = "" (stale) → drop inner
    const tree = {
      picker: {
        values: { innerPicker: "" },
        sub: { innerPicker: { values: { answer: "old" }, sub: {} } },
      },
    };
    const result = pruneNestedTree(
      tree,
      outerParams,
      { picker: "mid" },
      promptsList,
    );
    expect(result.picker.sub).toEqual({});
    expect(result.picker.values).toEqual({ innerPicker: "" });
  });

  test("preserves valid nested-inner slots down to level 2", () => {
    // outer.picker = "mid", inner.innerPicker = "leaf" (valid at both levels)
    const tree = {
      picker: {
        values: { innerPicker: "leaf" },
        sub: {
          innerPicker: {
            values: { answer: "42" },
            sub: {},
          },
        },
      },
    };
    const result = pruneNestedTree(
      tree,
      outerParams,
      { picker: "mid" },
      promptsList,
    );
    expect(result).toBe(tree); // untouched — everything valid
  });

  test("returns tree unchanged when input is null or not an object", () => {
    expect(pruneNestedTree(null, outerParams, {}, promptsList)).toBe(null);
    expect(pruneNestedTree(undefined, outerParams, {}, promptsList)).toBe(
      undefined,
    );
  });
});

// -----------------------------------------------------------------------------
// buildInnerArgs — recursive JSON-payload builder.
//
// These are the ON-THE-WIRE regression tests: what the backend actually sees
// after JSON.stringify(buildInnerArgs(...)).
// -----------------------------------------------------------------------------
describe("buildInnerArgs", () => {
  test("returns an empty object when innerParams is empty or missing", () => {
    expect(buildInnerArgs([], {}, promptsList, 1)).toEqual({});
    expect(buildInnerArgs(undefined, undefined, promptsList, 1)).toEqual({});
    expect(buildInnerArgs(null, null, promptsList, 1)).toEqual({});
  });

  test("string fields: trimmed; empty non-required dropped; empty required kept", () => {
    const params = [
      { name: "trimmed", type: "text" },
      { name: "emptyOptional", type: "text" },
      { name: "emptyRequired", type: "text", required: true },
    ];
    const node = { values: { trimmed: "  hi  ", emptyOptional: "   " } };
    expect(buildInnerArgs(params, node, promptsList, 1)).toEqual({
      trimmed: "hi",
      emptyRequired: "",
    });
  });

  test("boolean fields always emit 'true' or 'false' (unchecked → 'false')", () => {
    const params = [
      { name: "on", type: "boolean" },
      { name: "onStr", type: "boolean" },
      { name: "off", type: "boolean" },
      { name: "missing", type: "boolean" },
    ];
    const node = { values: { on: true, onStr: "true", off: false } };
    expect(buildInnerArgs(params, node, promptsList, 1)).toEqual({
      on: "true",
      onStr: "true",
      off: "false",
      missing: "false",
    });
  });

  test("empty picker value: dropped when optional, kept as '' when required", () => {
    const params = [
      { name: "optPicker", type: "prompts" },
      { name: "reqPicker", type: "prompts", required: true },
    ];
    const node = { values: {} };
    expect(buildInnerArgs(params, node, promptsList, 1)).toEqual({
      reqPicker: "",
    });
  });

  test("level-1 picker emits <Name> + <Name>_Args as a JSON string", () => {
    const outerParams = [{ name: "picker", type: "prompts" }];
    const outerNode = { values: { picker: "leaf" }, sub: {} };
    // buildInnerArgs is called for the OUTER block at level 0 (matching how
    // handleSubmit invokes it for the top-level parameters).
    const result = buildInnerArgs(outerParams, outerNode, promptsList, 0);
    expect(result.picker).toBe("leaf");
    // "leaf" has one required text field ("answer") — even when empty it must
    // be emitted as "" (regression: required-empty pass-through at inner
    // level).
    expect(result.picker_Args).toBeDefined();
    const decoded = JSON.parse(result.picker_Args);
    expect(decoded).toEqual({ answer: "", flag: "false" });
  });

  test("level-1 picker: _Args omitted when the picked prompt yields nothing", () => {
    // "mid" has: innerPicker (optional prompts, empty) + note (optional text,
    // empty). Both should drop, so _Args is omitted entirely.
    const outerParams = [{ name: "picker", type: "prompts" }];
    const outerNode = { values: { picker: "mid" }, sub: {} };
    const result = buildInnerArgs(outerParams, outerNode, promptsList, 0);
    expect(result).toEqual({ picker: "mid" });
    expect(result.picker_Args).toBeUndefined();
  });

  test("level-2 picker produces JSON-strings-inside-JSON-strings", () => {
    // outer.picker=mid → level 1 block, mid.innerPicker=leaf → level 2 block,
    // leaf.answer="42".
    const outerParams = [{ name: "picker", type: "prompts" }];
    const outerNode = {
      values: { picker: "mid" },
      sub: {
        picker: {
          values: { innerPicker: "leaf" },
          sub: {
            innerPicker: {
              values: { answer: "42", flag: true },
              sub: {},
            },
          },
        },
      },
    };
    const result = buildInnerArgs(outerParams, outerNode, promptsList, 0);
    expect(result.picker).toBe("mid");
    // First-level _Args is a JSON string.
    const midDecoded = JSON.parse(result.picker_Args);
    expect(midDecoded.innerPicker).toBe("leaf");
    // Its own _Args is ALSO a JSON string (strings-inside-strings).
    expect(typeof midDecoded.innerPicker_Args).toBe("string");
    const leafDecoded = JSON.parse(midDecoded.innerPicker_Args);
    expect(leafDecoded).toEqual({ answer: "42", flag: "true" });
    // leaf.deepest (type: prompts) has no picked value → dropped at level 2.
    expect(leafDecoded.deepest).toBeUndefined();
    expect(leafDecoded.deepest_Args).toBeUndefined();
  });

  test("depth cap: picker at level === MAX_NESTED_LEVEL is skipped entirely", () => {
    // Even with a "picked" value in state, a picker at MAX_NESTED_LEVEL must
    // NOT emit anything — its _Args would drive a backend sub-render past
    // promptTextMaxDepth. Defense-in-depth alongside the disabled render.
    const params = [
      { name: "wouldBeDeeper", type: "prompts" },
      { name: "note", type: "text" },
    ];
    const node = {
      values: { wouldBeDeeper: "sentinel", note: "kept" },
      sub: {},
    };
    const result = buildInnerArgs(
      params,
      node,
      promptsList,
      MAX_NESTED_LEVEL,
    );
    expect(result.wouldBeDeeper).toBeUndefined();
    expect(result.wouldBeDeeper_Args).toBeUndefined();
    // Non-picker sibling still emitted at the same level.
    expect(result.note).toBe("kept");
  });

  test("depth cap: even required picker at MAX_NESTED_LEVEL is skipped", () => {
    // The required-empty pass-through for pickers below the cap MUST NOT apply
    // at the cap itself — a required picker at level 3 would otherwise emit
    // "" and confuse the backend into thinking the caller wanted a picker
    // there. Verified: cap-skip wins over required-empty.
    const params = [{ name: "requiredDeep", type: "prompts", required: true }];
    const result = buildInnerArgs(
      params,
      { values: {} },
      promptsList,
      MAX_NESTED_LEVEL,
    );
    expect(result).toEqual({});
  });

  test("picker with unknown picked prompt: <Name> emitted but no _Args", () => {
    // Backend will fail to render an unknown prompt, but the UI still forwards
    // the picked name so the error is diagnosable server-side.
    const params = [{ name: "picker", type: "prompts" }];
    const node = { values: { picker: "not-in-list" }, sub: {} };
    const result = buildInnerArgs(params, node, promptsList, 0);
    expect(result).toEqual({ picker: "not-in-list" });
  });

  test("v1 regression: outer picker with non-prompts-only inner is byte-compatible", () => {
    // With no `type: prompts` fields in the inner params, the recursive
    // builder must collapse to the exact per-field output the v1 flat builder
    // produced. This pins the "existing single-level flows unchanged"
    // acceptance criterion.
    const flatInnerPrompt = {
      name: "flat",
      parameters: [
        { name: "title", type: "text", required: true },
        { name: "notes", type: "text" },
        { name: "enable", type: "boolean" },
      ],
    };
    const params = [{ name: "picker", type: "prompts" }];
    const node = {
      values: { picker: "flat" },
      sub: {
        picker: {
          values: { title: "T", notes: "  ", enable: true },
          sub: {},
        },
      },
    };
    const result = buildInnerArgs(
      params,
      node,
      [flatInnerPrompt, ...promptsList],
      0,
    );
    expect(result.picker).toBe("flat");
    expect(JSON.parse(result.picker_Args)).toEqual({
      title: "T",
      enable: "true",
    });
  });
});


// -----------------------------------------------------------------------------
// collectPickedPaths — walks the tree and enumerates every currently-picked
// node so the remembered-args fetch effect can fire one request per depth.
// -----------------------------------------------------------------------------
describe("collectPickedPaths", () => {
  const outerParams = [
    { name: "picker", type: "prompts" },
    { name: "note", type: "text" },
  ];

  test("returns [] when the tree is empty or non-object", () => {
    expect(collectPickedPaths({}, outerParams, {}, promptsList)).toEqual([]);
    expect(collectPickedPaths(null, outerParams, {}, promptsList)).toEqual([]);
    expect(
      collectPickedPaths(undefined, outerParams, {}, promptsList),
    ).toEqual([]);
  });

  test("returns [] when no picker has a value", () => {
    const tree = { picker: { values: {}, sub: {} } };
    expect(collectPickedPaths(tree, outerParams, {}, promptsList)).toEqual([]);
  });

  test("skips pickers whose picked value does not match a known prompt", () => {
    const tree = { picker: { values: {}, sub: {} } };
    const result = collectPickedPaths(
      tree,
      outerParams,
      { picker: "unknown" },
      promptsList,
    );
    expect(result).toEqual([]);
  });

  test("emits one entry per picked level with the joined path", () => {
    // outer.picker=mid, mid.innerPicker=leaf → two entries (level 1 + level 2).
    const tree = {
      picker: {
        values: { innerPicker: "leaf" },
        sub: {
          innerPicker: {
            values: { answer: "42" },
            sub: {},
          },
        },
      },
    };
    const result = collectPickedPaths(
      tree,
      outerParams,
      { picker: "mid" },
      promptsList,
    );
    expect(result).toHaveLength(2);
    expect(result[0]).toEqual({
      path: ["picker"],
      pickedPromptName: "mid",
      prompt: midPrompt,
    });
    expect(result[1]).toEqual({
      path: ["picker", "innerPicker"],
      pickedPromptName: "leaf",
      prompt: leafPrompt,
    });
  });

  test("only pickers appear — text/boolean outer params are ignored", () => {
    const tree = { picker: { values: {}, sub: {} } };
    const result = collectPickedPaths(
      tree,
      outerParams,
      { picker: "mid", note: "hello" },
      promptsList,
    );
    expect(result).toHaveLength(1);
    expect(result[0].path).toEqual(["picker"]);
  });

  test("descends into a picker whose subtree slot is missing (level-1 only entry)", () => {
    // outer.picker=mid picked, but sub.picker not yet created (fresh tree).
    // Should still emit the outer entry.
    const tree = {};
    const result = collectPickedPaths(
      tree,
      outerParams,
      { picker: "mid" },
      promptsList,
    );
    expect(result).toHaveLength(1);
    expect(result[0].path).toEqual(["picker"]);
    expect(result[0].pickedPromptName).toBe("mid");
  });
});

