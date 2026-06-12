// web/static/hooks/useBeadsIntegration.js
// Manages the entire beads-view cluster for App: which workspace folder is
// currently shown in the beads panel, issue auto-selection state, the
// beadsIssueSessionMap derived value, prompt fetching helpers, and the
// handlers for opening the beads panel, running per-issue / list prompts,
// and navigating from a conversation's linked-issue link into the beads view.
const { useState, useCallback, useMemo } = window.preact;

import { apiUrl, authFetch, secureFetch } from "../utils/index.js";
import { promptMenus, menuSatisfiesRequires } from "../utils/prompts.js";

/**
 * Beads-view integration hook.
 *
 * @param {Object} deps
 * @param {Array}    deps.allSessions      - All sessions (active + stored); drives beadsIssueSessionMap.
 * @param {Array}    deps.workspaces       - All configured workspaces; used to pick is_default on run.
 * @param {Function} deps.newSession       - Creates a new conversation (from useWebSocket).
 * @param {Function} deps.showToast        - Toast dispatcher.
 * @param {Function} deps.setMainView      - Switches main view to "beads" / "conversation".
 * @param {Function} deps.setShowSidebar   - Closes sidebar overlay (mobile).
 * @param {Function} deps.setShowSidePanel - Closes side panel (used in handleOpenBeadsIssue).
 */
