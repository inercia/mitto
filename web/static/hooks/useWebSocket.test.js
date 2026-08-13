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
