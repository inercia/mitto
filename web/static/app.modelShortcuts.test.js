import { readFileSync } from "node:fs";
import { describe, test, expect } from "./utils/testing/testGlobals.js";
import { resolveModelShortcut } from "./utils/modelShortcuts.js";
import { KEYBOARD_SHORTCUTS } from "./constants.js";

const source = readFileSync(new URL("./app.js", import.meta.url), "utf8");

function configSeams() {
  let mappings = [];
  let profiles = [];
  const calls = [];
  const bindings = {
    setModelShortcuts: (value) => { mappings = value; },
    setModelProfiles: (value) => { profiles = value; },
  };
  const mountStart = source.indexOf(".then((config) => {", source.indexOf("// Fetch config on mount"));
  const mountEnd = source.indexOf("// Track if config is read-only", mountStart);
  const mount = new Function("config", ...Object.keys(bindings), source.slice(mountStart + ".then((config) => {".length, mountEnd));
  const saveMarker = "onSave=${async () => {";
  const saveStart = source.indexOf(saveMarker);
  const saveEnd = source.indexOf("// Reload UI settings", saveStart);
  // Execute the real Save refresh prefix, ending after both config assignments.
  const save = new Function("fetchConfig", "refreshWorkspaces", "invalidateConfigCache", ...Object.keys(bindings),
    `return async () => {${source.slice(saveStart + saveMarker.length, saveEnd)} } } catch (error) { throw error; } };`);
  return {
    get mappings() { return mappings; }, get profiles() { return profiles; }, calls,
    mount(config) { mount(config, ...Object.values(bindings)); },
    save(config) { return save(async () => { calls.push("fetch"); return config; },
      () => calls.push("workspaces"), () => calls.push("invalidate"), ...Object.values(bindings))(); },
  };
}

describe("App runtime shortcut propagation", () => {
  test("initial load independently copies saved mappings and retains prompt profiles", () => {
    const h = configSeams();
    const config = { models: [{ name: "Prompt profile" }], model_shortcuts: [{ number: 1, model_tag: " Custom  Tag " }] };
    h.mount(config);
    expect(h.mappings).toEqual(config.model_shortcuts);
    expect(h.mappings).not.toBe(config.model_shortcuts);
    expect(h.mappings[0]).not.toBe(config.model_shortcuts[0]);
    expect(h.profiles).toBe(config.models);
    expect(resolveModelShortcut(1, h.mappings)).toEqual({ status: "apply", modelTag: "Custom  Tag" });
    config.model_shortcuts[0].model_tag = "mutated cache";
    expect(h.mappings[0].model_tag).toBe(" Custom  Tag ");
  });

  test("normal Save invalidates then fetches, replaces mappings and explicitly clears", async () => {
    const h = configSeams();
    h.mount({ model_shortcuts: [{ number: 1, model_tag: "Before" }] });
    const updated = { models: [{ name: "Still prompt profiles" }], model_shortcuts: [{ number: 2, model_tag: "After" }] };
    await h.save(updated);
    expect(h.calls).toEqual(["workspaces", "invalidate", "fetch"]);
    expect(h.mappings).toEqual(updated.model_shortcuts);
    expect(h.mappings[0]).not.toBe(updated.model_shortcuts[0]);
    expect(h.profiles).toBe(updated.models);
    await h.save({ model_shortcuts: [] });
    expect(h.mappings).toEqual([]);
    expect(resolveModelShortcut(2, h.mappings)).toEqual({ status: "unassigned" });
  });

  test("missing/null/nonarray mappings clear in both successful config seams, never infer profiles", async () => {
    for (const value of [undefined, null, {}, "bad"]) {
      const h = configSeams();
      h.mount({ model_shortcuts: [{ number: 1, model_tag: "Before" }] });
      h.mount({ model_shortcuts: value, models: [{ shortcut_number: 1, tags: ["legacy"] }] });
      expect(h.mappings).toEqual([]);
      h.mount({ model_shortcuts: [{ number: 1, model_tag: "Before" }] });
      await h.save({ model_shortcuts: value });
      expect(h.mappings).toEqual([]);
    }
  });

  test("state, callback and mappings reach ChatInput; legacy dropdown/prompt props remain", () => {
    expect(source).toContain("const [modelShortcuts, setModelShortcuts] = useState([]);");
    const socketBindings = source.slice(source.indexOf("const {", source.indexOf("function App")), source.indexOf("} = useWebSocket("));
    expect(socketBindings).toContain("applyModelTag,");
    const composerStart = source.search(/<\$\{ChatInput\}/);
    const composer = source.slice(composerStart, source.indexOf("/>", composerStart));
    expect(composer).toMatch(/modelShortcuts=\$\{modelShortcuts\}/);
    expect(composer).toMatch(/onApplyModelTag=\$\{applyModelTag\}/);
    expect(composer).toMatch(/onSetConfigOption=\$\{setConfigOption\}/);
    expect(composer).toMatch(/modelProfiles=\$\{modelProfiles\}/);
  });

  test("model help describes tags, Keyboard settings and both platform combinations", () => {
    const entry = KEYBOARD_SHORTCUTS.find((row) => row.keys === "⌘⇧1-5");
    expect(entry.description).toContain("tag");
    expect(entry.hint).toContain("Settings > Keyboard");
    expect(entry.hint).toContain("Command+Shift+1-5");
    expect(entry.hint).toContain("Ctrl+Shift+1-5");
    const dialog = readFileSync(new URL("./components/KeyboardShortcutsDialog.js", import.meta.url), "utf8");
    expect(dialog).toContain("KEYBOARD_SHORTCUTS");
    expect(dialog).toContain("shortcut.hint");
  });
});
