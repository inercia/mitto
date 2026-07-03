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
 */
export function useBackgroundNotifications({
  showToast,
  focusSession,
  activeSessionId,
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

      // Show in-app toast. When the notification carries a session_id, clicking
      // it brings that conversation into focus (leaving the beads view if open).
      showToast({
        style: data.style || "info",
        title: data.title || "Notification",
        message: data.message || "",
        duration: data.style === "error" ? 8000 : 5000,
        onClick: data.session_id ? () => focusSession(data.session_id) : null,
      });
    };
    window.addEventListener("mitto:notification", handleNotification);
    return () => {
      window.removeEventListener("mitto:notification", handleNotification);
    };
  }, [showToast, focusSession]);

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
