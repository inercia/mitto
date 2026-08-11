/**
 * Regression tests for the mitto-7gta.22 JS-client examples
 * (examples/js-client/browser-snippet, examples/js-client/prompt-stream).
 *
 * These files live outside web/static/ (they are standalone, dependency-free
 * runnable programs, not part of the app bundle), so nothing else in
 * `bun test web/static` would otherwise notice a regression in them. This
 * file follows the same out-of-tree-target pattern as
 * web/static/eslintSdkBoundary.test.js: it lives under web/static/ (so
 * `make test-js` discovers it) but exercises files elsewhere in the repo by
 * absolute path.
 *
 * `tests/integration/inprocess/sdk_contract_test.go`
 * (TestSDKContract_GoAndJSAgree) already proves the underlying SDK/
 * WebSocket-header-adapter behavior end-to-end against a real server via a
 * near-identical driver script (tests/integration/sdkcontract/driver.js), so
 * this file does not duplicate that live-server round trip (see the Plan
 * comment on mitto-7gta.22). Instead it pins the properties that are
 * specific to *these* example files and fast/deterministic to check:
 * static correctness (parses, lints clean) and the graceful-failure UX
 * (no dependencies, no server) that is the whole point of a "runnable
 * example" — a broken example is worse than no example.
 */
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { ESLint } from "eslint";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(__dirname, "..", "..");
const CONFIG_PATH = path.join(REPO_ROOT, "eslint.config.js");
const CLI_PATH = path.join(
  REPO_ROOT,
  "examples/js-client/prompt-stream/main.js",
);
const SNIPPET_PATH = path.join(
  REPO_ROOT,
  "examples/js-client/browser-snippet/index.html",
);

/** Runs `runtime CLI_PATH ...args`, returning { exitCode, stdout, stderr }. */
function runCli(runtime, args, { timeout = 10_000 } = {}) {
  const proc = Bun.spawnSync({
    cmd: [runtime, CLI_PATH, ...args],
    cwd: REPO_ROOT,
    stdout: "pipe",
    stderr: "pipe",
    timeout,
  });
  return {
    exitCode: proc.exitCode,
    stdout: proc.stdout.toString("utf8"),
    stderr: proc.stderr.toString("utf8"),
  };
}

// No stack-trace line (e.g. "    at Object.<anonymous> ...") should ever
// reach the user for an expected failure mode — that's the difference
// between a clean example and a crashing one.
const STACK_TRACE_LINE = /^\s+at /m;

describe.each(["bun", "node"])(
  "prompt-stream CLI under %s (mitto-7gta.22)",
  (runtime) => {
    test("unknown flag: fails cleanly with exit 1, no stack trace", () => {
      const { exitCode, stderr } = runCli(runtime, ["--bogus", "x"]);
      expect(exitCode).toBe(1);
      expect(stderr).toContain("prompt-stream: unknown flag --bogus");
      expect(stderr).not.toMatch(STACK_TRACE_LINE);
    });

    test("unreachable server: fails cleanly with exit 1, no stack trace", () => {
      // Port 1 is a privileged port nothing listens on locally, so the
      // connection is refused immediately (no real network dependency, no
      // flakiness from an external host).
      const { exitCode, stderr } = runCli(runtime, [
        "--url",
        "http://127.0.0.1:1",
        "--prompt",
        "hi",
      ]);
      expect(exitCode).toBe(1);
      expect(stderr).toContain("prompt-stream: ");
      expect(stderr).not.toMatch(STACK_TRACE_LINE);
      // This exercises resolveWebSocketImpl() for the given runtime (Bun's
      // native WebSocket vs. Node's dynamic `import("ws")`) before ever
      // reaching the network call, so a broken runtime-detection branch
      // would surface here as a *different* error message, not this one.
      expect(stderr).not.toContain("needs the optional 'ws' package");
    });
  },
);

test("prompt-stream/main.js: examples/js-client/**/*.js eslint block reports zero problems (mitto-7gta.22)", async () => {
  const eslint = new ESLint({
    overrideConfigFile: CONFIG_PATH,
    cwd: REPO_ROOT,
  });
  const [result] = await eslint.lintText(readFileSync(CLI_PATH, "utf8"), {
    filePath: CLI_PATH,
  });
  expect(result.messages).toEqual([]);
});

describe("browser-snippet/index.html (mitto-7gta.22)", () => {
  const html = readFileSync(SNIPPET_PATH, "utf8");
  const inlineScript = html.match(
    /<script type="module">([\s\S]*?)<\/script>/,
  )?.[1];

  test('has exactly one balanced <script type="module"> block', () => {
    expect(inlineScript).toBeTruthy();
    expect((html.match(/<script /g) || []).length).toBe(1);
    expect((html.match(/<\/script>/g) || []).length).toBe(1);
  });

  test("inline script is syntactically valid JS", () => {
    const proc = Bun.spawnSync({
      cmd: ["node", "--check", "/dev/stdin"],
      stdin: Buffer.from(inlineScript, "utf8"),
      stdout: "pipe",
      stderr: "pipe",
    });
    expect(proc.exitCode).toBe(0);
  });

  test("resolves the SDK via a dynamic import(), not a static import (cross-origin fix, mitto-7gta.22)", () => {
    // A static `import ... from "/sdk/index.js"` only ever resolves against
    // this page's own origin, which silently breaks the documented
    // cross-origin mode (see the Implementation comment's "Deviations from
    // the plan"). Guard against regressing back to it.
    expect(inlineScript).toMatch(/\bimport\(/);
    expect(inlineScript).not.toMatch(
      /^\s*import\s.*from\s+["']\/sdk\/index\.js["']/m,
    );
  });

  test("supports both same-origin (no auth) and cross-origin (sharedTokenAuth) modes", () => {
    expect(inlineScript).toMatch(/sharedTokenAuth/);
    expect(inlineScript).toMatch(/auth:\s*token\s*\?\s*sharedTokenAuth/);
  });
});
