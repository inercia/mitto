// Slack channel subscriptions for the staged loop-settings editor.
const { html, useEffect, useMemo, useState } = window.preact;

import { PlusIcon, SearchIcon, TrashIcon } from "./Icons.js";
import { Portal } from "./ContextMenu.js";
import { Modal } from "./Modal.js";
import { NativeSelectWithChevron } from "./NativeSelectWithChevron.js";
import { getSdkClient } from "../utils/sdkClient.js";
import {
  DEFAULT_SLACK_EVENT_MODE,
  DEFAULT_SLACK_THREAD_POLICY,
} from "../utils/loopSettings.js";
import { SLACK_INTEGRATIONS_UPDATED_EVENT } from "../utils/slackEvents.js";

// Slack's conversations.list supports up to 200 per page (Slack's own
// documented ceiling, mirrored server-side as slackcatalog.MaxChannelPageSize);
// using the max minimizes the number of round-trips needed to load a full
// workspace's public and visible private channel list.
const CHANNEL_PAGE_SIZE = 200;

function isAbortError(error) {
  return error?.name === "AbortError" || error?.cause?.name === "AbortError";
}

function installationLabel(installation) {
  const parts = [
    installation?.name,
    installation?.team_name || installation?.team_id,
    installation?.app_name,
    installation?.credential_kind === "user" ? "Delegated user" : "Bot",
  ].filter(Boolean);
  return [...new Set(parts)].join(" · ") || installation?.id || "Unknown";
}

function isDelegatedUserInstallation(installation) {
  return installation?.credential_kind === "user";
}

function mergeChannels(current, incoming) {
  const byID = new Map((current || []).map((channel) => [channel.id, channel]));
  for (const channel of incoming || []) byID.set(channel.id, channel);
  return [...byID.values()];
}

function channelSearchText(channel) {
  return [
    channel?.name,
    channel?.id,
    channel?.is_private ? "private" : "public",
    channel?.is_member ? "joined" : "not joined invite bot",
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
}

// --- Client-scoped, installation-keyed channel cache (mitto-wi2u) ----------
// Slack's conversations.list only supports cursor pagination, not full-text
// search, so responsive local filtering requires the full channel set to be
// loaded. This cache lives outside the component so it survives remounts
// (e.g. closing/reopening the Loop Properties panel): the background fetch
// resumes from the last cursor instead of restarting from page 1, and
// already-loaded channels stay visible immediately on remount.
//
// Keyed by the SDK client instance (WeakMap<client, PerClientCache>), not a
// bare module-level Map, for two reasons:
//   1. Test isolation: each test constructs its own mock `client` object, so
//      its cache entry is unreachable (and GC-eligible) the moment the
//      client itself goes out of scope — no explicit reset needed between
//      tests, and no risk of one test's cached channels/errors leaking into
//      another via a shared global.
//   2. Correctness: the production singleton client (getSdkClient()) is
//      shared by every mounted editor instance, so they naturally share one
//      cache entry per installation — remounting the editor keeps reusing
//      the same client object and therefore the same cache.
//
// This is a distinct caching layer from slackcatalog.Service's per-page,
// 1-minute TTL cache (internal/slackcatalog/service.go): the backend cache
// avoids redundant Slack API calls across concurrent/repeated requests for
// the *same page*, while this cache avoids re-fetching the *complete,
// already-assembled* channel list across UI remounts. Slack exposes no channel
// collection revision or ETag, so a complete list stays fresh for 24 hours as
// a bounded safety net. A much shorter search-miss threshold revalidates only
// when the user's query suggests a newly created or renamed channel may be
// absent. Both paths use stale-while-revalidate: cached channels stay visible.
const CHANNEL_CACHE_TTL_MS = 24 * 60 * 60 * 1000;
const CHANNEL_CACHE_SEARCH_MISS_AGE_MS = 5 * 60 * 1000;

/** @typedef {{channels: object[], nextCursor: string, complete: boolean, loading: boolean, error: string, completedAt: number}} ChannelCacheEntry */
/** @typedef {{store: Map<string, ChannelCacheEntry>, listeners: Map<string, Set<() => void>>, controllers: Map<string, AbortController>}} PerClientCache */

/** @type {WeakMap<object, PerClientCache>} */
const clientCaches = new WeakMap();

function getClientCache(client) {
  let cache = clientCaches.get(client);
  if (!cache) {
    cache = { store: new Map(), listeners: new Map(), controllers: new Map() };
    clientCaches.set(client, cache);
  }
  return cache;
}

function getChannelCacheEntry(client, installationId) {
  return (
    getClientCache(client).store.get(installationId) || {
      channels: [],
      nextCursor: "",
      complete: false,
      loading: false,
      error: "",
      completedAt: 0,
    }
  );
}

function isChannelCacheStale(entry) {
  return (
    entry.complete && Date.now() - entry.completedAt > CHANNEL_CACHE_TTL_MS
  );
}

function shouldRevalidateSearchMiss(entry) {
  return (
    entry.complete &&
    Date.now() - entry.completedAt > CHANNEL_CACHE_SEARCH_MISS_AGE_MS
  );
}

function notifyChannelCacheListeners(cache, installationId) {
  for (const listener of cache.listeners.get(installationId) || []) listener();
}

function setChannelCacheEntry(client, installationId, patch) {
  const cache = getClientCache(client);
  cache.store.set(installationId, {
    ...getChannelCacheEntry(client, installationId),
    ...patch,
  });
  notifyChannelCacheListeners(cache, installationId);
}

// Subscribes `listener` to changes for `installationId`'s cache entry on
// `client`. Returns an unsubscribe function. When the last listener for an
// installation unsubscribes (no mounted editor still cares about it), the
// in-flight background fetch — if any — is aborted (request dedup: at most
// one fetch per client+installation in flight at a time); already-cached
// channels and the resume cursor are kept, so a later remount continues
// where the fetch left off instead of restarting from page 1.
function subscribeChannelCache(client, installationId, listener) {
  const cache = getClientCache(client);
  let listeners = cache.listeners.get(installationId);
  if (!listeners) {
    listeners = new Set();
    cache.listeners.set(installationId, listeners);
  }
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) {
      cache.listeners.delete(installationId);
      const controller = cache.controllers.get(installationId);
      controller?.abort();
      cache.controllers.delete(installationId);
      if (controller) {
        cache.store.set(installationId, {
          ...getChannelCacheEntry(client, installationId),
          loading: false,
        });
      }
    }
  };
}

