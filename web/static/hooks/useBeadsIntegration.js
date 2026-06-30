// web/static/hooks/useBeadsIntegration.js
// Manages the entire beads-view cluster for App: which workspace folder is
// currently shown in the beads panel, issue auto-selection state, the
// beadsIssueSessionMap derived value, prompt fetching helpers, and the
// handlers for opening the beads panel, running per-issue / list prompts,
// and navigating from a conversation's linked-issue link into the beads view.
const { useState, useCallback, useMemo, useRef } = window.preact;

import { authFetch, endpoints } from "../utils/index.js";
import {
  promptMenuIncludes,
  menuSatisfies,
  collectPromptArguments,
  getMissingPromptParameters,
  promptResolveAsPeriodic,
} from "../utils/prompts.js";
import { useConversationSeeding } from "./useConversationSeeding.js";

/**
 * Beads-view integration hook.
 *
 * @param {Object} deps
 * @param {Array}    deps.allSessions           - All sessions (active + stored); drives beadsIssueSessionMap.
 * @param {Array}    deps.workspaces            - All configured workspaces; used to pick is_default on run.
 * @param {Function} deps.newSession            - Creates a new conversation (from useWebSocket).
 * @param {Function} deps.showToast             - Toast dispatcher.
 * @param {Function} deps.switchSession         - Activates a conversation by id (from useWebSocket).
 * @param {Function} deps.setMainView           - Switches main view to "beads" / "conversation".
 * @param {Function} deps.setShowSidebar        - Closes sidebar overlay (mobile).
 * @param {Function} deps.setShowSidePanel      - Closes/opens the side panel (used in handleOpenBeadsIssue / return).
 * @param {Function} deps.setSidePanelTab       - Selects the side panel tab (used when returning to a conversation).
 * @param {Function} [deps.onOpenPeriodicDialog] - Opens the periodic schedule dialog.
 *   Signature: (prompt, onSchedule: ({ value, unit, at? }) => void) => void.
 *   When absent, periodic prompts fall back to the one-time named-prompt path.
 * @param {Function} [deps.onOpenPromptParamDialog] - Opens the prompt parameter dialog
 *   to collect free-text parameters that the beadsIssues menu cannot auto-fill.
 *   Signature: (prompt, parameters, onSubmit: (argsMap) => void) => void.
 *   When absent, prompts with missing params are dispatched without the dialog.
 */
