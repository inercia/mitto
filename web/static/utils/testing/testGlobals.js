// Dual-runner re-export of the standard Jest/Bun test lifecycle globals and
// the `jest` namespace so a single import line works under both Jest and
// bun:test.
//
// Under Jest, `describe`/`it`/`expect`/`beforeEach`/`afterEach`/`beforeAll`/
// `afterAll` are already injected as globals (see the `test` script in
// package.json which runs with `--experimental-vm-modules`). Re-importing
// them from `@jest/globals` is redundant but harmless.
//
// Under bun:test, those same names are provided as named exports on the
// `bun:test` module — BUT any test file that also imports from
// `@jest/globals` disables bun's auto-injection of the globals, so importing
// them explicitly from this shim is necessary.
//
// The `jest` namespace is exported from both modules. Under Bun it is a
// compat object exposing the subset most tests rely on (`fn`, `spyOn`,
// `useFakeTimers`, `useRealTimers`, `advanceTimersByTime`, `restoreAllMocks`,
// `clearAllMocks`, `resetAllMocks`, `mock`, ...). Under Jest it is the real
// Jest global.
//
// Top-level await is supported by Jest's `--experimental-vm-modules` loader
// and by Bun natively.

let _mod;
if (typeof Bun !== "undefined") {
  _mod = await import("bun:test");
} else {
  _mod = await import("@jest/globals");
}

export const describe = _mod.describe;
export const it = _mod.it;
export const test = _mod.test;
export const expect = _mod.expect;
export const beforeEach = _mod.beforeEach;
export const afterEach = _mod.afterEach;
export const beforeAll = _mod.beforeAll;
export const afterAll = _mod.afterAll;
export const jest = _mod.jest;
