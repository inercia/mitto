// web/static/hooks/useWorkspacePrompts.js
// Manages workspace-prompt fetching, caching, and derived prompt lists for the
// App. Handles initial fetch on workspace change, re-fetch on session switch,
// loop 30-second refresh, visibility-based refresh, and file-watcher events
// (mitto:prompts_changed). Exposes the full prompt list, the "prompts" dropup
// subset, the loop-selector subset, and per-session / per-beads-issue fetch
// helpers.
const { useState, useEffect, useRef, useCallback, useMemo } = window.preact;

import { fetchWorkspacePromptsCached } from "../utils/index.js";
import {
  promptMenus,
  promptMenuExcludes,
  promptMenuIncludes,
  menuSatisfies,
} from "../utils/prompts.js";

/** Debounce window (ms) for the mitto:prompts_changed event fan-out. The
 * event is dispatched once per server prompts_changed message AND once per
 * mcp_tools_available message, and the server itself re-broadcasts after an
 * async MCP-tools re-verify — so a single on-disk change can fire several
 * events in quick succession. Collapsing them into one force refresh avoids
 * a burst of full-body requests (mitto-8x9). */
const PROMPTS_CHANGED_DEBOUNCE_MS = 250;

/**
 * Workspace-prompts fetch/cache hook.
 *
 * @param {Object} deps
 * @param {string|null|undefined} deps.workingDir - Working directory of the
 *   active session (sessionInfo?.working_dir). Drives workspace-change fetches.
 * @param {string|null|undefined} deps.activeSessionId - Active session id.
 *   Drives per-session re-fetches (CEL expressions vary per session).
 * @returns {{ workspacePrompts: Array, predefinedPrompts: Array,
 *   loopPrompts: Array, fetchWorkspacePrompts: Function,
 *   fetchConversationPromptsForSession: Function }}
 */
