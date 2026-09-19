// Mitto Web Interface - Message List Component
// Renders the scrollable messages area: empty state, reversed message list with
// date separators and retry buttons, load-more controls, infinite-scroll sentinel,
// and the scroll-to-bottom floating button.
const { html, Fragment, useMemo, useState, useEffect, useLayoutEffect, memo } =
  window.preact;

import { Message } from "./Message.js";
import { SpinnerIcon, ArrowDownIcon, SettingsIcon } from "./Icons.js";
import {
  buildRetryTargets,
  canReplayNamedPrompt,
  messageKey,
  coalesceAgentMessages,
  COALESCE_DEFAULTS,
} from "../lib.js";
import { useVisibleInterval } from "../hooks/useVisibleInterval.js";
import { perfMark, perfMeasure } from "../utils/perfMarks.js";
import { useRenderCounter } from "../hooks/useRenderCounter.js";
import { useActiveSessionMessages } from "../hooks/useSessionsStore.js";
import { useWorkspaces } from "../hooks/useWorkspacesStore.js";

/**
 * @param {boolean}  hasMoreMessages
 * @param {boolean}  hasReachedLimit
 * @param {boolean}  isLoadingMore
 * @param {boolean}  isStreaming
 * @param {object}   agentWorking      - Transient "agent is still working" heartbeat
 *                                       ({ idleMs, toolTitle, receivedAt }) or null
 * @param {Function} onLoadMore
 * @param {Function} onScrollToBottom
 * @param {boolean}  isUserAtBottom
 * @param {boolean}  hasNewMessages
 * @param {object}   sentinelRef       - ref forwarded to the IntersectionObserver sentinel
 * @param {Function} onRetry           - called with (text, images) for a plain-text retry, or
 *                                        ("", images, [], { promptName, arguments }) to replay a
 *                                        named prompt exactly (matches handleSendPrompt's signature)
 * @param {string}   activeSessionId
 * @param {string}   swipeDirection    - 'left'|'right'|null
 * @param {string}   swipeArrow        - 'left'|'right'|null
 * @param {boolean}  connected
 * @param {object}   sessionInfo
 * @param {object}   messagesContainerRef - ref attached to the scrollable container
 * @param {object}   mcpInitState      - Active session workspace's MCP-init state from
 *                                        useMCPInitState ({ initializing, timedOutAt, servers })
 *                                        or null when no MCP-init event has fired (mitto-8fm).
 * @param {Function} clearMCPInit      - (workspaceUUID, workingDir) => void; clears mcpInitState
 *                                        early (e.g. on first stream chunk) instead of waiting
 *                                        for the safety-cap sweep.
 */
