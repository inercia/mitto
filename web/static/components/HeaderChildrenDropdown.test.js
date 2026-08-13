/**
 * Unit tests for the "children dropdown" header-toolbar item added in
 * mitto-7vpp. The dropdown item is built inline in web/static/app.js as an
 * entry of `conversationToolbarItems`; because that module pulls in the
 * full app (preact/htm globals, WebSocket client, DOM), we duplicate the two
 * pure pieces of behaviour under test here (as Message.test.js does for
 * isModelErrorThought) and pin them to the acceptance criteria of the bead:
 *
 *   - Button visible on ≥ md, hidden on smaller (via `className`).
 *   - Menu lists the current conversation's children.
 *   - No children ⇒ item is absent from the toolbar array (empty-state hide).
 *   - Menu updates when children change (pure filter, so it is by construction).
 *   - Clicking an entry calls focusSession(child.session_id).
 *
 * Kept as a plain-JS unit test (jsdom) — no Playwright / server dependencies —
 * so it runs in `make test-js` alongside the other component tests.
 */

// -----------------------------------------------------------------------------
// Pure filter under test: mirrors the `activeChildren` useMemo in app.js.
// -----------------------------------------------------------------------------
function computeActiveChildren(allSessions, activeSessionId) {
  if (!activeSessionId) return [];
  return allSessions.filter(
    (s) => s.parent_session_id === activeSessionId && !s.archived,
  );
}

// -----------------------------------------------------------------------------
// Pure builder under test: mirrors the dropdown-item entry pushed into
// `conversationToolbarItems` in app.js. Returns either a single-element array
// (the dropdown item descriptor) or [] when the item must be hidden — matching
// the spread-guard pattern used at the call site.
// -----------------------------------------------------------------------------
function buildChildrenDropdownItems(activeSessionId, activeChildren, deps) {
  if (!activeSessionId || activeChildren.length === 0) return [];
  return [
    {
      kind: "dropdown",
      testId: "header-children-dropdown",
      // Count badge is rendered as a daisyUI `indicator` over the LayersIcon.
      // We surface the count and its testId here so the shape can be pinned
      // without pulling in preact/htm.
      indicator: {
        count: activeChildren.length,
        testId: "header-children-count",
      },
      tip: `Children (${activeChildren.length})`,
      ariaLabel: `Children of this conversation (${activeChildren.length})`,
      align: "end",
      portal: true,
      className: "hidden md:block",
      // Controlled-open + outside-click dismissal (mitto follow-up): the
      // dropdown's open state lives in the parent so clicks outside the pill
      // (or an Escape keypress) can close it via the Toolbar helper.
      open: deps.childrenMenuOpen,
      onToggle: deps.setChildrenMenuOpen,
      closeOnOutsideClick: true,
      // Concrete row descriptors (title + click handler + status flags) —
      // exercised by tests below instead of shipping the raw htm template,
      // which would require preact globals to render.
      rows: activeChildren.map((child) => ({
        key: child.session_id,
        testId: `header-children-item-${child.session_id}`,
        title: child.name || child.description || "Untitled",
        loop: !!child.loop_configured,
        streaming: !child.archived && !!child.isStreaming,
        origin: child.child_origin || null,
        waitingForChildren: !!child.isWaitingForChildren,
        waitingForUserInput: !!child.isWaitingForUserInput,
        onClick: () => {
          deps.setChildrenMenuOpen(false);
          deps.focusSession(child.session_id);
        },
      })),
    },
  ];
}

