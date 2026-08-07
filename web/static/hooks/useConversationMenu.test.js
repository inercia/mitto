/**
 * Tests for useConversationMenu.js — the "Copy" submenu added in mitto-a6v1.
 *
 * The hook (and its ContextMenu.js / Icons.js dependencies) destructure
 * useState/useMemo/useCallback/html from window.preact at module-load time,
 * so — mirroring useWorkspacePrompts.test.js / prompts.test.js
 * (buildPromptGroupMenuItems) — we install pass-through stubs BEFORE
 * dynamically importing the hook: useCallback/useMemo just invoke their
 * function (no memoization), useState returns [initial, noop], and html
 * returns a plain marker object so Icons.js/ContextMenu.js can render
 * without a real DOM.
 *
 * Per mitto-txpp.6, the shim uses per-field top-up (not a hard `window.preact
 * = {...}` assignment and not `window.preact || {...}`) so it survives
 * Bun's shared-process test runner regardless of file execution order.
 */

import {
  describe,
  test,
  expect,
  beforeAll,
} from "../utils/testing/testGlobals.js";

global.window = global.window || {};
window.preact = window.preact || {};
window.preact.html =
  window.preact.html ||
  ((strings, ...values) => ({ __htmlStub: true, strings, values }));

// useState is bound into useConversationMenu.js at MODULE-LOAD time (the
// single dynamic import in beforeAll below), so per-test reassignment of
// `window.preact.useState` afterward would NOT affect the already-captured
// reference. Instead, install one stateful closure now (hard assignment, not
// OR'd with any pre-existing stub from an earlier test file under Bun's
// shared-process runner) that stays bound forever but reads a mutable
// `menuPromptsOverride` variable on every call — tests toggle that variable
// (see withMenuPrompts below), not the useState function itself. The hook
// calls useState exactly twice per invocation, always in the same order
// (1st = contextMenu, 2nd = menuPrompts), so a simple parity counter
// distinguishes them without needing real per-render hook bookkeeping.
let menuPromptsOverride; // undefined = no override; real initial value flows through
let useStateCallCount = 0;
window.preact.useState = (initial) => {
  useStateCallCount++;
  if (useStateCallCount % 2 === 0 && menuPromptsOverride !== undefined) {
    return [menuPromptsOverride, () => {}];
  }
  return [typeof initial === "function" ? initial() : initial, () => {}];
};
window.preact.useMemo = window.preact.useMemo || ((fn) => fn());
window.preact.useCallback = window.preact.useCallback || ((fn) => fn);

let useConversationMenu;

beforeAll(async () => {
  ({ useConversationMenu } = await import("./useConversationMenu.js"));
});

// Finds a top-level contextMenuItems entry by its label.
function findItem(items, label) {
  return items.find((i) => i.label === label);
}

// Finds an entry inside a submenu by its label.
function findSub(item, label) {
  return (item.submenu || []).find((s) => s.label === label);
}

const SESSION = { name: "My Convo", session_id: "sess-1" };

