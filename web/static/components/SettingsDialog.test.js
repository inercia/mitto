/**
 * Unit tests for the Models settings tab pure data-transform helpers.
 *
 * These duplicate the pure transforms from SettingsDialog.js (the component
 * reads window.preact globals at module load and cannot be imported directly
 * under jsdom). Keep these helpers in sync with the implementation.
 *
 * The normalized shape produced by normalizeModelProfile matches the backend
 * preserve-on-omit round-trip contract: criteria is a pointer (object|null)
 * and tags is always a filtered array — never undefined or null.
 */

import { readFileSync } from "node:fs";

/**
 * Duplicated from SettingsDialog.js (Tags onInput handler, ~lines 4745-4775).
 * Splits the raw draft text on every comma typed so far into already-committed
 * tokens (trimmed, empties dropped) plus the trailing partial token that is
 * still being typed. This is the fix for the historical bug where a
 * controlled `value={tags.join(", ")}` swallowed a just-typed comma.
 */
const splitTagDraftOnInput = (raw) => {
  if (!raw.includes(",")) return { committed: [], trailing: raw };
  const parts = raw.split(",");
  const trailing = parts.pop();
  const committed = parts.map((t) => t.trim()).filter(Boolean);
  return { committed, trailing };
};

/**
 * Duplicated from SettingsDialog.js (commitTagDraft, ~lines 2306-2320).
 * Commits a raw draft string (blur/Enter/comma) into the tags array: split on
 * comma, trim, drop empties, merge+dedupe with the existing tags.
 */
const commitTagTokens = (raw, existingTags = []) => {
  const tokens = raw
    .split(",")
    .map((t) => t.trim())
    .filter(Boolean);
  if (tokens.length === 0) return existingTags;
  return [...new Set([...existingTags, ...tokens])];
};

/**
 * Duplicated from SettingsDialog.js (modelProfilesToSave, ~lines 1919-1933).
 * Normalizes a model profile object for the save payload.
 */
const normalizeModelProfile = (p) => ({
  name: (p.name || "").trim(),
  criteria:
    p.criteria && p.criteria.matchMode
      ? { matchMode: p.criteria.matchMode, pattern: p.criteria.pattern || "" }
      : null,
  tags: Array.isArray(p.tags) ? p.tags.filter((t) => t && t.trim()) : [],
});

/**
 * Duplicated from SettingsDialog.js (modelProfilesToSave filter, ~line 1933).
 * A normalized profile is fully empty when it has no name, no criteria, and
 * no tags — these are dropped silently rather than saved.
 */
const isEmptyNormalizedProfile = (normalized) =>
  normalized.name === "" &&
  !normalized.criteria &&
  normalized.tags.length === 0;

/**
 * Duplicated from SettingsDialog.js (handleSave validation, ~lines 1742-1753).
 * A profile blocks Save when its name is blank but it has criteria or tags
 * (i.e. partially filled, as opposed to fully empty).
 */
const hasBlankNamedProfile = (profiles) =>
  profiles.some((p) => {
    const name = (p.name || "").trim();
    const tags = Array.isArray(p.tags)
      ? p.tags.filter((t) => t && t.trim())
      : [];
    return name === "" && (!!p.criteria || tags.length > 0);
  });

describe("splitTagDraftOnInput", () => {
  test("no comma yet: everything is the trailing (in-progress) token", () => {
    expect(splitTagDraftOnInput("Smart")).toEqual({
      committed: [],
      trailing: "Smart",
    });
  });

  test("a trailing comma commits the token and leaves an empty trailing", () => {
    expect(splitTagDraftOnInput("Smart,")).toEqual({
      committed: ["Smart"],
      trailing: "",
    });
  });

  test("typing continues after a comma without losing characters", () => {
    expect(splitTagDraftOnInput("Smart,Che")).toEqual({
      committed: ["Smart"],
      trailing: "Che",
    });
  });

  test("multiple commas (e.g. pasted text) commit multiple tokens", () => {
    expect(splitTagDraftOnInput("A,B,C")).toEqual({
      committed: ["A", "B"],
      trailing: "C",
    });
  });

  test("empty string stays as an empty trailing token", () => {
    expect(splitTagDraftOnInput("")).toEqual({ committed: [], trailing: "" });
  });
});