describe("computeActiveChildren (mitto-7vpp filter)", () => {
  const P = "parent-1";

  test("returns [] when no active session", () => {
    expect(computeActiveChildren([{ session_id: "a" }], null)).toEqual([]);
    expect(computeActiveChildren([{ session_id: "a" }], "")).toEqual([]);
    expect(computeActiveChildren([{ session_id: "a" }], undefined)).toEqual([]);
  });

  test("filters to sessions whose parent_session_id matches", () => {
    const sessions = [
      { session_id: "root", parent_session_id: "" },
      { session_id: "c1", parent_session_id: P },
      { session_id: "c2", parent_session_id: P },
      { session_id: "other-child", parent_session_id: "some-other-parent" },
    ];
    const kids = computeActiveChildren(sessions, P);
    expect(kids.map((c) => c.session_id)).toEqual(["c1", "c2"]);
  });

  test("excludes archived children", () => {
    const sessions = [
      { session_id: "c1", parent_session_id: P, archived: false },
      { session_id: "c2", parent_session_id: P, archived: true },
      { session_id: "c3", parent_session_id: P },
    ];
    const kids = computeActiveChildren(sessions, P);
    expect(kids.map((c) => c.session_id)).toEqual(["c1", "c3"]);
  });

  test("preserves input order (mirrors sidebar sort)", () => {
    const sessions = [
      { session_id: "c-z", parent_session_id: P },
      { session_id: "c-a", parent_session_id: P },
      { session_id: "c-m", parent_session_id: P },
    ];
    const kids = computeActiveChildren(sessions, P);
    expect(kids.map((c) => c.session_id)).toEqual(["c-z", "c-a", "c-m"]);
  });

  test("returns [] when the active session has no children", () => {
    const sessions = [
      { session_id: P, parent_session_id: "" },
      { session_id: "unrelated", parent_session_id: "root-2" },
    ];
    expect(computeActiveChildren(sessions, P)).toEqual([]);
  });

  test("returns [] when all children are archived", () => {
    const sessions = [
      { session_id: "c1", parent_session_id: P, archived: true },
      { session_id: "c2", parent_session_id: P, archived: true },
    ];
    expect(computeActiveChildren(sessions, P)).toEqual([]);
  });
});

// Minimal call spy — the ESM-loaded test harness (make test-js runs jest with
// --experimental-vm-modules) does not expose jest.fn() as a global, so mirror
// the BeadsView.test.js pattern instead.
function makeSpy() {
  const calls = [];
  const spy = (...args) => calls.push(args);
  spy.calls = calls;
  spy.callCount = () => calls.length;
  spy.lastCall = () => calls[calls.length - 1];
  spy.reset = () => {
    calls.length = 0;
  };
  return spy;
}

