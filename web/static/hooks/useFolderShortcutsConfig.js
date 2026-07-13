// Mitto Web Interface — Folder Shortcuts Config Hook
//
// Owns all folder-shortcuts state, the tab-open load effect, the folder-switch
// reset effect, the four row-mutation handlers (add/update/remove/move), the
// persist-on-save function, and the memoized redundant-prompt-names map for
// the folder-level Shortcuts tab. Extracted verbatim from WorkspacesDialog.js
// (mitto-90f.4 Increment D-4) so the shell can drop ~130 LOC and pass two
// grouped objects (shortcuts/shortcutsHandlers) instead of 9 individual props
// through WorkspaceFolderEditor.
//
// Behavior-preserving: state names, setter names, handler names, effect deps
// and error surfacing all match the original shell code exactly. The hook
// takes selectedFolder, activeTab, getSelectedFolderDir and shortcutSections
// (the section-descriptor list constant) as arguments. Unlike the prompts/
// processors hooks this one does NOT receive setError, because the shortcuts
// tab has its own dedicated error state (shortcutsError) surfaced through
// WorkspaceFolderShortcutsTab, and the persist step surfaces errors through
// the shared shell banner via a thrown Error that handleSave catches.
//
// The shell also needs shortcutsLoaded (to know whether to call persistShortcuts
// from handleSave) and persistShortcuts itself; both are returned as top-level
// fields on the hook result so the shell can destructure them alongside the
// grouped {shortcuts, shortcutsHandlers} objects.

const { useState, useEffect, useMemo } = window.preact;

import {
  authFetch,
  secureFetch,
  endpoints,
  errorMessageFromData,
} from "../utils/index.js";

import { promptMenuIncludes } from "../utils/prompts.js";

/**
 * useFolderShortcutsConfig — cohesive state/handler bundle for the folder
 * Shortcuts tab.
 *
 * @param {Object} params
 * @param {string|null} params.selectedFolder — the currently selected folder display name
 * @param {string} params.activeTab — the currently active folder tab id
 * @param {() => string|null} params.getSelectedFolderDir — resolver for the folder's working_dir
 * @param {Array<{id: string}>} params.shortcutSections — section descriptors (id/label/desc)
 * @returns {{
 *   shortcuts: Object,
 *   shortcutsHandlers: Object,
 *   shortcutsLoaded: boolean,
 *   persistShortcuts: () => Promise<void>,
 * }}
 */
