/**
 * Injectable pending-prompt queue — a host-agnostic replacement for the
 * `mitto_pending_prompts` localStorage queue in web/static/lib.js. Used by
 * SessionStream.sendPrompt()'s delivery-verification path to persist an
 * outbound prompt before the ACK arrives, and by retryPendingPrompts() to
 * resend anything still outstanding after a reconnect.
 *
 * Never touches `localStorage` directly — the storage-backed variant is
 * built on the injected storage contract from sdk/core/config.js
 * (getItem/setItem/removeItem).
 */

// Prompts older than this are considered stale and are excluded from
// getForSession() (mirrors lib.js's PROMPT_EXPIRY_MS).
const PROMPT_EXPIRY_MS = 5 * 60 * 1000;

// Default storage key for createStoragePendingPromptStore(). Deliberately
// distinct from lib.js's "mitto_pending_prompts" key: the SDK store and the
// legacy hook-based store are independent until the UI migration issues
// (.17/.18) replace the hook path, so the two must never collide.
const DEFAULT_STORAGE_KEY = "mitto_sdk_pending_prompts";

export const PENDING_PROMPTS_CONSTANTS = { PROMPT_EXPIRY_MS, DEFAULT_STORAGE_KEY };

/** Generates a unique prompt ID for delivery tracking. */
export function generatePromptId() {
  return `prompt_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`;
}

function unexpired(entries, expiryMs, now) {
  const t = now();
  const results = [];
  for (const [promptId, data] of entries) {
    if (t - data.timestamp < expiryMs) results.push({ promptId, ...data });
  }
  results.sort((a, b) => a.timestamp - b.timestamp);
  return results;
}

/** In-memory pending-prompt store; the default when no `pendingPromptStore` is injected. */
export function createMemoryPendingPromptStore(options = {}) {
  const expiryMs = options.expiryMs ?? PROMPT_EXPIRY_MS;
  const now = options.now ?? Date.now;
  const store = new Map();

  return {
    save(sessionId, promptId, message, imageIds = [], fileIds = []) {
      store.set(promptId, { sessionId, message, imageIds, fileIds, timestamp: now() });
    },
    remove(promptId) {
      store.delete(promptId);
    },
    getForSession(sessionId) {
      const entries = [...store].filter(([, data]) => data.sessionId === sessionId);
      return unexpired(entries, expiryMs, now);
    },
  };
}

/**
 * Pending-prompt store backed by an injected storage adapter (the same
 * getItem/setItem/removeItem contract as `config.storage`).
 */
export function createStoragePendingPromptStore(storage, options = {}) {
  const expiryMs = options.expiryMs ?? PROMPT_EXPIRY_MS;
  const now = options.now ?? Date.now;
  const key = options.storageKey ?? DEFAULT_STORAGE_KEY;

  function readAll() {
    try {
      const raw = storage.getItem(key);
      return raw ? JSON.parse(raw) || {} : {};
    } catch (_err) {
      return {};
    }
  }
  function writeAll(all) {
    storage.setItem(key, JSON.stringify(all));
  }

  return {
    save(sessionId, promptId, message, imageIds = [], fileIds = []) {
      const all = readAll();
      all[promptId] = { sessionId, message, imageIds, fileIds, timestamp: now() };
      writeAll(all);
    },
    remove(promptId) {
      const all = readAll();
      if (promptId in all) {
        delete all[promptId];
        writeAll(all);
      }
    },
    getForSession(sessionId) {
      const entries = Object.entries(readAll()).filter(([, data]) => data.sessionId === sessionId);
      return unexpired(entries, expiryMs, now);
    },
  };
}