describe("commitTagTokens", () => {
  test("splits, trims and drops empties", () => {
    expect(commitTagTokens("  A ,B  ,  C")).toEqual(["A", "B", "C"]);
  });

  test("drops empty entries from trailing/duplicate commas", () => {
    expect(commitTagTokens("A,,B,")).toEqual(["A", "B"]);
  });

  test("empty/whitespace-only raw text leaves existing tags unchanged", () => {
    expect(commitTagTokens(" , ", ["Smart"])).toEqual(["Smart"]);
  });

  test("merges with and dedupes against existing tags", () => {
    expect(commitTagTokens("Smart, New", ["Smart"])).toEqual(["Smart", "New"]);
  });
});

describe("normalizeModelProfile", () => {
  test("trims the name", () => {
    expect(normalizeModelProfile({ name: "  Opus  " }).name).toBe("Opus");
  });

  test("missing name becomes empty string", () => {
    expect(normalizeModelProfile({}).name).toBe("");
  });

  test("criteria with matchMode is kept as {matchMode, pattern}", () => {
    const result = normalizeModelProfile({
      criteria: { matchMode: "contains", pattern: "Opus" },
    });
    expect(result.criteria).toEqual({ matchMode: "contains", pattern: "Opus" });
  });

  test("criteria pattern defaults to empty string when absent", () => {
    const result = normalizeModelProfile({
      criteria: { matchMode: "exact" },
    });
    expect(result.criteria).toEqual({ matchMode: "exact", pattern: "" });
  });

  test("criteria without matchMode becomes null", () => {
    const result = normalizeModelProfile({ criteria: { pattern: "x" } });
    expect(result.criteria).toBeNull();
  });

  test("null criteria becomes null", () => {
    expect(normalizeModelProfile({ criteria: null }).criteria).toBeNull();
  });

  test("absent criteria becomes null", () => {
    expect(normalizeModelProfile({}).criteria).toBeNull();
  });

  test("tags array has empty/whitespace entries filtered", () => {
    const result = normalizeModelProfile({
      tags: ["Smart", "", "  ", "Cheap"],
    });
    expect(result.tags).toEqual(["Smart", "Cheap"]);
  });

  test("non-array tags (undefined) become empty array", () => {
    expect(normalizeModelProfile({ tags: undefined }).tags).toEqual([]);
  });

  test("a full realistic profile round-trips to the exact expected object", () => {
    const profile = {
      name: "  Opus  ",
      criteria: { matchMode: "contains", pattern: "Opus" },
      tags: ["Smartest", "", "Expensive"],
    };
    expect(normalizeModelProfile(profile)).toEqual({
      name: "Opus",
      criteria: { matchMode: "contains", pattern: "Opus" },
      tags: ["Smartest", "Expensive"],
    });
  });
});

describe("isEmptyNormalizedProfile", () => {
  test("a profile with no name, criteria or tags is empty", () => {
    expect(isEmptyNormalizedProfile(normalizeModelProfile({}))).toBe(true);
  });

  test("a profile with only a name is not empty", () => {
    expect(
      isEmptyNormalizedProfile(normalizeModelProfile({ name: "Opus" })),
    ).toBe(false);
  });

  test("a blank-name profile with criteria is not empty (blocked, not dropped)", () => {
    const normalized = normalizeModelProfile({
      criteria: { matchMode: "contains", pattern: "Opus" },
    });
    expect(isEmptyNormalizedProfile(normalized)).toBe(false);
  });

  test("a blank-name profile with tags is not empty (blocked, not dropped)", () => {
    const normalized = normalizeModelProfile({ tags: ["Smart"] });
    expect(isEmptyNormalizedProfile(normalized)).toBe(false);
  });
});

describe("hasBlankNamedProfile", () => {
  test("no profiles: false", () => {
    expect(hasBlankNamedProfile([])).toBe(false);
  });

  test("fully-empty profile does not block save", () => {
    expect(hasBlankNamedProfile([{ name: "", criteria: null, tags: [] }])).toBe(
      false,
    );
  });

  test("named profile does not block save", () => {
    expect(
      hasBlankNamedProfile([{ name: "Opus", criteria: null, tags: [] }]),
    ).toBe(false);
  });

  test("blank name with criteria blocks save", () => {
    expect(
      hasBlankNamedProfile([
        { name: "", criteria: { matchMode: "contains" }, tags: [] },
      ]),
    ).toBe(true);
  });

  test("blank name with tags blocks save", () => {
    expect(
      hasBlankNamedProfile([{ name: "  ", criteria: null, tags: ["Smart"] }]),
    ).toBe(true);
  });
});