export function useFolderShortcutsConfig({
  selectedFolder,
  activeTab,
  getSelectedFolderDir,
  shortcutSections,
}) {
  // Folder shortcuts state (for the Shortcuts tab)
  const [shortcutsSections, setShortcutsSections] = useState({});
  const [shortcutsLoading, setShortcutsLoading] = useState(false);
  const [shortcutsLoaded, setShortcutsLoaded] = useState(false);
  const [shortcutsError, setShortcutsError] = useState("");
  // Per-section prompt lists, filtered by the section's prompt menu tag and
  // sorted by name. Section ids match those persisted on the server side.
  const [sectionPrompts, setSectionPrompts] = useState({
    tasksList: [],
    conversations: [],
    beadsIssue: [],
  });
  // Global shortcut sections (from settings.json). Used only to derive which
  // prompts are already configured globally so they can be excluded from the
  // folder-level dropdowns and any duplicate folder rows greyed out.
  const [globalShortcutsSections, setGlobalShortcutsSections] = useState({});

  // Lazily load shortcuts when the Shortcuts folder tab is opened.
  useEffect(() => {
    if (activeTab !== "shortcuts" || !selectedFolder) return;
    const workingDir = getSelectedFolderDir();
    if (!workingDir) return;
    setShortcutsLoading(true);
    setShortcutsError("");
    Promise.all([
      authFetch(endpoints.folders.shortcuts({ working_dir: workingDir }))
        .then((r) => r.json())
        .then((data) => setShortcutsSections(data.sections || {})),
      // Global shortcuts: prompts already configured here are excluded from the
      // folder dropdowns (and any duplicate folder rows are greyed out).
      authFetch(endpoints.global.shortcuts())
        .then((r) => r.json())
        .then((data) => setGlobalShortcutsSections(data.sections || {}))
        .catch(() => setGlobalShortcutsSections({})),
      authFetch(
        endpoints.workspacePrompts.list({
          working_dir: workingDir,
          include_global: true,
        }),
      )
        .then((r) => r.json())
        .then((data) => {
          const all = data.prompts || [];
          const byMenu = (menu) =>
            all
              .filter((p) => promptMenuIncludes(p, menu))
              .sort((a, b) => a.name.localeCompare(b.name));
          setSectionPrompts({
            tasksList: byMenu("beadsList"),
            conversations: byMenu("prompts"),
            beadsIssue: byMenu("beadsIssues"),
          });
        }),
    ])
      .then(() => setShortcutsLoaded(true))
      .catch((err) =>
        setShortcutsError("Failed to load shortcuts: " + err.message),
      )
      .finally(() => setShortcutsLoading(false));
  }, [activeTab, selectedFolder]);

  // Reset shortcuts state when switching folders.
  useEffect(() => {
    setShortcutsSections({});
    setSectionPrompts({ tasksList: [], conversations: [], beadsIssue: [] });
    setShortcutsError("");
    setShortcutsLoaded(false);
  }, [selectedFolder]);

  // Immutably update a row in the given section.
  const updateShortcutRow = (section, idx, patch) => {
    setShortcutsSections((prev) => {
      const list = [...(prev[section] || [])];
      list[idx] = { ...list[idx], ...patch };
      return { ...prev, [section]: list };
    });
  };

  // Remove a row from the given section.
  const removeShortcutRow = (section, idx) => {
    setShortcutsSections((prev) => {
      const list = [...(prev[section] || [])];
      list.splice(idx, 1);
      return { ...prev, [section]: list };
    });
  };

  // Move a row up (dir=-1) or down (dir=1) within the given section.
  const moveShortcutRow = (section, idx, dir) => {
    setShortcutsSections((prev) => {
      const list = [...(prev[section] || [])];
      const target = idx + dir;
      if (target < 0 || target >= list.length) return prev;
      [list[idx], list[target]] = [list[target], list[idx]];
      return { ...prev, [section]: list };
    });
  };

  // Append a new row to the given section, seeded with sensible defaults so it
  // renders as a complete, usable shortcut right away.
  const addShortcutRow = (section) => {
    // Default the prompt to the first available prompt for this section (if any)
    // so the row is immediately editable rather than showing an empty selector.
    const available = sectionPrompts[section] || [];
    const defaultPrompt = available.length > 0 ? available[0].name : "";
    setShortcutsSections((prev) => {
      const list = [...(prev[section] || [])];
      if (list.length >= 10) return prev;
      // Empty icon → fall back to the linked prompt's own icon at render time.
      list.push({ icon: "", prompt: defaultPrompt });
      return { ...prev, [section]: list };
    });
  };

  // Persist the shortcuts sections via PUT /api/folders/shortcuts.
  // Throws on failure; updates local state on success. Invoked by the
  // dialog footer Save (handleSave).
  const persistShortcuts = async () => {
    const workingDir = getSelectedFolderDir();
    if (!workingDir) return;
    // Build all sections: drop rows with empty prompt, cap to 10 per section.
    const sections = {};
    for (const id of ["tasksList", "conversations", "beadsIssue"]) {
      sections[id] = (shortcutsSections[id] || [])
        .filter((r) => r.prompt)
        .slice(0, 10);
    }
    const res = await secureFetch(
      endpoints.folders.shortcuts({ working_dir: workingDir }),
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ sections }),
      },
    );
    const data = await res.json().catch(() => ({}));
    if (!res.ok)
      throw new Error(errorMessageFromData(data, "Failed to save shortcuts"));
    setShortcutsSections(data.sections || {});
    // Notify any open Tasks list (BeadsView) so its shortcut buttons refresh
    // immediately, without requiring a full page reload.
    window.dispatchEvent(
      new CustomEvent("mitto:folder_shortcuts_updated", {
        detail: { working_dir: workingDir },
      }),
    );
  };

  // Per-section set of prompt names configured at the GLOBAL level. Folder rows
  // referencing these are greyed out and the prompts are excluded from the
  // folder-level dropdowns (they are already shown via the global shortcuts).
  const shortcutRedundantPromptNames = useMemo(() => {
    const out = {};
    for (const { id } of shortcutSections) {
      out[id] = new Set(
        (globalShortcutsSections[id] || [])
          .map((r) => r.prompt)
          .filter(Boolean),
      );
    }
    return out;
  }, [globalShortcutsSections, shortcutSections]);

  const shortcuts = {
    shortcutsSections,
    sectionPrompts,
    shortcutsLoading,
    shortcutsError,
    shortcutRedundantPromptNames,
  };
  const shortcutsHandlers = {
    addShortcutRow,
    updateShortcutRow,
    removeShortcutRow,
    moveShortcutRow,
  };
  return { shortcuts, shortcutsHandlers, shortcutsLoaded, persistShortcuts };
}
