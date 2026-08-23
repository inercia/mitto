// web/static/components/slack/SlackChannelPickerModal.js
// Reusable Slack channel-picker modal (mitto-uqq.2), extracted from
// SlackSubscriptionEditor.js so both the Loop-settings editor and the
// prompt-dialog slackChannel field render the SAME search/refresh/retry UI.
// Self-contained: owns its own search text, subscribes to the shared channel
// cache for `installationId` (re-rendering as pages land), and revalidates
// once on a stale search miss. Reads whatever is already in the cache; the
// initial background fetch remains the caller's responsibility (see the note
// below), exactly as before extraction. Preserves the exact data-testids
// used by tests/ui/specs/loop-slack-settings.spec.ts.
//
// Props:
//   client            {object}   - SDK client (shared channel cache is keyed
//                                   by this instance; see utils/slackChannels.js).
//   installationId    {string}   - installation whose channels are shown.
//   usesDelegatedUser {boolean}  - selects delegated-user vs bot copy/badges.
//   open              {boolean}  - controls modal visibility.
//   onClose           {Function} - called on close (backdrop/✕/Escape).
//   onSelect          {Function} - called with the selected channel id.
const { html, useEffect, useState } = window.preact;

import { Portal } from "../ContextMenu.js";
import { Modal } from "../Modal.js";
import {
  channelSearchText,
  fetchAllChannelsInBackground,
  getChannelCacheEntry,
  shouldRevalidateSearchMiss,
  subscribeChannelCache,
} from "../../utils/slackChannels.js";

