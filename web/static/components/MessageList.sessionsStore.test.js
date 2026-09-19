/**
 * Unit tests for MessageList's read-side migration to
 * stores/sessionsStore.js (mitto-b1k).
 *
 * MessageList.js imports window.preact/htm globals at module load, so
 * (following MessageList.mcpInit.test.js's established pattern) it cannot
 * be imported directly under jsdom; the exact derivation is mirrored here
 * instead:
 *
 *   const messages = useActiveSessionMessages(activeSessionId);
 *   const displayMessages = useMemo(
 *     () => coalesceAgentMessages(messages, {
 *       hrBreaksCoalescing: COALESCE_DEFAULTS.hrBreaksCoalescing,
 *     }),
 *     [messages],
 *   );
 *
 * Rather than going through the Preact hook wrapper (hooks/useSessionsStore.js,
 * which destructures useState/useEffect from window.preact ONCE at module
 * load -- stubbing that safely from more than one test file would require
 * coordinating which file's stub "wins" the shared module cache, which
 * stores/sessionsStore.test.js's file header already flags as the reason it
 * imports the store directly), this file drives the SAME underlying store
 * (getMessages/replaceAll, which have no window dependency -- see
 * stores/sessionsStore.test.js) that the hook itself reads from. The hook
 * wrapper's own subscribe/unsubscribe machinery is already covered by
 * useSessionsStore.test.js and the store's per-slice notification isolation
 * by stores/sessionsStore.test.js; this file covers the piece unique to
 * mitto-b1k: MessageList's own composition of that store data with
 * coalesceAgentMessages.
 */

import {
  describe,
  test,
  expect,
  beforeEach,
} from "../utils/testing/testGlobals.js";

import {
  getMessages,
  replaceAll,
  _resetSessionsStoreForTests,
} from "../stores/sessionsStore.js";
import {
  coalesceAgentMessages,
  COALESCE_DEFAULTS,
  ROLE_AGENT,
  ROLE_USER,
} from "../lib.js";

// Mirrors MessageList.js's derivation verbatim (see mitto-b1k Implementation
// comment / git show e4359425 -- MessageList.js), modulo the useMemo
// wrapper: useActiveSessionMessages(id) === getMessages(id) || [] (see
// hooks/useSessionsStore.js), and useMemo's role here is purely a caching
// optimization over this same pure computation -- not additional logic to
// verify.
function deriveDisplayMessages(sessionId) {
  const messages = getMessages(sessionId) || [];
  return coalesceAgentMessages(messages, {
    hrBreaksCoalescing: COALESCE_DEFAULTS.hrBreaksCoalescing,
  });
}

beforeEach(() => {
  _resetSessionsStoreForTests();
});

describe("MessageList sessionsStore migration (mitto-b1k) > basic derivation", () => {
  test("an unset session yields an empty displayMessages array", () => {
    expect(deriveDisplayMessages("nope")).toEqual([]);
  });

  test("coalesces adjacent agent messages via the real coalesceAgentMessages/COALESCE_DEFAULTS wiring", () => {
    replaceAll({
      s1: {
        messages: [
          { role: ROLE_AGENT, seq: 1, html: "<p>Hello</p>" },
          { role: ROLE_AGENT, seq: 2, html: "<p>World</p>" },
          { role: ROLE_USER, seq: 3, html: "<p>Thanks</p>" },
        ],
      },
    });

    const result = deriveDisplayMessages("s1");

    // Two adjacent agent messages coalesce into one; the user message stays
    // separate -- matches lib.test.js's coalesceAgentMessages contract, and
    // proves this call site passes hrBreaksCoalescing through from
    // COALESCE_DEFAULTS rather than omitting/hardcoding the option.
    expect(result).toHaveLength(2);
    expect(result[0].role).toBe(ROLE_AGENT);
    expect(result[0].html).toBe("<p>Hello</p><p>World</p>");
    expect(result[0].coalescedSeqs).toEqual([1, 2]);
    expect(result[1].role).toBe(ROLE_USER);
  });

  test("an <hr/>-only agent message breaks coalescing and is dropped (hrBreaksCoalescing wired through)", () => {
    replaceAll({
      s1: {
        messages: [
          { role: ROLE_AGENT, seq: 1, html: "<p>Hello</p>" },
          { role: ROLE_AGENT, seq: 2, html: "<hr/>" },
          { role: ROLE_AGENT, seq: 3, html: "<p>World</p>" },
        ],
      },
    });

    const result = deriveDisplayMessages("s1");

    expect(result).toHaveLength(2);
    expect(result[0].html).toBe("<p>Hello</p>");
    expect(result[1].html).toBe("<p>World</p>");
  });
});

describe("MessageList sessionsStore migration (mitto-b1k) > render isolation", () => {
  test("a background session's chunk does not change the active session's raw messages reference (the input useMemo keys off)", () => {
    const s1Messages = [{ role: ROLE_USER, seq: 1, html: "<p>active</p>" }];
    replaceAll({
      s1: { messages: s1Messages },
      s2: {
        messages: [{ role: ROLE_USER, seq: 1, html: "<p>background</p>" }],
      },
    });

    const before = getMessages("s1");

    // Only session s2 (background) receives a new chunk. replaceAll's own
    // diffing (stores/sessionsStore.test.js) already proves the "info"
    // subscriber isn't woken; what matters for the MessageList migration
    // specifically is that s1's `messages` array is the exact same
    // reference the reused-object caller passed, so the real component's
    // `useMemo(..., [messages])` never recomputes displayMessages for the
    // untouched session.
    replaceAll({
      s1: { messages: s1Messages },
      s2: {
        messages: [
          { role: ROLE_USER, seq: 1, html: "<p>background</p>" },
          { role: ROLE_USER, seq: 2, html: "<p>background 2</p>" },
        ],
      },
    });

    const after = getMessages("s1");
    expect(after).toBe(before);
    expect(deriveDisplayMessages("s1")).toEqual(
      coalesceAgentMessages(s1Messages, {
        hrBreaksCoalescing: COALESCE_DEFAULTS.hrBreaksCoalescing,
      }),
    );
  });

  test("switching the active session id adopts the new session's messages", () => {
    replaceAll({
      s1: { messages: [{ role: ROLE_USER, seq: 1, html: "<p>s1</p>" }] },
      s2: { messages: [{ role: ROLE_USER, seq: 1, html: "<p>s2</p>" }] },
    });

    expect(deriveDisplayMessages("s1").map((m) => m.html)).toEqual([
      "<p>s1</p>",
    ]);
    expect(deriveDisplayMessages("s2").map((m) => m.html)).toEqual([
      "<p>s2</p>",
    ]);
  });
});
