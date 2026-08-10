/**
 * Regression tests for the mitto-7gta.19 SDK-boundary ESLint rules
 * (eslint.config.js: no-restricted-globals / no-restricted-syntax /
 * no-restricted-imports scoped to web/static/**\/*.js).
 *
 * These exercise the ESLint Node API directly against the real
 * eslint.config.js so a future edit that weakens or removes a rule (or
 * grows the allowlist unintentionally) fails `bun run test-js`, not just
 * `bun run lint:js` run by hand.
 */
import { fileURLToPath } from "node:url";
import path from "node:path";
import { ESLint } from "eslint";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(__dirname, "..", "..");
const CONFIG_PATH = path.join(REPO_ROOT, "eslint.config.js");

const RESTRICTED_RULE_IDS = [
  "no-restricted-globals",
  "no-restricted-syntax",
  "no-restricted-imports",
];

/** Lints `code` as if it lived at `filePath` (relative to the repo root). */
async function lint(code, filePath) {
  const eslint = new ESLint({ overrideConfigFile: CONFIG_PATH, cwd: REPO_ROOT });
  const [result] = await eslint.lintText(code, {
    filePath: path.join(REPO_ROOT, filePath),
  });
  return result.messages;
}

function restrictedRuleIds(messages) {
  return messages.map((m) => m.ruleId).filter((id) => RESTRICTED_RULE_IDS.includes(id));
}

const NON_ALLOWLISTED_FILE = "web/static/components/FakeComponent.js";

describe("SDK boundary ESLint rules (mitto-7gta.19)", () => {
  test("bans bare fetch() outside the SDK", async () => {
    const messages = await lint('fetch("/api/x");\n', NON_ALLOWLISTED_FILE);
    expect(restrictedRuleIds(messages)).toContain("no-restricted-globals");
  });

  test("bans bare WebSocket/XMLHttpRequest/EventSource globals outside the SDK", async () => {
    for (const snippet of ["new WebSocket(url);", "new XMLHttpRequest();", "new EventSource(url);"]) {
      const messages = await lint(`const url = "/x";\n${snippet}\n`, NON_ALLOWLISTED_FILE);
      expect(restrictedRuleIds(messages)).toContain("no-restricted-globals");
    }
  });

  test("bans window.fetch / globalThis.fetch / self.fetch qualified access", async () => {
    for (const receiver of ["window", "globalThis", "self"]) {
      const messages = await lint(`${receiver}.fetch("/x");\n`, NON_ALLOWLISTED_FILE);
      expect(restrictedRuleIds(messages)).toContain("no-restricted-syntax");
    }
  });

  test("bans `new WebSocket(...)` construction via no-restricted-syntax too", async () => {
    const messages = await lint('const ws = new WebSocket("wss://x");\n', NON_ALLOWLISTED_FILE);
    const ids = restrictedRuleIds(messages);
    // Both no-restricted-globals (bare identifier) and no-restricted-syntax
    // (NewExpression selector) are expected to fire on this construct.
    expect(ids).toContain("no-restricted-globals");
    expect(ids).toContain("no-restricted-syntax");
  });

  test("bans deep sdk/ imports outside web/static/sdk/", async () => {
    const messages = await lint(
      'import { createEndpoints } from "../sdk/core/endpoints.js";\n',
      NON_ALLOWLISTED_FILE,
    );
    expect(restrictedRuleIds(messages)).toContain("no-restricted-imports");
  });

  test("bans re-importing authFetch/secureFetch from utils/csrf.js", async () => {
    const messages = await lint(
      'import { authFetch } from "../utils/csrf.js";\n',
      "web/static/hooks/useFakeHook.js",
    );
    expect(restrictedRuleIds(messages)).toContain("no-restricted-imports");
  });

  test("does not flag importing the public SDK entrypoint", async () => {
    const messages = await lint(
      'import { getSdkClient } from "../sdk/index.js";\nconsole.log(getSdkClient);\n',
      NON_ALLOWLISTED_FILE,
    );
    expect(restrictedRuleIds(messages)).toHaveLength(0);
  });

  test.each([
    ["web/static/sdk/core/transport.js", 'fetch("/x");\n'],
    ["web/static/sw.js", 'fetch("/x");\n'],
    ["web/static/auth.js", 'fetch("/x");\n'],
    ["web/static/utils/sdkClient.js", 'fetch("/x");\n'],
    ["web/static/utils/csrf.js", 'import { x } from "../sdk/auth/browser-cookie.js";\nconsole.log(x);\n'],
    ["web/static/utils/endpoints.js", 'import { x } from "../sdk/core/endpoints.js";\nconsole.log(x);\n'],
  ])("SDK_BOUNDARY_ALLOWLIST exempts %s", async (filePath, code) => {
    const messages = await lint(code, filePath);
    expect(restrictedRuleIds(messages)).toHaveLength(0);
  });
});