describe("buildChildrenDropdownItems (mitto-7vpp toolbar shape)", () => {
  const deps = {
    focusSession: makeSpy(),
    setChildrenMenuOpen: makeSpy(),
    childrenMenuOpen: false,
  };
  beforeEach(() => {
    deps.focusSession.reset();
    deps.setChildrenMenuOpen.reset();
    deps.childrenMenuOpen = false;
  });

  test("returns [] when no active session (empty-state hide)", () => {
    expect(buildChildrenDropdownItems(null, [], deps)).toEqual([]);
  });

  test("returns [] when active session has zero children (empty-state hide)", () => {
    // Acceptance: "When no children, the button is hidden."
    expect(buildChildrenDropdownItems("p1", [], deps)).toEqual([]);
  });

  test("returns exactly one dropdown descriptor when children exist", () => {
    const kids = [{ session_id: "c1", name: "Child One" }];
    const items = buildChildrenDropdownItems("p1", kids, deps);
    expect(items).toHaveLength(1);
    expect(items[0].kind).toBe("dropdown");
    expect(items[0].testId).toBe("header-children-dropdown");
  });

  test("advertises the child count in tip and aria-label", () => {
    const kids = [
      { session_id: "c1", name: "One" },
      { session_id: "c2", name: "Two" },
      { session_id: "c3", name: "Three" },
    ];
    const [item] = buildChildrenDropdownItems("p1", kids, deps);
    expect(item.tip).toBe("Children (3)");
    expect(item.ariaLabel).toBe("Children of this conversation (3)");
  });

  test("carries the desktop-only responsive className", () => {
    // Acceptance: "Button visible on ≥ md screens, hidden on smaller screens."
    const [item] = buildChildrenDropdownItems(
      "p1",
      [{ session_id: "c1", name: "x" }],
      deps,
    );
    expect(item.className).toBe("hidden md:block");
  });

  test("aligns the dropdown menu to the end (right-edge of the header)", () => {
    const [item] = buildChildrenDropdownItems(
      "p1",
      [{ session_id: "c1", name: "x" }],
      deps,
    );
    expect(item.align).toBe("end");
    expect(item.portal).toBe(true);
  });

  test("builds one row per child with a stable testId and per-row click handler", () => {
    const kids = [
      { session_id: "c1", name: "Child One" },
      { session_id: "c2", name: "Child Two" },
    ];
    const [item] = buildChildrenDropdownItems("p1", kids, deps);
    expect(item.rows.map((r) => r.testId)).toEqual([
      "header-children-item-c1",
      "header-children-item-c2",
    ]);
    expect(item.rows.map((r) => r.title)).toEqual(["Child One", "Child Two"]);
  });

  test("row title falls back through name → description → 'Untitled'", () => {
    const kids = [
      { session_id: "a", name: "Named", description: "ignored" },
      { session_id: "b", name: "", description: "From description" },
      { session_id: "c", name: null, description: null },
    ];
    const [item] = buildChildrenDropdownItems("p1", kids, deps);
    expect(item.rows.map((r) => r.title)).toEqual([
      "Named",
      "From description",
      "Untitled",
    ]);
  });

  test("row flags the child as a loop when loop_configured is set", () => {
    const kids = [
      { session_id: "loop", name: "L", loop_configured: true },
      { session_id: "plain", name: "P", loop_configured: false },
      { session_id: "no-flag", name: "N" },
    ];
    const [item] = buildChildrenDropdownItems("p1", kids, deps);
    expect(item.rows.map((r) => r.loop)).toEqual([true, false, false]);
  });

  test("row onClick closes the menu and then delegates to focusSession", () => {
    // Acceptance: "Clicking a child entry switches the main view to that child."
    // Follow-up: the dropdown also closes before switching, so the menu does
    // not remain visible over the newly focused conversation.
    const kids = [
      { session_id: "c1", name: "One" },
      { session_id: "c2", name: "Two" },
    ];
    const [item] = buildChildrenDropdownItems("p1", kids, deps);
    item.rows[1].onClick();
    expect(deps.setChildrenMenuOpen.callCount()).toBe(1);
    expect(deps.setChildrenMenuOpen.lastCall()).toEqual([false]);
    expect(deps.focusSession.callCount()).toBe(1);
    expect(deps.focusSession.lastCall()).toEqual(["c2"]);
  });

  test("exposes an indicator badge carrying the child count", () => {
    // Acceptance (follow-up 2): "A daisyUI indicator badge shows the number of
    // children on the toolbar icon."
    const kids = [
      { session_id: "c1", name: "One" },
      { session_id: "c2", name: "Two" },
      { session_id: "c3", name: "Three" },
    ];
    const [item] = buildChildrenDropdownItems("p1", kids, deps);
    expect(item.indicator).toEqual({
      count: 3,
      testId: "header-children-count",
    });
  });

  test("wires controlled open state + closeOnOutsideClick", () => {
    // Acceptance (follow-up 1): "Clicking outside the dropdown (or pressing
    // Escape) closes it." Delegated to the Toolbar via closeOnOutsideClick;
    // requires a controlled open/onToggle pair to actually close the DOM.
    deps.childrenMenuOpen = true;
    const [item] = buildChildrenDropdownItems(
      "p1",
      [{ session_id: "c1", name: "x" }],
      deps,
    );
    expect(item.closeOnOutsideClick).toBe(true);
    expect(item.open).toBe(true);
    expect(item.onToggle).toBe(deps.setChildrenMenuOpen);
  });

  test("row carries streaming / origin / waiting status flags", () => {
    // Acceptance (follow-up 3): "Each menu item shows a small status icon
    // matching the sidebar's visual language." The renderer picks the icon;
    // the row shape just needs the raw flags in the same slots the sidebar
    // reads them from.
    const kids = [
      {
        session_id: "c1",
        name: "streaming",
        isStreaming: true,
        child_origin: "auto",
      },
      {
        session_id: "c2",
        name: "waiting-children",
        child_origin: "mcp",
        isWaitingForChildren: true,
      },
      {
        session_id: "c3",
        name: "waiting-user",
        child_origin: "human",
        isWaitingForUserInput: true,
      },
      { session_id: "c4", name: "plain" },
      // Streaming flag on an archived child must NOT surface — mirrors the
      // sidebar (SessionItem: `isStreaming = !isArchived && ...`).
      {
        session_id: "c5",
        name: "archived-streaming",
        isStreaming: true,
        archived: true,
      },
    ];
    const [item] = buildChildrenDropdownItems("p1", kids, deps);
    expect(item.rows.map((r) => r.streaming)).toEqual([
      true,
      false,
      false,
      false,
      false,
    ]);
    expect(item.rows.map((r) => r.origin)).toEqual([
      "auto",
      "mcp",
      "human",
      null,
      null,
    ]);
    expect(item.rows.map((r) => r.waitingForChildren)).toEqual([
      false,
      true,
      false,
      false,
      false,
    ]);
    expect(item.rows.map((r) => r.waitingForUserInput)).toEqual([
      false,
      false,
      true,
      false,
      false,
    ]);
  });
});