export function SlackChannelPickerModal({
  client,
  installationId,
  usesDelegatedUser,
  open,
  onClose,
  onSelect,
}) {
  const [search, setSearch] = useState("");
  // Forces a re-render when this installation's cached channels change;
  // channel data itself lives in the client-scoped channel cache (shared
  // with any other subscribed consumer, e.g. the editor's per-row badges),
  // not component state.
  const [, forceRerender] = useState(0);

  useEffect(() => {
    if (!installationId) return undefined;
    return subscribeChannelCache(client, installationId, () =>
      forceRerender((count) => count + 1),
    );
  }, [client, installationId]);

  // Deliberately does NOT kick off a background fetch on mount/open: the
  // initial fetch is the caller's responsibility (e.g. the editor's per-row
  // effect for installations referenced by a subscription), exactly as
  // before extraction. Auto-retrying here would silently re-attempt a
  // previously failed fetch every time the modal opens, overriding a
  // surfaced error before the user acts on it — a behavior change the
  // extraction must not introduce. Refresh/Retry (explicit, force where
  // applicable) and the search-miss revalidation effect below remain the
  // only ways this modal itself triggers a fetch.
  useEffect(() => {
    if (!open) setSearch("");
  }, [open]);

  const page = getChannelCacheEntry(client, installationId);
  const channels = page.channels || [];
  const searchTerm = search.trim().toLowerCase();
  const filteredChannels = searchTerm
    ? channels.filter((channel) =>
        channelSearchText(channel).includes(searchTerm),
      )
    : channels;

  // Slack has no cheap list-version check. A miss against an older cache is a
  // strong signal that the user may be looking for a newly-created or
  // renamed channel, so revalidate once without making every picker open hit
  // Slack.
  useEffect(() => {
    if (
      !open ||
      !searchTerm ||
      filteredChannels.length > 0 ||
      page.loading ||
      page.error ||
      !shouldRevalidateSearchMiss(page)
    ) {
      return;
    }
    fetchAllChannelsInBackground(client, installationId, { force: true });
  }, [
    client,
    open,
    installationId,
    searchTerm,
    filteredChannels.length,
    page.loading,
    page.error,
    page.completedAt,
  ]);

  return html`
    <${Portal}>
      <${Modal}
        isOpen=${open}
        onClose=${onClose}
        title="Select a Slack channel"
        testid="slack-channel-picker-modal"
        closeTestid="slack-channel-picker-close"
        backdropTestid="slack-channel-picker-backdrop"
        boxClass="max-h-[80vh]"
        bodyClass="flex flex-col flex-1 min-h-0 gap-3 p-4 overflow-y-auto"
      >
      <input
        type="search"
        class="input input-sm w-full"
        placeholder="Filter by name, ID, type, or membership"
        aria-label="Filter channels"
        value=${search}
        data-testid="slack-channel-picker-search"
        onInput=${(event) => setSearch(event.target.value)}
      />
      <div class="flex items-center justify-between gap-2">
        <p class="text-xs text-mitto-text-muted" data-testid="slack-channel-picker-count">
          Showing ${filteredChannels.length} of ${channels.length} loaded channels
        </p>
        <button
          type="button"
          class="btn btn-ghost btn-xs"
          disabled=${page.loading || !installationId}
          data-testid="slack-channel-picker-refresh"
          onClick=${() =>
            fetchAllChannelsInBackground(client, installationId, {
              force: true,
            })}
        >
          Refresh
        </button>
      </div>
      ${
        page.loading &&
        channels.length === 0 &&
        html`<div class="flex items-center gap-2 text-xs text-mitto-text-muted">
          <span class="loading loading-spinner loading-xs"></span>
          Loading Slack channels…
        </div>`
      }
      ${
        page.loading &&
        channels.length > 0 &&
        html`<div
          class="flex items-center gap-2 text-xs text-mitto-text-muted"
          data-testid="slack-channel-picker-loading-more"
        >
          <span class="loading loading-spinner loading-xs"></span>
          Loading more channels in the background; Slack throttling is retried
          automatically…
        </div>`
      }
      ${
        page.error &&
        html`<div role="alert" class="alert alert-warning alert-soft text-sm">
          <span>${page.error}</span>
          <button
            type="button"
            class="btn btn-sm"
            data-testid="slack-channel-picker-retry"
            onClick=${() =>
              fetchAllChannelsInBackground(client, installationId)}
          >
            Retry
          </button>
        </div>`
      }
      ${
        !page.loading &&
        !page.error &&
        filteredChannels.length === 0 &&
        html`<p
          class="text-sm text-mitto-text-muted"
          data-testid="slack-channel-picker-empty"
        >
          ${channels.length === 0
            ? usesDelegatedUser
              ? "No channels are visible to the authorizing user yet. Check membership and authorization, then refresh."
              : "No channels are visible yet. Invite the bot to a private channel, then refresh."
            : "No loaded channels match your search."}
        </p>`
      }
      ${
        filteredChannels.length > 0 &&
        html`<ul class="list" data-testid="slack-channel-picker-list">
          ${filteredChannels.map(
            (channel) =>
              html`<li key=${channel.id}>
                <button
                  type="button"
                  class="list-row w-full text-left rounded-box hover:bg-mitto-surface-2"
                  data-testid="slack-channel-picker-row-${channel.id}"
                  onClick=${() => onSelect?.(channel.id)}
                >
                  <span class="list-col-grow min-w-0">
                    <span class="font-medium">#${channel.name}</span>
                    <span class="text-xs text-mitto-text-muted ml-2"
                      >${channel.id}</span
                    >
                  </span>
                  <span class="flex flex-wrap justify-end gap-1">
                    <span class="badge badge-sm badge-soft badge-neutral"
                      >${channel.is_private ? "Private" : "Public"}</span
                    >
                    <span
                      class="badge badge-sm badge-soft ${channel.is_member
                        ? "badge-success"
                        : "badge-warning"}"
                      >${usesDelegatedUser
                        ? channel.is_member
                          ? "Member"
                          : "Not a member"
                        : channel.is_member
                          ? "Joined"
                          : "Not joined"}</span
                    >
                  </span>
                </button>
              </li>`,
          )}
        </ul>`
      }
      </${Modal}>
    <//>
  `;
}