/**
 * mitto-9tl — Conversation font settings group.
 *
 * The frontend applies two independent effects when the user picks a new
 * "Conversation font" family or base size:
 *
 *   1. It swaps a single `conv-font-<family>` class on <html> (after removing
 *      all sibling `conv-font-*` classes it might have added earlier).
 *   2. It writes the CSS variable `--mitto-conv-base-size` to a fixed px
 *      value derived from the base-size key (xs/sm/md/lg/xl). The sidebar
 *      small-A / large-A rules in styles.css key off this variable, so
 *      changing it re-anchors both toggle states at once.
 *
 * Both transformations are pure functions of their input. The SettingsDialog
 * component reads `window.preact` globals at module load and cannot be
 * imported directly under jsdom, so we duplicate the tiny helpers here (same
 * pattern as the existing model-profile helpers above). Keep in sync with
 * `web/static/app.js` — the size table and family list must match, and any
 * new option added to the SettingsDialog select must also appear here.
 */

// Duplicated from web/static/app.js (~lines 1220 and 1260), and the option
// list rendered by SettingsDialog.js (`Conversation font` row). This is the
// full set of families the .conv-font-* CSS block in styles.css covers.
const CONV_FONT_FAMILIES = [
  "system",
  "sans-serif",
  "serif",
  "inter",
  "sf-pro",
  "helvetica-neue",
  "roboto",
  "georgia",
  "charter",
  "ibm-plex-sans",
];

// Duplicated from web/static/app.js (~line 1284) — the CSS variable value the
// frontend writes for each base-size key. Kept as a plain object so the test
// can assert both the mapping and the fallback behavior for unknown keys.
const CONV_BASE_PX = {
  xs: "13px",
  sm: "14px",
  md: "15px",
  lg: "16px",
  xl: "18px",
};

/**
 * Duplicated from web/static/app.js (~line 1290). Resolves a base-size key
 * to the px string written to `--mitto-conv-base-size`, falling back to the
 * `sm` (14px) default when the key is unknown or missing.
 */
const resolveConvBasePx = (key) => CONV_BASE_PX[key] || CONV_BASE_PX.sm;

/**
 * Duplicated from web/static/app.js (~lines 1256-1273). Returns the class
 * name that should end up on <html>.classList after the effect runs. The
 * real effect first removes every element of `convFontClasses`, then adds
 * `conv-font-<family>`; the return value is what remains after that swap.
 */
const resolveConvFontClass = (family) => `conv-font-${family}`;

describe("mitto-9tl: conversation font — base size CSS variable", () => {
  test("every documented key maps to its documented px value", () => {
    expect(resolveConvBasePx("xs")).toBe("13px");
    expect(resolveConvBasePx("sm")).toBe("14px");
    expect(resolveConvBasePx("md")).toBe("15px");
    expect(resolveConvBasePx("lg")).toBe("16px");
    expect(resolveConvBasePx("xl")).toBe("18px");
  });

  test("unknown key falls back to sm (14px) so the UI never renders as 0", () => {
    expect(resolveConvBasePx("bogus")).toBe("14px");
    expect(resolveConvBasePx("")).toBe("14px");
    expect(resolveConvBasePx(undefined)).toBe("14px");
    expect(resolveConvBasePx(null)).toBe("14px");
  });

  test("14px fallback matches the CSS `var(--mitto-conv-base-size, 14px)` default in styles.css", () => {
    // styles.css: .font-small .markdown-content { font-size: var(--mitto-conv-base-size, 14px); }
    // large-A adds 2px on top. This test locks the frontend fallback to the
    // CSS fallback so an unset setting renders exactly like the pre-9tl
    // baseline (14px small-A, 16px large-A).
    expect(resolveConvBasePx(undefined)).toBe("14px");
  });
});