describe("useConversationMenu — Copy entry (mitto-a6v1)", () => {
  test("no onCopyConversation → no Copy entry at all", () => {
    const { contextMenuItems } = useConversationMenu({ session: SESSION });
    expect(findItem(contextMenuItems, "Copy as Markdown")).toBeUndefined();
    expect(findItem(contextMenuItems, "Copy")).toBeUndefined();
  });

  test("onCopyConversation alone (no siblings) → flat 'Copy as Markdown' entry (back-compat, e.g. SessionItem.js)", () => {
    const onCopyConversation = () => {};
    const { contextMenuItems } = useConversationMenu({
      session: SESSION,
      onCopyConversation,
    });
    const flat = findItem(contextMenuItems, "Copy as Markdown");
    expect(flat).toBeDefined();
    expect(flat.submenu).toBeUndefined();
    expect(findItem(contextMenuItems, "Copy")).toBeUndefined();
  });

  test("flat entry onClick invokes onCopyConversation with the session", () => {
    const calls = [];
    const onCopyConversation = (s) => calls.push(s);
    const { contextMenuItems } = useConversationMenu({
      session: SESSION,
      onCopyConversation,
    });
    findItem(contextMenuItems, "Copy as Markdown").onClick();
    expect(calls).toEqual([SESSION]);
  });

  test("any sibling callback present → 'Copy' entry with a 4-action submenu", () => {
    const { contextMenuItems } = useConversationMenu({
      session: SESSION,
      onCopyConversation: () => {},
      onCopyConversationName: () => {},
    });
    expect(findItem(contextMenuItems, "Copy as Markdown")).toBeUndefined();
    const copy = findItem(contextMenuItems, "Copy");
    expect(copy).toBeDefined();
    expect(copy.submenu).toHaveLength(4);
    expect(copy.submenu.map((s) => s.label)).toEqual([
      "Copy conversation name",
      "Copy conversation ID",
      "Copy full contents as Markdown",
      "Copy last response as Markdown",
    ]);
  });

  test("each submenu entry onClick delegates to its own callback with the session", () => {
    const nameCalls = [];
    const idCalls = [];
    const mdCalls = [];
    const lastCalls = [];
    const { contextMenuItems } = useConversationMenu({
      session: SESSION,
      onCopyConversation: (s) => mdCalls.push(s),
      onCopyConversationName: (s) => nameCalls.push(s),
      onCopyConversationId: (s) => idCalls.push(s),
      onCopyLastResponse: (s) => lastCalls.push(s),
    });
    const copy = findItem(contextMenuItems, "Copy");
    findSub(copy, "Copy conversation name").onClick();
    findSub(copy, "Copy conversation ID").onClick();
    findSub(copy, "Copy full contents as Markdown").onClick();
    findSub(copy, "Copy last response as Markdown").onClick();
    expect(nameCalls).toEqual([SESSION]);
    expect(idCalls).toEqual([SESSION]);
    expect(mdCalls).toEqual([SESSION]);
    expect(lastCalls).toEqual([SESSION]);
  });

  test("disabled rules: name/ID disabled when session lacks name/session_id", () => {
    const { contextMenuItems } = useConversationMenu({
      session: { name: "", session_id: "" },
      onCopyConversation: () => {},
      onCopyConversationName: () => {},
      onCopyConversationId: () => {},
    });
    const copy = findItem(contextMenuItems, "Copy");
    expect(findSub(copy, "Copy conversation name").disabled).toBe(true);
    expect(findSub(copy, "Copy conversation ID").disabled).toBe(true);
  });

  test("name/ID entries enabled when session has a name/session_id", () => {
    const { contextMenuItems } = useConversationMenu({
      session: SESSION,
      onCopyConversation: () => {},
      onCopyConversationName: () => {},
      onCopyConversationId: () => {},
    });
    const copy = findItem(contextMenuItems, "Copy");
    expect(findSub(copy, "Copy conversation name").disabled).toBe(false);
    expect(findSub(copy, "Copy conversation ID").disabled).toBe(false);
  });

  test("hasConversationMarkdown / hasLastResponseMarkdown default to true (not disabled) when omitted", () => {
    const { contextMenuItems } = useConversationMenu({
      session: SESSION,
      onCopyConversation: () => {},
      onCopyLastResponse: () => {},
    });
    const copy = findItem(contextMenuItems, "Copy");
    expect(findSub(copy, "Copy full contents as Markdown").disabled).toBe(
      false,
    );
    expect(findSub(copy, "Copy last response as Markdown").disabled).toBe(
      false,
    );
  });

  test("hasConversationMarkdown:false disables the full-Markdown entry only", () => {
    const { contextMenuItems } = useConversationMenu({
      session: SESSION,
      onCopyConversation: () => {},
      onCopyLastResponse: () => {},
      hasConversationMarkdown: false,
    });
    const copy = findItem(contextMenuItems, "Copy");
    expect(findSub(copy, "Copy full contents as Markdown").disabled).toBe(
      true,
    );
    expect(findSub(copy, "Copy last response as Markdown").disabled).toBe(
      false,
    );
  });

  test("hasLastResponseMarkdown:false disables the last-response entry only", () => {
    const { contextMenuItems } = useConversationMenu({
      session: SESSION,
      onCopyConversation: () => {},
      onCopyConversationName: () => {},
      hasLastResponseMarkdown: false,
    });
    const copy = findItem(contextMenuItems, "Copy");
    expect(findSub(copy, "Copy last response as Markdown").disabled).toBe(
      true,
    );
    expect(findSub(copy, "Copy conversation name").disabled).toBe(false);
  });
});