// Invalidates every cached installation's channels for `client` (mitto-wi2u):
// aborts any in-flight fetches and clears all entries, notifying mounted
// listeners so they re-render and the bootstrap effect below re-fetches from
// scratch. Called when SLACK_INTEGRATIONS_UPDATED_EVENT fires, since
// credentials or installations may have changed.
function clearChannelCache(client) {
  const cache = getClientCache(client);
  for (const controller of cache.controllers.values()) controller.abort();
  cache.controllers.clear();
  const installationIds = [...cache.store.keys()];
  cache.store.clear();
  for (const installationId of installationIds)
    notifyChannelCacheListeners(cache, installationId);
}

// Sequentially fetches every conversations.list page for `installationId` in
// the background, merging each page into the shared cache as it arrives so
// partial progress (and local filtering over it) is visible immediately —
// no "Load more" interaction required. Resumes from the entry's last known
// `nextCursor` (set on error) so a retry does not re-fetch already-loaded
// pages. A no-op if a fetch for this client+installation is already in
// flight (dedup) or the full channel set was already loaded and still fresh
// (see CHANNEL_CACHE_TTL_MS); a stale-but-complete entry triggers a
// stale-while-revalidate background refresh instead of blocking on it.
async function fetchAllChannelsInBackground(
  client,
  installationId,
  { force = false } = {},
) {
  const cache = getClientCache(client);
  if (!installationId || cache.controllers.has(installationId)) return;
  const entry = getChannelCacheEntry(client, installationId);
  if (!force && entry.complete && !isChannelCacheStale(entry)) return;

  const controller = new AbortController();
  cache.controllers.set(installationId, controller);
  setChannelCacheEntry(client, installationId, { loading: true, error: "" });

  const refreshing = entry.complete;
  let cursor = refreshing ? "" : entry.nextCursor || "";
  let channels = refreshing ? [] : entry.channels || [];
  try {
    for (;;) {
      const data = await client.slack.listChannels(
        installationId,
        { cursor, limit: CHANNEL_PAGE_SIZE },
        { signal: controller.signal },
      );
      if (
        controller.signal.aborted ||
        cache.controllers.get(installationId) !== controller
      ) {
        return;
      }
      channels = mergeChannels(channels, data?.channels);
      cursor = data?.next_cursor || "";
      const complete = !cursor;
      setChannelCacheEntry(client, installationId, {
        channels,
        nextCursor: cursor,
        complete,
        loading: !complete,
        error: "",
        completedAt: complete ? Date.now() : 0,
      });
      if (complete) break;
    }
  } catch (error) {
    if (cache.controllers.get(installationId) !== controller) return;
    if (isAbortError(error)) {
      setChannelCacheEntry(client, installationId, { loading: false });
    } else {
      setChannelCacheEntry(client, installationId, {
        loading: false,
        nextCursor: cursor,
        error:
          "Channels could not be loaded. Check channels:read and groups:read, apply the current app manifest, reauthorize the app, and retry.",
      });
    }
  } finally {
    if (cache.controllers.get(installationId) === controller) {
      cache.controllers.delete(installationId);
    }
  }
}

