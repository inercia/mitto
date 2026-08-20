/**
 * Unit tests for the MCP-init persistent inline indicator in MessageList.js
 * (mitto-8fm).
 *
 * MessageList.js imports `window.preact` (html/Fragment/useState/useEffect)
 * at module load time and renders via HTM template literals, so — following
 * the established pattern for this kind of gating logic (see
 * HeaderArchiveButton.test.js, Message.test.js) — the three pure predicates
 * added for this feature are mirrored here verbatim rather than importing
 * the full component. When MessageList.js changes, keep these mirrors in
 * sync with lines ~79-94 (auto-clear effect) and ~320-348 (placeholder
 * gates), referenced below.
 */

// Mirrors MessageList.js L320-332: gate for the "Waiting for MCP servers…"
// placeholder, placed directly below the existing "Establishing ACP
// session..." block and sharing its base gating (connected, active session,
// sessionInfo present, not archived).
function shouldShowMCPWaiting({
  connected,
  activeSessionId,
  sessionInfo,
  mcpInitState,
}) {
  return !!(
    connected &&
    activeSessionId &&
    sessionInfo &&
    !sessionInfo.archived &&
    mcpInitState?.initializing
  );
}

// Mirrors MessageList.js L333-348: gate + message text for the persistent
// "MCP server(s) failed to start" hint.
function shouldShowMCPTimedOut({
  connected,
  activeSessionId,
  sessionInfo,
  mcpInitState,
}) {
  return !!(
    connected &&
    activeSessionId &&
    sessionInfo &&
    !sessionInfo.archived &&
    mcpInitState?.timedOutAt
  );
}

function formatMCPTimedOutMessage(mcpInitState) {
  const suffix = mcpInitState.servers?.length
    ? `: ${mcpInitState.servers.join(", ")}`
    : "";
  return `MCP server(s) failed to start${suffix}. Check your MCP configuration.`;
}

// Mirrors the auto-clear useEffect body, MessageList.js L84-94.
function shouldClearMCPInitOnStream({ isStreaming, mcpInitState }) {
  return !!(isStreaming && mcpInitState?.initializing);
}

const baseSessionInfo = {
  workspace_uuid: "ws1",
  working_dir: "/repo",
  acp_ready: true,
  archived: false,
};

describe("MessageList — MCP-init waiting placeholder gate (mitto-8fm)", () => {
  test("shows when connected, active session, and mcpInitState.initializing", () => {
    expect(
      shouldShowMCPWaiting({
        connected: true,
        activeSessionId: "s1",
        sessionInfo: baseSessionInfo,
        mcpInitState: { initializing: true },
      }),
    ).toBe(true);
  });

  test("hidden when mcpInitState is null", () => {
    expect(
      shouldShowMCPWaiting({
        connected: true,
        activeSessionId: "s1",
        sessionInfo: baseSessionInfo,
        mcpInitState: null,
      }),
    ).toBe(false);
  });

  test("hidden when not connected", () => {
    expect(
      shouldShowMCPWaiting({
        connected: false,
        activeSessionId: "s1",
        sessionInfo: baseSessionInfo,
        mcpInitState: { initializing: true },
      }),
    ).toBe(false);
  });

  test("hidden when the session is archived (AC4: no regression / consistent gating)", () => {
    expect(
      shouldShowMCPWaiting({
        connected: true,
        activeSessionId: "s1",
        sessionInfo: { ...baseSessionInfo, archived: true },
        mcpInitState: { initializing: true },
      }),
    ).toBe(false);
  });

  test("hidden once mcpInitState.initializing flips false (e.g. after timeout)", () => {
    expect(
      shouldShowMCPWaiting({
        connected: true,
        activeSessionId: "s1",
        sessionInfo: baseSessionInfo,
        mcpInitState: { initializing: false, timedOutAt: 123 },
      }),
    ).toBe(false);
  });
});

describe("MessageList — MCP-init timed-out placeholder gate + message (mitto-8fm)", () => {
  test("shows when mcpInitState.timedOutAt is set", () => {
    expect(
      shouldShowMCPTimedOut({
        connected: true,
        activeSessionId: "s1",
        sessionInfo: baseSessionInfo,
        mcpInitState: { timedOutAt: 123, servers: [] },
      }),
    ).toBe(true);
  });

  test("hidden while only initializing (not yet timed out)", () => {
    expect(
      shouldShowMCPTimedOut({
        connected: true,
        activeSessionId: "s1",
        sessionInfo: baseSessionInfo,
        mcpInitState: { initializing: true, timedOutAt: null },
      }),
    ).toBe(false);
  });

  test("message names the failed servers when present (AC3)", () => {
    expect(
      formatMCPTimedOutMessage({
        timedOutAt: 123,
        servers: ["snow-cmr-automation", "splunk-mcp-ap"],
      }),
    ).toBe(
      "MCP server(s) failed to start: snow-cmr-automation, splunk-mcp-ap. Check your MCP configuration.",
    );
  });

  test("message falls back to a generic hint when servers is empty (mitto-m8nx AC2 fallback)", () => {
    expect(formatMCPTimedOutMessage({ timedOutAt: 123, servers: [] })).toBe(
      "MCP server(s) failed to start. Check your MCP configuration.",
    );
  });

  test("message tolerates a missing servers field", () => {
    expect(formatMCPTimedOutMessage({ timedOutAt: 123 })).toBe(
      "MCP server(s) failed to start. Check your MCP configuration.",
    );
  });
});

describe("MessageList — auto-clear on stream start (mitto-8fm AC2)", () => {
  test("clears once streaming starts while still marked initializing", () => {
    expect(
      shouldClearMCPInitOnStream({
        isStreaming: true,
        mcpInitState: { initializing: true },
      }),
    ).toBe(true);
  });

  test("no-op when not streaming", () => {
    expect(
      shouldClearMCPInitOnStream({
        isStreaming: false,
        mcpInitState: { initializing: true },
      }),
    ).toBe(false);
  });

  test("no-op when mcpInitState is null", () => {
    expect(
      shouldClearMCPInitOnStream({ isStreaming: true, mcpInitState: null }),
    ).toBe(false);
  });

  test("no-op once already cleared/not initializing (avoids redundant clears)", () => {
    expect(
      shouldClearMCPInitOnStream({
        isStreaming: true,
        mcpInitState: { initializing: false, timedOutAt: 123 },
      }),
    ).toBe(false);
  });
});
