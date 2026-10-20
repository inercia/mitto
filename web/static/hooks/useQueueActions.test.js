import {
  afterEach,
  beforeEach,
  describe,
  expect,
  jest,
  test,
} from "../utils/testing/testGlobals.js";

let slots;
let cursor;
let effects;
let cleanups;
let effectCursor;
const sameDeps = (a, b) =>
  a &&
  b &&
  a.length === b.length &&
  a.every((value, i) => Object.is(value, b[i]));

window.preact = {
  useState(initial) {
    const index = cursor++;
    if (!slots[index]) slots[index] = { value: initial };
    return [
      slots[index].value,
      (next) => {
        slots[index].value =
          typeof next === "function" ? next(slots[index].value) : next;
      },
    ];
  },
  useRef(initial) {
    const index = cursor++;
    if (!slots[index]) slots[index] = { value: { current: initial } };
    return slots[index].value;
  },
  useCallback(fn, deps) {
    const index = cursor++;
    const prior = slots[index];
    if (prior && sameDeps(prior.deps, deps)) return prior.value;
    slots[index] = { deps, value: fn };
    return fn;
  },
  useEffect(fn, deps) {
    effects.push({ fn, deps, index: effectCursor++ });
  },
};

beforeEach(() => {
  slots = [];
  cursor = 0;
  effects = [];
  cleanups = [];
  effectCursor = 0;
});

afterEach(() => {
  jest.restoreAllMocks();
});

async function loadHook() {
  const { useQueueActions } = await import("./useQueueActions.js");
  return {
    render: (activeSessionId, overrides = {}) => {
      cursor = 0;
      effectCursor = 0;
      effects = [];
      const result = useQueueActions({
        activeSessionId,
        showToast: jest.fn(),
        updateDraft: jest.fn(),
        fetchQueueMessages: jest.fn(),
        addToQueue: async () => ({ success: true }),
        deleteQueueMessage: jest.fn(),
        moveQueueMessage: jest.fn(),
        settingsDialogOpen: false,
        workspacesDialogOpen: false,
        showSidebar: false,
        ...overrides,
      });
      return result;
    },
    flushEffects() {
      for (const effect of effects) {
        const prior = cleanups[effect.index];
        if (!prior || !sameDeps(prior.deps, effect.deps)) {
          cleanups[effect.index] = {
            deps: effect.deps,
            cleanup: effect.fn(),
          };
        }
      }
    },
  };
}

describe("useQueueActions — active-session changes", () => {
  test("closes the dropdown and clears its auto-close timer on selection change", async () => {
    const clearTimeoutSpy = jest.spyOn(global, "clearTimeout");
    const harness = await loadHook();
    let actions = harness.render("session-A");
    harness.flushEffects();

    await actions.handleAddToQueue("hello");
    expect(actions.showQueueDropdown).toBe(false);
    actions = harness.render("session-A");
    expect(actions.showQueueDropdown).toBe(true);

    const timerId = slots[5].value.current;
    expect(timerId).toBeTruthy();
    actions = harness.render("session-B");
    harness.flushEffects();

    expect(clearTimeoutSpy).toHaveBeenCalledWith(timerId);
    actions = harness.render("session-B");
    expect(actions.showQueueDropdown).toBe(false);
    expect(slots[5].value.current).toBeNull();
  });
});
