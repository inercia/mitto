// web/static/hooks/useWorkspacePrompts.js
// Manages workspace-prompt fetching, caching, and derived prompt lists for the
// App. Handles initial fetch on workspace change, re-fetch on session switch,
// periodic 30-second refresh, visibility-based refresh, and file-watcher events
// (mitto:prompts_changed). Exposes the full prompt list, the "prompts" dropup
// subset, the periodic-selector subset, and per-session / per-beads-issue fetch
// helpers.
const { useState, useEffect, useCallback, useMemo } = window.preact;

import { authFetch, endpoints } from "../utils/index.js";
import { promptMenus, menuSatisfies } from "../utils/prompts.js";

/**
 * Workspace-prompts fetch/cache hook.
 *
 * @param {Object} deps
 * @param {string|null|undefined} deps.workingDir - Working directory of the
 *   active session (sessionInfo?.working_dir). Drives workspace-change fetches.
 * @param {string|null|undefined} deps.activeSessionId - Active session id.
 *   Drives per-session re-fetches (CEL expressions vary per session).
 * @returns {{ workspacePrompts: Array, predefinedPrompts: Array,
 *   periodicPrompts: Array, fetchWorkspacePrompts: Function,
 *   fetchConversationPromptsForSession: Function }}
 */
export function useWorkspacePrompts({
  workingDir,
  activeSessionId,
  showToast,
}) {
  const [workspacePrompts, setWorkspacePrompts] = useState([]); // All prompts for current workspace (merged from all sources by backend)
  const [workspacePromptsDir, setWorkspacePromptsDir] = useState(null); // Current workspace dir for prompts cache
  const [workspacePromptsLastModified, setWorkspacePromptsLastModified] =
    useState(null); // Last-Modified header for conditional requests

  // Predefined prompts: prompts whose `menus` list includes "prompts" (the ChatInput dropup).
  // Parameters that the "prompts" menu cannot auto-fill are collected via the
  // PromptParameterDialog when the user selects such a prompt (mitto-hcf.3).
  const predefinedPrompts = useMemo(
    () => workspacePrompts.filter((p) => promptMenus(p).includes("prompts")),
    [workspacePrompts],
  );

  // Periodic prompts: prompts shown in the PeriodicPromptSelector dropdown. A
  // prompt appears here if it opts into "prompts" (default dropup) OR
  // "promptsPeriodic" (periodic-selector-specific). The union keeps existing
  // prompts available in the selector while letting authors target a prompt
  // ONLY at the periodic selector via `menus: promptsPeriodic`.
  const periodicPrompts = useMemo(
    () =>
      workspacePrompts.filter((p) => {
        const menus = promptMenus(p);
        return (
          (menus.includes("prompts") && menuSatisfies(p, "prompts")) ||
          (menus.includes("promptsPeriodic") &&
            menuSatisfies(p, "promptsPeriodic"))
        );
      }),
    [workspacePrompts],
  );

  // Fetch the prompts whose `menus` list includes `conversation` for a SPECIFIC
  // conversation, evaluating each prompt's `enabledWhen` against that
  // conversation's own context (child status, children, permissions, tools).
  //
  // The context menu must reflect the conversation being right-clicked, not the
  // active session, so we cannot reuse the active-session `workspacePrompts`
  // list. Instead we query /api/workspace-prompts with the target session_id so
  // the backend evaluates `enabledWhen` for that conversation, then keep only the
  // prompts that opt into the conversation menu via `menus`.
  const fetchConversationPromptsForSession = useCallback(
    async (session, workingDir) => {
      const sessionId = session?.session_id;
      const dir = workingDir || session?.working_dir;
      if (!sessionId || !dir) return [];
      try {
        const res = await authFetch(
          endpoints.workspacePrompts.list({
            working_dir: dir,
            session_id: sessionId,
          }),
        );
        if (!res.ok) return [];
        const data = await res.json();
        const all = data?.prompts || [];
        // Parameters that the "conversation" menu cannot auto-fill are collected
        // via the PromptParameterDialog when the user selects such a prompt
        // (mitto-hcf.3). No menuSatisfies gate — all params can be user-filled.
        return all.filter((p) => p && promptMenus(p).includes("conversation"));
      } catch (err) {
        console.error("Failed to fetch conversation prompts for session:", err);
        return [];
      }
    },
    [],
  );

  // Fetch workspace prompts with conditional request support (If-Modified-Since)
  // This enables efficient periodic refresh without transferring data if unchanged
  const fetchWorkspacePrompts = useCallback(
    async (workingDir, forceRefresh = false) => {
      if (!workingDir) return;

      const headers = {};
      // Use If-Modified-Since for conditional requests (unless forcing refresh)
      if (
        !forceRefresh &&
        workspacePromptsLastModified &&
        workingDir === workspacePromptsDir
      ) {
        headers["If-Modified-Since"] = workspacePromptsLastModified;
      }

      try {
        const res = await authFetch(
          endpoints.workspacePrompts.list({
            working_dir: workingDir,
            session_id: activeSessionId,
          }),
          { headers },
        );

        // 304 Not Modified - prompts haven't changed
        if (res.status === 304) {
          return;
        }

        if (!res.ok) {
          throw new Error(`HTTP ${res.status}`);
        }

        const data = await res.json();
        setWorkspacePrompts(data?.prompts || []);
        setWorkspacePromptsDir(workingDir);

        // One-time notice when the backend migrated legacy .md prompt files to
        // the new .prompt.yaml format. The backend reports this only once per
        // migration (afterwards the .prompt.yaml already exists), so no extra
        // client-side de-duplication is needed.
        const migrated = data?.migrated;
        if (showToast && Array.isArray(migrated) && migrated.length > 0) {
          const names = migrated.join(", ");
          showToast({
            style: "info",
            title: `Migrated ${migrated.length} prompt${migrated.length === 1 ? "" : "s"} to the new format`,
            message: `New .prompt.yaml files were written for: ${names}. You can remove the old .md files when ready.`,
          });
        }

        // Store Last-Modified header for future conditional requests
        const lastModified = res.headers.get("Last-Modified");
        setWorkspacePromptsLastModified(lastModified);
      } catch (err) {
        console.error("Failed to fetch workspace prompts:", err);
        // Only clear prompts on error if this is a new workspace
        if (workingDir !== workspacePromptsDir) {
          setWorkspacePrompts([]);
          setWorkspacePromptsDir(workingDir);
          setWorkspacePromptsLastModified(null);
        }
      }
    },
    [
      workspacePromptsDir,
      workspacePromptsLastModified,
      activeSessionId,
      showToast,
    ],
  );

  // Fetch workspace prompts when the active session's working_dir changes
  useEffect(() => {
    if (!workingDir) return;

    // Always fetch if workspace changed
    if (workingDir !== workspacePromptsDir) {
      fetchWorkspacePrompts(workingDir, true); // Force refresh for new workspace
    }
  }, [workingDir, workspacePromptsDir, fetchWorkspacePrompts]);

  // Re-fetch prompts when active session changes (session switch in same workspace)
  // CEL expressions like session.isChild and parent.exists vary per session,
  // so the filtered prompt list may differ even for the same workspace files.
  useEffect(() => {
    if (!workingDir || !activeSessionId) return;
    // Only re-fetch if we already have prompts for this workspace
    // (initial fetch is handled by the working_dir change effect above)
    if (workingDir === workspacePromptsDir) {
      fetchWorkspacePrompts(workingDir, true); // Force to bypass conditional request (304)
    }
  }, [activeSessionId]); // intentionally omit workingDir/workspacePromptsDir/fetchWorkspacePrompts from deps

  // Periodic refresh of workspace prompts (every 30 seconds)
  // Uses conditional requests to avoid unnecessary data transfer
  useEffect(() => {
    if (!workingDir) return;

    const intervalId = setInterval(() => {
      fetchWorkspacePrompts(workingDir, false); // Conditional request
    }, 30000); // 30 seconds

    return () => clearInterval(intervalId);
  }, [workingDir, fetchWorkspacePrompts]);

  // Refresh workspace prompts when app becomes visible (tab switch, phone wake)
  useEffect(() => {
    const handleVisibilityChange = () => {
      if (document.visibilityState === "visible" && workingDir) {
        // Small delay to avoid racing with other visibility handlers
        setTimeout(() => {
          fetchWorkspacePrompts(workingDir, false);
        }, 500);
      }
    };

    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () =>
      document.removeEventListener("visibilitychange", handleVisibilityChange);
  }, [workingDir, fetchWorkspacePrompts]);

  // Refresh prompts when file watcher detects changes (mitto:prompts_changed event)
  // This event is dispatched by handleGlobalEvent when receiving prompts_changed from WebSocket
  useEffect(() => {
    const handlePromptsChanged = (event) => {
      console.log("[prompts] File watcher detected changes:", event.detail);

      // Refresh workspace prompts (force refresh to skip conditional request)
      // The backend merges all sources (global + server + workspace), so this is all we need.
      if (workingDir) {
        fetchWorkspacePrompts(workingDir, true);
      }
    };

    window.addEventListener("mitto:prompts_changed", handlePromptsChanged);
    return () =>
      window.removeEventListener("mitto:prompts_changed", handlePromptsChanged);
  }, [workingDir, fetchWorkspacePrompts]);

  return {
    workspacePrompts,
    predefinedPrompts,
    periodicPrompts,
    fetchWorkspacePrompts,
    fetchConversationPromptsForSession,
  };
}
