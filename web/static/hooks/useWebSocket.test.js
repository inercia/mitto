/**
 * Regression tests for the mitto-yvel.4 no_archive transport plumbing in
 * useWebSocket.js.
 *
 * useWebSocket.js cannot be imported directly under jsdom (it reads
 * `window.preact` at module load time, same limitation documented in
 * SessionItem.test.js / BeadsView.test.js), so these tests read the raw
 * source and assert on the exact wiring rather than executing the hook.
 */

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const __dirname = dirname(fileURLToPath(import.meta.url));
const useWebSocketJs = readFileSync(
  resolve(__dirname, "useWebSocket.js"),
  "utf8",
);
const sessionPanelJs = readFileSync(
  resolve(__dirname, "../components/SessionPanel.js"),
  "utf8",
);
const chatInputJs = readFileSync(
  resolve(__dirname, "../components/ChatInput.js"),
  "utf8",
);

describe("useWebSocket.js: no_archive transport (mitto-yvel.4)", () => {
  test("activeSessions derivation sources no_archive from connected-message info or the stored session", () => {
    expect(useWebSocketJs).toMatch(
      /no_archive: data\.info\?\.no_archive \|\| storedSession\?\.no_archive \|\| false,/,
    );
  });

  test("structural fingerprint includes no_archive so a protection change re-renders the sidebar", () => {
    expect(useWebSocketJs).toMatch(
      /\$\{s\.archived\}\|\$\{s\.no_archive\}\|\$\{s\.isActive\}/,
    );
  });

  test("connected-message handler resolves no_archive via ?? (always-sent server value wins over stale info)", () => {
    expect(useWebSocketJs).toMatch(
      /no_archive:\s*\n\s*msg\.data\.no_archive \?\? session\.info\?\.no_archive \?\? false,/,
    );
  });
});

describe("useWebSocket.js: task label color global refresh (mitto-ggs6)", () => {
  test("bridges the server event to the same-window event consumed by Tasks views", () => {
    expect(useWebSocketJs).toMatch(
      /case "task_label_colors_updated":[\s\S]*?new CustomEvent\("mitto:task_label_colors_updated", \{[\s\S]*?detail: msg\.data/,
    );
  });
});

describe("loop configuration synchronization (mitto-w7hh.3)", () => {
  test("forwards the complete authoritative config while preserving glance fields", () => {
    const loopHandler = useWebSocketJs.slice(
      useWebSocketJs.indexOf('case "loop_updated":'),
      useWebSocketJs.indexOf('case "loop_started":'),
    );
    expect(loopHandler).toMatch(
      /loopConfig:\s*msg\.data\.loop_config && typeof msg\.data\.loop_config === "object"\s*\? msg\.data\.loop_config\s*: null/,
    );
    for (const field of [
      "loop_enabled",
      "loop_configured",
      "loop_iteration_count",
      "loop_max_iterations",
      "loop_stopped_reason",
      "loop_trigger",
      "loop_triggers",
    ]) {
      expect(loopHandler).toContain(field);
    }
  });

  test("keeps an open SessionPanel current and ignores other sessions", () => {
    expect(sessionPanelJs).toMatch(
      /if \(detail\.sessionId !== sessionId\) return;[\s\S]*?loopConfigVersionRef\.current \+= 1;[\s\S]*?setLoopConfig\(detail\.loopConfig\)/,
    );
    expect(sessionPanelJs).toMatch(
      /window\.addEventListener\(\s*"mitto:loop_config_updated",\s*handleLoopConfigUpdated/,
    );
    expect(sessionPanelJs).toMatch(/\}, \[isOpen, sessionId\]\);/);
  });

  test("clears deleted loops and prevents stale fetches from winning", () => {
    expect(sessionPanelJs).toMatch(
      /if \(detail\.loopConfigured === false\) \{\s*setLoopConfig\(null\);\s*return;/,
    );
    expect(sessionPanelJs).toMatch(
      /const loopConfigVersion = loopConfigVersionRef\.current;[\s\S]*?if \(!cancelled && loopConfigVersion === loopConfigVersionRef\.current\) \{\s*setLoopConfig\(loopData \|\| null\)/,
    );
    expect(sessionPanelJs).toMatch(
      /return \(\) => \{\s*cancelled = true;\s*\};\s*\}, \[isOpen, sessionId, sessionInfo\?\.loop_configured\]\);/,
    );
  });

  test("applies authoritative compact-control state and resets on deletion or switch", () => {
    expect(chatInputJs).toMatch(
      /if \(loopConfig && typeof loopConfig === "object"\) \{\s*loopConfigVersionRef\.current \+= 1;\s*applyLoopConfigState\(loopConfig\);\s*return;/,
    );
    expect(chatInputJs).toMatch(
      /if \(loopConfigured === false\) \{\s*loopConfigVersionRef\.current \+= 1;\s*resetLoopConfigState\(\);\s*return;/,
    );
    expect(chatInputJs).toMatch(
      /loopConfigVersionRef\.current \+= 1;\s*resetLoopConfigState\(\);\s*setIsLoopSaving\(false\);\s*\}, \[sessionId, resetLoopConfigState\]\);/,
    );
    expect(chatInputJs).toMatch(
      /cancelled \|\| loopConfigVersion !== loopConfigVersionRef\.current/,
    );
  });
});
