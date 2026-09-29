/**
 * Unit tests for the Cmd/Ctrl+Shift+<digit> composer model-switch shortcut
 * resolver (mitto-qmqq.2).
 */

import { describe, test, expect } from "./testing/testGlobals.js";
import { resolveModelShortcut } from "./modelShortcuts.js";

function modelOption(options, currentValue) {
  return { options, current_value: currentValue };
}

describe("resolveModelShortcut", () => {
  test("returns no_model_option when modelOption is missing", () => {
    const result = resolveModelShortcut(1, [{ shortcut_number: 1 }], null);
    expect(result).toEqual({ status: "no_model_option" });
  });

  test("returns no_model_option when modelOption.options is not an array", () => {
    const result = resolveModelShortcut(1, [{ shortcut_number: 1 }], { options: "nope" });
    expect(result).toEqual({ status: "no_model_option" });
  });

  test("returns unassigned when no profile carries the requested shortcut_number", () => {
    const profiles = [
      { name: "Claude Opus", shortcut_number: 1, criteria: { matchMode: "contains", pattern: "Opus" } },
    ];
    const opt = modelOption([{ value: "claude-opus", name: "Claude Opus 5.5" }], "claude-opus");
    const result = resolveModelShortcut(2, profiles, opt);
    expect(result).toEqual({ status: "unassigned" });
  });

  test("returns unassigned when modelProfiles is null/undefined", () => {
    const opt = modelOption([{ value: "x", name: "X" }], "x");
    expect(resolveModelShortcut(1, null, opt)).toEqual({ status: "unassigned" });
    expect(resolveModelShortcut(1, undefined, opt)).toEqual({ status: "unassigned" });
  });

  test("treats absent shortcut_number as 0 (never matches digit 1-5)", () => {
    const profiles = [{ name: "No Shortcut", criteria: { matchMode: "contains", pattern: "X" } }];
    const opt = modelOption([{ value: "x", name: "X" }], "x");
    expect(resolveModelShortcut(1, profiles, opt)).toEqual({ status: "unassigned" });
  });

  test("returns no_match (with profile) when the profile's criteria match no catalog model", () => {
    const profiles = [
      { name: "Gemini", shortcut_number: 3, criteria: { matchMode: "contains", pattern: "Gemini" } },
    ];
    const opt = modelOption(
      [
        { value: "claude-opus", name: "Claude Opus 5.5" },
        { value: "claude-sonnet", name: "Claude Sonnet 5" },
      ],
      "claude-sonnet",
    );
    const result = resolveModelShortcut(3, profiles, opt);
    expect(result.status).toBe("no_match");
    expect(result.profile).toBe(profiles[0]);
  });

  test("returns apply when a match is found and it differs from the current model (contains)", () => {
    const profiles = [
      { name: "Claude Opus", shortcut_number: 1, criteria: { matchMode: "contains", pattern: "Opus" } },
    ];
    const opt = modelOption(
      [
        { value: "claude-opus", name: "Claude Opus 5.5" },
        { value: "claude-sonnet", name: "Claude Sonnet 5" },
      ],
      "claude-sonnet",
    );
    const result = resolveModelShortcut(1, profiles, opt);
    expect(result).toEqual({
      status: "apply",
      profile: profiles[0],
      value: "claude-opus",
      name: "Claude Opus 5.5",
    });
  });

  test("returns already_active when the matched model is the current model", () => {
    const profiles = [
      { name: "Claude Sonnet", shortcut_number: 2, criteria: { matchMode: "contains", pattern: "Sonnet" } },
    ];
    const opt = modelOption(
      [
        { value: "claude-opus", name: "Claude Opus 5.5" },
        { value: "claude-sonnet", name: "Claude Sonnet 5" },
      ],
      "claude-sonnet",
    );
    const result = resolveModelShortcut(2, profiles, opt);
    expect(result).toEqual({
      status: "already_active",
      profile: profiles[0],
      value: "claude-sonnet",
      name: "Claude Sonnet 5",
    });
  });

  test("supports exact match mode", () => {
    const profiles = [
      { name: "Auto", shortcut_number: 4, criteria: { matchMode: "exact", pattern: "Auto" } },
    ];
    const opt = modelOption(
      [
        { value: "auto", name: "Auto" },
        { value: "auto-plus", name: "Auto Plus" },
      ],
      "auto-plus",
    );
    const result = resolveModelShortcut(4, profiles, opt);
    // exact mode must not match "Auto Plus"; only the literal "Auto" model.
    expect(result).toEqual({
      status: "apply",
      profile: profiles[0],
      value: "auto",
      name: "Auto",
    });
  });

  test("supports startsWith match mode", () => {
    const profiles = [
      { name: "GPT", shortcut_number: 5, criteria: { matchMode: "startsWith", pattern: "GPT-5" } },
    ];
    const opt = modelOption(
      [
        { value: "gpt-5", name: "GPT-5 Turbo" },
        { value: "gpt-4", name: "GPT-4" },
      ],
      "gpt-4",
    );
    const result = resolveModelShortcut(5, profiles, opt);
    expect(result.status).toBe("apply");
    expect(result.value).toBe("gpt-5");
  });

  test("supports regex match mode", () => {
    const profiles = [
      { name: "Sonnet regex", shortcut_number: 1, criteria: { matchMode: "regex", pattern: "sonnet.*5" } },
    ];
    const opt = modelOption(
      [
        { value: "claude-sonnet-5", name: "Claude Sonnet 5" },
        { value: "claude-opus", name: "Claude Opus" },
      ],
      "claude-opus",
    );
    const result = resolveModelShortcut(1, profiles, opt);
    expect(result.status).toBe("apply");
    expect(result.value).toBe("claude-sonnet-5");
  });

  test("regex match mode fails closed (no_match) on invalid pattern", () => {
    const profiles = [
      { name: "Bad regex", shortcut_number: 1, criteria: { matchMode: "regex", pattern: "(unterminated" } },
    ];
    const opt = modelOption([{ value: "x", name: "X" }], "x");
    const result = resolveModelShortcut(1, profiles, opt);
    expect(result.status).toBe("no_match");
  });

  test("supports lookAlike (word-based) match mode", () => {
    const profiles = [
      { name: "Opus lookalike", shortcut_number: 1, criteria: { matchMode: "lookAlike", pattern: "Opus 5" } },
    ];
    const opt = modelOption(
      [{ value: "claude-opus-5-5", name: "Claude Opus 5.5" }],
      "other",
    );
    const result = resolveModelShortcut(1, profiles, opt);
    expect(result.status).toBe("apply");
    expect(result.value).toBe("claude-opus-5-5");
  });

  test("unknown matchMode never matches (default: false)", () => {
    const profiles = [
      { name: "Weird", shortcut_number: 1, criteria: { matchMode: "fuzzy", pattern: "Opus" } },
    ];
    const opt = modelOption([{ value: "claude-opus", name: "Claude Opus 5.5" }], "claude-opus");
    const result = resolveModelShortcut(1, profiles, opt);
    expect(result.status).toBe("no_match");
  });

  test("last match wins when multiple catalog entries satisfy the same criteria", () => {
    const profiles = [
      { name: "Claude", shortcut_number: 1, criteria: { matchMode: "contains", pattern: "Claude" } },
    ];
    const opt = modelOption(
      [
        { value: "claude-a", name: "Claude A" },
        { value: "claude-b", name: "Claude B" },
        { value: "claude-c", name: "Claude C" },
      ],
      "other",
    );
    const result = resolveModelShortcut(1, profiles, opt);
    // resolveProfileModel iterates without early-break, so the LAST match wins.
    expect(result.status).toBe("apply");
    expect(result.value).toBe("claude-c");
  });

  test("digits outside 1-5 are still resolved by this function (range enforcement is the caller's job)", () => {
    // resolveModelShortcut itself does not restrict digit to 1-5 — ChatInput.js's
    // regex is what enforces that range before calling this function. Passing a
    // profile with shortcut_number 9 still resolves normally here.
    const profiles = [
      { name: "Nine", shortcut_number: 9, criteria: { matchMode: "contains", pattern: "Nine" } },
    ];
    const opt = modelOption([{ value: "nine", name: "Model Nine" }], "other");
    const result = resolveModelShortcut(9, profiles, opt);
    expect(result.status).toBe("apply");
    expect(result.value).toBe("nine");
  });
});