// -----------------------------------------------------------------------------
// End-to-end pipeline: computeActiveChildren feeds buildChildrenDropdownItems,
// which pins the "live update" acceptance criterion — the item shape is a pure
// function of the sessions list, so a new WebSocket sessions push
// automatically re-renders the menu (React/preact rerun on props change).
// -----------------------------------------------------------------------------
describe("full pipeline (mitto-7vpp acceptance criteria)", () => {
  const deps = { focusSession: makeSpy() };
  const P = "parent-42";

  test("no children → item hidden", () => {
    const sessions = [{ session_id: P, parent_session_id: "" }];
    const kids = computeActiveChildren(sessions, P);
    expect(buildChildrenDropdownItems(P, kids, deps)).toEqual([]);
  });

  test("children spawned → item appears with one row per live child", () => {
    const sessions = [
      { session_id: P, parent_session_id: "" },
      { session_id: "c1", parent_session_id: P, name: "First" },
      { session_id: "c2", parent_session_id: P, name: "Second" },
    ];
    const kids = computeActiveChildren(sessions, P);
    const items = buildChildrenDropdownItems(P, kids, deps);
    expect(items).toHaveLength(1);
    expect(items[0].rows.map((r) => r.title)).toEqual(["First", "Second"]);
  });

  test("child archived → row disappears without any other change", () => {
    const before = [
      { session_id: P, parent_session_id: "" },
      { session_id: "c1", parent_session_id: P, name: "First" },
      { session_id: "c2", parent_session_id: P, name: "Second" },
    ];
    const after = before.map((s) =>
      s.session_id === "c2" ? { ...s, archived: true } : s,
    );
    const kidsBefore = computeActiveChildren(before, P);
    const kidsAfter = computeActiveChildren(after, P);
    expect(kidsBefore.map((c) => c.session_id)).toEqual(["c1", "c2"]);
    expect(kidsAfter.map((c) => c.session_id)).toEqual(["c1"]);
    const [itemAfter] = buildChildrenDropdownItems(P, kidsAfter, deps);
    expect(itemAfter.rows.map((r) => r.testId)).toEqual([
      "header-children-item-c1",
    ]);
  });
});