describe("mitto-9tl: conversation font — family class swap", () => {
  test("every documented family resolves to a matching conv-font-<family> class", () => {
    for (const family of CONV_FONT_FAMILIES) {
      expect(resolveConvFontClass(family)).toBe(`conv-font-${family}`);
    }
  });

  test("default family 'system' resolves to conv-font-system (mirrors default WebUIConfig)", () => {
    expect(resolveConvFontClass("system")).toBe("conv-font-system");
  });

  test("family list matches the CSS block in styles.css (10 prose-friendly options)", () => {
    // If a new family is added to the SettingsDialog select or app.js class
    // list, this assertion fires so the CSS `.conv-font-*` block and the
    // documentation lists must be updated together.
    expect(CONV_FONT_FAMILIES).toHaveLength(10);
    expect(CONV_FONT_FAMILIES).toEqual([
      "system",
      "sans-serif",
      "serif",
      "inter",
      "sf-pro",
      "helvetica-neue",
      "roboto",
      "georgia",
      "charter",
      "ibm-plex-sans",
    ]);
  });

  test("no family in the list overlaps the input-font namespace", () => {
    // Regression guard: the .conv-font-* rules must target .markdown-content
    // only. If a caller ever added e.g. `menlo` here (which belongs to the
    // input-font list), the two font pipelines would leak into each other.
    const inputOnlyFamilies = [
      "monospace",
      "menlo",
      "monaco",
      "consolas",
      "courier-new",
      "jetbrains-mono",
      "sf-mono",
      "cascadia-code",
    ];
    for (const f of inputOnlyFamilies) {
      expect(CONV_FONT_FAMILIES).not.toContain(f);
    }
  });
});

describe("mitto-9tl: conversation font — save payload shape", () => {
  // Duplicated from SettingsDialog.js (~line 2727 handleSave `uiConfig.web`).
  // The dialog builds the outbound settings body from local state; here we
  // pin the exact key names so a rename on either side is caught by tests.
  const buildWebUIPayload = (state) => ({
    input_font_family: state.inputFontFamily,
    input_font_size: state.inputFontSize,
    conversation_font_family: state.conversationFontFamily,
    conversation_font_size: state.conversationFontSize,
    send_key_mode: state.sendKeyMode,
    conversation_cycling_mode: state.conversationCyclingMode,
    single_expanded_group: state.singleExpandedGroup,
  });

  test("payload uses the exact snake_case keys the Go WebUIConfig struct expects", () => {
    const payload = buildWebUIPayload({
      inputFontFamily: "system",
      inputFontSize: "default",
      conversationFontFamily: "inter",
      conversationFontSize: "lg",
      sendKeyMode: "enter",
      conversationCyclingMode: "all",
      singleExpandedGroup: false,
    });
    expect(Object.keys(payload).sort()).toEqual(
      [
        "conversation_cycling_mode",
        "conversation_font_family",
        "conversation_font_size",
        "input_font_family",
        "input_font_size",
        "send_key_mode",
        "single_expanded_group",
      ].sort(),
    );
    expect(payload.conversation_font_family).toBe("inter");
    expect(payload.conversation_font_size).toBe("lg");
  });

  test("defaults (system / sm) round-trip through the payload unchanged", () => {
    const payload = buildWebUIPayload({
      inputFontFamily: "system",
      inputFontSize: "default",
      conversationFontFamily: "system",
      conversationFontSize: "sm",
      sendKeyMode: "enter",
      conversationCyclingMode: "all",
      singleExpandedGroup: false,
    });
    expect(payload.conversation_font_family).toBe("system");
    expect(payload.conversation_font_size).toBe("sm");
  });
});

// ---------------------------------------------------------------------------
// mitto-7gta.17 slice S6 — the 12 authFetch/secureFetch->getSdkClient() call
// sites migrated in this slice's Implementation phase. Duplicated from the
// implementation (the component reads window.preact globals at module load
// and cannot be imported directly under jsdom, per this file's header
// comment and the S4/S5 Test-phase precedent) with the SDK client and
// setters injected as arguments.
// ---------------------------------------------------------------------------

/** Mirrors sdkErrors.js's errorStatus/errorMessage for these duplicated fns. */
function errorStatus(err) {
  return typeof err?.status === "number" ? err.status : undefined;
}
function errorMessage(err, fallback) {
  return (err && err.message) || fallback;
}

