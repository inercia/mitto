// Mitto Web Interface — Beads Folder Config Hook
//
// Owns all beads-related state, handlers, and effects for the folder-level
// Beads Config + upstream (Jira/GitHub/GitLab/Linear/prompts) tab. Extracted
// verbatim from WorkspacesDialog.js (mitto-90f.4 Increment D-1) so the shell
// can drop ~250 LOC and pass three grouped objects (beads/beadsSetters/
// beadsHandlers) instead of 24 individual props through WorkspaceFolderEditor
// down to WorkspaceFolderBeadsTab.
//
// Behavior-preserving: state names, setter names, handler names, effect deps
// and reset timing all match the original shell code exactly. The hook is
// pure with respect to non-beads shell state — it takes selectedFolder,
// activeTab, getSelectedFolderDir, and setError as arguments.

const { useState, useEffect } = window.preact;

import { authFetch, secureFetch, endpoints } from "../utils/index.js";

// Flatten the canonical nested error envelope {error:{code,message,details}} to a
// flat message string. Returns "" when there is no error. Also accepts the legacy
// flat {error:"..."} shape (the HTTP-200 bd-failure path) unchanged.
function beadsErrorMessage(data) {
  if (!data || !data.error) return "";
  if (typeof data.error === "object") {
    return (data.error && data.error.message) || "Request failed";
  }
  return data.error;
}

/**
 * useBeadsFolderConfig — cohesive state/handler bundle for the folder Beads tab.
 *
 * All beads handlers currently surface failures through the tab-local
 * `beadsConfigError` state (rendered by WorkspaceFolderBeadsTab), so the shell's
 * top-level `setError` banner is intentionally not wired in here; add it to the
 * signature if a future handler needs to surface an error to the shell.
 *
 * @param {Object} params
 * @param {string|null} params.selectedFolder — the currently selected folder display name
 * @param {string} params.activeTab — the currently active folder tab id
 * @param {() => string|null} params.getSelectedFolderDir — resolver for the folder's working_dir
 * @returns {{ beads: Object, beadsSetters: Object, beadsHandlers: Object }}
 */
