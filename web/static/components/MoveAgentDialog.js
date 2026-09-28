// Mitto Web Interface - Move Agent Dialog Component (mitto-f7yo.6)
//
// Confirmation dialog shown after picking a target agent from the "Move to
// agent" context-menu submenu (see hooks/useConversationMenu.js). Fetches the
// server-computed preflight (GET .../move-agent/preflight) on open — the
// server is the single source of truth for busy/archived/loop/children-count
// state, this dialog never re-derives it client-side — and, on confirm,
// POSTs the move (POST .../move-agent). Both calls go through the SDK
// resource layer (getSdkClient().sessions.moveAgentPreflight/moveAgent), NOT
// authFetch + endpoints.sessions.* directly — see SessionList.test.js /
// SessionPanel.test.js / ConversationPropertiesPanel.test.js guard tests
// enforcing that convention for session-related components.

const { html, useState, useEffect, useCallback } = window.preact;

import { Modal } from "./Modal.js";
import { getSdkClient } from "../utils/sdkClient.js";
import { errorMessage } from "../utils/sdkErrors.js";
import { useToast } from "../hooks/useToast.js";

/**
 * Builds the success toast title for a completed move, folding in any
 * skipped-children count and resume warning. Exported (pure, no deps) so it
 * can be unit-tested without mounting the dialog.
 *
 * @param {object} result - MoveAgentResult: {moved, skipped, previous_agent,
 *   previous_baseline_model?, resume_error?}
 * @param {string} targetAgent
 * @returns {{style: string, title: string}} a showToast()-shaped payload
 */
export function buildMoveAgentToast(result, targetAgent) {
  const skippedCount = result?.skipped?.length || 0;
  const parts = [`Moved to ${targetAgent}`];
  if (skippedCount > 0) {
    parts.push(
      `${skippedCount} child conversation${skippedCount === 1 ? "" : "s"} skipped`,
    );
  }
  if (result?.resume_error) {
    // A resume_error means the move itself succeeded (the session is
    // rebound) but resuming the ACP session under the new agent hit an
    // error — still worth a distinct warning-styled toast rather than
    // silently dropping it.
    return {
      style: "warning",
      title: `${parts.join(" — ")}. Warning: ${result.resume_error}`,
    };
  }
  return { style: "success", title: parts.join(" — ") };
}

/**
 * Whether the Confirm button should be disabled, given the fetched
 * preflight. Exported (pure) for unit testing. Mirrors the server's own
 * ErrMoveAgentBusy / ErrMoveAgentArchived guards — this is a UX convenience
 * (grey out + explain) only; the server re-validates on POST regardless.
 *
 * @param {object|null} preflight
 * @returns {boolean}
 */
export function isMoveAgentConfirmDisabled(preflight) {
  if (!preflight) return true;
  return !!preflight.busy || !!preflight.archived;
}

/**
 * MoveAgentDialog - confirms rebinding a conversation to a different ACP
 * agent, after fetching server-side preflight info (busy/archived/loop/
 * children-count/candidate availability).
 *
 * @param {object}   props
 * @param {object|null} props.session      - the session being moved (needs session_id); dialog renders nothing (via Modal isOpen=false) when null
 * @param {string}   props.targetAgent     - name of the agent to move to
 * @param {Function} props.onClose         - called to close the dialog (confirm success/error or cancel)
 */