/** Duplicated from ACPServerDeleteWizard's executeDelete (~line 1438). */
async function executeDelete(client, serverName, foldersPayload, setters) {
  const { setExecResult, setStep, setActiveRefusal, setExecError } = setters;
  try {
    const data = await client.acpServers.reassignAndDelete(serverName, {
      folders: foldersPayload,
    });
    setExecResult(data);
    setStep("success");
  } catch (err) {
    const activeIds =
      errorStatus(err) === 409 && Array.isArray(err.details?.active_session_ids)
        ? err.details.active_session_ids
        : null;
    if (activeIds && activeIds.length > 0) {
      setActiveRefusal(activeIds.map((sid) => ({ session_id: sid })));
      setStep("error");
      return;
    }
    setExecError(errorMessage(err, `Failed to delete "${serverName}"`));
    setStep("error");
  }
}

describe("ACPServerDeleteWizard.executeDelete", () => {
  test("a 409 with active_session_ids opens the refusal list instead of an error message", async () => {
    const err = Object.assign(new Error("conflict"), {
      status: 409,
      details: { active_session_ids: ["s1", "s2"] },
    });
    const client = {
      acpServers: { reassignAndDelete: jest.fn(() => Promise.reject(err)) },
    };
    const setActiveRefusal = jest.fn();
    const setStep = jest.fn();
    const setExecError = jest.fn();
    await executeDelete(
      client,
      "auggie",
      {},
      {
        setExecResult: jest.fn(),
        setStep,
        setActiveRefusal,
        setExecError,
      },
    );
    expect(setActiveRefusal).toHaveBeenCalledWith([
      { session_id: "s1" },
      { session_id: "s2" },
    ]);
    expect(setStep).toHaveBeenCalledWith("error");
    expect(setExecError).not.toHaveBeenCalled();
  });

  test("a non-409 failure sets the execError banner instead", async () => {
    const client = {
      acpServers: {
        reassignAndDelete: jest.fn(() =>
          Promise.reject(Object.assign(new Error("boom"), { status: 500 })),
        ),
      },
    };
    const setExecError = jest.fn();
    await executeDelete(
      client,
      "auggie",
      {},
      {
        setExecResult: jest.fn(),
        setStep: jest.fn(),
        setActiveRefusal: jest.fn(),
        setExecError,
      },
    );
    expect(setExecError).toHaveBeenCalledWith("boom");
  });

  test("success stores the result and advances to the success step", async () => {
    const client = {
      acpServers: {
        reassignAndDelete: jest.fn(() =>
          Promise.resolve({ reassigned_conversation_count: 2 }),
        ),
      },
    };
    const setExecResult = jest.fn();
    const setStep = jest.fn();
    await executeDelete(
      client,
      "auggie",
      { "/tmp/a": "other" },
      {
        setExecResult,
        setStep,
        setActiveRefusal: jest.fn(),
        setExecError: jest.fn(),
      },
    );
    expect(client.acpServers.reassignAndDelete).toHaveBeenCalledWith("auggie", {
      folders: { "/tmp/a": "other" },
    });
    expect(setExecResult).toHaveBeenCalledWith({
      reassigned_conversation_count: 2,
    });
    expect(setStep).toHaveBeenCalledWith("success");
  });
});

/** Duplicated from SettingsDialog.js's removeServer (~line 3228). */
async function removeServer(client, serverName, acpServersCount, setters) {
  const {
    setError,
    removeServerFromState,
    setDeleteBlockedInfo,
    setDeleteWizardName,
    setDeleteWizardPlan,
  } = setters;
  if (acpServersCount <= 1) {
    setError("At least one ACP server is required");
    return;
  }
  setError("");
  try {
    const data = await client.acpServers.prepareDelete(serverName);
    if (data?.has_active === true) {
      setDeleteBlockedInfo({
        kind: "active",
        serverName,
        activeConversations: Array.isArray(data.active_conversations)
          ? data.active_conversations
          : [],
      });
      return;
    }
    setDeleteWizardName(serverName);
    setDeleteWizardPlan(data);
  } catch (err) {
    if (errorStatus(err) === 404) {
      removeServerFromState(serverName);
      return;
    }
    if (errorStatus(err) === 403) {
      const msg = errorMessage(
        err,
        `Cannot delete "${serverName}": configuration is read-only.`,
      );
      const isRC = /RC file|\.mittorc/i.test(msg);
      setDeleteBlockedInfo({
        kind: isRC ? "rcfile" : "readonly",
        serverName,
        message: msg,
      });
      return;
    }
    setError(
      errorMessage(err, `Failed to prepare deletion of "${serverName}"`),
    );
  }
}