export function useBeadsIntegration({
  allSessions,
  workspaces,
  newSession,
  showToast,
  setMainView,
  setShowSidebar,
  setShowSidePanel,
}) {
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

  // Fetch the prompts whose `menus` list includes `beadsIssues` for a workspace
  // directory. Used by the per-issue context menu in the Beads list view. There
  // is no specific conversation here, so `enabledWhen` is evaluated without a
  // session_id; we only keep the prompts that opt into the beads menu via
  // `menus`.
  const fetchBeadsPromptsForWorkspace = useCallback(async (workingDir) => {
    if (!workingDir) return [];
    try {
      const res = await authFetch(
        apiUrl(`/api/workspace-prompts?dir=${encodeURIComponent(workingDir)}`),
      );
      if (!res.ok) return [];
      const data = await res.json();
      const all = data?.prompts || [];
      return all
        .filter(
          (p) =>
            p &&
            promptMenus(p).includes("beadsIssues") &&
            menuSatisfiesRequires(p, "beadsIssues"),
        )
        .sort((a, b) => (a.name || "").localeCompare(b.name || ""));
    } catch (err) {
      console.error("Failed to fetch beads prompts for workspace:", err);
      return [];
    }
  }, []);

  // Fetch the prompts whose `menus` list includes `beadsList` for a workspace
  // directory. Used by the list-level prompts button in the Beads list view.
  // These prompts operate on the whole issue list (e.g. cleanup, triage) rather
  // than a single issue, so they take no parameters. There is no specific
  // conversation here, so `enabledWhen` is evaluated without a session_id; we
  // only keep the prompts that opt into the beads-list menu via `menus`.
  const fetchBeadsListPromptsForWorkspace = useCallback(async (workingDir) => {
    if (!workingDir) return [];
    try {
      const res = await authFetch(
        apiUrl(`/api/workspace-prompts?dir=${encodeURIComponent(workingDir)}`),
      );
      if (!res.ok) return [];
      const data = await res.json();
      const all = data?.prompts || [];
      return all
        .filter(
          (p) =>
            p &&
            promptMenus(p).includes("beadsList") &&
            menuSatisfiesRequires(p, "beadsList"),
        )
        .sort((a, b) => (a.name || "").localeCompare(b.name || ""));
    } catch (err) {
      console.error("Failed to fetch beads list prompts for workspace:", err);
      return [];
    }
  }, []);

  // Run a beads prompt against a specific issue: create a new conversation in
  // the beads workspace, then seed it with the prompt text plus a single
  // `ISSUE_ID` argument. The backend's ${VAR} substitution engine resolves
  // `${ISSUE_ID}` in the prompt body when the queued message is sent (see the
  // queue `arguments` support from mitto-t93); the prompt itself loads any
  // further detail via `bd show ${ISSUE_ID}`. Mirrors handleSendPromptToConversation's
  // queue delivery (the queue runs the message once the new conversation is idle).
  const handleRunBeadsPrompt = useCallback(
    async (prompt, issue) => {
      const text = prompt?.prompt;
      if (!text || !issue || !beadsWorkingDir) return;

      // When a folder has several workspaces (e.g. Opus and Sonnet variants),
      // prefer the one marked is_default so beads launches use the intended agent.
      const beadsMatches = workspaces.filter((w) => w.working_dir === beadsWorkingDir);
      const ws = beadsMatches.find((w) => w.is_default) || beadsMatches[0];
      // Name the conversation after the issue (e.g. "mitto-kp7 · Fix login") so
      // it doesn't linger as "New conversation". The prompt is delivered via the
      // queue, and auto-title generation on that path only runs once the queued
      // turn completes — which is delayed for beads prompts that immediately wait
      // on user input. Setting an explicit name fixes the title right away and
      // also suppresses auto-title generation (it only runs when the name is empty).
      const convName = issue.title ? `${issue.id} · ${issue.title}` : issue.id;
      const result = await newSession({
        workingDir: beadsWorkingDir,
        acpServer: ws?.acp_server,
        name: convName,
        beadsIssue: issue.id,
      });
      if (!result?.sessionId) {
        showToast({
          style: "error",
          title: result?.error || "Failed to create conversation",
          duration: 4000,
        });
        return;
      }

      // Seed the new conversation with the prompt text and a single `ISSUE_ID`
      // argument. The backend substitutes `${ISSUE_ID}` into the prompt body
      // when the message is sent; the prompt loads any further detail itself
      // via `bd show ${ISSUE_ID}`.
      try {
        await secureFetch(apiUrl(`/api/sessions/${result.sessionId}/queue`), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            message: text,
            arguments: { ISSUE_ID: issue.id },
          }),
        });
      } catch (err) {
        console.error("Failed to seed beads conversation:", err);
      }

      // newSession already activates the new conversation; switch the main view
      // back from the beads panel so the new conversation is shown.
      setMainView("conversation");
      showToast({
        style: "success",
        title: `Started "${prompt.name}" for ${issue.id}`,
        duration: 3000,
      });
    },
    [beadsWorkingDir, workspaces, newSession, showToast],
  );

  // Run a beads-list prompt: create a new conversation in the beads workspace,
  // seed it with the prompt text alone (these prompts operate on the whole issue
  // list and take no parameters), then switch to it. Mirrors handleRunBeadsPrompt
  // minus the per-issue context. The conversation is named after the prompt so it
  // doesn't linger as "New conversation" (this also suppresses auto-title gen).
  const handleRunBeadsListPrompt = useCallback(
    async (prompt) => {
      const text = prompt?.prompt;
      if (!text || !beadsWorkingDir) return;

      // Prefer the folder's default workspace when several share this directory.
      const beadsMatches = workspaces.filter((w) => w.working_dir === beadsWorkingDir);
      const ws = beadsMatches.find((w) => w.is_default) || beadsMatches[0];
      const result = await newSession({
        workingDir: beadsWorkingDir,
        acpServer: ws?.acp_server,
        name: prompt.name,
      });
      if (!result?.sessionId) {
        showToast({
          style: "error",
          title: result?.error || "Failed to create conversation",
          duration: 4000,
        });
        return;
      }

      try {
        await secureFetch(apiUrl(`/api/sessions/${result.sessionId}/queue`), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ message: text }),
        });
      } catch (err) {
        console.error("Failed to seed beads list conversation:", err);
      }

      // newSession already activates the new conversation; switch the main view
      // back from the beads panel so the new conversation is shown.
      setMainView("conversation");
      showToast({
        style: "success",
        title: `Started "${prompt.name}"`,
        duration: 3000,
      });
    },
    [beadsWorkingDir, workspaces, newSession, showToast],
  );

  // Handle Beads button — switch main view to the beads panel for the given workspace.
  // opts.keepSidebarOpen is set when this is the Tasks auto-open triggered by
  // expanding a folder (see SessionList.handleFolderOpened); in that case the
  // mobile sidebar drawer stays open and the beads view loads underneath. A
  // direct Tasks/beads-button click leaves it unset so the drawer closes.
  const handleBeadsOpen = useCallback((workingDir, opts) => {
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
    setBeadsWorkingDir(workingDir);
    setBeadsCreateNonce((n) => n + 1);
    setMainView("beads");
    setShowSidebar(false);
    setShowSidePanel(false);
  }, []);

  // Open the beads view focused on a specific issue (used by the conversation
  // properties panel's linked-issue link). The nonce bump lets BeadsView
  // re-select even when the same issue is opened again.
  const handleOpenBeadsIssue = useCallback((issueId, workingDir) => {
    if (!issueId || !workingDir) return;
    setBeadsWorkingDir(workingDir);
    setBeadsInitialIssueId(issueId);
    setBeadsSelectNonce((n) => n + 1);
    setMainView("beads");
    setShowSidebar(false);
    setShowSidePanel(false);
  }, []);

  return {
    beadsWorkingDir,
    beadsInitialIssueId,
    beadsSelectNonce,
    beadsCreateNonce,
    beadsIssueSessionMap,
    fetchBeadsPromptsForWorkspace,
    fetchBeadsListPromptsForWorkspace,
    handleRunBeadsPrompt,
    handleRunBeadsListPrompt,
    handleBeadsOpen,
    handleBeadsCreate,
    handleOpenBeadsIssue,
  };
}