export function MoveAgentDialog({ session, targetAgent, onClose }) {
  const isOpen = !!session && !!targetAgent;
  const sessionId = session?.session_id;

  const [preflight, setPreflight] = useState(null);
  const [loadError, setLoadError] = useState("");
  const [loading, setLoading] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [includeChildren, setIncludeChildren] = useState(false);
  const { showToast } = useToast();

  // Fetch preflight whenever the dialog opens for a (session, targetAgent)
  // pair. Reset all local state first so a previous open's stale data never
  // flashes before the new fetch resolves.
  useEffect(() => {
    if (!isOpen) return;
    let cancelled = false;
    setPreflight(null);
    setLoadError("");
    setIncludeChildren(false);
    setLoading(true);
    (async () => {
      try {
        const result =
          await getSdkClient().sessions.moveAgentPreflight(sessionId);
        if (!cancelled) setPreflight(result);
      } catch (err) {
        if (!cancelled) {
          setLoadError(errorMessage(err, "Failed to load move-agent details"));
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [isOpen, sessionId, targetAgent]);

  const handleClose = useCallback(() => {
    if (confirming) return;
    onClose?.();
  }, [confirming, onClose]);

  const handleConfirm = useCallback(async () => {
    if (!sessionId || !targetAgent) return;
    setConfirming(true);
    try {
      const result = await getSdkClient().sessions.moveAgent(sessionId, {
        target_agent: targetAgent,
        include_children: includeChildren,
      });
      showToast(buildMoveAgentToast(result, targetAgent));
      onClose?.();
    } catch (err) {
      showToast({
        style: "error",
        title: errorMessage(err, "Failed to move conversation"),
      });
      onClose?.();
    } finally {
      setConfirming(false);
    }
  }, [sessionId, targetAgent, includeChildren, showToast, onClose]);

  const candidate = (preflight?.candidates || []).find(
    (c) => c.name === targetAgent,
  );
  const loopPromptUnavailable =
    !!preflight?.is_loop && candidate?.loop_prompt_available === false;
  const confirmDisabled =
    loading || confirming || isMoveAgentConfirmDisabled(preflight);

  const footer = html`
    <button
      class="btn btn-sm btn-ghost"
      onClick=${handleClose}
      disabled=${confirming}
      data-testid="move-agent-cancel"
    >
      Cancel
    </button>
    <button
      class="btn btn-sm btn-primary"
      onClick=${handleConfirm}
      disabled=${confirmDisabled}
      data-testid="move-agent-confirm"
    >
      ${confirming
        ? html`<span class="loading loading-spinner loading-xs"></span>`
        : null}
      Move
    </button>
  `;

  return html`
    <${Modal}
      isOpen=${isOpen}
      onClose=${handleClose}
      title="Move to ${targetAgent}"
      testid="move-agent-dialog"
      backdropTestid="move-agent-backdrop"
      footer=${footer}
    >
      ${
        loading &&
        html`
          <div class="text-center py-6" data-testid="move-agent-loading">
            <span
              class="loading loading-spinner loading-lg mb-3 text-mitto-accent"
            ></span>
            <p class="text-mitto-text-secondary">Checking conversation…</p>
          </div>
        `
      }

      ${
        !loading &&
        loadError &&
        html`
          <div
            class="alert alert-error text-sm"
            data-testid="move-agent-load-error"
          >
            <span>${loadError}</span>
          </div>
        `
      }

      ${
        !loading &&
        !loadError &&
        preflight &&
        html`
          <div class="flex flex-col gap-3">
            <div class="alert alert-warning text-sm" role="alert">
              <span
                >The agent's internal context will be lost — only recent turns
                are carried over as text.</span
              >
            </div>
            <div class="alert alert-warning text-sm" role="alert">
              <span
                >MCP tools, prompts and models may differ on
                ${targetAgent}.</span
              >
            </div>

            ${preflight.is_loop &&
            html`
              <div class="text-sm text-mitto-text-secondary">
                Loop prompt:
                <span class="font-medium text-mitto-text"
                  >${preflight.loop_prompt_name || "(unnamed)"}</span
                >
              </div>
            `}
            ${loopPromptUnavailable &&
            html`
              <div
                class="alert alert-warning text-sm"
                role="alert"
                data-testid="move-agent-loop-prompt-warning"
              >
                <span
                  >This loop prompt is not available on ${targetAgent} — the
                  loop may not run correctly after the move.</span
                >
              </div>
            `}
            ${(preflight.busy || preflight.archived) &&
            html`
              <div
                class="alert alert-error text-sm"
                role="alert"
                data-testid="move-agent-busy"
              >
                <span
                  >${preflight.archived
                    ? "This conversation is archived and cannot be moved."
                    : preflight.busy_reason ||
                      "This conversation is busy right now."}</span
                >
              </div>
            `}
            ${preflight.children_count > 0 &&
            html`
              <label
                class="flex items-center gap-2 text-sm cursor-pointer"
                data-testid="move-agent-include-children"
              >
                <input
                  type="checkbox"
                  class="checkbox checkbox-sm"
                  checked=${includeChildren}
                  onChange=${(e) => setIncludeChildren(e.target.checked)}
                />
                <span
                  >Also move ${preflight.children_count} child
                  conversation${preflight.children_count === 1 ? "" : "s"}</span
                >
              </label>
            `}
          </div>
        `
      }
    </${Modal}>
  `;
}