export function useWorkspacePrompts({
  workingDir,
  activeSessionId,
  showToast,
}) {
  const [workspacePrompts, setWorkspacePrompts] = useState([]); // All prompts for current workspace (merged from all sources by backend)
  const [workspacePromptsDir, setWorkspacePromptsDir] = useState(null); // Current workspace dir for prompts cache
  // Last-Modified bookkeeping for conditional requests now lives inside
  // promptsCache.js (keyed per request-params), not as hook state — keeping
  // it here made fetchWorkspacePrompts's identity change on every response,
  // which tore down and re-armed the 30s interval / visibility effects below
  // on every fetch instead of running on a stable cadence (mitto-8x9).
  const promptsChangedTimerRef = useRef(null);

  // Predefined prompts: prompts whose `menus` list includes "prompts" (the ChatInput dropup).
  // Parameters that the "prompts" menu cannot auto-fill are collected via the
  // PromptParameterDialog when the user selects such a prompt (mitto-hcf.3).
  const predefinedPrompts = useMemo(
    () => workspacePrompts.filter((p) => promptMenuIncludes(p, "prompts")),
    [workspacePrompts],
  );

  // Loop prompts: prompts shown in the LoopPromptSelector dropdown. A
  // prompt appears here if it opts into "prompts" (default dropup) OR
  // "promptsLoop" (loop-selector-specific). The union keeps existing
  // prompts available in the selector while letting authors target a prompt
  // ONLY at the loop selector via `menus: promptsLoop`.
  //
  // Exclusion: `!promptsLoop` in a prompt's `menus` field suppresses it
  // from the loop selector even when it would otherwise be included via
  // the union (e.g. a one-shot prompt with `menus: prompts, !promptsLoop`).
  // The exclusion is applied BEFORE the satisfaction check so it always wins.
  const loopPrompts = useMemo(
    () =>
      workspacePrompts.filter((p) => {
        // Explicit exclusion takes precedence over the union rule.
        if (promptMenuExcludes(p).has("promptsLoop")) return false;
        const menus = promptMenus(p);
        return (
          (menus.includes("prompts") && menuSatisfies(p, "prompts")) ||
          (menus.includes("promptsLoop") && menuSatisfies(p, "promptsLoop"))
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
    async (session, workingDir, menus = ["conversation"]) => {
      const sessionId = session?.session_id;
      const dir = workingDir || session?.working_dir;
      if (!sessionId || !dir) return [];
      try {
        const data = await fetchWorkspacePromptsCached({
          working_dir: dir,
          session_id: sessionId,
        });
        const all = data?.prompts || [];
        // Keep prompts that opt into ANY of the requested menus. Parameters that
        // a menu cannot auto-fill are collected via the PromptParameterDialog
        // when the user selects such a prompt (mitto-hcf.3). No menuSatisfies
        // gate — all params can be user-filled.
        return all.filter(
          (p) => p && menus.some((m) => promptMenuIncludes(p, m)),
        );
      } catch (err) {
        console.error("Failed to fetch conversation prompts for session:", err);
        return [];
      }
    },
    [],
  );

  // Fetch workspace prompts via the shared promptsCache (mitto-8x9): TTL +
  // in-flight dedup collapse bursts from this hook's several triggers
  // (workspace change, session switch, 30s interval, visibility, file
  // watcher) and revalidates with If-Modified-Since once the TTL expires.
  const fetchWorkspacePrompts = useCallback(
    async (workingDir, forceRefresh = false) => {
      if (!workingDir) return;

      try {
        const data = await fetchWorkspacePromptsCached(
          { working_dir: workingDir, session_id: activeSessionId },
          { force: forceRefresh },
        );

        setWorkspacePrompts(data.prompts);
        setWorkspacePromptsDir(workingDir);

        // One-time notice when the backend migrated legacy .md prompt files to
        // the new .prompt.yaml format. The backend reports this only once per
        // migration (afterwards the .prompt.yaml already exists), so no extra
        // client-side de-duplication is needed.
        const migrated = data.migrated;
        if (showToast && Array.isArray(migrated) && migrated.length > 0) {
          const names = migrated.join(", ");
          showToast({
            style: "info",
            title: `Migrated ${migrated.length} prompt${migrated.length === 1 ? "" : "s"} to the new format`,
            message: `New .prompt.yaml files were written for: ${names}. You can remove the old .md files when ready.`,
          });
        }
      } catch (err) {
        console.error("Failed to fetch workspace prompts:", err);
        // Only clear prompts on error if this is a new workspace
        if (workingDir !== workspacePromptsDir) {
          setWorkspacePrompts([]);
          setWorkspacePromptsDir(workingDir);
        }
      }
    },
    [workspacePromptsDir, activeSessionId, showToast],
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

  // Loop refresh of workspace prompts (every 30 seconds)
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
  // This event is dispatched by handleGlobalEvent when receiving prompts_changed from WebSocket.
  // It fans out on more than one server message (prompts_changed AND
  // mcp_tools_available, plus a re-broadcast after an async MCP-tools
  // re-verify), so a single on-disk change can fire several events in quick
  // succession. Trailing-edge debounce collapses a burst into one force
  // refresh instead of one per event (mitto-8x9). Force is kept (not the TTL
  // cache) so a genuine change is never masked by a stale cached response.
  useEffect(() => {
    const handlePromptsChanged = (event) => {
      console.log("[prompts] File watcher detected changes:", event.detail);

      if (!workingDir) return;
      if (promptsChangedTimerRef.current) {
        clearTimeout(promptsChangedTimerRef.current);
      }
      promptsChangedTimerRef.current = setTimeout(() => {
        promptsChangedTimerRef.current = null;
        fetchWorkspacePrompts(workingDir, true);
      }, PROMPTS_CHANGED_DEBOUNCE_MS);
    };

    window.addEventListener("mitto:prompts_changed", handlePromptsChanged);
    return () => {
      window.removeEventListener("mitto:prompts_changed", handlePromptsChanged);
      if (promptsChangedTimerRef.current) {
        clearTimeout(promptsChangedTimerRef.current);
        promptsChangedTimerRef.current = null;
      }
    };
  }, [workingDir, fetchWorkspacePrompts]);

  return {
    workspacePrompts,
    predefinedPrompts,
    loopPrompts,
    fetchWorkspacePrompts,
    fetchConversationPromptsForSession,
  };
}
