// Dual-runner mock-function shim for Jest and bun:test.
//
// Both runners expose a chainable mock with the same
// `.mockResolvedValue` / `.mockRejectedValue` / `.mockReturnValue`
// surface used by our unit tests, but they live in different modules
// that reject being loaded by the wrong runner:
//   - `@jest/globals` throws "Do not import @jest/globals outside of
//     the Jest test environment" when required from Bun.
//   - `bun:test` is only resolvable when running under `bun test`.
//
// Detect the runner and re-export the appropriate factory as `mockFn`.
// Top-level await is supported by Jest's `--experimental-vm-modules`
// loader (see the `test` script in package.json) and by Bun natively.

let _mockFn;
if (typeof Bun !== "undefined") {
  const { mock } = await import("bun:test");
  _mockFn = mock;
} else {
  const { jest } = await import("@jest/globals");
  _mockFn = jest.fn.bind(jest);
}

export const mockFn = _mockFn;
