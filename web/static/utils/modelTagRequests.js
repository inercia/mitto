// Requester-only confirmations; config events remain the model-state authority.
const RESULT_STATUSES = new Set([
  "applied", "already_active", "no_match", "catalog_unavailable",
  "invalid_tag", "invalid_request", "missing_session", "internal_error",
]);

export function createModelTagRequests({
  sendToSession,
  timeoutMs = 100000,
  makeRequestId,
  setTimeoutFn = setTimeout,
  clearTimeoutFn = clearTimeout,
}) {
  const pending = new Map();
  const prefix = globalThis.crypto?.randomUUID?.() ??
    `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
  let sequence = 0;
  let disposed = false;

  function settle(requestId, status) {
    const request = pending.get(requestId);
    if (!request) return;
    pending.delete(requestId);
    clearTimeoutFn(request.timer);
    request.resolve({ status });
  }

  function applyModelTag(sessionId, modelTag) {
    if (disposed) return Promise.resolve({ status: "cancelled" });
    if (typeof sessionId !== "string" || !sessionId.trim()) {
      return Promise.resolve({ status: "invalid_target" });
    }
    if (typeof modelTag !== "string" || !modelTag.trim()) {
      return Promise.resolve({ status: "invalid_tag" });
    }
    const tag = modelTag.trim();
    // A counter also protects against a repeated/empty injected ID generator.
    const requestId = `${makeRequestId?.() || prefix}-${++sequence}`;
    return new Promise((resolve) => {
      const request = { sessionId, tag, resolve, timer: null };
      pending.set(requestId, request);
      request.timer = setTimeoutFn(() => settle(requestId, "timeout"), timeoutMs);
      try {
        if (sendToSession(sessionId, {
          type: "apply_model_tag",
          data: { model_tag: tag, request_id: requestId },
        }) === false) {
          settle(requestId, "send_failed");
        }
      } catch {
        settle(requestId, "send_failed");
      }
    });
  }

  function handleModelTagApplied(socketSessionId, data) {
    const request = pending.get(data?.request_id);
    if (!request || request.sessionId !== socketSessionId ||
        request.sessionId !== data.session_id || request.tag !== data.model_tag) return;
    settle(data.request_id, RESULT_STATUSES.has(data.status) ? data.status : "protocol_error");
  }

  function dispose() {
    disposed = true;
    for (const requestId of pending.keys()) settle(requestId, "cancelled");
  }

  return { applyModelTag, handleModelTagApplied, dispose };
}