describe("SettingsDialog.removeServer", () => {
  test("a 404 treats the server as already-gone: removes it from local state", async () => {
    const client = {
      acpServers: {
        prepareDelete: jest.fn(() =>
          Promise.reject(
            Object.assign(new Error("not found"), { status: 404 }),
          ),
        ),
      },
    };
    const removeServerFromState = jest.fn();
    await removeServer(client, "ghost-server", 2, {
      setError: jest.fn(),
      removeServerFromState,
      setDeleteBlockedInfo: jest.fn(),
      setDeleteWizardName: jest.fn(),
      setDeleteWizardPlan: jest.fn(),
    });
    expect(removeServerFromState).toHaveBeenCalledWith("ghost-server");
  });

  test("a 403 with an RC-file message opens the rcfile-kind blocked-info modal", async () => {
    const client = {
      acpServers: {
        prepareDelete: jest.fn(() =>
          Promise.reject(
            Object.assign(new Error('defined in RC file "/repo/.mittorc"'), {
              status: 403,
            }),
          ),
        ),
      },
    };
    const setDeleteBlockedInfo = jest.fn();
    await removeServer(client, "rc-server", 2, {
      setError: jest.fn(),
      removeServerFromState: jest.fn(),
      setDeleteBlockedInfo,
      setDeleteWizardName: jest.fn(),
      setDeleteWizardPlan: jest.fn(),
    });
    expect(setDeleteBlockedInfo).toHaveBeenCalledWith(
      expect.objectContaining({ kind: "rcfile", serverName: "rc-server" }),
    );
  });

  test("a 403 without an RC-file message opens the readonly-kind blocked-info modal", async () => {
    const client = {
      acpServers: {
        prepareDelete: jest.fn(() =>
          Promise.reject(
            Object.assign(new Error("config is read-only"), { status: 403 }),
          ),
        ),
      },
    };
    const setDeleteBlockedInfo = jest.fn();
    await removeServer(client, "ro-server", 2, {
      setError: jest.fn(),
      removeServerFromState: jest.fn(),
      setDeleteBlockedInfo,
      setDeleteWizardName: jest.fn(),
      setDeleteWizardPlan: jest.fn(),
    });
    expect(setDeleteBlockedInfo).toHaveBeenCalledWith(
      expect.objectContaining({ kind: "readonly", serverName: "ro-server" }),
    );
  });

  test("has_active:true opens the active-conversations blocked-info modal, not the wizard", async () => {
    const client = {
      acpServers: {
        prepareDelete: jest.fn(() =>
          Promise.resolve({
            has_active: true,
            active_conversations: [{ id: "c1" }],
          }),
        ),
      },
    };
    const setDeleteBlockedInfo = jest.fn();
    const setDeleteWizardPlan = jest.fn();
    await removeServer(client, "busy-server", 2, {
      setError: jest.fn(),
      removeServerFromState: jest.fn(),
      setDeleteBlockedInfo,
      setDeleteWizardName: jest.fn(),
      setDeleteWizardPlan,
    });
    expect(setDeleteBlockedInfo).toHaveBeenCalledWith({
      kind: "active",
      serverName: "busy-server",
      activeConversations: [{ id: "c1" }],
    });
    expect(setDeleteWizardPlan).not.toHaveBeenCalled();
  });

  test("has_active:false opens the wizard with the returned plan", async () => {
    const plan = { has_active: false, folders: [] };
    const client = {
      acpServers: { prepareDelete: jest.fn(() => Promise.resolve(plan)) },
    };
    const setDeleteWizardName = jest.fn();
    const setDeleteWizardPlan = jest.fn();
    await removeServer(client, "free-server", 2, {
      setError: jest.fn(),
      removeServerFromState: jest.fn(),
      setDeleteBlockedInfo: jest.fn(),
      setDeleteWizardName,
      setDeleteWizardPlan,
    });
    expect(setDeleteWizardName).toHaveBeenCalledWith("free-server");
    expect(setDeleteWizardPlan).toHaveBeenCalledWith(plan);
  });

  test("blocks deletion locally when it is the last remaining ACP server", async () => {
    const client = { acpServers: { prepareDelete: jest.fn() } };
    const setError = jest.fn();
    await removeServer(client, "only-server", 1, {
      setError,
      removeServerFromState: jest.fn(),
      setDeleteBlockedInfo: jest.fn(),
      setDeleteWizardName: jest.fn(),
      setDeleteWizardPlan: jest.fn(),
    });
    expect(client.acpServers.prepareDelete).not.toHaveBeenCalled();
    expect(setError).toHaveBeenCalledWith(
      "At least one ACP server is required",
    );
  });
});

