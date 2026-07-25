/**
 * Unit tests for tildifyPath — home-directory prefix shortening for display.
 */

import { describe, test, expect } from "./testing/testGlobals.js";
import { tildifyPath } from "./paths.js";

describe("tildifyPath", () => {
  test("shortens a macOS home path", () => {
    expect(
      tildifyPath("/Users/alvaro/Development/inercia/blog-inerciatech"),
    ).toBe("~/Development/inercia/blog-inerciatech");
  });

  test("shortens a Linux home path", () => {
    expect(tildifyPath("/home/alice/projects/foo")).toBe("~/projects/foo");
  });

  test("returns '~' for exactly /Users/<name>", () => {
    expect(tildifyPath("/Users/alvaro")).toBe("~");
  });

  test("returns '~' for /Users/<name>/ (trailing slash)", () => {
    expect(tildifyPath("/Users/alvaro/")).toBe("~");
  });

  test("returns '~' for exactly /home/<name>", () => {
    expect(tildifyPath("/home/alice")).toBe("~");
  });

  test("leaves non-home absolute paths unchanged", () => {
    expect(tildifyPath("/opt/nowhere")).toBe("/opt/nowhere");
  });

  test("leaves '/Users' alone unchanged (no user segment)", () => {
    expect(tildifyPath("/Users")).toBe("/Users");
  });

  test("leaves '/Users/' unchanged (empty user segment)", () => {
    expect(tildifyPath("/Users/")).toBe("/Users/");
  });

  test("does not match '/Usersfoo/bar' (must be anchored on '/Users/')", () => {
    expect(tildifyPath("/Usersfoo/bar")).toBe("/Usersfoo/bar");
  });

  test("empty string passes through", () => {
    expect(tildifyPath("")).toBe("");
  });

  test("null passes through unchanged", () => {
    expect(tildifyPath(null)).toBe(null);
  });

  test("undefined passes through unchanged", () => {
    expect(tildifyPath(undefined)).toBe(undefined);
  });

  test("handles deeply nested paths", () => {
    expect(
      tildifyPath("/Users/alice/deep/nested/path/with/many/segments"),
    ).toBe("~/deep/nested/path/with/many/segments");
  });

  test("preserves spaces in path segments", () => {
    expect(tildifyPath("/Users/alvaro/My Drive/Personal")).toBe(
      "~/My Drive/Personal",
    );
  });

  test("handles non-ascii usernames", () => {
    expect(tildifyPath("/Users/álvaro/proj")).toBe("~/proj");
  });
});
