/**
 * Regression tests for the mitto-7gta.17 slice S4 SDK migration in
 * ConversationPropertiesPanel.js (sessions domain: authFetch/secureFetch ->
 * getSdkClient()).
 *
 * ConversationPropertiesPanel.js cannot be imported directly under jsdom (it
 * reads `window.preact` at module load time, same limitation documented in
 * SessionItem.test.js / BeadsView.test.js / app.test.js), so these tests
 * read the raw source and assert on the exact wiring rather than executing
 * the component.
 */

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const __dirname = dirname(fileURLToPath(import.meta.url));
const panelJs = readFileSync(
  resolve(__dirname, "ConversationPropertiesPanel.js"),
  "utf8",
);

describe("ConversationPropertiesPanel.js: SDK migration (mitto-7gta.17 slice S4)", () => {
  test("imports getSdkClient/errorMessage; no authFetch/secureFetch remain", () => {
    expect(panelJs).toMatch(
      /import \{ getSdkClient \} from "\.\.\/utils\/sdkClient\.js";/,
    );
    expect(panelJs).toMatch(
      /import \{ errorMessage \} from "\.\.\/utils\/sdkErrors\.js";/,
    );
    expect(panelJs).not.toMatch(/authFetch|secureFetch/);
  });

  test("properties fetch: loop/callback/flags/settings each tolerate their own failure with .catch(() => null)", () => {
    const idx = panelJs.indexOf(
      "const [loopData, callbackData, flagsData, settingsData] =",
    );
    expect(idx).toBeGreaterThan(-1);
    const snippet = panelJs.slice(idx, idx + 700);
    expect(snippet).toMatch(
      /loopConfigured\s*\n\s*\? getSdkClient\(\)\s*\n\s*\.sessions\.loop\.get\(sessionId\)\s*\n\s*\.catch\(\(\) => null\)/,
    );
    expect(snippet).toMatch(
      /loopConfigured\s*\n\s*\? getSdkClient\(\)\s*\n\s*\.sessions\.getCallback\(sessionId\)\s*\n\s*\.catch\(\(\) => null\)/,
    );
    expect(snippet).toMatch(
      /getSdkClient\(\)\s*\n\s*\.misc\.advancedFlags\(\)\s*\n\s*\.catch\(\(\) => null\)/,
    );
    expect(snippet).toMatch(
      /getSdkClient\(\)\s*\n\s*\.sessions\.getSettings\(sessionId\)\s*\n\s*\.catch\(\(\) => null\)/,
    );
  });

  test("handleFreshContextChange calls getSdkClient().sessions.loop.update(sessionId, { fresh_context }) and applies the server-echoed value optimistically", () => {
    const idx = panelJs.indexOf(
      "const handleFreshContextChange = useCallback(",
    );
    expect(idx).toBeGreaterThan(-1);
    const snippet = panelJs.slice(idx, idx + 400);
    expect(snippet).toMatch(
      /const data = await getSdkClient\(\)\.sessions\.loop\.update\(sessionId, \{\s*\n\s*fresh_context: newValue,\s*\n\s*\}\);/,
    );
    expect(snippet).toMatch(/fresh_context: data\.fresh_context \?\? newValue/);
  });

  test("callback CRUD handlers (enable/rotate/revoke) swallow SDK throws as a no-op, matching the old !res.ok no-op", () => {
    expect(panelJs).toMatch(
      /const data = await getSdkClient\(\)\.sessions\.createCallback\(sessionId\);/,
    );
    expect(panelJs).toMatch(
      /await getSdkClient\(\)\.sessions\.revokeCallback\(sessionId\);/,
    );
    const noopComments =
      panelJs.match(/\/\* mirrors the prior !res\.ok no-op \*\//g) || [];
    expect(noopComments.length).toBeGreaterThanOrEqual(2);
  });

  test("handleFlagChange surfaces the real SDK error message via errorMessage()", () => {
    expect(panelJs).toMatch(
      /const data = await getSdkClient\(\)\.sessions\.updateSettings\(sessionId, \{/,
    );
    expect(panelJs).toMatch(
      /setFlagsError\(errorMessage\(err, "Failed to save setting"\)\);/,
    );
  });
});

describe("ConversationPropertiesPanel.js: processor overhead statistics (mitto-08q.3)", () => {
  const idx = panelJs.indexOf("processor_cumulative_injected_tokens > 0 ||");
  const section =
    idx > -1 ? panelJs.slice(idx - 40, idx + 5200) : "SECTION_NOT_FOUND";

  test("populated: subsection guard requires at least one of the four overhead signals", () => {
    expect(idx).toBeGreaterThan(-1);
    expect(section).toMatch(/sessionInfo\?\.processor_reruns_total > 0/);
    expect(section).toMatch(/sessionInfo\?\.processor_skipped_total > 0/);
    expect(section).toMatch(/sessionInfo\?\.processor_cumulative_aux_tokens > 0/);
  });

  test("populated: each row reads its own field and renders the '~N tok' shorthand", () => {
    expect(section).toMatch(
      /sessionInfo\?\.processor_last_prompt_injected_tokens > 0/,
    );
    expect(section).toMatch(/~\$\{sessionInfo\.processor_last_prompt_injected_tokens\}/);
    expect(section).toMatch(/~\$\{sessionInfo\.processor_cumulative_injected_tokens\}/);
    expect(section).toMatch(/~\$\{sessionInfo\.processor_cumulative_aux_tokens\}/);
  });

  test("populated: tooltips distinguish injected estimates from provider-retained/auxiliary context", () => {
    expect(section).toMatch(
      /Estimated tokens injected into your prompt by processors on the most recent turn\. Length-based estimate, not a provider count\./,
    );
    expect(section).toMatch(
      /Cumulative estimated tokens processors have injected into your prompts\. Separate from provider-retained context\./,
    );
    expect(section).toMatch(
      /Estimated tokens sent to auxiliary sessions \(background\/utility work\)\. Does not enter your primary context\./,
    );
  });

  test("populated: reruns row surfaces the by-reason breakdown via title, skipped row is a plain count", () => {
    expect(section).toMatch(/sessionInfo\?\.processor_reruns_by_reason/);
    expect(section).toMatch(
      /Object\.entries\(\s*sessionInfo\.processor_reruns_by_reason,?\s*\)/,
    );
    expect(section).toMatch(/\$\{reason\}: \$\{count\}/);
    expect(section).toMatch(/>Skipped<\/span/);
  });

  test("populated: top-processors ranking renders name, injected tokens, and run count per entry", () => {
    expect(section).toMatch(
      /sessionInfo\?\.processor_top_by_tokens\?\.length > 0/,
    );
    expect(section).toMatch(/Top processors by injected tokens/);
    expect(section).toMatch(
      /sessionInfo\.processor_top_by_tokens\.map\(\s*\(p\) =>/,
    );
    expect(section).toMatch(/~\$\{p\.tokens\} tok \(\$\{p\.runs\} runs\)/);
  });

  test("empty/legacy: every row and the outer subsection are individually field-guarded, so undefined processor_* fields render nothing", () => {
    // Empty sessions (no processor telemetry) and legacy sessions (events
    // recorded before mitto-08q.2, so these fields are simply absent from the
    // `connected` payload) must fall through every guard below to false —
    // there is no unguarded row that could render with undefined data.
    const guardedFields = [
      "processor_last_prompt_injected_tokens",
      "processor_cumulative_injected_tokens",
      "processor_cumulative_aux_tokens",
      "processor_reruns_total",
      "processor_skipped_total",
    ];
    for (const field of guardedFields) {
      expect(section).toMatch(new RegExp(`sessionInfo\\?\\.${field} > 0`));
    }
    expect(section).toMatch(/sessionInfo\?\.processor_top_by_tokens\?\.length > 0/);
  });

  test("empty/legacy: subsection is inserted after the existing Processors row and before Mitto MCP calls, leaving the legacy row unaffected", () => {
    const processorsRowIdx = panelJs.indexOf(">Processors<");
    const mcpCallsRowIdx = panelJs.indexOf(">Mitto MCP calls<");
    expect(processorsRowIdx).toBeGreaterThan(-1);
    expect(mcpCallsRowIdx).toBeGreaterThan(-1);
    expect(idx).toBeGreaterThan(processorsRowIdx);
    expect(idx).toBeLessThan(mcpCallsRowIdx);
  });
});