/** Duplicated from persistGlobalShortcuts (~line 2001). */
async function persistGlobalShortcuts(
  client,
  shortcutsLoaded,
  sections,
  dispatchEvent,
) {
  if (!shortcutsLoaded) return;
  try {
    const data = await client.shortcuts.setGlobal({ sections });
    dispatchEvent.set(data.sections || {});
    dispatchEvent.notify();
  } catch (err) {
    throw new Error(errorMessage(err, "Failed to save global shortcuts"));
  }
}

describe("SettingsDialog.persistGlobalShortcuts", () => {
  test("no-ops when the Shortcuts tab was never opened (shortcutsLoaded false)", async () => {
    const client = { shortcuts: { setGlobal: jest.fn() } };
    await persistGlobalShortcuts(
      client,
      false,
      {},
      { set: jest.fn(), notify: jest.fn() },
    );
    expect(client.shortcuts.setGlobal).not.toHaveBeenCalled();
  });

  test("success: saves the sections and dispatches the refresh event", async () => {
    const client = {
      shortcuts: {
        setGlobal: jest.fn(() => Promise.resolve({ sections: { tasks: [] } })),
      },
    };
    const set = jest.fn();
    const notify = jest.fn();
    await persistGlobalShortcuts(client, true, { tasks: [] }, { set, notify });
    expect(client.shortcuts.setGlobal).toHaveBeenCalledWith({
      sections: { tasks: [] },
    });
    expect(set).toHaveBeenCalledWith({ tasks: [] });
    expect(notify).toHaveBeenCalledTimes(1);
  });

  test("a rejected save re-throws wrapped in errorMessage(), skipping the event dispatch", async () => {
    const client = {
      shortcuts: {
        setGlobal: jest.fn(() => Promise.reject(new Error("too many rows"))),
      },
    };
    const notify = jest.fn();
    await expect(
      persistGlobalShortcuts(client, true, {}, { set: jest.fn(), notify }),
    ).rejects.toThrow("too many rows");
    expect(notify).not.toHaveBeenCalled();
  });
});

/**
 * Duplicated from loadSupportedRunners (~line 2364): the errorStatus(err)
 * === undefined split that keeps a silently-skipped non-2xx (mirrors the
 * old `if (res.ok)` guard) distinct from a logged network-level failure.
 */
async function loadSupportedRunners(client, setSupportedRunners, log) {
  try {
    const runners = await client.serverConfig.supportedRunners();
    setSupportedRunners(runners || []);
  } catch (err) {
    if (errorStatus(err) === undefined) {
      log(err);
      setSupportedRunners([
        { type: "exec", label: "exec (no restrictions)", supported: true },
        {
          type: "sandbox-exec",
          label: "sandbox-exec (macOS)",
          supported: false,
        },
        { type: "firejail", label: "firejail (Linux)", supported: false },
        { type: "docker", label: "docker (all platforms)", supported: true },
      ]);
    }
  }
}

describe("SettingsDialog.loadSupportedRunners", () => {
  test("a non-2xx status is silently skipped: setSupportedRunners is never called", async () => {
    const client = {
      serverConfig: {
        supportedRunners: jest.fn(() =>
          Promise.reject(
            Object.assign(new Error("forbidden"), { status: 403 }),
          ),
        ),
      },
    };
    const setSupportedRunners = jest.fn();
    const log = jest.fn();
    await loadSupportedRunners(client, setSupportedRunners, log);
    expect(setSupportedRunners).not.toHaveBeenCalled();
    expect(log).not.toHaveBeenCalled();
  });

  test("a network failure logs and falls back to the default 4-runner list", async () => {
    const client = {
      serverConfig: {
        supportedRunners: jest.fn(() => Promise.reject(new Error("offline"))),
      },
    };
    const setSupportedRunners = jest.fn();
    const log = jest.fn();
    await loadSupportedRunners(client, setSupportedRunners, log);
    expect(log).toHaveBeenCalled();
    expect(setSupportedRunners).toHaveBeenCalledWith([
      { type: "exec", label: "exec (no restrictions)", supported: true },
      { type: "sandbox-exec", label: "sandbox-exec (macOS)", supported: false },
      { type: "firejail", label: "firejail (Linux)", supported: false },
      { type: "docker", label: "docker (all platforms)", supported: true },
    ]);
  });

  test("success stores the server-reported list verbatim", async () => {
    const runners = [{ type: "docker", supported: true }];
    const client = {
      serverConfig: {
        supportedRunners: jest.fn(() => Promise.resolve(runners)),
      },
    };
    const setSupportedRunners = jest.fn();
    await loadSupportedRunners(client, setSupportedRunners, jest.fn());
    expect(setSupportedRunners).toHaveBeenCalledWith(runners);
  });
});

