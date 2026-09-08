/**
 * Regression tests for the SDK's JSDoc-derived type declarations (mitto-7gta.20).
 *
 * Two layers of protection:
 *  - a live `tsc --noEmit` run: fails fast on any regression in JSDoc
 *    annotations under sdk/ (missing/incorrect types, TS1016 optional-after-
 *    required, etc.) without needing to regenerate or diff `types/`.
 *  - content checks on the *committed* `types/**` declarations: guard the
 *    specific structural-typing decisions recorded in the Implementation
 *    comment (typed `config` params, no blanket `@returns {object}`) that a
 *    passing `tsc` run alone would not catch — a widened-but-still-valid
 *    `any`/`object` return type produces zero tsc errors yet defeats the
 *    point of this bead.
 *
 * `make check-sdk-types` (wired into CI) remains the authoritative
 * freshness gate for `types/` vs. source; these tests are a fast, local
 * complement that runs with the rest of `bun test`.
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const SDK_DIR = dirname(fileURLToPath(import.meta.url));
const TYPES_DIR = join(SDK_DIR, "types");

function readType(relPath) {
  return readFileSync(join(TYPES_DIR, relPath), "utf8");
}

describe("SDK type declarations (mitto-7gta.20)", () => {
  test("tsc --noEmit reports zero errors over the SDK's JSDoc", () => {
    const proc = Bun.spawnSync({
      cmd: ["bunx", "tsc", "-p", "tsconfig.json", "--noEmit"],
      cwd: SDK_DIR,
      stdout: "pipe",
      stderr: "pipe",
    });
    const output = proc.stdout.toString("utf8") + proc.stderr.toString("utf8");
    expect(proc.exitCode).toBe(0);
    expect(output).not.toMatch(/error TS/);
  });

  test("createClient()'s return type exposes real per-resource method signatures, not any/object", () => {
    const indexDts = readType("index.d.ts");
    // Decision 6: dropping the blanket `@returns {object}` on each resource
    // factory should let tsc infer literal method signatures. Regressing to
    // a bare `object`/`any` return would still type-check cleanly, so this
    // must be asserted on content, not just on the tsc exit code.
    expect(indexDts).toMatch(/sessions:\s*\{/);
    expect(indexDts).toMatch(/prompts:\s*\{/);
    expect(indexDts).not.toMatch(/sessions:\s*(any|object)\s*;/);
    expect(indexDts).not.toMatch(/prompts:\s*(any|object)\s*;/);
    // The resolved config is exposed too, and must stay strongly typed.
    expect(indexDts).toMatch(/config:\s*Readonly<import\(["'].\/core\/config\.js["']\)\.ResolvedConfig>/);
  });

  test("resource factories type their config param as ResolvedConfig, not a bare object", () => {
    for (const rel of ["resources/sessions.d.ts", "resources/workspaces.d.ts", "resources/shortcuts.d.ts"]) {
      const dts = readType(rel);
      expect(dts).toMatch(/config:\s*import\(["'"]?\.\.\/core\/config\.js["'"]?\)\.ResolvedConfig/);
    }
  });

  test("core/errors.d.ts types constructor params via the *ErrorInfo typedefs, not a bare object", () => {
    const dts = readType("core/errors.d.ts");
    expect(dts).toMatch(/MittoApiErrorInfo/);
    expect(dts).toMatch(/MittoNetworkErrorInfo/);
  });

  test("core/transport.d.ts exposes a named RequestOptions type for the options bag", () => {
    const dts = readType("core/transport.d.ts");
    expect(dts).toMatch(/RequestOptions/);
  });

  test("core/transport.d.ts's RequestOptions exposes suppressUnauthorizedRedirect (mitto-4mz / mitto-2f6)", () => {
    // Regression guard for mitto-2f6: the committed .d.ts previously lagged
    // this source-level JSDoc addition, and `tsc --noEmit` above only checks
    // the *source*'s validity — it would stay green even if the committed
    // types/ went stale again. Asserting on the checked-in declaration
    // content (not just tsc's exit code) is what actually catches that.
    const dts = readType("core/transport.d.ts");
    expect(dts).toMatch(/suppressUnauthorizedRedirect\?:\s*boolean;/);
  });

  test("resource methods document their trailing opts bag as RequestOptions, not a bare object", () => {
    // The bead calls for real "request option" typedefs, so the documented
    // `opts` params must reference core/transport.js's RequestOptions rather
    // than collapsing to `object` — which type-checks cleanly and would
    // therefore survive both tsc and the freshness gate.
    for (const rel of [
      "resources/sessions.d.ts",
      "resources/config.d.ts",
      "resources/dashboard.d.ts",
      "resources/prompts.d.ts",
      "resources/shortcuts.d.ts",
      "resources/workspaces.d.ts",
    ]) {
      const dts = readType(rel);
      expect(dts).toMatch(/opts\?:\s*import\(["'][^"']*core\/transport\.js["']\)\.RequestOptions/);
      expect(dts).not.toMatch(/opts\?:\s*object/);
    }
  });

  test("misc resource + createClient() expose the full webauthn method surface (mitto-4mz / mitto-2f6)", () => {
    // Same freshness-regression rationale as the transport test above: the
    // committed types/ previously lagged the webauthn methods that mitto-4mz
    // added to the SDK source's JSDoc.
    const miscDts = readType("resources/misc.d.ts");
    const indexDts = readType("index.d.ts");
    for (const dts of [miscDts, indexDts]) {
      expect(dts).toMatch(/webauthn:\s*\{/);
      expect(dts).toMatch(/registerBegin:\s*\(opts:\s*any\)\s*=>\s*Promise<any>/);
      expect(dts).toMatch(/registerFinish:\s*\(credential:\s*object,\s*opts:\s*any\)\s*=>\s*Promise<any>/);
      expect(dts).toMatch(/delete:\s*\(id:\s*string,\s*opts:\s*any\)\s*=>\s*Promise<any>/);
      expect(dts).toMatch(/loginBegin:\s*\(opts:\s*any\)\s*=>\s*Promise<any>/);
      expect(dts).toMatch(/loginFinish:\s*\(assertion:\s*object,\s*opts:\s*any\)\s*=>\s*Promise<any>/);
    }
  });
});
