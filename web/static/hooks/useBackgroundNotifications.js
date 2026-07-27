// web/static/hooks/useBackgroundNotifications.js
// Registers the App's background event-listener effects that surface toasts and
// native notifications for server-pushed events (runner fallback, memory recycle,
// ACP start/permanent errors, hook failures, and generic notifications), plus the
// cleanup of native notifications for the active conversation on focus.
// Side-effect only: returns nothing.
const { useEffect } = window.preact;

import { playAgentCompletedSound } from "../utils/index.js";

/**
 * Wires the background notification window-event listeners.
 *
 * @param {Object} deps
 * @param {Function} deps.showToast - Toast dispatcher from useToast.
 * @param {Function} deps.focusSession - Brings a conversation into focus by id.
 * @param {string|null} deps.activeSessionId - Currently focused conversation id.
 * @param {string|null} deps.activeWorkspaceUUID - Currently viewed workspace UUID; when a
 *   notification carries a workspace_uuid that does not match, the toast is skipped
 *   so workspace-scoped notifications from mitto_workspace_ui_notify (mitto-6bn)
 *   only appear for users viewing the target workspace. Notifications without a
 *   workspace_uuid always show (backward compatible).
 */
export function useBackgroundNotifications({
  showToast,
  focusSession,
  activeSessionId,
  activeWorkspaceUUID,
}) {
  // Listen for runner fallback events
  useEffect(() => {
    const handleRunnerFallback = (event) => {
      const data = event.detail;
      if (data) {
        showToast({
          style: "warning",
          title: "Runner Not Supported",
          message: `Requested: ${data.requested_type} — Using: ${data.fallback_type} (no restrictions). ${data.reason || ""}`,
          duration: 10000,
        });
      }
    };
    window.addEventListener("mitto:runner_fallback", handleRunnerFallback);
    return () => {
      window.removeEventListener("mitto:runner_fallback", handleRunnerFallback);
    };
  }, [showToast]);

  // Listen for memory-recycle events (GC Tier 4 restarted a bloated idle agent)
  useEffect(() => {
    const handleMemoryRecycled = (event) => {
      const data = event.detail;
      if (!data) return;
      const toMB = (b) => Math.round((Number(b) || 0) / (1024 * 1024));
      const name =
        data.workspace_name ||
        (data.working_dir ? data.working_dir.split("/").pop() : "") ||
        "a workspace";
      const used = toMB(data.rss_bytes);
      const limit = toMB(data.threshold_bytes);
      const count = data.session_count || 0;
      const convs = count === 1 ? "conversation" : "conversations";
      showToast({
        style: "info",
        title: `Memory reclaimed: ${name}`,
        message: `Idle agent using ${used} MB (limit ${limit} MB) was restarted to free memory. ${count} ${convs} will resume automatically when reopened.`,
        duration: 10000,
      });
    };
    window.addEventListener("mitto:memory_recycled", handleMemoryRecycled);
    return () => {
      window.removeEventListener("mitto:memory_recycled", handleMemoryRecycled);
    };
  }, [showToast]);

  // Listen for MCP-init progress events (mitto-8ul.1): agent is blocked waiting
  // for MCP servers to initialize on cold start. Informational, low-priority toast.
  useEffect(() => {
    const handleMCPInitializing = (event) => {
      const data = event.detail;
      if (!data) return;
      const name =
        data.workspace_name ||
        (data.working_dir ? data.working_dir.split("/").pop() : "") ||
        "a workspace";
      showToast({
        style: "info",
        title: `Starting MCP servers: ${name}`,
        message:
          "The agent is initializing its MCP servers. First response may take up to a few minutes.",
        duration: 8000,
      });
    };
    window.addEventListener("mitto:mcp_initializing", handleMCPInitializing);
    return () => {
      window.removeEventListener(
        "mitto:mcp_initializing",
        handleMCPInitializing,
      );
    };
  }, [showToast]);

  // Listen for MCP-init timeout events (mitto-8ul.1): the agent gave up on its
  // MCP-init wait and aborted the pending session/new. Persistent error toast.
  useEffect(() => {
    const handleMCPInitTimedOut = (event) => {
      const data = event.detail;
      if (!data) return;
      const name =
        data.workspace_name ||
        (data.working_dir ? data.working_dir.split("/").pop() : "") ||
        "a workspace";
      showToast({
        style: "error",
        title: `MCP initialization timed out: ${name}`,
        message:
          "The agent could not start all configured MCP servers. Check that every MCP server is reachable or remove it from the workspace configuration.",
        duration: 30000,
      });
    };
    window.addEventListener("mitto:mcp_init_timed_out", handleMCPInitTimedOut);
    return () => {
      window.removeEventListener(
        "mitto:mcp_init_timed_out",
        handleMCPInitTimedOut,
      );
    };
  }, [showToast]);

  // Listen for prewarm pin alert events (mitto-mw0): the adaptive pre-warming
  // controller pinned a workspace due to slow/broken MCP init, OR force-expired
  // a stuck pin after its max-pin-duration cap elapsed. Warning toast.
  useEffect(() => {
    const handlePrewarmPinAlert = (event) => {
      const data = event.detail;
      if (!data) return;
      const name =
        data.workspace_name ||
        (data.working_dir ? data.working_dir.split("/").pop() : "") ||
        "a workspace";
      const reasonSuffix = data.reason ? ` ${data.reason}` : "";
      if (data.expired) {
        showToast({
          style: "warning",
          title: `MCP pin released: ${name}`,
          message: `The warm pin was released after the max-pin-duration cap because the MCP servers stayed slow or unavailable — check the workspace's MCP configuration.${reasonSuffix}`,
        });
      } else {
        showToast({
          style: "warning",
          title: `Slow MCP workspace pinned: ${name}`,
          message: `This workspace's MCP servers were slow to start, so a warm session was pinned to speed up the first prompt.${reasonSuffix}`,
        });
      }
    };
    window.addEventListener("mitto:prewarm_pin_alert", handlePrewarmPinAlert);
    return () => {
      window.removeEventListener(
        "mitto:prewarm_pin_alert",
        handlePrewarmPinAlert,
      );
    };
  }, [showToast]);

  // Listen for ACP start failed events
  useEffect(() => {
    const handleAcpStartFailed = (event) => {
      const data = event.detail;
      if (data) {
        showToast({
          style: "error",
          title: "AI Agent Failed to Start",
          message:
            "Try switching to the session and sending a message to retry.",
          duration: 10000,
          onClick: data.session_id ? () => focusSession(data.session_id) : null,
        });
      }
    };
    window.addEventListener("mitto:acp_start_failed", handleAcpStartFailed);
    return () => {
      window.removeEventListener(
        "mitto:acp_start_failed",
        handleAcpStartFailed,
      );
    };
  }, [showToast, focusSession]);

  // Listen for ACP permanent error events (non-retryable errors with guidance)
  useEffect(() => {
    const handleAcpPermanentError = (event) => {
      const data = event.detail;
      if (data) {
        const detail = [
          data.user_guidance,
          data.command ? `Command: ${data.command}` : "",
        ]
          .filter(Boolean)
          .join(" — ");
        showToast({
          style: "error",
          title: data.user_message || "ACP Server Error",
          message: detail,
          duration: 30000,
        });
      }
    };
    window.addEventListener(
      "mitto:acp_error_permanent",
      handleAcpPermanentError,
    );
    return () => {
      window.removeEventListener(
        "mitto:acp_error_permanent",
        handleAcpPermanentError,
      );
    };
  }, [showToast]);

  // Listen for hook failed events
  useEffect(() => {
    const handleHookFailed = (event) => {
      const data = event.detail;
      if (data) {
        const exitPart =
          data.exit_code !== undefined ? ` (exit code ${data.exit_code})` : "";
        showToast({
          style: "warning",
          title: `Hook Failed: ${data.name || "up"}${exitPart}`,
          message: data.error || "",
          duration: 10000,
        });
      }
    };
    window.addEventListener("mitto:hook_failed", handleHookFailed);
    return () => {
      window.removeEventListener("mitto:hook_failed", handleHookFailed);
    };
  }, [showToast]);

  // Listen for mitto:notification events dispatched by useWebSocket
  useEffect(() => {
    const handleNotification = (event) => {
      const data = event.detail;
      if (!data) return;

      // Filter workspace-scoped notifications (mitto-6bn): a notification
      // carrying workspace_uuid is only shown in clients currently viewing
      // that workspace. Notifications without workspace_uuid always show
      // (backward compatible with pre-mitto-6bn callers).
      if (
        data.workspace_uuid &&
        activeWorkspaceUUID &&
        data.workspace_uuid !== activeWorkspaceUUID
      ) {
        return;
      }

      // Play sound if requested (reuse the agent-completed sound)
      if (data.sound && window.mittoAgentCompletedSoundEnabled) {
        playAgentCompletedSound();
      }

      // Show native notification if requested and available (macOS app only)
      if (
        data.native &&
        window.mittoNativeNotificationsEnabled &&
        typeof window.mittoShowNativeNotification === "function"
      ) {
        window.mittoShowNativeNotification(
          data.title || "Notification",
          data.message || "",
          data.session_id || "",
          data.sticky || false,
        );
      }

      // Show in-app toast. Click precedence:
      //  1. beads_issue → open the beads viewer for that issue (takes priority
      //     because a bead-scoped notification is implicitly about the bead,
      //     not the conversation). Guarded on window.mittoOpenBeadsIssue so
      //     old/stripped frontends no-op instead of throwing.
      //  2. session_id → focus that conversation (pre-existing behavior).
      //  3. otherwise non-clickable.
      let onClick = null;
      if (data.beads_issue) {
        const beadsIssue = data.beads_issue;
        onClick = () => {
          if (typeof window.mittoOpenBeadsIssue === "function") {
            window.mittoOpenBeadsIssue(beadsIssue);
          }
        };
      } else if (data.session_id) {
        onClick = () => focusSession(data.session_id);
      }
      showToast({
        style: data.style || "info",
        title: data.title || "Notification",
        message: data.message || "",
        duration: data.style === "error" ? 8000 : 5000,
        onClick,
      });
    };
    window.addEventListener("mitto:notification", handleNotification);
    return () => {
      window.removeEventListener("mitto:notification", handleNotification);
    };
  }, [showToast, focusSession, activeWorkspaceUUID]);

  // Remove native notifications for the active session when switching to it
  // This prevents stale notifications from lingering in Notification Center
  useEffect(() => {
    if (
      activeSessionId &&
      typeof window.mittoRemoveNotificationsForSession === "function"
    ) {
      window.mittoRemoveNotificationsForSession(activeSessionId);
    }
  }, [activeSessionId]);
}
