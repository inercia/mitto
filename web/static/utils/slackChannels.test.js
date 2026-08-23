/**
 * Tests for slackChannels.js (mitto-uqq.2).
 *
 * This module was extracted out of SlackSubscriptionEditor.js so it can be
 * shared by both the Loop editor and the future prompt-dialog slackChannel
 * field (uqq.4). It has no preact dependency, so it is tested directly here
 * as a pure module + async cache, independent of any component consumer.
 * SlackSubscriptionEditor.test.js already exercises this module end-to-end
 * through the editor/modal; these tests cover the module's own contract in
 * isolation (helpers + the client-scoped WeakMap cache), including the
 * cross-client isolation guarantee that the extraction relies on.
 */
import { describe, test, expect, jest } from "./testing/testGlobals.js";
import {
  isAbortError,
  channelLoadErrorMessage,
  installationLabel,
  isDelegatedUserInstallation,
  mergeChannels,
  channelSearchText,
  getChannelCacheEntry,
  subscribeChannelCache,
  clearChannelCache,
  fetchAllChannelsInBackground,
  CHANNEL_PAGE_SIZE,
} from "./slackChannels.js";

function makeClient(listChannelsImpl) {
  return { slack: { listChannels: jest.fn(listChannelsImpl) } };
}

describe("slackChannels — pure helpers", () => {
  test("isAbortError matches error.name and error.cause.name", () => {
    expect(isAbortError({ name: "AbortError" })).toBe(true);
    expect(isAbortError({ cause: { name: "AbortError" } })).toBe(true);
    expect(isAbortError(new Error("nope"))).toBe(false);
    expect(isAbortError(undefined)).toBe(false);
  });

  test("channelLoadErrorMessage branches on error code", () => {
    expect(channelLoadErrorMessage({ code: "rate_limited" })).toContain(
      "still rate limiting",
    );
    expect(channelLoadErrorMessage({ code: "network_error" })).toContain(
      "temporarily unavailable",
    );
    expect(channelLoadErrorMessage({ code: "unavailable" })).toContain(
      "temporarily unavailable",
    );
    expect(channelLoadErrorMessage(new Error("boom"))).toContain(
      "channels:read and groups:read",
    );
  });

  test("installationLabel joins parts and dedupes, always including the credential badge", () => {
    expect(
      installationLabel({
        name: "Workspace",
        team_name: "Team",
        app_name: "App",
        credential_kind: "user",
      }),
    ).toBe("Workspace · Team · App · Delegated user");
    expect(installationLabel({ name: "Workspace", team_id: "T1" })).toBe(
      "Workspace · T1 · Bot",
    );
    // team_name/team_id both equal to name collapses via Set dedup.
    expect(installationLabel({ name: "Same", team_name: "Same" })).toBe(
      "Same · Bot",
    );
    // The credential badge ("Bot"/"Delegated user") is always present, so
    // the id/"Unknown" fallback only matters if a caller ever passes an
    // installation with no other identifying fields.
    expect(installationLabel({ id: "inst-1" })).toBe("Bot");
    expect(installationLabel({})).toBe("Bot");
  });

  test("isDelegatedUserInstallation only true for credential_kind 'user'", () => {
    expect(isDelegatedUserInstallation({ credential_kind: "user" })).toBe(true);
    expect(isDelegatedUserInstallation({ credential_kind: "bot" })).toBe(false);
    expect(isDelegatedUserInstallation({})).toBe(false);
    expect(isDelegatedUserInstallation(undefined)).toBe(false);
  });

  test("mergeChannels dedupes by id, incoming overrides, new ids appended", () => {
    const current = [
      { id: "C1", name: "general" },
      { id: "C2", name: "random" },
    ];
    const incoming = [
      { id: "C1", name: "general-renamed" },
      { id: "C3", name: "new-channel" },
    ];
    expect(mergeChannels(current, incoming)).toEqual([
      { id: "C1", name: "general-renamed" },
      { id: "C2", name: "random" },
      { id: "C3", name: "new-channel" },
    ]);
    expect(mergeChannels(undefined, undefined)).toEqual([]);
    expect(mergeChannels(current, undefined)).toEqual(current);
  });

  test("channelSearchText joins name/id/privacy/membership, lowercased", () => {
    expect(
      channelSearchText({
        id: "C1",
        name: "General",
        is_private: true,
        is_member: false,
      }),
    ).toBe("general c1 private not joined invite bot");
    expect(channelSearchText({ id: "C2", name: "Ops", is_member: true })).toBe(
      "ops c2 public joined",
    );
  });
});

