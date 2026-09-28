/**
 * Tests for MoveAgentDialog.js (mitto-f7yo.6).
 *
 * MoveAgentDialog.js destructures html/useState/useEffect/useCallback from
 * window.preact at module-load time, so — mirroring
 * useConversationMenu.test.js — pass-through stubs are installed BEFORE a
 * dynamic import. Only the exported pure helper functions
 * (buildMoveAgentToast / isMoveAgentConfirmDisabled) are exercised directly;
 * neither calls a hook, so the trivial stubs below are sufficient (no
 * per-call-order state machine like useConversationMenu.test.js needs).
 * The JSX gating logic (loading/error/busy/loop-prompt-warning/children
 * checkbox) is covered via source-scan assertions on the raw file text,
 * following the SessionItem.test.js / LoopSettingsTab.test.js precedent for
 * components too stateful to safely re-mount under jsdom/happy-dom stubs.
 */

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import {
  describe,
  test,
  expect,
  beforeAll,
} from "../utils/testing/testGlobals.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const dialogJs = readFileSync(resolve(__dirname, "MoveAgentDialog.js"), "utf8");

global.window = global.window || {};
window.preact = window.preact || {};
window.preact.html =
  window.preact.html ||
  ((strings, ...values) => ({ __htmlStub: true, strings, values }));
window.preact.useState =
  window.preact.useState ||
  ((initial) => [
    typeof initial === "function" ? initial() : initial,
    () => {},
  ]);
window.preact.useEffect = window.preact.useEffect || (() => {});
window.preact.useCallback = window.preact.useCallback || ((fn) => fn);

let buildMoveAgentToast;
let isMoveAgentConfirmDisabled;

beforeAll(async () => {
  ({ buildMoveAgentToast, isMoveAgentConfirmDisabled } =
    await import("./MoveAgentDialog.js"));
});

describe("buildMoveAgentToast", () => {
  test("plain success: no skipped children, no resume_error", () => {
    expect(
      buildMoveAgentToast({ moved: ["s1"], skipped: [] }, "agent-b"),
    ).toEqual({ style: "success", title: "Moved to agent-b" });
  });

  test("mentions the skipped-children count (plural)", () => {
    expect(
      buildMoveAgentToast(
        {
          moved: ["s1"],
          skipped: [
            { id: "c1", reason: "busy" },
            { id: "c2", reason: "busy" },
          ],
        },
        "agent-b",
      ),
    ).toEqual({
      style: "success",
      title: "Moved to agent-b — 2 child conversations skipped",
    });
  });

  test("mentions the skipped-children count (singular)", () => {
    expect(
      buildMoveAgentToast(
        { moved: ["s1"], skipped: [{ id: "c1", reason: "busy" }] },
        "agent-b",
      ),
    ).toEqual({
      style: "success",
      title: "Moved to agent-b — 1 child conversation skipped",
    });
  });

  test("resume_error downgrades style to warning and appends the message", () => {
    expect(
      buildMoveAgentToast(
        { moved: ["s1"], skipped: [], resume_error: "agent unreachable" },
        "agent-b",
      ),
    ).toEqual({
      style: "warning",
      title: "Moved to agent-b. Warning: agent unreachable",
    });
  });

  test("skipped count and resume_error combine in one warning toast", () => {
    expect(
      buildMoveAgentToast(
        {
          moved: ["s1"],
          skipped: [{ id: "c1", reason: "busy" }],
          resume_error: "agent unreachable",
        },
        "agent-b",
      ),
    ).toEqual({
      style: "warning",
      title:
        "Moved to agent-b — 1 child conversation skipped. Warning: agent unreachable",
    });
  });
});

describe("isMoveAgentConfirmDisabled", () => {
  test("null/undefined preflight (still loading or fetch failed) disables confirm", () => {
    expect(isMoveAgentConfirmDisabled(null)).toBe(true);
    expect(isMoveAgentConfirmDisabled(undefined)).toBe(true);
  });

  test("busy:true disables confirm", () => {
    expect(isMoveAgentConfirmDisabled({ busy: true, archived: false })).toBe(
      true,
    );
  });

  test("archived:true disables confirm", () => {
    expect(isMoveAgentConfirmDisabled({ busy: false, archived: true })).toBe(
      true,
    );
  });

  test("neither busy nor archived enables confirm", () => {
    expect(isMoveAgentConfirmDisabled({ busy: false, archived: false })).toBe(
      false,
    );
  });
});

describe("MoveAgentDialog.js: source-scan gating (mitto-f7yo.6)", () => {
  test("uses getSdkClient().sessions.moveAgentPreflight / moveAgent — never authFetch/endpoints.sessions", () => {
    expect(dialogJs).toMatch(
      /getSdkClient\(\)\.sessions\.moveAgentPreflight\(/,
    );
    expect(dialogJs).toMatch(/getSdkClient\(\)\.sessions\.moveAgent\(/);
    expect(dialogJs).not.toMatch(/authFetch\(/);
    // The header comment legitimately mentions "endpoints.sessions.*" to
    // explain why it's NOT used, so assert on the absence of any actual call
    // syntax instead of the bare substring.
    expect(dialogJs).not.toMatch(/endpoints\.sessions\.\w+\(/);
  });

  test("isOpen is false (Modal renders nothing) when session or targetAgent is missing", () => {
    expect(dialogJs).toMatch(/const isOpen = !!session && !!targetAgent;/);
  });

  test("Confirm button is disabled while loading, confirming, or per isMoveAgentConfirmDisabled(preflight)", () => {
    expect(dialogJs).toMatch(
      /const confirmDisabled =\s*\n\s*loading \|\| confirming \|\| isMoveAgentConfirmDisabled\(preflight\);/,
    );
    expect(dialogJs).toMatch(/disabled=\$\{confirmDisabled\}/);
  });

  test("busy_reason (or an archived-specific message) renders when busy or archived", () => {
    expect(dialogJs).toMatch(/preflight\.busy \|\| preflight\.archived/);
    expect(dialogJs).toMatch(/preflight\.busy_reason/);
  });

  test("loop info renders only when is_loop, and the loop-prompt-unavailable warning is gated on candidate.loop_prompt_available === false", () => {
    expect(dialogJs).toMatch(/preflight\.is_loop &&/);
    expect(dialogJs).toMatch(
      /const loopPromptUnavailable =\s*\n\s*!!preflight\?\.is_loop && candidate\?\.loop_prompt_available === false;/,
    );
  });

  test("children-count checkbox is gated on children_count > 0 and toggles include_children in the POST body", () => {
    expect(dialogJs).toMatch(/preflight\.children_count > 0 &&/);
    expect(dialogJs).toMatch(/include_children: includeChildren/);
  });

  test("shows a loading state while the preflight fetch is in flight", () => {
    expect(dialogJs).toMatch(/data-testid="move-agent-loading"/);
  });

  test("shows a load-error state when the preflight fetch fails", () => {
    expect(dialogJs).toMatch(/data-testid="move-agent-load-error"/);
  });

  test("POST body sends target_agent and include_children", () => {
    expect(dialogJs).toMatch(/target_agent: targetAgent,/);
  });

  test("success and error paths both call onClose after showing a toast", () => {
    // Two showToast(...) call sites (success + error), each followed
    // eventually by onClose?.() within the same handler.
    const calls = dialogJs.match(/showToast\(/g) || [];
    expect(calls.length).toBeGreaterThanOrEqual(2);
    expect(dialogJs).toMatch(/onClose\?\.\(\);/);
  });
});
