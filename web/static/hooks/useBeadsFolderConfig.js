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

const { useState, useEffect, useRef } = window.preact;

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
 * @param {boolean} [params.isOpen] — whether the enclosing dialog is open; observed
 *   by the load effect so reopening the dialog on the same folder re-issues the
 *   config/upstream fetches (mitto-xdqx)
 * @param {() => string|null} params.getSelectedFolderDir — resolver for the folder's working_dir
 * @returns {{ beads: Object, beadsSetters: Object, beadsHandlers: Object }}
 */
export function useBeadsFolderConfig({
  selectedFolder,
  activeTab,
  isOpen,
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

  // Nonce/token tracking for in-flight fetches. Each async loader captures the
  // current token; when it resolves, it only touches state if the token still
  // matches. Bumping the token (on folder change / dialog close / new fetch)
  // invalidates every prior in-flight response — preventing the finally block
  // of an orphaned/never-resolving fetch from re-latching loading=true after
  // the reset effect cleared it (mitto-xdqx).
  const configLoadTokenRef = useRef(0);
  const upstreamLoadTokenRef = useRef(0);
  const upstreamPromptsLoadTokenRef = useRef(0);

  // Refs mirroring the six upstream-prompt values (three names + three arg
  // maps). The save handlers below PUT the FULL upstream body — the field being
  // changed plus the OTHER five values — so they must read the LATEST values,
  // not a render-time closure. Without these refs, changing one prompt select
  // before the initial `reloadBeadsUpstream` GET has resolved would ship "" for
  // the untouched fields and wipe them on the backend; that in turn empties
  // BeadsView's pullPromptName / pushPromptName / syncPromptName state on its
  // next fetch, disabling the pull/push/sync toolbar buttons.
  const beadsPullPromptRef = useRef("");
  const beadsPushPromptRef = useRef("");
  const beadsSyncPromptRef = useRef("");
  const beadsPullPromptArgsRef = useRef({});
  const beadsPushPromptArgsRef = useRef({});
  const beadsSyncPromptArgsRef = useRef({});

  // Load (reload) beads config for the selected folder via GET /api/issues/config.
  const reloadBeadsConfig = async (workingDir) => {
    const token = ++configLoadTokenRef.current;
    setBeadsConfigLoading(true);
    setBeadsConfigError("");
    try {
      const res = await authFetch(
        endpoints.issues.config({ working_dir: workingDir }),
      );
      const data = await res.json();
      if (token !== configLoadTokenRef.current) return;
      const errMsg = beadsErrorMessage(data);
      if (errMsg) {
        // bd missing or not initialized in this folder, or a validation error.
        setBeadsConfig(null);
        setBeadsConfigError(errMsg);
      } else {
        setBeadsConfig(data || {});
      }
    } catch (err) {
      if (token !== configLoadTokenRef.current) return;
      setBeadsConfig(null);
      setBeadsConfigError(err.message || "Failed to load beads config");
    } finally {
      if (token === configLoadTokenRef.current) {
        setBeadsConfigLoading(false);
      }
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
    const token = ++upstreamLoadTokenRef.current;
    try {
      const res = await authFetch(
        endpoints.issues.upstream({ working_dir: workingDir }),
      );
      const data = await res.json().catch(() => ({}));
      if (token !== upstreamLoadTokenRef.current) return;
      const pull = (data && data.pull_prompt) || "";
      const push = (data && data.push_prompt) || "";
      const sync = (data && data.sync_prompt) || "";
      const pullArgs = (data && data.pull_prompt_args) || {};
      const pushArgs = (data && data.push_prompt_args) || {};
      const syncArgs = (data && data.sync_prompt_args) || {};
      setBeadsUpstream((data && data.upstream) || "none");
      setBeadsPullPrompt(pull);
      setBeadsPushPrompt(push);
      setBeadsSyncPrompt(sync);
      setBeadsPullPromptArgs(pullArgs);
      setBeadsPushPromptArgs(pushArgs);
      setBeadsSyncPromptArgs(syncArgs);
      beadsPullPromptRef.current = pull;
      beadsPushPromptRef.current = push;
      beadsSyncPromptRef.current = sync;
      beadsPullPromptArgsRef.current = pullArgs;
      beadsPushPromptArgsRef.current = pushArgs;
      beadsSyncPromptArgsRef.current = syncArgs;
    } catch (_err) {
      if (token !== upstreamLoadTokenRef.current) return;
      setBeadsUpstream("none");
    }
  };

  // Load available enabled folder prompts for the "prompts" upstream pickers.
  // Parametrized prompts are included; per-prompt arguments are configured via
  // the sliders button next to each row.
  const loadBeadsUpstreamPrompts = async (workingDir) => {
    if (!workingDir) return;
    const token = ++upstreamPromptsLoadTokenRef.current;
    setBeadsUpstreamPromptsLoading(true);
    try {
      const res = await authFetch(
        endpoints.workspacePrompts.list({
          working_dir: workingDir,
          include_global: true,
        }),
      );
      const data = await res.json().catch(() => ({}));
      if (token !== upstreamPromptsLoadTokenRef.current) return;
      const all = (data && data.prompts) || [];
      setBeadsUpstreamPrompts(all.filter((p) => p.enabled !== false));
    } catch (_err) {
      if (token !== upstreamPromptsLoadTokenRef.current) return;
      setBeadsUpstreamPrompts([]);
    } finally {
      if (token === upstreamPromptsLoadTokenRef.current) {
        setBeadsUpstreamPromptsLoading(false);
      }
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
        // Read from refs, not closure — the PUT is a full-replace and the other
        // five fields must reflect the LATEST committed values, not what this
        // render captured. See ref declarations above for the wipe scenario.
        body.pull_prompt = beadsPullPromptRef.current;
        body.push_prompt = beadsPushPromptRef.current;
        body.sync_prompt = beadsSyncPromptRef.current;
        body.pull_prompt_args = beadsPullPromptArgsRef.current;
        body.push_prompt_args = beadsPushPromptArgsRef.current;
        body.sync_prompt_args = beadsSyncPromptArgsRef.current;
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
      const rPull = (data && data.pull_prompt) || "";
      const rPush = (data && data.push_prompt) || "";
      const rSync = (data && data.sync_prompt) || "";
      const rPullArgs = (data && data.pull_prompt_args) || {};
      const rPushArgs = (data && data.push_prompt_args) || {};
      const rSyncArgs = (data && data.sync_prompt_args) || {};
      setBeadsUpstream((data && data.upstream) || upstream);
      setBeadsPullPrompt(rPull);
      setBeadsPushPrompt(rPush);
      setBeadsSyncPrompt(rSync);
      setBeadsPullPromptArgs(rPullArgs);
      setBeadsPushPromptArgs(rPushArgs);
      setBeadsSyncPromptArgs(rSyncArgs);
      beadsPullPromptRef.current = rPull;
      beadsPushPromptRef.current = rPush;
      beadsSyncPromptRef.current = rSync;
      beadsPullPromptArgsRef.current = rPullArgs;
      beadsPushPromptArgsRef.current = rPushArgs;
      beadsSyncPromptArgsRef.current = rSyncArgs;
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
    const refMap = {
      pull_prompt: beadsPullPromptRef,
      push_prompt: beadsPushPromptRef,
      sync_prompt: beadsSyncPromptRef,
    };
    const setter = setterMap[field];
    const ref = refMap[field];
    if (!setter || !ref) return;
    const prev = ref.current;
    setter(value); // optimistic
    ref.current = value; // keep refs in sync with optimistic state
    setBeadsUpstreamSaving(true);
    try {
      const res = await secureFetch(
        endpoints.issues.upstream({ working_dir: workingDir }),
        {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            upstream: "prompts",
            // Read from refs, not closure — see ref declarations for the
            // race that leaves the untouched fields as "" and wipes them.
            pull_prompt: beadsPullPromptRef.current,
            push_prompt: beadsPushPromptRef.current,
            sync_prompt: beadsSyncPromptRef.current,
            pull_prompt_args: beadsPullPromptArgsRef.current,
            push_prompt_args: beadsPushPromptArgsRef.current,
            sync_prompt_args: beadsSyncPromptArgsRef.current,
          }),
        },
      );
      const data = await res.json().catch(() => ({}));
      if (!res.ok)
        throw new Error(beadsErrorMessage(data) || "Failed to save prompt");
      if (data && data.error) throw new Error(beadsErrorMessage(data));
    } catch (err) {
      setter(prev); // revert on failure
      ref.current = prev;
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
    const refMap = {
      pull_prompt: beadsPullPromptArgsRef,
      push_prompt: beadsPushPromptArgsRef,
      sync_prompt: beadsSyncPromptArgsRef,
    };
    const setter = setterMap[field];
    const ref = refMap[field];
    if (!setter || !ref) return;
    const prev = ref.current;
    setter(args); // optimistic
    ref.current = args; // keep refs in sync with optimistic state
    setBeadsUpstreamSaving(true);
    try {
      const res = await secureFetch(
        endpoints.issues.upstream({ working_dir: workingDir }),
        {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            upstream: "prompts",
            // Read from refs, not closure — same rationale as saveBeadsPromptName.
            pull_prompt: beadsPullPromptRef.current,
            push_prompt: beadsPushPromptRef.current,
            sync_prompt: beadsSyncPromptRef.current,
            pull_prompt_args: beadsPullPromptArgsRef.current,
            push_prompt_args: beadsPushPromptArgsRef.current,
            sync_prompt_args: beadsSyncPromptArgsRef.current,
          }),
        },
      );
      const data = await res.json().catch(() => ({}));
      if (!res.ok)
        throw new Error(beadsErrorMessage(data) || "Failed to save arguments");
      if (data && data.error) throw new Error(beadsErrorMessage(data));
    } catch (err) {
      setter(prev); // revert on failure
      ref.current = prev;
      setBeadsConfigError(err.message || "Failed to save arguments");
    } finally {
      setBeadsUpstreamSaving(false);
    }
  };

  // Lazily load beads config + upstream when the Beads folder tab is opened.
  // isOpen is in the deps so reopening the dialog on the same folder with
  // activeTab already === "beads" re-issues the fetch — WorkspacesDialog only
  // renders null while closed (no unmount), so without this the load effect
  // would be a no-op on reopen and any latched loading flag would stay stuck
  // (mitto-xdqx).
  useEffect(() => {
    if (!isOpen || activeTab !== "beads" || !selectedFolder) return;
    const workingDir = getSelectedFolderDir();
    if (workingDir) {
      reloadBeadsConfig(workingDir);
      reloadBeadsUpstream(workingDir);
    }
  }, [isOpen, activeTab, selectedFolder]);

  // Load argument-free folder prompts when the Beads tab is active and upstream is "prompts".
  useEffect(() => {
    if (activeTab !== "beads" || !selectedFolder || beadsUpstream !== "prompts")
      return;
    const workingDir = getSelectedFolderDir();
    if (workingDir) loadBeadsUpstreamPrompts(workingDir);
  }, [activeTab, selectedFolder, beadsUpstream]);

  // Reset beads config state when switching folders. Also clears the four
  // loading/saving flags so an in-flight fetch from the previous folder whose
  // finally() has not yet run cannot leave the Tasks tab latched on
  // "Loading…" (mitto-xdqx). Bumping the load tokens invalidates any orphaned
  // in-flight response so its late finally() cannot re-latch loading=true.
  useEffect(() => {
    configLoadTokenRef.current++;
    upstreamLoadTokenRef.current++;
    upstreamPromptsLoadTokenRef.current++;
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
    beadsPullPromptRef.current = "";
    beadsPushPromptRef.current = "";
    beadsSyncPromptRef.current = "";
    beadsPullPromptArgsRef.current = {};
    beadsPushPromptArgsRef.current = {};
    beadsSyncPromptArgsRef.current = {};
    setBeadsUpstreamPrompts([]);
    setBeadsConfigLoading(false);
    setBeadsConfigSaving(false);
    setBeadsUpstreamPromptsLoading(false);
    setBeadsUpstreamSaving(false);
  }, [selectedFolder]);

  // Extra safety net: when the dialog closes, force-clear the loading flags
  // and bump the load tokens so any orphaned in-flight fetch (e.g. the user
  // closed the dialog mid-request) cannot leave the spinner latched on the
  // next reopen (mitto-xdqx).
  useEffect(() => {
    if (isOpen) return;
    configLoadTokenRef.current++;
    upstreamLoadTokenRef.current++;
    upstreamPromptsLoadTokenRef.current++;
    setBeadsConfigLoading(false);
    setBeadsConfigSaving(false);
    setBeadsUpstreamPromptsLoading(false);
    setBeadsUpstreamSaving(false);
  }, [isOpen]);

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
