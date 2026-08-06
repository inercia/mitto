/**
 * Unit tests for tildifyPath — home-directory prefix shortening for display.
 */

import { describe, test, expect } from "./testing/testGlobals.js";
import {
  tildifyPath,
  getRelativePathIfInWorkspace,
  routeDroppedPaths,
} from "./paths.js";

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

describe("getRelativePathIfInWorkspace", () => {
  test("returns the relative path for a file inside the workspace", () => {
    expect(
      getRelativePathIfInWorkspace("/repo/src/main.go", "/repo"),
    ).toBe("src/main.go");
  });

  test("returns null for a file outside the workspace", () => {
    expect(
      getRelativePathIfInWorkspace("/Users/alvaro/Downloads/GLB.md", "/repo"),
    ).toBe(null);
  });

  test("returns null for a path that merely shares the workspace prefix", () => {
    // "/repo-other" must not be treated as inside "/repo".
    expect(
      getRelativePathIfInWorkspace("/repo-other/file.txt", "/repo"),
    ).toBe(null);
  });

  test("tolerates a trailing slash on the workspace path", () => {
    expect(
      getRelativePathIfInWorkspace("/repo/src/main.go", "/repo/"),
    ).toBe("src/main.go");
  });

  test("returns null for empty/missing inputs", () => {
    expect(getRelativePathIfInWorkspace("", "/repo")).toBe(null);
    expect(getRelativePathIfInWorkspace("/repo/file.txt", "")).toBe(null);
    expect(getRelativePathIfInWorkspace(null, "/repo")).toBe(null);
  });
});

describe("routeDroppedPaths", () => {
  test("routes an all-inside-workspace drop entirely to insertAsText", () => {
    expect(
      routeDroppedPaths(["/repo/a.txt", "/repo/sub/b.txt"], "/repo"),
    ).toEqual({
      insertAsText: ["a.txt", "sub/b.txt"],
      uploadFromPath: [],
    });
  });

  test("mitto-q8fx: routes an outside-workspace path to uploadFromPath instead of dropping it", () => {
    expect(
      routeDroppedPaths(["/Users/alvaro/Downloads/GLB.md"], "/repo"),
    ).toEqual({
      insertAsText: [],
      uploadFromPath: ["/Users/alvaro/Downloads/GLB.md"],
    });
  });

  test("splits a mixed inside/outside drop into both buckets", () => {
    expect(
      routeDroppedPaths(
        ["/repo/a.txt", "/Users/alvaro/Downloads/GLB.md"],
        "/repo",
      ),
    ).toEqual({
      insertAsText: ["a.txt"],
      uploadFromPath: ["/Users/alvaro/Downloads/GLB.md"],
    });
  });

  test("returns empty buckets for no paths", () => {
    expect(routeDroppedPaths([], "/repo")).toEqual({
      insertAsText: [],
      uploadFromPath: [],
    });
  });
});
