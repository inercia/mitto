// Mitto Web Interface - Message List Component
// Renders the scrollable messages area: empty state, reversed message list with
// date separators and retry buttons, load-more controls, infinite-scroll sentinel,
// and the scroll-to-bottom floating button.
const { html, Fragment } = window.preact;

import { Message } from "./Message.js";
import { SpinnerIcon, ArrowDownIcon, SettingsIcon } from "./Icons.js";

/**
 * @param {Array}    displayMessages   - Coalesced messages to render
 * @param {Array}    messages          - Raw messages (length check for empty state)
 * @param {boolean}  hasMoreMessages
 * @param {boolean}  hasReachedLimit
 * @param {boolean}  isLoadingMore
 * @param {boolean}  isStreaming
 * @param {Function} onLoadMore
 * @param {Function} onScrollToBottom
 * @param {boolean}  isUserAtBottom
 * @param {boolean}  hasNewMessages
 * @param {object}   sentinelRef       - ref forwarded to the IntersectionObserver sentinel
 * @param {Function} onRetry           - called with (text, images) for error retry
 * @param {string}   activeSessionId
 * @param {string}   swipeDirection    - 'left'|'right'|null
 * @param {string}   swipeArrow        - 'left'|'right'|null
 * @param {boolean}  connected
 * @param {object}   sessionInfo
 * @param {Array}    workspaces
 * @param {object}   messagesContainerRef - ref attached to the scrollable container
 */
export function MessageList({
  displayMessages,
  messages,
  hasMoreMessages,
  hasReachedLimit,
  isLoadingMore,
  isStreaming,
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
  workspaces,
  messagesContainerRef,
}) {
  return html`
    <${Fragment}>
      <!-- Messages (scrollable container with normal scroll) -->
      <div
        ref=${messagesContainerRef}
        class="absolute inset-0 overflow-y-auto scroll-smooth p-4 messages-container-reverse"
      >
        ${swipeDirection &&
        html`
          <div
            key=${`flash-${activeSessionId}`}
            class="swipe-flash swipe-flash-${swipeDirection}"
          />
        `}
        ${swipeArrow &&
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
        `}
        <div
          key=${activeSessionId}
          class="max-w-2xl mx-auto flex flex-col-reverse ${swipeDirection
            ? `swipe-slide-${swipeDirection}`
            : ""}"
        >
          ${messages.length === 0 &&
          !hasMoreMessages &&
          html`
            <div class="flex items-center justify-center h-full">
              <div class="text-center text-mitto-text-muted">
                <img src="./favicon.png" alt="Mitto" class="w-24 h-24 mb-6 opacity-30 mx-auto" />
                <p class="text-2xl font-medium text-mitto-text-secondary mb-4">
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
                        <div class="text-base text-mitto-text-muted max-w-md">
                          <p>
                            Create a new conversation using the
                            <span
                              class="inline-flex items-center justify-center w-6 h-6 rounded text-white text-sm font-bold mx-1"
                              >+</span
                            >
                            button in the sidebar
                          </p>
                          ${workspaces.length > 1
                            ? html`
                                <p class="text-sm text-gray-600 mt-3">
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
                    <span
                      class="w-3 h-3 border-2 border-yellow-500 border-t-transparent rounded-full animate-spin"
                    ></span>
                    Connecting to AI agent...
                  </p>
                `}
              </div>
            </div>
          `}
          ${[...displayMessages]
            .reverse()
            .flatMap((msg, i, arr) => {
              let retryHandler = undefined;
              if (msg.role === "error") {
                const origIdx = arr.length - 1 - i;
                for (let j = origIdx - 1; j >= 0; j--) {
                  const prev = displayMessages[j];
                  if (prev.role === "user" && prev.text) {
                    const retryText = prev.text;
                    const retryImages = prev.images || [];
                    retryHandler = () => onRetry(retryText, retryImages);
                    break;
                  }
                }
              }

              const origIdx = arr.length - 1 - i;
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
                      year: d.getFullYear() !== now.getFullYear() ? "numeric" : undefined,
                    });
                  }
                  dateSeparator = html`
                    <div key=${"sep-" + origIdx} class="date-separator">
                      ${label}
                    </div>
                  `;
                }
              }

              const msgEl = html`
                <${Message}
                  key=${msg.timestamp + "-" + origIdx}
                  message=${msg}
                  isLast=${i === 0}
                  isStreaming=${isStreaming}
                  onRetry=${retryHandler}
                />
              `;
              return dateSeparator ? [dateSeparator, msgEl] : [msgEl];
            })}
          ${(hasMoreMessages || hasReachedLimit) &&
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
                          >Message limit reached (${messages.length}
                          messages loaded)</span
                        >
                      </div>
                    `
                  : html`
                      <button
                        onClick=${onLoadMore}
                        class="load-more-btn px-4 py-2 text-sm text-mitto-text-muted hover:text-mitto-text-200 hover:bg-gray-700/50 rounded-lg transition-colors flex items-center gap-2"
                        data-testid="load-more-button"
                      >
                        <span>↑</span>
                        <span>Load earlier messages...</span>
                      </button>
                    `}
            </div>
          `}
          ${html`
            <div ref=${sentinelRef} class="h-1 w-full" aria-hidden="true" />
          `}
        </div>
      </div>
      <!-- End of scrollable messages container -->

      <!-- Scroll to bottom button -->
      ${(!isUserAtBottom || hasNewMessages) &&
      messages.length > 0 &&
      html`
        <div class="scroll-to-bottom-wrapper">
          <button
            onClick=${() => onScrollToBottom(true)}
            class="scroll-to-bottom-btn ${hasNewMessages ? "has-new" : ""}"
            title="Scroll to bottom"
          >
            <${ArrowDownIcon} className="w-5 h-5" />
            ${hasNewMessages &&
            html` <span class="new-messages-indicator"></span> `}
          </button>
        </div>
      `}
    </${Fragment}>
  `;
}