describe("slackChannels — per-client channel cache", () => {
  test("getChannelCacheEntry returns a safe default for an unknown installation", () => {
    const client = makeClient(async () => ({ channels: [], next_cursor: "" }));
    expect(getChannelCacheEntry(client, "inst-unknown")).toEqual({
      channels: [],
      nextCursor: "",
      complete: false,
      loading: false,
      error: "",
      completedAt: 0,
    });
  });

  test("cache identity is isolated per client (WeakMap keyed by client instance)", async () => {
    const clientA = makeClient(async () => ({
      channels: [{ id: "A1", name: "alpha" }],
      next_cursor: "",
    }));
    const clientB = makeClient(async () => ({
      channels: [{ id: "B1", name: "beta" }],
      next_cursor: "",
    }));
    await fetchAllChannelsInBackground(clientA, "inst-a");
    await fetchAllChannelsInBackground(clientB, "inst-a");
    expect(getChannelCacheEntry(clientA, "inst-a").channels).toEqual([
      { id: "A1", name: "alpha" },
    ]);
    expect(getChannelCacheEntry(clientB, "inst-a").channels).toEqual([
      { id: "B1", name: "beta" },
    ]);
  });

  test("fetchAllChannelsInBackground paginates until next_cursor is empty, using CHANNEL_PAGE_SIZE", async () => {
    const client = makeClient(async (_installationId, params) =>
      params.cursor
        ? { channels: [{ id: "C2", name: "page2" }], next_cursor: "" }
        : { channels: [{ id: "C1", name: "page1" }], next_cursor: "next" },
    );
    await fetchAllChannelsInBackground(client, "inst-a");
    expect(client.slack.listChannels.mock.calls[0][1]).toEqual(
      expect.objectContaining({ cursor: "", limit: CHANNEL_PAGE_SIZE }),
    );
    const entry = getChannelCacheEntry(client, "inst-a");
    expect(entry.complete).toBe(true);
    expect(entry.channels.map((c) => c.id)).toEqual(["C1", "C2"]);
  });

  test("dedups concurrent calls for the same client+installation (single in-flight fetch)", async () => {
    let releasePage;
    const gate = new Promise((resolve) => {
      releasePage = resolve;
    });
    const client = makeClient(async () => {
      await gate;
      return { channels: [{ id: "C1", name: "general" }], next_cursor: "" };
    });
    const first = fetchAllChannelsInBackground(client, "inst-a");
    const second = fetchAllChannelsInBackground(client, "inst-a");
    releasePage();
    await Promise.all([first, second]);
    expect(client.slack.listChannels.mock.calls.length).toBe(1);
  });

  test("a failed page preserves partial channels and resumes from the failed cursor", async () => {
    let attempt = 0;
    const client = makeClient(async (_installationId, params) => {
      if (!params.cursor) {
        return { channels: [{ id: "C1", name: "general" }], next_cursor: "p2" };
      }
      attempt += 1;
      if (attempt === 1)
        throw Object.assign(new Error("boom"), { code: "rate_limited" });
      return { channels: [{ id: "C2", name: "ops" }], next_cursor: "" };
    });
    await fetchAllChannelsInBackground(client, "inst-a");
    let entry = getChannelCacheEntry(client, "inst-a");
    expect(entry.complete).toBe(false);
    expect(entry.error).toContain("still rate limiting");
    expect(entry.channels.map((c) => c.id)).toEqual(["C1"]);

    await fetchAllChannelsInBackground(client, "inst-a");
    entry = getChannelCacheEntry(client, "inst-a");
    expect(entry.complete).toBe(true);
    expect(entry.error).toBe("");
    expect(entry.channels.map((c) => c.id)).toEqual(["C1", "C2"]);
    expect(
      client.slack.listChannels.mock.calls.map((call) => call[1].cursor),
    ).toEqual(["", "p2", "p2"]);
  });

  test("a complete, fresh entry is not refetched unless force is passed", async () => {
    const client = makeClient(async () => ({
      channels: [{ id: "C1", name: "general" }],
      next_cursor: "",
    }));
    await fetchAllChannelsInBackground(client, "inst-a");
    await fetchAllChannelsInBackground(client, "inst-a");
    expect(client.slack.listChannels.mock.calls.length).toBe(1);
    await fetchAllChannelsInBackground(client, "inst-a", { force: true });
    expect(client.slack.listChannels.mock.calls.length).toBe(2);
  });

  test("subscribeChannelCache notifies listeners and aborts the in-flight fetch when the last listener unsubscribes", async () => {
    let releasePage;
    const gate = new Promise((resolve) => {
      releasePage = resolve;
    });
    const client = makeClient(async (_installationId, _params, opts) => {
      await gate;
      if (opts?.signal?.aborted) {
        throw Object.assign(new Error("aborted"), { name: "AbortError" });
      }
      return { channels: [{ id: "C1", name: "general" }], next_cursor: "" };
    });
    const listener = jest.fn();
    const unsubscribe = subscribeChannelCache(client, "inst-a", listener);
    const fetchPromise = fetchAllChannelsInBackground(client, "inst-a");
    await new Promise((resolve) => setTimeout(resolve, 5));
    expect(listener).toHaveBeenCalled();
    const [, , opts] = client.slack.listChannels.mock.calls[0];

    unsubscribe();
    expect(opts.signal.aborted).toBe(true);
    releasePage();
    await fetchPromise;
    expect(getChannelCacheEntry(client, "inst-a").loading).toBe(false);
  });

  test("clearChannelCache aborts in-flight fetches, clears entries, and notifies listeners", async () => {
    let releasePage;
    const gate = new Promise((resolve) => {
      releasePage = resolve;
    });
    const client = makeClient(async (_installationId, _params, opts) => {
      await gate;
      if (opts?.signal?.aborted) {
        throw Object.assign(new Error("aborted"), { name: "AbortError" });
      }
      return { channels: [{ id: "C1", name: "general" }], next_cursor: "" };
    });
    const listener = jest.fn();
    subscribeChannelCache(client, "inst-a", listener);
    const fetchPromise = fetchAllChannelsInBackground(client, "inst-a");
    await new Promise((resolve) => setTimeout(resolve, 5));
    listener.mockClear();

    clearChannelCache(client);
    expect(listener).toHaveBeenCalled();
    expect(getChannelCacheEntry(client, "inst-a")).toEqual({
      channels: [],
      nextCursor: "",
      complete: false,
      loading: false,
      error: "",
      completedAt: 0,
    });

    releasePage();
    await fetchPromise;
  });
});