// mitto-wnc8: on a conversation that is already configured as a loop, the
// context menu still lists loop-CONFIGURING prompts (loop.mode: "always"),
// which are meaningless there — decideLoopAction() resolves to "one-shot" for
// such a session, so the prompt's per-iteration body is injected as a single
// turn instead of reconfiguring the loop. Only mode:"optional" / mode:"none"
// prompts make sense as a one-shot send on an already-looping conversation.
//
// `menuPrompts` (the fetched menus:conversation prompts) is plain useState
// state with no setter available to callers, so the shared pass-through
// useState shim above (always returns the *initial* value, i.e. []) cannot
// seed it. We locally override window.preact.useState for the duration of
// each test in this block to return a fixed prompt list for the SECOND
// useState call inside the hook body (menuPrompts; the first call is
// contextMenu) — mirroring the positional-index stubbing convention used by
// useBeadsFolderConfig.test.js — then restore the shared shim afterward so
// later tests/files are unaffected.
describe("useConversationMenu — hide loop-configuring prompts on loop conversations (mitto-wnc8)", () => {
  const ALWAYS_LOOP_PROMPT = {
    name: "Loop fixing bug",
    group: "Development",
    loop: { mode: "always" },
  };
  const OPTIONAL_LOOP_PROMPT = {
    name: "Optional loop prompt",
    group: "Development",
    loop: { mode: "optional" },
  };
  const NONE_LOOP_PROMPT = {
    name: "Regular prompt",
    group: "Development",
  };
  const ONLY_ALWAYS_LOOP_PROMPT = {
    name: "Loop until complete",
    group: "Work flow",
    loop: { mode: "always" },
  };

  // Runs `fn` with the module-level `menuPromptsOverride` set so the hook's
  // SECOND useState() call (menuPrompts) returns `prompts` for the duration
  // of `fn`; always clears the override afterward so later tests default
  // back to the real initial value ([]).
  function withMenuPrompts(prompts, fn) {
    menuPromptsOverride = prompts;
    try {
      fn();
    } finally {
      menuPromptsOverride = undefined;
    }
  }

  test("loop-configured conversation: loop.mode:'always' prompts are hidden, optional/none prompts remain", () => {
    withMenuPrompts(
      [ALWAYS_LOOP_PROMPT, OPTIONAL_LOOP_PROMPT, NONE_LOOP_PROMPT],
      () => {
        const { promptGroupItems } = useConversationMenu({
          session: { session_id: "sess-1", loop_configured: true },
          isLoopConfigured: true,
          onSendPromptToConversation: () => {},
        });
        const devGroup = promptGroupItems.find(
          (g) => g.label === "Development",
        );
        const labels = (devGroup?.submenu || []).map((s) => s.label);
        expect(labels).not.toContain("Loop fixing bug");
        expect(labels).toEqual(["Optional loop prompt", "Regular prompt"]);
      },
    );
  });

  test("loop-configured conversation: a group containing ONLY loop.mode:'always' prompts disappears entirely", () => {
    withMenuPrompts([ONLY_ALWAYS_LOOP_PROMPT], () => {
      const { promptGroupItems } = useConversationMenu({
        session: { session_id: "sess-1", loop_configured: true },
        isLoopConfigured: true,
        onSendPromptToConversation: () => {},
      });
      expect(
        promptGroupItems.find((g) => g.label === "Work flow"),
      ).toBeUndefined();
    });
  });

  test("regular (non-loop, non-child) conversation: loop.mode:'always' prompts still appear", () => {
    withMenuPrompts(
      [ALWAYS_LOOP_PROMPT, OPTIONAL_LOOP_PROMPT, NONE_LOOP_PROMPT],
      () => {
        const { promptGroupItems } = useConversationMenu({
          session: { session_id: "sess-1" },
          isLoopConfigured: false,
          onSendPromptToConversation: () => {},
        });
        const devGroup = promptGroupItems.find(
          (g) => g.label === "Development",
        );
        const labels = (devGroup?.submenu || []).map((s) => s.label);
        expect(labels).toEqual([
          "Loop fixing bug",
          "Optional loop prompt",
          "Regular prompt",
        ]);
      },
    );
  });
});