function blankSubscription() {
  return {
    installationId: "",
    channelId: "",
    eventMode: DEFAULT_SLACK_EVENT_MODE,
    threadPolicy: DEFAULT_SLACK_THREAD_POLICY,
  };
}

export function SlackSubscriptionEditor({
  subscriptions = [],
  onChange,
  fieldErrors = {},
  client: clientOverride,
}) {
  const client = clientOverride || getSdkClient();
  const [catalog, setCatalog] = useState({
    apps: [],
    installations: [],
    loading: true,
    error: "",
  });
  const [catalogVersion, setCatalogVersion] = useState(0);
  // Picker modal state lives here (not per-row) so exactly one modal instance
  // exists; `index` identifies the active subscription and is always read
  // fresh from state at render/handler time, so re-renders or row
  // additions/removals never leave a handler closing over a stale index.
  const [picker, setPicker] = useState({ open: false, index: -1, search: "" });
  // Forces a re-render when a subscribed installation's cached channels
  // change (see the subscribeChannelCache effect below); channel data itself
  // lives in the client-scoped channel cache, not component state, so it
  // survives this component remounting (as long as the same `client` — the
  // production singleton from getSdkClient() — is reused).
  const [, forceChannelCacheRerender] = useState(0);

  useEffect(() => {
    const refresh = () => {
      clearChannelCache(client);
      setCatalogVersion((current) => current + 1);
    };
    window.addEventListener(SLACK_INTEGRATIONS_UPDATED_EVENT, refresh);
    return () =>
      window.removeEventListener(SLACK_INTEGRATIONS_UPDATED_EVENT, refresh);
  }, [client]);

  useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;
    setCatalog((current) => ({ ...current, loading: true, error: "" }));

    const load = async () => {
      try {
        const appData = await client.slack.listApps({
          signal: controller.signal,
        });
        const apps = Array.isArray(appData?.apps) ? appData.apps : [];
        const results = await Promise.all(
          apps.map(async (app) => {
            try {
              const data = await client.slack.listInstallations(app.id, {
                signal: controller.signal,
              });
              return {
                failed: false,
                values: (data?.installations || []).map((installation) => ({
                  ...installation,
                  app_name: app.name,
                  app_token_configured: app.token_configured === true,
                })),
              };
            } catch (error) {
              if (isAbortError(error)) throw error;
              return { failed: true, values: [] };
            }
          }),
        );
        if (cancelled) return;
        setCatalog({
          apps,
          installations: results.flatMap((result) => result.values),
          loading: false,
          error: results.some((result) => result.failed)
            ? "Some Slack workspace installations could not be loaded."
            : "",
        });
      } catch (error) {
        if (!cancelled && !isAbortError(error)) {
          setCatalog((current) => ({
            ...current,
            loading: false,
            error: "Slack integrations could not be loaded.",
          }));
        }
      }
    };
    load();
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [catalogVersion, client]);

  const installationIDs = useMemo(
    () =>
      [
        ...new Set(
          subscriptions.map((item) => item.installationId).filter(Boolean),
        ),
      ].sort(),
    [subscriptions],
  );
  const installationIDsKey = installationIDs.join("\u0000");

  // Subscribes this component to the shared channel cache for every
  // installation currently referenced by a subscription row, re-rendering
  // when a background page (fetched here or by another mounted editor
  // instance) lands. Unsubscribing (row removed / component unmounted) lets
  // subscribeChannelCache abort the fetch once no editor still cares.
  useEffect(() => {
    const unsubscribes = installationIDs.map((installationId) =>
      subscribeChannelCache(client, installationId, () =>
        forceChannelCacheRerender((count) => count + 1),
      ),
    );
    return () => {
      for (const unsubscribe of unsubscribes) unsubscribe();
    };
  }, [installationIDsKey, client]);

  // Kicks off (or resumes) the sequential background page fetch for every
  // referenced installation with a configured bot token. A no-op for
  // installations already complete or already being fetched.
  useEffect(() => {
    for (const installationId of installationIDs) {
      const installation = catalog.installations.find(
        (item) => item.id === installationId,
      );
      if (installation?.token_configured === true) {
        fetchAllChannelsInBackground(client, installationId);
      }
    }
  }, [installationIDsKey, catalog.installations, client]);

  // appMention is delivered only through the bot event stream. A credential
  // replacement can change an installation's mode without changing its stable
  // ID, so normalize stale drafts after every catalog refresh while preserving
  // their installation, channel, thread policy, and unknown future fields.
  useEffect(() => {
    if (catalog.loading) return;
    let changed = false;
    const next = subscriptions.map((subscription) => {
      const installation = catalog.installations.find(
        (item) => item.id === subscription.installationId,
      );
      if (
        isDelegatedUserInstallation(installation) &&
        subscription.eventMode === "appMention"
      ) {
        changed = true;
        return { ...subscription, eventMode: DEFAULT_SLACK_EVENT_MODE };
      }
      return subscription;
    });
    if (changed) onChange?.(next);
  }, [catalog.loading, catalog.installations, subscriptions, onChange]);

  const updateSubscription = (index, patch) => {
    onChange?.(
      subscriptions.map((subscription, itemIndex) =>
        itemIndex === index ? { ...subscription, ...patch } : subscription,
      ),
    );
  };

  const removeSubscription = (index) => {
    onChange?.(
      subscriptions.filter((_subscription, itemIndex) => itemIndex !== index),
    );
    if (picker.index === index) {
      setPicker({ open: false, index: -1, search: "" });
    }
  };

  const openChannelPicker = (index) => {
    setPicker({ open: true, index, search: "" });
  };

  const closeChannelPicker = () => {
    setPicker((current) => ({ ...current, open: false, search: "" }));
  };

  const selectChannel = (channelId) => {
    if (picker.index < 0) return;
    updateSubscription(picker.index, { channelId });
    closeChannelPicker();
  };

  // Derived from `picker` + current props/state on every render (never
  // memoized against a stale index), so they always reflect the actively
  // open row even if subscriptions are added/removed while the modal is open.
  const pickerSubscription =
    picker.index >= 0 ? subscriptions[picker.index] : null;
  const pickerInstallationId = pickerSubscription?.installationId || "";
  const pickerInstallation = catalog.installations.find(
    (item) => item.id === pickerInstallationId,
  );
  const pickerUsesDelegatedUser =
    isDelegatedUserInstallation(pickerInstallation);
  const pickerPage = getChannelCacheEntry(client, pickerInstallationId);
  const pickerChannels = pickerPage.channels || [];
  const pickerSearchTerm = picker.search.trim().toLowerCase();
  const pickerFilteredChannels = pickerSearchTerm
    ? pickerChannels.filter((channel) =>
        channelSearchText(channel).includes(pickerSearchTerm),
      )
    : pickerChannels;

  // Slack has no cheap list-version check. A miss against an older cache is a
  // strong signal that the user may be looking for a newly-created or renamed
  // channel, so revalidate once without making every picker open hit Slack.
  useEffect(() => {
    if (
      !picker.open ||
      !pickerSearchTerm ||
      pickerFilteredChannels.length > 0 ||
      pickerPage.loading ||
      pickerPage.error ||
      !shouldRevalidateSearchMiss(pickerPage)
    ) {
      return;
    }
    fetchAllChannelsInBackground(client, pickerInstallationId, { force: true });
  }, [
    client,
    picker.open,
    pickerInstallationId,
    pickerSearchTerm,
    pickerFilteredChannels.length,
    pickerPage.loading,
    pickerPage.error,
    pickerPage.completedAt,
  ]);

  return html`
    <div class="flex flex-col gap-3" data-testid="slack-subscription-editor">
      <p class="text-xs text-mitto-text-muted">
        New human-authored messages are included by default. Bot, self, and
        subtype messages remain excluded.
      </p>

      ${
        catalog.loading &&
        html`<div class="flex items-center gap-2 text-xs text-mitto-text-muted">
          <span class="loading loading-spinner loading-xs"></span>
          Loading Slack workspaces…
        </div>`
      }
      ${
        catalog.error &&
        html`<div role="alert" class="alert alert-warning alert-soft text-sm">
          <span>${catalog.error} Existing draft values are preserved.</span>
        </div>`
      }
      ${
        !catalog.loading &&
        catalog.apps.length === 0 &&
        html`<div role="alert" class="alert alert-warning alert-soft text-sm">
          <span
            >Configure a Slack app and workspace installation before enabling
            this trigger.</span
          >
        </div>`
      }
      ${
        fieldErrors.slackSubscriptions &&
        html`<span class="label text-mitto-danger"
          >${fieldErrors.slackSubscriptions}</span
        >`
      }
      ${subscriptions.map((subscription, index) => {
        const installation = catalog.installations.find(
          (item) => item.id === subscription.installationId,
        );
        const installationMissing =
          !!subscription.installationId && !catalog.loading && !installation;
        const credentialsMissing =
          !!installation &&
          (installation.token_configured !== true ||
            installation.app_token_configured !== true);
        const channelCredentialMissing =
          !!installation && installation.token_configured !== true;
        const usesDelegatedUser = isDelegatedUserInstallation(installation);
        const page = getChannelCacheEntry(client, subscription.installationId);
        const channels = page.channels || [];
        const selectedChannel = channels.find(
          (channel) => channel.id === subscription.channelId,
        );
        const selectedChannelNeedsInvite =
          !!selectedChannel && selectedChannel.is_member !== true;
        // "Started" covers both a page in flight and a paused/errored
        // mid-fetch state (partial results cached, retry pending) — either
        // way at least one page has landed or is landing, so it's worth
        // distinguishing "still checking" (channelUnresolved) from
        // "checked every page, not found" (channelMissing, requires
        // `complete`).
        const started =
          page.loading || page.complete || !!page.error || channels.length > 0;
        const channelMissing =
          !!subscription.channelId && page.complete && !selectedChannel;
        const channelUnresolved =
          !!subscription.channelId &&
          started &&
          !page.complete &&
          !selectedChannel;
        const field = (name) =>
          fieldErrors[`slackSubscription.${index}.${name}`];

        return html`
          <div
            key=${index}
            class="border border-mitto-border rounded-box bg-mitto-surface-2 p-3 flex flex-col gap-3"
            data-testid="slack-subscription-row-${index}"
          >
            <div class="flex items-center justify-between gap-2">
              <span class="font-medium text-sm">Channel ${index + 1}</span>
              <button
                type="button"
                class="btn btn-ghost btn-square btn-xs"
                aria-label=${`Remove Slack channel ${index + 1}`}
                data-testid="slack-subscription-remove-${index}"
                onClick=${() => removeSubscription(index)}
              >
                <${TrashIcon} className="w-4 h-4" />
              </button>
            </div>

            <label class="fieldset">
              <span class="fieldset-legend">Slack workspace</span>
              <${NativeSelectWithChevron}
                ariaLabel="Slack workspace"
                value=${subscription.installationId}
                testId="slack-installation-${index}"
                onChange=${(event) =>
                  updateSubscription(index, {
                    installationId: event.target.value,
                    channelId:
                      event.target.value === subscription.installationId
                        ? subscription.channelId
                        : "",
                    eventMode: isDelegatedUserInstallation(
                      catalog.installations.find(
                        (item) => item.id === event.target.value,
                      ),
                    )
                      ? DEFAULT_SLACK_EVENT_MODE
                      : subscription.eventMode,
                  })}
              >
                <option value="">Select a configured workspace</option>
                ${installationMissing &&
                html`<option value=${subscription.installationId}>
                  Missing integration · ${subscription.installationId}
                </option>`}
                ${catalog.installations.map(
                  (item) =>
                    html`<option key=${item.id} value=${item.id}>
                      ${installationLabel(item)}
                      ${item.token_configured && item.app_token_configured
                        ? ""
                        : " · credentials required"}
                    </option>`,
                )}
              <//>
              ${field("installationId") &&
              html`<span class="label text-mitto-danger"
                >${field("installationId")}</span
              >`}
            </label>

            ${installationMissing &&
            html`<div
              role="alert"
              class="alert alert-warning alert-soft text-sm"
            >
              <span
                >This saved installation no longer exists. Choose another
                workspace or manage integrations.</span
              >
            </div>`}
            ${credentialsMissing &&
            html`<div
              role="alert"
              class="alert alert-warning alert-soft text-sm"
            >
              <span
                >This integration needs configured app and installation
                credentials before the trigger can run. Configure the
                ${usesDelegatedUser ? "delegated-user" : "bot"} credential to
                load channels.</span
              >
            </div>`}
            ${installation &&
            html`<div
              class="flex flex-wrap items-center gap-2 text-xs text-mitto-text-muted"
              data-testid="slack-credential-mode-${index}"
              role="status"
            >
              <span
                class="badge badge-sm badge-soft ${usesDelegatedUser
                  ? "badge-secondary"
                  : "badge-primary"}"
                >${usesDelegatedUser ? "Delegated user" : "Bot"}</span
              >
              <span>
                ${usesDelegatedUser
                  ? "Channel visibility follows the authorizing user's membership and lasts only while that authorization remains active. Mention-only mode is unavailable; stale mention-only drafts reset to human messages."
                  : "Channel visibility follows the bot's membership. Invite the bot to private channels before using them."}
              </span>
            </div>`}

            <label class="fieldset">
              <span class="fieldset-legend">Channel ID</span>
              <div class="join w-full">
                <input
                  type="text"
                  class="input input-sm join-item flex-1"
                  value=${subscription.channelId}
                  placeholder="C0123456789"
                  data-testid="slack-channel-id-${index}"
                  onInput=${(event) =>
                    updateSubscription(index, {
                      channelId: event.target.value,
                    })}
                />
                <button
                  type="button"
                  class="btn btn-sm join-item btn-square"
                  aria-label="Search Slack channels"
                  title="Search Slack channels"
                  disabled=${!installation || channelCredentialMissing}
                  data-testid="slack-channel-picker-open-${index}"
                  onClick=${() => openChannelPicker(index)}
                >
                  <${SearchIcon} className="w-4 h-4" />
                </button>
              </div>
              ${field("channelId") &&
              html`<span class="label text-mitto-danger"
                >${field("channelId")}</span
              >`}
            </label>

            ${channelMissing &&
            html`<div
              role="alert"
              class="alert alert-warning alert-soft text-sm"
            >
              <span
                >${usesDelegatedUser
                  ? "The saved channel is not visible to the authorizing user. The saved ID remains in the draft; restore the user's membership or authorization, refresh, or choose another channel."
                  : "The saved channel is not visible to this app. Private channels appear only after the bot is invited. The saved ID remains in the draft; invite the bot, refresh, or choose another channel."}</span
              >
            </div>`}
            ${selectedChannelNeedsInvite &&
            html`<div
              role="alert"
              class="alert alert-warning alert-soft text-sm"
              data-testid="slack-channel-invite-guidance-${index}"
            >
              <span
                >${usesDelegatedUser
                  ? `The authorizing user must join #${selectedChannel.name} and keep the authorization active before enabling this trigger.`
                  : `Invite the bot to #${selectedChannel.name} before enabling this trigger so Slack can deliver its messages.`}</span
              >
            </div>`}
            ${channelUnresolved &&
            html`<div role="status" class="alert alert-info alert-soft text-sm">
              <span
                >Still checking loaded channels for a match; the saved ID
                remains in the draft while more channels load in the
                background.</span
              >
            </div>`}

            <div class="flex flex-col gap-3">
              <label class="fieldset">
                <span class="fieldset-legend">Message mode</span>
                <${NativeSelectWithChevron}
                  ariaLabel="Message mode"
                  value=${subscription.eventMode}
                  testId="slack-event-mode-${index}"
                  onChange=${(event) =>
                    updateSubscription(index, {
                      eventMode: event.target.value,
                    })}
                >
                  <option value="anyHumanMessage">Any new human message</option>
                  ${!usesDelegatedUser &&
                  html`<option value="appMention">
                    Only messages mentioning the app
                  </option>`}
                <//>
                ${field("eventMode") &&
                html`<span class="label text-mitto-danger"
                  >${field("eventMode")}</span
                >`}
              </label>
              <label class="fieldset">
                <span class="fieldset-legend">Threads</span>
                <${NativeSelectWithChevron}
                  ariaLabel="Threads"
                  value=${subscription.threadPolicy}
                  testId="slack-thread-policy-${index}"
                  onChange=${(event) =>
                    updateSubscription(index, {
                      threadPolicy: event.target.value,
                    })}
                >
                  <option value="any">Root messages and replies</option>
                  <option value="rootOnly">Root messages only</option>
                  <option value="repliesOnly">Thread replies only</option>
                <//>
                ${field("threadPolicy") &&
                html`<span class="label text-mitto-danger"
                  >${field("threadPolicy")}</span
                >`}
              </label>
            </div>
          </div>
        `;
      })}

      <button
        type="button"
        class="btn btn-sm"
        data-testid="slack-subscription-add"
        onClick=${() => onChange?.([...subscriptions, blankSubscription()])}
      >
        <${PlusIcon} className="w-4 h-4" />
        Add channel
      </button>

      <${Portal}>
        <${Modal}
          isOpen=${picker.open}
          onClose=${closeChannelPicker}
          title="Select a Slack channel"
          testid="slack-channel-picker-modal"
          closeTestid="slack-channel-picker-close"
          backdropTestid="slack-channel-picker-backdrop"
          bodyClass="flex flex-col gap-3 p-4 overflow-y-auto"
        >
        <input
          type="search"
          class="input input-sm w-full"
          placeholder="Filter by name, ID, type, or membership"
          aria-label="Filter channels"
          value=${picker.search}
          data-testid="slack-channel-picker-search"
          onInput=${(event) =>
            setPicker((current) => ({
              ...current,
              search: event.target.value,
            }))}
        />
        <div class="flex items-center justify-between gap-2">
          <p
            class="text-xs text-mitto-text-muted"
            data-testid="slack-channel-picker-count"
          >
            Showing ${pickerFilteredChannels.length} of ${pickerChannels.length}
            loaded channels
          </p>
          <button
            type="button"
            class="btn btn-ghost btn-xs"
            disabled=${pickerPage.loading || !pickerInstallationId}
            data-testid="slack-channel-picker-refresh"
            onClick=${() =>
              fetchAllChannelsInBackground(client, pickerInstallationId, {
                force: true,
              })}
          >
            Refresh
          </button>
        </div>
        ${
          pickerPage.loading &&
          pickerChannels.length === 0 &&
          html`<div
            class="flex items-center gap-2 text-xs text-mitto-text-muted"
          >
            <span class="loading loading-spinner loading-xs"></span>
            Loading Slack channels…
          </div>`
        }
        ${
          pickerPage.loading &&
          pickerChannels.length > 0 &&
          html`<div
            class="flex items-center gap-2 text-xs text-mitto-text-muted"
            data-testid="slack-channel-picker-loading-more"
          >
            <span class="loading loading-spinner loading-xs"></span>
            Loading more channels in the background…
          </div>`
        }
        ${
          pickerPage.error &&
          html`<div role="alert" class="alert alert-warning alert-soft text-sm">
            <span>${pickerPage.error}</span>
            <button
              type="button"
              class="btn btn-sm"
              data-testid="slack-channel-picker-retry"
              onClick=${() =>
                fetchAllChannelsInBackground(client, pickerInstallationId)}
            >
              Retry
            </button>
          </div>`
        }
        ${
          !pickerPage.loading &&
          !pickerPage.error &&
          pickerFilteredChannels.length === 0 &&
          html`<p
            class="text-sm text-mitto-text-muted"
            data-testid="slack-channel-picker-empty"
          >
            ${pickerChannels.length === 0
              ? pickerUsesDelegatedUser
                ? "No channels are visible to the authorizing user yet. Check membership and authorization, then refresh."
                : "No channels are visible yet. Invite the bot to a private channel, then refresh."
              : "No loaded channels match your search."}
          </p>`
        }
        ${
          pickerFilteredChannels.length > 0 &&
          html`<ul class="list" data-testid="slack-channel-picker-list">
            ${pickerFilteredChannels.map(
              (channel) =>
                html`<li key=${channel.id}>
                  <button
                    type="button"
                    class="list-row w-full text-left rounded-box hover:bg-mitto-surface-2"
                    data-testid="slack-channel-picker-row-${channel.id}"
                    onClick=${() => selectChannel(channel.id)}
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
                        >${pickerUsesDelegatedUser
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
    </div>
  `;
}