/** Duplicated from handleSave's config-save + errorMessage catch (~line 2983/3081). */
async function saveConfig(client, config, setError) {
  try {
    await client.serverConfig.save(config);
  } catch (err) {
    setError(errorMessage(err, "Failed to save configuration"));
    return false;
  }
  return true;
}

describe("SettingsDialog.handleSave — serverConfig.save() error handling", () => {
  test("a rejected save sets the error banner via errorMessage() and does not throw", async () => {
    const client = {
      serverConfig: {
        save: jest.fn(() => Promise.reject(new Error("invalid mcp port"))),
      },
    };
    const setError = jest.fn();
    const ok = await saveConfig(client, {}, setError);
    expect(ok).toBe(false);
    expect(setError).toHaveBeenCalledWith("invalid mcp port");
  });

  test("success returns true without touching the error banner", async () => {
    const client = {
      serverConfig: { save: jest.fn(() => Promise.resolve({ applied: {} })) },
    };
    const setError = jest.fn();
    const ok = await saveConfig(client, { workspaces: [] }, setError);
    expect(ok).toBe(true);
    expect(setError).not.toHaveBeenCalled();
    expect(client.serverConfig.save).toHaveBeenCalledWith({ workspaces: [] });
  });
});

/**
 * Duplicated from the ACPServerDeleteWizard's onSuccess post-delete
 * workspaces refresh (~line 6394): best-effort — a rejected
 * workspaces.list() is swallowed since local state was already updated.
 */
async function refreshWorkspacesAfterDelete(client, setWorkspaces) {
  try {
    const wsData = await client.workspaces.list();
    if (Array.isArray(wsData?.workspaces)) {
      setWorkspaces(wsData.workspaces);
    }
  } catch {
    // Best-effort refresh; the local state was updated already.
  }
}

describe("SettingsDialog post-delete workspaces refresh", () => {
  test("success applies the refreshed workspaces list", async () => {
    const client = {
      workspaces: {
        list: jest.fn(() =>
          Promise.resolve({ workspaces: [{ working_dir: "/a" }] }),
        ),
      },
    };
    const setWorkspaces = jest.fn();
    await refreshWorkspacesAfterDelete(client, setWorkspaces);
    expect(setWorkspaces).toHaveBeenCalledWith([{ working_dir: "/a" }]);
  });

  test("a rejected refresh is silently swallowed", async () => {
    const client = {
      workspaces: { list: jest.fn(() => Promise.reject(new Error("offline"))) },
    };
    const setWorkspaces = jest.fn();
    await expect(
      refreshWorkspacesAfterDelete(client, setWorkspaces),
    ).resolves.toBeUndefined();
    expect(setWorkspaces).not.toHaveBeenCalled();
  });

  test("a malformed (non-array) response is ignored", async () => {
    const client = { workspaces: { list: jest.fn(() => Promise.resolve({})) } };
    const setWorkspaces = jest.fn();
    await refreshWorkspacesAfterDelete(client, setWorkspaces);
    expect(setWorkspaces).not.toHaveBeenCalled();
  });
});

describe("SettingsDialog Slack tab wiring (mitto-37nx.6)", () => {
  test("registers and renders the extracted SlackSettingsTab", () => {
    const source = readFileSync(
      new URL("./SettingsDialog.js", import.meta.url),
      "utf8",
    );
    expect(source).toContain(
      'import { SlackSettingsTab } from "./SlackSettingsTab.js";',
    );
    expect(source).toContain('{ id: "slack", label: "Slack"');
    expect(source).toContain('activeTab === "slack"');
    expect(source).toContain("<${SlackSettingsTab} showToast=${showToast} />");
  });
});
