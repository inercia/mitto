// Shared Slack channel-picker infrastructure (mitto-uqq.2): extracted from
// SlackSubscriptionEditor.js so both the Loop-settings editor and the
// prompt-dialog slackChannel field consume ONE implementation. Pure module
// (no preact dependency) — safe to import from components, hooks, or tests.

// Slack's conversations.list supports up to 200 per page (Slack's own
// documented ceiling, mirrored server-side as slackcatalog.MaxChannelPageSize);
// using the max minimizes the number of round-trips needed to load a full
// workspace's public and visible private channel list.
export const CHANNEL_PAGE_SIZE = 200;

export function isAbortError(error) {
  return error?.name === "AbortError" || error?.cause?.name === "AbortError";
}

export function channelLoadErrorMessage(error) {
  if (error?.code === "rate_limited") {
    return "Slack is still rate limiting channel discovery after automatic retries. Loaded channels were preserved; Retry resumes where loading paused.";
  }
  if (error?.code === "unavailable" || error?.code === "network_error") {
    return "Slack channel discovery is temporarily unavailable after automatic retries. Loaded channels were preserved; Retry resumes where loading paused.";
  }
  return "Channels could not be loaded. Check channels:read and groups:read, apply the current app manifest, reauthorize the app, and retry.";
}

export function installationLabel(installation) {
  const parts = [
    installation?.name,
    installation?.team_name || installation?.team_id,
    installation?.app_name,
    installation?.credential_kind === "user" ? "Delegated user" : "Bot",
  ].filter(Boolean);
  return [...new Set(parts)].join(" · ") || installation?.id || "Unknown";
}

export function isDelegatedUserInstallation(installation) {
  return installation?.credential_kind === "user";
}

export function mergeChannels(current, incoming) {
  const byID = new Map((current || []).map((channel) => [channel.id, channel]));
  for (const channel of incoming || []) byID.set(channel.id, channel);
  return [...byID.values()];
}

export function channelSearchText(channel) {
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
// loaded. This cache lives outside any component so it survives remounts
// (e.g. closing/reopening the Loop Properties panel, or opening the prompt
// parameter dialog): the background fetch resumes from the last cursor
// instead of restarting from page 1, and already-loaded channels stay
// visible immediately on remount.
//
// Keyed by the SDK client instance (WeakMap<client, PerClientCache>), not a
// bare module-level Map, for two reasons:
//   1. Test isolation: each test constructs its own mock `client` object, so
//      its cache entry is unreachable (and GC-eligible) the moment the
//      client itself goes out of scope — no explicit reset needed between
//      tests, and no risk of one test's cached channels/errors leaking into
//      another via a shared global.
//   2. Correctness: the production singleton client (getSdkClient()) is
//      shared by every consumer (editor rows, the picker modal, the prompt
//      dialog), so they naturally share one cache entry per installation —
//      no double-fetching across consumers or remounts.
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
export const CHANNEL_CACHE_TTL_MS = 24 * 60 * 60 * 1000;
export const CHANNEL_CACHE_SEARCH_MISS_AGE_MS = 5 * 60 * 1000;

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

export function getChannelCacheEntry(client, installationId) {
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

export function shouldRevalidateSearchMiss(entry) {
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
// installation unsubscribes (no mounted consumer still cares about it), the
// in-flight background fetch — if any — is aborted (request dedup: at most
// one fetch per client+installation in flight at a time); already-cached
// channels and the resume cursor are kept, so a later remount continues
// where the fetch left off instead of restarting from page 1.
export function subscribeChannelCache(client, installationId, listener) {
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
// listeners so they re-render and any bootstrap effect re-fetches from
// scratch. Called when SLACK_INTEGRATIONS_UPDATED_EVENT fires, since
// credentials or installations may have changed.
export function clearChannelCache(client) {
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
export async function fetchAllChannelsInBackground(
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
        error: channelLoadErrorMessage(error),
      });
    }
  } finally {
    if (cache.controllers.get(installationId) === controller) {
      cache.controllers.delete(installationId);
    }
  }
}
