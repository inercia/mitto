import { describe, test, expect } from "./testing/testGlobals.js";
import { resolveModelShortcut, modelShortcutDigit, modelShortcutToast } from "./modelShortcuts.js";

describe("runtime model tag mappings", () => {
  test("custom tags need no catalog/profiles; trims only edges, never mutates", () => {
    const rows = Object.freeze([
      Object.freeze({ number: 1, model_tag: " My  Custom Tag " }),
      Object.freeze({ number: 2, model_tag: "My  Custom Tag" }),
    ]);
    expect(resolveModelShortcut(1, rows)).toEqual({ status: "apply", modelTag: "My  Custom Tag" });
    expect(resolveModelShortcut(2, rows)).toEqual(resolveModelShortcut(1, rows));
    expect(rows[0].model_tag).toBe(" My  Custom Tag ");
  });

  test("malformed mappings fail closed; first valid numeric match wins", () => {
    for (const rows of [undefined, null, {}, "tag", [], [null, {}, { shortcut_number: 1, model_tag: "legacy" }],
      [{ number: "1", model_tag: "string" }], [{ number: 1, model_tag: null }], [{ number: 1, model_tag: "  " }]]) {
      expect(resolveModelShortcut(1, rows)).toEqual({ status: "unassigned" });
    }
    const rows = [null, { number: 1, model_tag: " " }, { number: 1, model_tag: "First" }, { number: 1, model_tag: "Second" }];
    expect(resolveModelShortcut(1, rows)).toEqual({ status: "apply", modelTag: "First" });
    for (const digit of ["1", 1.5, 0, 6, -1, NaN, null, undefined]) {
      expect(resolveModelShortcut(digit, rows)).toEqual({ status: "unassigned" });
    }
  });
});

describe("model shortcut physical key predicate", () => {
  test("Meta and Ctrl, shifted symbols, all five digits, Alt/repeat compatibility", () => {
    for (const modifier of ["metaKey", "ctrlKey"]) {
      for (let digit = 1; digit <= 5; digit++) {
        expect(modelShortcutDigit({ [modifier]: true, shiftKey: true, code: `Digit${digit}`, key: "!", altKey: true, repeat: true })).toBe(digit);
      }
    }
  });
  test("irrelevant keys and missing modifiers fall through", () => {
    for (const code of ["Digit0", "Digit6", "Digit9", "Numpad1", "KeyA", "Enter", undefined]) {
      expect(modelShortcutDigit({ metaKey: true, shiftKey: true, code })).toBe(null);
    }
    for (const event of [{ metaKey: true }, { ctrlKey: true }, { shiftKey: true }, {}]) {
      expect(modelShortcutDigit({ ...event, code: "Digit1" })).toBe(null);
    }
  });
});

describe("safe model tag feedback", () => {
  const styles = {
    applied: "success", already_active: "info", unassigned: "info", no_match: "warning",
    catalog_unavailable: "warning", invalid_tag: "error", invalid_request: "error",
    missing_session: "error", invalid_target: "error", internal_error: "error",
    protocol_error: "error", send_failed: "error", timeout: "warning",
  };
  for (const [status, style] of Object.entries(styles)) {
    test(`${status} supplies style/title/message and ignores raw server text`, () => {
      const toast = modelShortcutToast({ status, message: "SERVER_SECRET" }, "Custom Tag", 3);
      expect(toast.style).toBe(style);
      expect(toast.title).toBeTruthy();
      expect(toast.message).toBeTruthy();
      expect(toast.message).not.toContain("SERVER_SECRET");
      expect(toast.message).not.toMatch(/switched|rollback/i);
    });
  }
  test("acceptance allows deferral; uncertain outcomes do not imply failure", () => {
    expect(modelShortcutToast({ status: "applied" }, "Tag").message).toMatch(/accepted.*may take effect before the next prompt/);
    expect(modelShortcutToast({ status: "timeout" }).message).toMatch(/may still apply.*before retrying/);
    expect(modelShortcutToast({ status: "send_failed" }).message).toContain("sent or confirmed");
    expect(modelShortcutToast({ status: "unassigned" }, "", 3).message).toContain("Settings > Keyboard");
    expect(modelShortcutToast({ status: "catalog_unavailable" }).message).toContain("catalog");
    expect(modelShortcutToast({ status: "no_match" }, "Tag").message).toContain("No matching model");
    expect(modelShortcutToast({ status: "cancelled" })).toBe(null);
    expect(modelShortcutToast({ status: "unsupported" }).style).toBe("error");
  });
});