export function useBeadsIntegration({
  allSessions,
  workspaces,
  newSession,
  showToast,
  switchSession,
  setMainView,
  setShowSidebar,
  setShowSidePanel,
  setSidePanelTab,
  onOpenPeriodicDialog,
  onOpenPromptParamDialog,
  activeSessionId,
}) {
  const { startConversationWithPrompt } = useConversationSeeding({
    newSession,
  });
  const [beadsWorkingDir, setBeadsWorkingDir] = useState(null);
  // When the beads view is opened from a linked conversation (e.g. the
  // properties panel's "Linked beads issue" link), these drive auto-selecting
  // that issue once the list loads. The nonce bumps on every open so clicking
  // the same issue again re-selects it.
  const [beadsInitialIssueId, setBeadsInitialIssueId] = useState(null);
  const [beadsSelectNonce, setBeadsSelectNonce] = useState(0);
  // Bumped to ask the beads view to open its "create" panel for a brand-new
  // issue (e.g. from the global "new task" keyboard shortcut). The nonce lets
  // BeadsView re-open create even when it is already mounted.
  const [beadsCreateNonce, setBeadsCreateNonce] = useState(0);
  // Bumped to ask an open beads view to re-fetch its issue list / open its
  // "clean up closed issues" confirmation. Used by the sidebar Tasks menu so
  // its Refresh / Cleanup actions drive the beads view's existing handlers.
  const [beadsRefreshNonce, setBeadsRefreshNonce] = useState(0);
  const [beadsCleanupNonce, setBeadsCleanupNonce] = useState(0);
  // Whether the single-issue viewer (BeadsIssueView) is open as a docked overlay
  // over the conversation. Unlike the beads list view it does NOT replace the
  // main view — the conversation stays mounted and visible behind it — so this
  // is tracked independently of mainView.
  const [beadsIssueOpen, setBeadsIssueOpen] = useState(false);
  // Session id of the conversation a single issue was opened from (via the
  // properties panel's "Linked beads issue" link). When the beads view's detail
  // panel for that issue is closed, we return to this conversation and re-open
  // its properties panel instead of leaving the user on the beads list. Held in
  // a ref so it survives re-renders without re-triggering effects; cleared once
  // the return is performed (or when the open did not originate from a panel).
  const beadsReturnSessionRef = useRef(null);
  // Whether closing the standalone issue viewer should re-open the originating
  // conversation's properties panel. Only set when the viewer was opened from
  // that panel's "Linked beads issue" link — auto-detected links in the
  // conversation body return to the conversation without popping the panel.
  const beadsReturnOpenPropertiesRef = useRef(false);

  // Map a beads issue ID → the most recently updated conversation linked to it.
  // The beads view uses this to render issue IDs as links that open the
  // associated conversation (if any).
  const beadsIssueSessionMap = useMemo(() => {
    const map = {};
    const updatedAt = {};
    for (const s of allSessions) {
      const issue = s.beads_issue;
      if (!issue) continue;
      const t = new Date(s.updated_at || 0).getTime();
      if (!(issue in map) || t >= updatedAt[issue]) {
        map[issue] = s.session_id;
        updatedAt[issue] = t;
      }
    }
    return map;
  }, [allSessions]);

  // Set of beads issue IDs that have at least one linked conversation currently
  // streaming (its agent is responding). The beads view uses this to render a
  // pulsing ring on those issues, mirroring the loading ring shown on streaming
  // conversations in the sidebar.
  const beadsIssueStreamingSet = useMemo(() => {
    const set = new Set();
    for (const s of allSessions) {
      const issue = s.beads_issue;
      if (!issue) continue;
      if (s.isStreaming) set.add(issue);
    }
    return set;
  }, [allSessions]);

  // Fetch the prompts whose `menus` list includes `beadsIssues` for a workspace
  // directory. Used by the per-issue context menu in the Beads list view. When
  // `issue` is provided, appends item_* params so the server can evaluate
  // item.*-gated enabledWhen expressions per row (mitto-o0u.1). Prompts that
  // don't reference item.* are unaffected by the extra params.
  // Per-row params sent: item_kind, item_id, item_status, item_type,
  // item_priority (when numeric), item_labels (comma-separated, when non-empty).
  //
  // enabled_context=workspace tells the server to evaluate the full enabledWhen
  // gates even without a session (mitto-gns). We also pass the current active
  // session_id when one exists so real per-session permission flags +
  // session.isChild apply (approach B); the server falls back to session-less
  // workspace defaults only when no session is active.
  const fetchBeadsPromptsForWorkspace = useCallback(
    async (workingDir, issue) => {
      if (!workingDir) return [];
      try {
        const params = {
          working_dir: workingDir,
          enabled_context: "workspace",
          session_id: activeSessionId,
        };
        if (issue) {
          params.item_kind = "beadsIssue";
          params.item_id = issue.id;
          params.item_status = issue.status;
          params.item_type = issue.issue_type;
          if (typeof issue.priority === "number") {
            params.item_priority = String(issue.priority);
          }
          if (Array.isArray(issue.labels) && issue.labels.length > 0) {
            params.item_labels = issue.labels.join(",");
          }
        }
        const res = await authFetch(endpoints.workspacePrompts.list(params));
        if (!res.ok) return [];
        const data = await res.json();
        const all = data?.prompts || [];
        return all
          .filter((p) => p && promptMenuIncludes(p, "beadsIssues"))
          .sort((a, b) => (a.name || "").localeCompare(b.name || ""));
      } catch (err) {
        console.error("Failed to fetch beads prompts for workspace:", err);
        return [];
      }
    },
    [activeSessionId],
  );

  // Fetch the prompts whose `menus` list includes `beadsList` for a workspace
  // directory. Used by the list-level prompts button in the Beads list view.
  // These prompts operate on the whole issue list (e.g. cleanup, triage) rather
  // than a single issue, so they take no item parameters.
  //
  // enabled_context=workspace asks the server to evaluate the full enabledWhen
  // gates (commandExists/dirExists/!session.isChild/tools/permissions) for these
  // prompts (mitto-gns); we pass the current active session_id when one exists so
  // real per-session flags + session.isChild apply (approach B), falling back to
  // session-less workspace defaults only when no session is active.
  const fetchBeadsListPromptsForWorkspace = useCallback(
    async (workingDir) => {
      if (!workingDir) return [];
      try {
        const res = await authFetch(
          endpoints.workspacePrompts.list({
            working_dir: workingDir,
            enabled_context: "workspace",
            session_id: activeSessionId,
          }),
        );
        if (!res.ok) return [];
        const data = await res.json();
        const all = data?.prompts || [];
        return all
          .filter(
            (p) =>
              p &&
              promptMenuIncludes(p, "beadsList") &&
              menuSatisfies(p, "beadsList"),
          )
          .sort((a, b) => (a.name || "").localeCompare(b.name || ""));
      } catch (err) {
        console.error("Failed to fetch beads list prompts for workspace:", err);
        return [];
      }
    },
    [activeSessionId],
  );

  // Run a beads prompt against a specific issue: create a new conversation in
  // the beads workspace, then seed it with the prompt text and a type-driven
  // arguments map built from the prompt's declared parameters. The backend's
  // ${VAR} substitution engine resolves each ${PARAM_NAME} in the prompt body
  // when the queued message is sent (mitto-t93). collectPromptArguments maps
  // each { name, type } parameter to the value supplied for its type (e.g.
  // beadsId → issue.id, beadsTitle → issue.title). Mirrors
  // handleSendPromptToConversation's queue delivery.
  const handleRunBeadsPrompt = useCallback(
    async (prompt, issue, opts) => {
      if (!prompt?.name || !issue || !beadsWorkingDir) return;

      // When a folder has several workspaces (e.g. Opus and Sonnet variants),
      // prefer the one marked is_default so beads launches use the intended agent.
      const beadsMatches = workspaces.filter(
        (w) => w.working_dir === beadsWorkingDir,
      );
      const ws = beadsMatches.find((w) => w.is_default) || beadsMatches[0];
      // Name the conversation after the issue (e.g. "mitto-kp7 · Fix login") so
      // it doesn't linger as "New conversation". The prompt is delivered via the
      // queue, and auto-title generation on that path only runs once the queued
      // turn completes — which is delayed for beads prompts that immediately wait
      // on user input. Setting an explicit name fixes the title right away and
      // also suppresses auto-title generation (it only runs when the name is empty).
      const convName = issue.title ? `${issue.id} · ${issue.title}` : issue.id;

      // Build the auto-filled args map and the list of parameters the menu
      // cannot supply UP-FRONT, so BOTH the periodic and one-time paths receive
      // the issue context (e.g. ${ISSUE_ID}). Previously the periodic branch
      // returned before these were computed, so periodic conversations were
      // created with no arguments and ${ISSUE_ID} was never substituted.
      const autoArgs = collectPromptArguments(prompt, {
        beadsId: issue.id,
        beadsTitle: issue.title,
      });
      const missing = getMissingPromptParameters(prompt, "beadsIssues");

      // Periodic prompts create a recurring conversation instead of a one-time seed.
      const asPeriodic = promptResolveAsPeriodic(prompt, opts?.asPeriodic);
      if (asPeriodic && onOpenPeriodicDialog) {
        // Open the periodic dialog and start the conversation with the resolved
        // arguments merged in (so ${VAR} substitution sees the issue context).
        const launchPeriodic = (args) => {
          onOpenPeriodicDialog(prompt, async (schedule) => {
            const result = await startConversationWithPrompt({
              workingDir: beadsWorkingDir,
              acpServer: ws?.acp_server,
              name: convName,
              beadsIssue: issue.id,
              prompt,
              arguments: args,
              periodic: schedule,
            });
            if (!result?.sessionId) {
              showToast({
                style: "error",
                title:
                  result?.error || "Failed to create periodic conversation",
                duration: 4000,
              });
              return;
            }
            setMainView("conversation");
            showToast({
              style: "success",
              title: `Started periodic "${prompt.name}" for ${issue.id}`,
              duration: 3000,
            });
          });
        };

        // When the menu can't auto-fill every parameter, collect the rest first,
        // then open the periodic dialog with the merged arguments.
        if (missing.length > 0 && onOpenPromptParamDialog) {
          onOpenPromptParamDialog(prompt, missing, async (userArgs) => {
            launchPeriodic({ ...autoArgs, ...userArgs });
          });
          return;
        }

        launchPeriodic(autoArgs);
        return;
      }

      // When there are parameters the menu cannot auto-fill, open the dialog so
      // the user can supply them. The dispatch happens inside the onSubmit callback.
      if (missing.length > 0 && onOpenPromptParamDialog) {
        onOpenPromptParamDialog(prompt, missing, async (userArgs) => {
          const result = await startConversationWithPrompt({
            workingDir: beadsWorkingDir,
            acpServer: ws?.acp_server,
            name: convName,
            beadsIssue: issue.id,
            prompt,
            arguments: { ...autoArgs, ...userArgs },
          });
          if (!result?.sessionId) {
            showToast({
              style: "error",
              title: result?.error || "Failed to create conversation",
              duration: 4000,
            });
            return;
          }
          setMainView("conversation");
          showToast({
            style: "success",
            title: `Started "${prompt.name}" for ${issue.id}`,
            duration: 3000,
          });
        });
        return;
      }

      // All params are auto-filled (or no params declared) — dispatch directly.
      const result = await startConversationWithPrompt({
        workingDir: beadsWorkingDir,
        acpServer: ws?.acp_server,
        name: convName,
        beadsIssue: issue.id,
        prompt,
        arguments: autoArgs,
      });
      if (!result?.sessionId) {
        showToast({
          style: "error",
          title: result?.error || "Failed to create conversation",
          duration: 4000,
        });
        return;
      }

      // startConversationWithPrompt creates + activates the new conversation;
      // switch the main view back from the beads panel so it is shown.
      setMainView("conversation");
      showToast({
        style: "success",
        title: `Started "${prompt.name}" for ${issue.id}`,
        duration: 3000,
      });
    },
    [
      beadsWorkingDir,
      workspaces,
      startConversationWithPrompt,
      showToast,
      onOpenPeriodicDialog,
      onOpenPromptParamDialog,
    ],
  );

  // Run a beads-list prompt: create a new conversation in the beads workspace,
  // seed it with the prompt text alone (these prompts operate on the whole issue
  // list and take no parameters), then switch to it. Mirrors handleRunBeadsPrompt
  // minus the per-issue context. The conversation is named after the prompt so it
  // doesn't linger as "New conversation" (this also suppresses auto-title gen).
  const handleRunBeadsListPrompt = useCallback(
    async (prompt, workingDirOverride, opts) => {
      // Allow an explicit working dir (e.g. the sidebar Tasks menu, which runs a
      // list prompt for a folder that may not be the one currently open in the
      // beads view). Falls back to the open beads working dir for in-view use.
      const wd = workingDirOverride || beadsWorkingDir;
      if (!prompt?.name || !wd) return;

      // Prefer the folder's default workspace when several share this directory.
      const beadsMatches = workspaces.filter((w) => w.working_dir === wd);
      const ws = beadsMatches.find((w) => w.is_default) || beadsMatches[0];

      // Periodic prompts create a recurring conversation instead of a one-time seed.
      const asPeriodic = promptResolveAsPeriodic(prompt, opts?.asPeriodic);
      if (asPeriodic && onOpenPeriodicDialog) {
        onOpenPeriodicDialog(prompt, async (schedule) => {
          const result = await startConversationWithPrompt({
            workingDir: wd,
            acpServer: ws?.acp_server,
            name: prompt.name,
            prompt,
            periodic: schedule,
          });
          if (!result?.sessionId) {
            showToast({
              style: "error",
              title: result?.error || "Failed to create periodic conversation",
              duration: 4000,
            });
            return;
          }
          setMainView("conversation");
          showToast({
            style: "success",
            title: `Started periodic "${prompt.name}"`,
            duration: 3000,
          });
        });
        return;
      }

      const result = await startConversationWithPrompt({
        workingDir: wd,
        acpServer: ws?.acp_server,
        name: prompt.name,
        prompt,
      });
      if (!result?.sessionId) {
        showToast({
          style: "error",
          title: result?.error || "Failed to create conversation",
          duration: 4000,
        });
        return;
      }

      // startConversationWithPrompt creates + activates the new conversation;
      // switch the main view back from the beads panel so it is shown.
      setMainView("conversation");
      showToast({
        style: "success",
        title: `Started "${prompt.name}"`,
        duration: 3000,
      });
    },
    [
      beadsWorkingDir,
      workspaces,
      startConversationWithPrompt,
      showToast,
      onOpenPeriodicDialog,
    ],
  );

  // Handle Beads button — switch main view to the beads panel for the given workspace.
  // opts.keepSidebarOpen is set when this is the Tasks auto-open triggered by
  // expanding a folder (see SessionList.handleFolderOpened); in that case the
  // mobile sidebar drawer stays open and the beads view loads underneath. A
  // direct Tasks/beads-button click leaves it unset so the drawer closes.
  const handleBeadsOpen = useCallback((workingDir, opts) => {
    // A direct Tasks/beads-button click shows the issue list, not a single
    // issue. Clear any stale single-issue selection left over from a previous
    // handleOpenBeadsIssue (the conversation properties panel's linked-issue
    // link): without this, BeadsView — which unmounts when the main view leaves
    // "beads" and so resets its applied-nonce ref — would remount with the old
    // beadsInitialIssueId and a still-non-zero beadsSelectNonce and auto-reopen
    // that same task on every Tasks click (mitto-17d follow-up).
    setBeadsInitialIssueId(null);
    beadsReturnSessionRef.current = null;
    setBeadsWorkingDir(workingDir);
    setMainView("beads");
    if (!opts?.keepSidebarOpen) {
      setShowSidebar(false);
    }
  }, []);

  // Open the beads view in "create" mode for the given workspace, switching the
  // main view if needed. The nonce bump tells BeadsView to open its create
  // panel even when it is already mounted (e.g. the user is already in the
  // beads view for this workspace).
  const handleBeadsCreate = useCallback((workingDir) => {
    if (!workingDir) return;
    // Clear any stale single-issue selection so the create panel opens cleanly
    // instead of a previously-opened issue re-selecting on remount (see
    // handleBeadsOpen for the full rationale).
    setBeadsInitialIssueId(null);
    beadsReturnSessionRef.current = null;
    setBeadsWorkingDir(workingDir);
    setBeadsCreateNonce((n) => n + 1);
    setMainView("beads");
    setShowSidebar(false);
    setShowSidePanel(false);
  }, []);

  // Open the beads view for a folder and force a refresh of its issue list.
  // Used by the sidebar Tasks menu's "Refresh" action. The nonce bump makes the
  // beads view re-fetch even when it is already showing this folder.
  const handleBeadsRefresh = useCallback((workingDir) => {
    if (!workingDir) return;
    // List-oriented action: clear any stale single-issue selection so it isn't
    // re-opened on remount (see handleBeadsOpen for the full rationale).
    setBeadsInitialIssueId(null);
    beadsReturnSessionRef.current = null;
    setBeadsWorkingDir(workingDir);
    setMainView("beads");
    setShowSidebar(false);
    setBeadsRefreshNonce((n) => n + 1);
  }, []);

  // Open the beads view for a folder and trigger its "clean up closed issues"
  // confirmation dialog. Used by the sidebar Tasks menu's "Cleanup closed"
  // action; the beads view owns the confirmation, cleanup request, and refresh.
  const handleBeadsCleanup = useCallback((workingDir) => {
    if (!workingDir) return;
    // List-oriented action: clear any stale single-issue selection so it isn't
    // re-opened on remount (see handleBeadsOpen for the full rationale).
    setBeadsInitialIssueId(null);
    beadsReturnSessionRef.current = null;
    setBeadsWorkingDir(workingDir);
    setMainView("beads");
    setShowSidebar(false);
    setBeadsCleanupNonce((n) => n + 1);
  }, []);

  // Open the standalone issue viewer for a specific issue. Two entry points use
  // it: the conversation properties panel's "Linked beads issue" link, and
  // auto-detected beads links in the conversation body. The nonce bump lets
  // BeadsIssueView re-fetch even when the same issue is opened again.
  // `originSessionId` is the conversation the link was clicked from; it is
  // remembered so closing the viewer returns there (see
  // handleReturnFromBeadsIssue). Pass `opts.reopenProperties` (true only for the
  // properties-panel link) to re-open that panel on close; auto-detected body
  // links omit it so closing just returns to the conversation.
  const handleOpenBeadsIssue = useCallback(
    (issueId, workingDir, originSessionId, opts) => {
      if (!issueId || !workingDir) return;
      beadsReturnSessionRef.current = originSessionId || null;
      beadsReturnOpenPropertiesRef.current = !!(opts && opts.reopenProperties);
      setBeadsWorkingDir(workingDir);
      setBeadsInitialIssueId(issueId);
      setBeadsSelectNonce((n) => n + 1);
      // Open as a docked overlay over the conversation rather than switching the
      // main view, so the conversation stays visible behind it. The properties
      // panel (if open) is closed so the overlay docks cleanly to the right edge.
      setBeadsIssueOpen(true);
      setShowSidebar(false);
      setShowSidePanel(false);
    },
    [],
  );

  // Return to the conversation an issue was opened from. Called by BeadsView when
  // the standalone detail panel is closed. The properties panel is re-opened only
  // when the viewer was opened from that panel's linked-issue link
  // (reopenProperties); auto-detected body links just return to the conversation
  // without popping the panel. No-op when the beads view was not entered from a
  // conversation (e.g. the Tasks button), so a normal close just leaves the user
  // on the beads list as before.
  const handleReturnFromBeadsIssue = useCallback(() => {
    const origin = beadsReturnSessionRef.current;
    const reopenProperties = beadsReturnOpenPropertiesRef.current;
    beadsReturnSessionRef.current = null;
    beadsReturnOpenPropertiesRef.current = false;
    // Close the docked overlay. The conversation was never replaced, so there is
    // no main-view navigation to undo — it is already visible behind the overlay.
    setBeadsIssueOpen(false);
    if (!origin) return;
    switchSession(origin);
    if (reopenProperties) {
      setSidePanelTab("properties");
      setShowSidePanel(true);
    }
  }, [switchSession, setSidePanelTab, setShowSidePanel]);

  return {
    beadsWorkingDir,
    beadsInitialIssueId,
    beadsSelectNonce,
    beadsCreateNonce,
    beadsRefreshNonce,
    beadsCleanupNonce,
    beadsIssueOpen,
    beadsIssueSessionMap,
    beadsIssueStreamingSet,
    fetchBeadsPromptsForWorkspace,
    fetchBeadsListPromptsForWorkspace,
    handleRunBeadsPrompt,
    handleRunBeadsListPrompt,
    handleBeadsOpen,
    handleBeadsCreate,
    handleBeadsRefresh,
    handleBeadsCleanup,
    handleOpenBeadsIssue,
    handleReturnFromBeadsIssue,
  };
}
