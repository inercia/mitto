// web/static/hooks/useBackgroundNotifications.js
// Registers the App's background event-listener effects that surface toasts and
// native notifications for server-pushed events (runner fallback, memory recycle,
// ACP start/permanent errors, hook failures, and generic notifications), plus the
// cleanup of native notifications for the active conversation on focus.
// Side-effect only: returns nothing.
const { useEffect, useRef } = window.preact;

import { playAgentCompletedSound } from "../utils/index.js";

// Minimum interval between "Slack journal rejecting" toasts for the same
// Slack app (mitto-mfd). The journal re-emits its connection-status feed on
// every rejected event, and the mitto-d8y incident produced ~52k rejections
// in 13h, so this keeps a sustained rejection storm to one toast per window
// instead of flooding the UI.
const SLACK_JOURNAL_TOAST_THROTTLE_MS = 5 * 60 * 1000;

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

  // Listen for health-recycle events (GC Tier 5/6 restarted a wedged agent
  // process that stopped completing session/new or session/load RPCs, mitto-aoo)
  useEffect(() => {
    const handleHealthRecycled = (event) => {
      const data = event.detail;
      if (!data) return;
      const name =
        data.workspace_name ||
        (data.working_dir ? data.working_dir.split("/").pop() : "") ||
        "a workspace";
      const count = data.session_count || 0;
      const convs = count === 1 ? "conversation" : "conversations";
      showToast({
        style: "warning",
        title: `Agent restarted: ${name}`,
        message: `A stuck agent process was automatically restarted. ${count} ${convs} will resume automatically when reopened.`,
        duration: 10000,
      });
    };
    window.addEventListener("mitto:agent_recycled", handleHealthRecycled);
    return () => {
      window.removeEventListener("mitto:agent_recycled", handleHealthRecycled);
    };
  }, [showToast]);

  // Listen for degraded-state events (GC Tier 5 detected — or cleared — a
  // saturated / MCP-init-gated / MCP-init-wedged shared ACP process, fired
  // BEFORE an eventual health recycle, mitto-13n.3). This is the pre-recycle
  // signal: a degraded process that stays busy (or not yet idle) can go
  // unnoticed for a long time otherwise, since the health-recycle toast only
  // fires once the process is actually stopped.
  useEffect(() => {
    const handleAgentDegraded = (event) => {
      const data = event.detail;
      if (!data) return;
      const name =
        data.workspace_name ||
        (data.working_dir ? data.working_dir.split("/").pop() : "") ||
        "a workspace";
      if (data.degraded) {
        showToast({
          style: "warning",
          title: `Agent degraded: ${name}`,
          message:
            "The agent process is stuck or slow to respond. Background features (titles, suggestions) are paused; it will be restarted automatically once idle.",
          duration: 10000,
        });
      } else {
        showToast({
          style: "info",
          title: `Agent recovered: ${name}`,
          message: "The agent process is responding normally again.",
          duration: 6000,
        });
      }
    };
    window.addEventListener("mitto:agent_degraded", handleAgentDegraded);
    return () => {
      window.removeEventListener("mitto:agent_degraded", handleAgentDegraded);
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
      // mitto-m8nx (AC2): name the offending MCP server(s) when the agent's
      // stderr included a per-server status line; fall back to the generic
      // workspace-only message when it did not.
      const servers = Array.isArray(data.mcp_servers) ? data.mcp_servers : [];
      const message =
        servers.length > 0
          ? `The following MCP server(s) did not start in time: ${servers.join(", ")}. Check that they are reachable or remove them from the workspace configuration.`
          : "The agent could not start all configured MCP servers. Check that every MCP server is reachable or remove it from the workspace configuration.";
      showToast({
        style: "error",
        title: `MCP initialization timed out: ${name}`,
        message,
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

  // Listen for hook failed events. Transient failures (e.g. cloudflared
  // loopback DNS refusal during bootstrap, mitto-y6i) are rendered as a
  // quieter info-style toast rather than a red warning so users are not
  // alarmed by normal network fluctuations — the backend already throttles
  // the first N transient failures per window so only persistent transient
  // failures reach the frontend.
  useEffect(() => {
    const handleHookFailed = (event) => {
      const data = event.detail;
      if (data) {
        const exitPart =
          data.exit_code !== undefined ? ` (exit code ${data.exit_code})` : "";
        const transient = !!data.transient;
        showToast({
          style: transient ? "info" : "warning",
          title: `Hook ${transient ? "Blip" : "Failed"}: ${data.name || "up"}${exitPart}`,
          message: data.error || "",
          duration: transient ? 5000 : 10000,
        });
      }
    };
    window.addEventListener("mitto:hook_failed", handleHookFailed);
    return () => {
      window.removeEventListener("mitto:hook_failed", handleHookFailed);
    };
  }, [showToast]);

  // Listen for Slack durable-journal-rejecting events (mitto-mfd): the
  // journal has started rejecting Accept() calls (e.g. it hit its hard cap)
  // for a Slack app while still connected — the exact condition that
  // eventually makes Slack auto-disable event delivery. Throttled per app so
  // a rejection storm surfaces one toast per window (see
  // SLACK_JOURNAL_TOAST_THROTTLE_MS) instead of flooding the UI.
  const slackJournalToastAtRef = useRef({});
  useEffect(() => {
    const handleSlackJournalRejecting = (event) => {
      const data = event.detail;
      const appId = data?.app_id;
      if (!appId) return;
      const now = Date.now();
      const last = slackJournalToastAtRef.current[appId] || 0;
      if (now - last < SLACK_JOURNAL_TOAST_THROTTLE_MS) return;
      slackJournalToastAtRef.current[appId] = now;
      showToast({
        style: "warning",
        title: "Slack event journal rejecting events",
        message:
          "The durable event journal is rejecting events for a Slack app. Delivery may be automatically disabled by Slack if this continues — check Settings > Slack.",
        duration: 10000,
      });
    };
    window.addEventListener(
      "mitto:slack_journal_rejecting",
      handleSlackJournalRejecting,
    );
    return () => {
      window.removeEventListener(
        "mitto:slack_journal_rejecting",
        handleSlackJournalRejecting,
      );
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