function MessageListImpl({
  hasMoreMessages,
  hasReachedLimit,
  isLoadingMore,
  isStreaming,
  agentWorking,
  onLoadMore,
  onScrollToBottom,
  isUserAtBottom,
  hasNewMessages,
  sentinelRef,
  onRetry,
  activeSessionId,
  swipeDirection,
  swipeArrow,
  connected,
  sessionInfo,
  messagesContainerRef,
  mcpInitState,
  clearMCPInit,
}) {
  // Dev-only render-count instrumentation (mitto-sus.7). No-op unless perf
  // instrumentation is enabled; see docs/devel/frontend-render-domains.md.
  useRenderCounter("MessageList");
  // Read this session's messages directly from the subscribable store
  // (mitto-b1k) instead of an App-passed prop, so a WS chunk to a
  // *different* (background) session never causes this component to
  // reconcile: `useActiveSessionMessages` only notifies when THIS
  // session id's messages slice changes. Coalescing (moved here from
  // app.js) recomputes only when the raw messages array reference changes.
  const messages = useActiveSessionMessages(activeSessionId);
  // Workspaces self-subscribe via stores/workspacesStore.js (mitto-sus.11)
  // instead of an App-passed prop -- workspaces rarely change, but this
  // keeps App's own re-renders from ever forcing this memoized component
  // to reconcile just because it happened to pass this prop through.
  const workspaces = useWorkspaces();
  const displayMessages = useMemo(() => {
    return coalesceAgentMessages(messages, {
      hrBreaksCoalescing: COALESCE_DEFAULTS.hrBreaksCoalescing,
    });
  }, [messages]);
  // Tick every 2s while the "agent is still working" heartbeat is visible, to
  // update the mm:ss timer and to re-evaluate staleness (auto-hide after 25s with
  // no new heartbeat). 2s resolution is invisible to the eye at mm:ss scale and
  // halves MessageList re-renders during long-running turns. Gated on visibility
  // via useVisibleInterval so the tick stops when the Mitto webview is hidden
  // (background/Cmd-H), and catches up on wake so the chip's mm:ss is never
  // stuck at an old value.
  const [workingNow, setWorkingNow] = useState(Date.now());
  useVisibleInterval(() => setWorkingNow(Date.now()), 2000, {
    enabled: isStreaming && !!agentWorking,
  });

  // mitto-sus.1.1: session-switch firstPaint seam. Dependency-gated (not
  // remount-gated — the inner message list re-keys on activeSessionId but
  // this component itself does not unmount), so it fires exactly once per
  // switch, synchronously after the new session's messages have committed
  // to the DOM but before the browser paints. Pairs with the
  // `session.switch.click` mark fired from `switchSession`.
  useLayoutEffect(() => {
    perfMark("session.switch.firstPaint");
    perfMeasure(
      "session.switch.click-to-firstPaint",
      "session.switch.click",
      "session.switch.firstPaint",
    );
  }, [activeSessionId]);

  // mitto-8fm: clear the persistent "Waiting for MCP servers…" indicator as
  // soon as the agent starts streaming a response — belt-and-braces on top of
  // the natural `sessionInfo.acp_ready` transition, since a stream can start
  // before acp_ready flips in some races (e.g. it was already true when the
  // mcp_initializing event fired for a *different* prior turn).
  useEffect(() => {
    if (isStreaming && mcpInitState?.initializing) {
      clearMCPInit?.(sessionInfo?.workspace_uuid, sessionInfo?.working_dir);
    }
  }, [
    isStreaming,
    mcpInitState?.initializing,
    clearMCPInit,
    sessionInfo?.workspace_uuid,
    sessionInfo?.working_dir,
  ]);

  const showAgentWorking =
    isStreaming && agentWorking && workingNow - agentWorking.receivedAt < 25000;

  const agentWorkingChip = showAgentWorking
    ? (() => {
        const totalSeconds = Math.floor(
          (agentWorking.idleMs + (workingNow - agentWorking.receivedAt)) / 1000,
        );
        const mm = String(Math.floor(totalSeconds / 60)).padStart(2, "0");
        const ss = String(totalSeconds % 60).padStart(2, "0");
        return html`
          <div key="agent-working-chip" class="flex justify-center mb-1">
            <div
              class="text-xs text-mitto-text-muted flex items-center gap-2 bg-mitto-surface-2 px-3 py-1.5 rounded-lg opacity-70"
            >
              <span
                >Working${agentWorking.toolTitle
                  ? ` — ${agentWorking.toolTitle}`
                  : ""}…
                (${mm}:${ss})</span
              >
            </div>
          </div>
        `;
      })()
    : null;

  // Memoize the reversed/flatMapped render list. Recomputes only when the
  // active session's messages, streaming state, or retry callback change —
  // not on every unrelated re-render (e.g. background-session streaming ticks).
  const renderedMessages = useMemo(() => {
    // Precompute retry targets in one forward pass (O(n)) instead of the
    // previous O(n·m) backward scan per error message.
    const retryTargets = buildRetryTargets(displayMessages);

    return [...displayMessages].reverse().flatMap((msg, i, arr) => {
      // i === 0 is the newest message (column-reverse layout)
      const origIdx = arr.length - 1 - i;

      let retryHandler = undefined;
      if (msg.role === "error") {
        const target = retryTargets.get(origIdx);
        if (target) {
          const { text, images, promptName, arguments: args } = target;
          // Replay the original named prompt (preserving its UI pill,
          // modelTag/preferredModels routing, and prompt-level processing)
          // when we have persisted argument values for ALL of its arguments.
          // Otherwise fall back to today's full-text replay — this covers
          // ad-hoc messages, older events with no persisted arguments, and
          // the case where a sensitive argument was omitted at persist time.
          retryHandler = canReplayNamedPrompt(target)
            ? () => onRetry("", images, [], { promptName, arguments: args })
            : () => onRetry(text, images);
        }
      }

      const key = messageKey(msg);

      let dateSeparator = null;
      if (msg.timestamp) {
        const msgDate = new Date(msg.timestamp).toDateString();
        const olderMsg = arr[i + 1];
        const olderDate = olderMsg?.timestamp
          ? new Date(olderMsg.timestamp).toDateString()
          : null;
        if (!olderMsg || msgDate !== olderDate) {
          const now = new Date();
          const yesterday = new Date(now);
          yesterday.setDate(yesterday.getDate() - 1);
          let label;
          const d = new Date(msg.timestamp);
          if (d.toDateString() === now.toDateString()) {
            label = "Today";
          } else if (d.toDateString() === yesterday.toDateString()) {
            label = "Yesterday";
          } else {
            label = d.toLocaleDateString([], {
              month: "short",
              day: "numeric",
              year:
                d.getFullYear() !== now.getFullYear() ? "numeric" : undefined,
            });
          }
          dateSeparator = html`
            <div key=${"sep-" + key} class="divider date-separator">
              ${label}
            </div>
          `;
        }
      }

      const msgEl = html`
        <${Message}
          key=${key}
          message=${msg}
          isLast=${i === 0}
          isStreaming=${isStreaming}
          onRetry=${retryHandler}
          workspaceUUID=${sessionInfo?.workspace_uuid}
          workspacePath=${sessionInfo?.working_dir}
        />
      `;
      return dateSeparator ? [dateSeparator, msgEl] : [msgEl];
    });
  }, [
    displayMessages,
    isStreaming,
    onRetry,
    sessionInfo?.workspace_uuid,
    sessionInfo?.working_dir,
  ]);

  return html`
    <${Fragment}>
      <!-- Messages (scrollable container with normal scroll) -->
      <div
        ref=${messagesContainerRef}
        class="absolute inset-0 overflow-y-auto scroll-smooth p-4 messages-container-reverse"
      >
        ${
          swipeDirection &&
          html`
            <div
              key=${`flash-${activeSessionId}`}
              class="swipe-flash swipe-flash-${swipeDirection}"
            />
          `
        }
        ${
          swipeArrow &&
          html`
            <div
              key=${`arrow-${activeSessionId}-${swipeArrow}`}
              class="swipe-arrow-indicator"
            >
              <div class="swipe-arrow-indicator__content">
                <span class="swipe-arrow-indicator__arrow"
                  >${swipeArrow === "left" ? "→" : "←"}</span
                >
              </div>
            </div>
          `
        }
        <div
          key=${activeSessionId}
          class="max-w-[60rem] mx-auto flex flex-col-reverse ${
            swipeDirection ? `swipe-slide-${swipeDirection}` : ""
          }"
        >
          ${
            messages.length === 0 &&
            !hasMoreMessages &&
            html`
              <div class="hero h-full">
                <div class="hero-content">
                  <div class="text-center text-mitto-text-muted">
                    <img
                      src="./favicon.png"
                      alt="Mitto"
                      class="w-24 h-24 mb-6 opacity-30 mx-auto"
                    />
                    <p
                      class="text-2xl font-medium text-mitto-text-secondary mb-4"
                    >
                      Welcome to Mitto
                    </p>
                    ${workspaces.length === 0
                      ? html`
                          <p class="text-base text-mitto-text-muted max-w-md">
                            Get started by creating a workspace in Settings
                            (<span class="inline-block align-middle">
                              <${SettingsIcon} className="w-5 h-5 inline" />
                            </span>
                            icon in the sidebar)
                          </p>
                        `
                      : activeSessionId
                        ? html`
                            <p class="text-base text-mitto-text-muted">
                              Type a message to start chatting with the AI agent
                            </p>
                          `
                        : html`
                            <div
                              class="text-base text-mitto-text-muted max-w-md"
                            >
                              <p>
                                Create a new conversation using the
                                <span
                                  class="inline-flex items-center justify-center w-6 h-6 rounded bg-primary text-primary-content text-sm font-bold mx-1"
                                  >+</span
                                >
                                button in the sidebar
                              </p>
                              ${workspaces.length > 1
                                ? html`
                                    <p
                                      class="text-sm text-mitto-text-muted mt-3"
                                    >
                                      You'll be able to choose which workspace
                                      to use
                                    </p>
                                  `
                                : ""}
                            </div>
                          `}
                    ${!connected &&
                    html`
                      <p class="text-sm mt-6 text-mitto-warning">
                        Connecting to server...
                      </p>
                    `}
                    ${connected &&
                    activeSessionId &&
                    sessionInfo &&
                    !sessionInfo.acp_ready &&
                    !sessionInfo.archived &&
                    html`
                      <p
                        class="text-sm mt-6 text-mitto-warning flex items-center gap-2"
                      >
                        Establishing agent session...
                      </p>
                    `}
                    ${connected &&
                    activeSessionId &&
                    sessionInfo &&
                    !sessionInfo.archived &&
                    mcpInitState?.initializing &&
                    html`
                      <p
                        class="text-sm mt-6 text-mitto-warning flex items-center gap-2"
                      >
                        <${SpinnerIcon} className="w-4 h-4" />
                        Waiting for MCP servers…
                      </p>
                    `}
                    ${connected &&
                    activeSessionId &&
                    sessionInfo &&
                    !sessionInfo.archived &&
                    mcpInitState?.timedOutAt &&
                    html`
                      <p
                        class="text-sm mt-6 text-mitto-danger flex items-center gap-2"
                      >
                        MCP server(s) failed to
                        start${mcpInitState.servers?.length
                          ? `: ${mcpInitState.servers.join(", ")}`
                          : ""}.
                        Check your MCP configuration.
                      </p>
                    `}
                  </div>
                </div>
              </div>
            `
          }
          ${agentWorkingChip}
          ${renderedMessages}
          ${
            (hasMoreMessages || hasReachedLimit) &&
            html`
              <div class="flex justify-center my-4">
                ${isLoadingMore
                  ? html`
                      <div
                        class="px-4 py-2 text-sm text-mitto-text-muted flex items-center gap-2"
                      >
                        <${SpinnerIcon} className="w-4 h-4" />
                        <span>Loading earlier messages...</span>
                      </div>
                    `
                  : hasReachedLimit
                    ? html`
                        <div
                          class="px-4 py-2 text-sm text-mitto-text-muted flex items-center gap-2"
                          data-testid="limit-reached-indicator"
                        >
                          <span>📚</span>
                          <span
                            >Message limit reached (${messages.length} messages
                            loaded)</span
                          >
                        </div>
                      `
                    : html`
                        <button
                          onClick=${onLoadMore}
                          class="btn btn-ghost btn-sm text-mitto-text-muted"
                          data-testid="load-more-button"
                        >
                          <span>↑</span>
                          <span>Load earlier messages...</span>
                        </button>
                      `}
              </div>
            `
          }
          ${html`
            <div ref=${sentinelRef} class="h-1 w-full" aria-hidden="true" />
          `}
        </div>
      </div>
      <!-- End of scrollable messages container -->

      <!-- Scroll to bottom button -->
      ${
        (!isUserAtBottom || hasNewMessages) &&
        messages.length > 0 &&
        html`
          <div class="scroll-to-bottom-wrapper">
            <button
              onClick=${() => onScrollToBottom(true)}
              class="btn btn-circle scroll-to-bottom-btn tooltip tooltip-bottom ${hasNewMessages
                ? "has-new"
                : ""}"
              data-tip="Scroll to bottom"
              aria-label="Scroll to bottom"
            >
              <${ArrowDownIcon} className="w-5 h-5" />
              ${hasNewMessages &&
              html`
                <span
                  class="new-messages-indicator badge badge-warning badge-xs"
                ></span>
              `}
            </button>
          </div>
        `
      }
    </${Fragment}>
  `;
}

// memo() (mitto-b1k): now that `messages`/`displayMessages` are read
// internally from the store instead of App-passed props, the remaining
// props are reference-stable across a background session's chunks — a
// shallow-prop-equal memo() stops this component from reconciling on
// unrelated App re-renders. See docs/devel/frontend-render-domains.md.
export const MessageList = memo(MessageListImpl);