export function useBeadsFolderConfig({
  selectedFolder,
  activeTab,
  getSelectedFolderDir,
}) {
  // Folder beads config state (for the Beads Config tab) — UI wrapper over `bd config`.
  // beadsConfig holds the raw {key: value} map last loaded from the server.
  // beadsConfigEntries is the editable list of {key, value} rows for namespaced keys.
  const [beadsConfig, setBeadsConfig] = useState(null);
  const [beadsConfigLoading, setBeadsConfigLoading] = useState(false);
  const [beadsConfigError, setBeadsConfigError] = useState("");
  const [beadsConfigSaving, setBeadsConfigSaving] = useState(false);
  const [newBeadsKey, setNewBeadsKey] = useState("");
  const [newBeadsValue, setNewBeadsValue] = useState("");
  // Folder beads upstream task system ("none"|"jira"|"github"|"gitlab"|"linear"|"prompts"),
  // persisted in folders.json via /api/issues/upstream.
  const [beadsUpstream, setBeadsUpstream] = useState("none");
  const [beadsUpstreamSaving, setBeadsUpstreamSaving] = useState(false);
  // "prompts" upstream: names of the three configured prompt actions.
  const [beadsPullPrompt, setBeadsPullPrompt] = useState("");
  const [beadsPushPrompt, setBeadsPushPrompt] = useState("");
  const [beadsSyncPrompt, setBeadsSyncPrompt] = useState("");
  // Saved argument maps (name→string) for each prompt action.
  const [beadsPullPromptArgs, setBeadsPullPromptArgs] = useState({});
  const [beadsPushPromptArgs, setBeadsPushPromptArgs] = useState({});
  const [beadsSyncPromptArgs, setBeadsSyncPromptArgs] = useState({});
  // Available enabled folder prompts (populated when upstream === "prompts").
  const [beadsUpstreamPrompts, setBeadsUpstreamPrompts] = useState([]);
  const [beadsUpstreamPromptsLoading, setBeadsUpstreamPromptsLoading] =
    useState(false);

  // Load (reload) beads config for the selected folder via GET /api/issues/config.
  const reloadBeadsConfig = async (workingDir) => {
    setBeadsConfigLoading(true);
    setBeadsConfigError("");
    try {
      const res = await authFetch(
        endpoints.issues.config({ working_dir: workingDir }),
      );
      const data = await res.json();
      const errMsg = beadsErrorMessage(data);
      if (errMsg) {
        // bd missing or not initialized in this folder, or a validation error.
        setBeadsConfig(null);
        setBeadsConfigError(errMsg);
      } else {
        setBeadsConfig(data || {});
      }
    } catch (err) {
      setBeadsConfig(null);
      setBeadsConfigError(err.message || "Failed to load beads config");
    } finally {
      setBeadsConfigLoading(false);
    }
  };

  // Set a single beads config key via PUT /api/issues/config, then reload.
  const setBeadsConfigKey = async (key, value) => {
    const workingDir = getSelectedFolderDir();
    if (!workingDir || !key) return;
    setBeadsConfigSaving(true);
    setBeadsConfigError("");
    try {
      const res = await secureFetch(
        endpoints.issues.config({ working_dir: workingDir }),
        {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ key, value }),
        },
      );
      const data = await res.json().catch(() => ({}));
      if (!res.ok)
        throw new Error(beadsErrorMessage(data) || "Failed to set config");
      if (data && data.error)
        throw new Error(data.stderr || beadsErrorMessage(data));
      await reloadBeadsConfig(workingDir);
    } catch (err) {
      setBeadsConfigError(err.message || "Failed to set config");
    } finally {
      setBeadsConfigSaving(false);
    }
  };

  // Delete a single beads config key via DELETE /api/issues/config, then reload.
  const unsetBeadsConfigKey = async (key) => {
    const workingDir = getSelectedFolderDir();
    if (!workingDir || !key) return;
    setBeadsConfigSaving(true);
    setBeadsConfigError("");
    try {
      const res = await secureFetch(
        endpoints.issues.config({ working_dir: workingDir, key }),
        { method: "DELETE" },
      );
      const data = await res.json().catch(() => ({}));
      if (!res.ok)
        throw new Error(beadsErrorMessage(data) || "Failed to delete config");
      if (data && data.error)
        throw new Error(data.stderr || beadsErrorMessage(data));
      await reloadBeadsConfig(workingDir);
    } catch (err) {
      setBeadsConfigError(err.message || "Failed to delete config");
    } finally {
      setBeadsConfigSaving(false);
    }
  };

  // Load the folder's upstream task system via GET /api/issues/upstream.
  const reloadBeadsUpstream = async (workingDir) => {
    try {
      const res = await authFetch(
        endpoints.issues.upstream({ working_dir: workingDir }),
      );
      const data = await res.json().catch(() => ({}));
      setBeadsUpstream((data && data.upstream) || "none");
      setBeadsPullPrompt((data && data.pull_prompt) || "");
      setBeadsPushPrompt((data && data.push_prompt) || "");
      setBeadsSyncPrompt((data && data.sync_prompt) || "");
      setBeadsPullPromptArgs((data && data.pull_prompt_args) || {});
      setBeadsPushPromptArgs((data && data.push_prompt_args) || {});
      setBeadsSyncPromptArgs((data && data.sync_prompt_args) || {});
    } catch (_err) {
      setBeadsUpstream("none");
    }
  };

  // Load available enabled folder prompts for the "prompts" upstream pickers.
  // Parametrized prompts are included; per-prompt arguments are configured via
  // the sliders button next to each row.
  const loadBeadsUpstreamPrompts = async (workingDir) => {
    if (!workingDir) return;
    setBeadsUpstreamPromptsLoading(true);
    try {
      const res = await authFetch(
        endpoints.workspacePrompts.list({
          working_dir: workingDir,
          include_global: true,
        }),
      );
      const data = await res.json().catch(() => ({}));
      const all = (data && data.prompts) || [];
      setBeadsUpstreamPrompts(all.filter((p) => p.enabled !== false));
    } catch (_err) {
      setBeadsUpstreamPrompts([]);
    } finally {
      setBeadsUpstreamPromptsLoading(false);
    }
  };

  // Persist the folder's upstream task system via PUT /api/issues/upstream.
  const saveBeadsUpstream = async (upstream) => {
    const workingDir = getSelectedFolderDir();
    if (!workingDir) return;
    const prev = beadsUpstream;
    setBeadsUpstream(upstream); // optimistic
    setBeadsUpstreamSaving(true);
    try {
      const body = { upstream };
      if (upstream === "prompts") {
        body.pull_prompt = beadsPullPrompt;
        body.push_prompt = beadsPushPrompt;
        body.sync_prompt = beadsSyncPrompt;
        body.pull_prompt_args = beadsPullPromptArgs;
        body.push_prompt_args = beadsPushPromptArgs;
        body.sync_prompt_args = beadsSyncPromptArgs;
      }
      const res = await secureFetch(
        endpoints.issues.upstream({ working_dir: workingDir }),
        {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        },
      );
      const data = await res.json().catch(() => ({}));
      if (!res.ok)
        throw new Error(beadsErrorMessage(data) || "Failed to set upstream");
      if (data && data.error) throw new Error(beadsErrorMessage(data));
      setBeadsUpstream((data && data.upstream) || upstream);
      setBeadsPullPrompt((data && data.pull_prompt) || "");
      setBeadsPushPrompt((data && data.push_prompt) || "");
      setBeadsSyncPrompt((data && data.sync_prompt) || "");
      setBeadsPullPromptArgs((data && data.pull_prompt_args) || {});
      setBeadsPushPromptArgs((data && data.push_prompt_args) || {});
      setBeadsSyncPromptArgs((data && data.sync_prompt_args) || {});
    } catch (err) {
      setBeadsUpstream(prev); // revert on failure
      setBeadsConfigError(err.message || "Failed to set upstream");
    } finally {
      setBeadsUpstreamSaving(false);
    }
  };

  // Persist a single pull/push/sync prompt selection for the "prompts" upstream.
  const saveBeadsPromptName = async (field, value) => {
    const workingDir = getSelectedFolderDir();
    if (!workingDir) return;
    const setterMap = {
      pull_prompt: setBeadsPullPrompt,
      push_prompt: setBeadsPushPrompt,
      sync_prompt: setBeadsSyncPrompt,
    };
    const prevMap = {
      pull_prompt: beadsPullPrompt,
      push_prompt: beadsPushPrompt,
      sync_prompt: beadsSyncPrompt,
    };
    const setter = setterMap[field];
    const prev = prevMap[field];
    if (!setter) return;
    setter(value); // optimistic
    setBeadsUpstreamSaving(true);
    try {
      const res = await secureFetch(
        endpoints.issues.upstream({ working_dir: workingDir }),
        {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            upstream: "prompts",
            pull_prompt: field === "pull_prompt" ? value : beadsPullPrompt,
            push_prompt: field === "push_prompt" ? value : beadsPushPrompt,
            sync_prompt: field === "sync_prompt" ? value : beadsSyncPrompt,
            pull_prompt_args: beadsPullPromptArgs,
            push_prompt_args: beadsPushPromptArgs,
            sync_prompt_args: beadsSyncPromptArgs,
          }),
        },
      );
      const data = await res.json().catch(() => ({}));
      if (!res.ok)
        throw new Error(beadsErrorMessage(data) || "Failed to save prompt");
      if (data && data.error) throw new Error(beadsErrorMessage(data));
    } catch (err) {
      setter(prev); // revert on failure
      setBeadsConfigError(err.message || "Failed to save prompt");
    } finally {
      setBeadsUpstreamSaving(false);
    }
  };

  // Persist the saved argument map for a single pull/push/sync prompt.
  // Sends the FULL upstream body (all three names + all three arg maps) so the
  // backend can round-trip; reverts on failure.
  const saveBeadsPromptArgs = async (field, args) => {
    const workingDir = getSelectedFolderDir();
    if (!workingDir) return;
    const setterMap = {
      pull_prompt: setBeadsPullPromptArgs,
      push_prompt: setBeadsPushPromptArgs,
      sync_prompt: setBeadsSyncPromptArgs,
    };
    const prevMap = {
      pull_prompt: beadsPullPromptArgs,
      push_prompt: beadsPushPromptArgs,
      sync_prompt: beadsSyncPromptArgs,
    };
    const setter = setterMap[field];
    const prev = prevMap[field];
    if (!setter) return;
    setter(args); // optimistic
    setBeadsUpstreamSaving(true);
    try {
      const res = await secureFetch(
        endpoints.issues.upstream({ working_dir: workingDir }),
        {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            upstream: "prompts",
            pull_prompt: beadsPullPrompt,
            push_prompt: beadsPushPrompt,
            sync_prompt: beadsSyncPrompt,
            pull_prompt_args:
              field === "pull_prompt" ? args : beadsPullPromptArgs,
            push_prompt_args:
              field === "push_prompt" ? args : beadsPushPromptArgs,
            sync_prompt_args:
              field === "sync_prompt" ? args : beadsSyncPromptArgs,
          }),
        },
      );
      const data = await res.json().catch(() => ({}));
      if (!res.ok)
        throw new Error(beadsErrorMessage(data) || "Failed to save arguments");
      if (data && data.error) throw new Error(beadsErrorMessage(data));
    } catch (err) {
      setter(prev); // revert on failure
      setBeadsConfigError(err.message || "Failed to save arguments");
    } finally {
      setBeadsUpstreamSaving(false);
    }
  };

  // Lazily load beads config + upstream when the Beads folder tab is opened.
  useEffect(() => {
    if (activeTab !== "beads" || !selectedFolder) return;
    const workingDir = getSelectedFolderDir();
    if (workingDir) {
      reloadBeadsConfig(workingDir);
      reloadBeadsUpstream(workingDir);
    }
  }, [activeTab, selectedFolder]);

  // Load argument-free folder prompts when the Beads tab is active and upstream is "prompts".
  useEffect(() => {
    if (activeTab !== "beads" || !selectedFolder || beadsUpstream !== "prompts")
      return;
    const workingDir = getSelectedFolderDir();
    if (workingDir) loadBeadsUpstreamPrompts(workingDir);
  }, [activeTab, selectedFolder, beadsUpstream]);

  // Reset beads config state when switching folders.
  useEffect(() => {
    setBeadsConfig(null);
    setBeadsConfigError("");
    setNewBeadsKey("");
    setNewBeadsValue("");
    setBeadsUpstream("none");
    setBeadsPullPrompt("");
    setBeadsPushPrompt("");
    setBeadsSyncPrompt("");
    setBeadsPullPromptArgs({});
    setBeadsPushPromptArgs({});
    setBeadsSyncPromptArgs({});
    setBeadsUpstreamPrompts([]);
  }, [selectedFolder]);

  const beads = {
    beadsConfig,
    beadsConfigLoading,
    beadsConfigError,
    beadsConfigSaving,
    newBeadsKey,
    newBeadsValue,
    beadsUpstream,
    beadsUpstreamSaving,
    beadsPullPrompt,
    beadsPushPrompt,
    beadsSyncPrompt,
    beadsPullPromptArgs,
    beadsPushPromptArgs,
    beadsSyncPromptArgs,
    beadsUpstreamPrompts,
    beadsUpstreamPromptsLoading,
  };
  const beadsSetters = { setNewBeadsKey, setNewBeadsValue };
  const beadsHandlers = {
    setBeadsConfigKey,
    unsetBeadsConfigKey,
    saveBeadsUpstream,
    saveBeadsPromptName,
    saveBeadsPromptArgs,
  };
  return { beads, beadsSetters, beadsHandlers };
}
