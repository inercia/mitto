// Unit tests for the mitto-sus.2 WKWebView-leg dump helpers in perfMarks.js
// (dumpPerfBufferToFile / exposePerfDumpForConsole). Follows the
// window-global pattern from phaseState.test.js / websocket.test.js — no
// import needed for describe/test/expect under bun:test.

import {
  dumpPerfBufferToFile,
  dumpPerfBufferToServer,
  exposePerfDumpForConsole,
  _resetPerfEnabledCacheForTests,
} from "./perfMarks.js";
import { mockFn as mock } from "./testing/mockFn.js";

function resetPerfState() {
  _resetPerfEnabledCacheForTests();
  delete window.__mittoPerf;
  delete window.__mittoPerfBuffer;
  delete window.mittoDumpPerfBuffer;
  delete window.mittoPerfDump;
  delete window.mittoPerfDumpServer;
  delete window.mittoApiPrefix;
  delete global.fetch;
}

beforeEach(() => {
  resetPerfState();
});

afterEach(() => {
  resetPerfState();
});

describe("dumpPerfBufferToFile", () => {
  test("returns false and does not call the bind when perf is disabled", () => {
    const bind = mock(() => {});
    window.mittoDumpPerfBuffer = bind;
    window.__mittoPerfBuffer = [
      { name: "mitto.x", entryType: "mark", startTime: 1, duration: 0 },
    ];

    expect(dumpPerfBufferToFile("scenario", "/tmp/out.jsonl")).toBe(false);
    expect(bind).not.toHaveBeenCalled();
  });

  test("returns false when perf is enabled but the native bind is absent", () => {
    window.__mittoPerf = true;
    expect(dumpPerfBufferToFile("scenario", "/tmp/out.jsonl")).toBe(false);
  });

  test("serializes the buffer into JSONL and calls the bind with (path, content)", () => {
    window.__mittoPerf = true;
    window.__mittoPerfBuffer = [
      {
        name: "mitto.composer.keystroke",
        entryType: "mark",
        startTime: 12.5,
        duration: 3.25,
      },
      {
        name: "mitto.ws.chunk.applied",
        entryType: "measure",
        startTime: 20,
        duration: 1.5,
        detail: { count: 4 },
      },
    ];
    const bind = mock(() => {});
    window.mittoDumpPerfBuffer = bind;

    expect(dumpPerfBufferToFile("composer.keystroke", "/tmp/out.jsonl")).toBe(
      true,
    );
    expect(bind).toHaveBeenCalledTimes(1);
    const [path, content] = bind.mock.calls[0];
    expect(path).toBe("/tmp/out.jsonl");

    const lines = content
      .trim()
      .split("\n")
      .map((l) => JSON.parse(l));
    expect(lines).toHaveLength(2);
    expect(lines[0]).toMatchObject({
      scenario: "composer.keystroke",
      metric: "mitto.composer.keystroke",
      value: 3.25,
      meta: { entryType: "mark", startTime: 12.5 },
    });
    expect(lines[0].meta.detail).toBeUndefined();
    expect(lines[1].meta.detail).toEqual({ count: 4 });
    expect(typeof lines[0].ts).toBe("string");
  });

  test("writes an empty string when the buffer is empty (still returns true)", () => {
    window.__mittoPerf = true;
    window.__mittoPerfBuffer = [];
    const bind = mock(() => {});
    window.mittoDumpPerfBuffer = bind;

    expect(dumpPerfBufferToFile("scenario", "/tmp/out.jsonl")).toBe(true);
    expect(bind).toHaveBeenCalledWith("/tmp/out.jsonl", "");
  });

  test("treats a missing __mittoPerfBuffer as empty rather than throwing", () => {
    window.__mittoPerf = true;
    const bind = mock(() => {});
    window.mittoDumpPerfBuffer = bind;

    expect(dumpPerfBufferToFile("scenario", "/tmp/out.jsonl")).toBe(true);
    expect(bind).toHaveBeenCalledWith("/tmp/out.jsonl", "");
  });

  test("never throws — a bind that throws degrades to false", () => {
    window.__mittoPerf = true;
    window.__mittoPerfBuffer = [
      { name: "mitto.x", entryType: "mark", startTime: 0, duration: 0 },
    ];
    window.mittoDumpPerfBuffer = () => {
      throw new Error("native bind failed");
    };

    expect(() =>
      dumpPerfBufferToFile("scenario", "/tmp/out.jsonl"),
    ).not.toThrow();
    expect(dumpPerfBufferToFile("scenario", "/tmp/out.jsonl")).toBe(false);
  });
});

describe("dumpPerfBufferToServer", () => {
  test("returns false and does not call fetch when perf is disabled", async () => {
    global.fetch = mock(() => Promise.resolve({ ok: true }));
    window.__mittoPerfBuffer = [
      { name: "mitto.x", entryType: "mark", startTime: 1, duration: 0 },
    ];

    expect(await dumpPerfBufferToServer("scenario", "ios-safari")).toBe(false);
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("POSTs the serialized JSONL body to /api/perf/dump with label/scenario query params", async () => {
    window.__mittoPerf = true;
    window.mittoApiPrefix = "/mitto";
    window.__mittoPerfBuffer = [
      {
        name: "mitto.composer.keystroke",
        entryType: "mark",
        startTime: 12.5,
        duration: 3.25,
      },
    ];
    global.fetch = mock(() => Promise.resolve({ ok: true }));

    expect(
      await dumpPerfBufferToServer("composer.keystroke", "ios-safari"),
    ).toBe(true);
    expect(global.fetch).toHaveBeenCalledTimes(1);
    const [url, options] = global.fetch.mock.calls[0];
    expect(url).toBe(
      "/mitto/api/perf/dump?label=ios-safari&scenario=composer.keystroke",
    );
    expect(options.method).toBe("POST");
    expect(options.credentials).toBe("include");
    const lines = options.body
      .trim()
      .split("\n")
      .map((l) => JSON.parse(l));
    expect(lines).toHaveLength(1);
    expect(lines[0]).toMatchObject({
      scenario: "composer.keystroke",
      metric: "mitto.composer.keystroke",
      value: 3.25,
    });
  });

  test("returns false when the response is non-2xx", async () => {
    window.__mittoPerf = true;
    global.fetch = mock(() => Promise.resolve({ ok: false }));

    expect(await dumpPerfBufferToServer("scenario", "ios-safari")).toBe(false);
  });

  test("never throws — a rejected fetch degrades to false", async () => {
    window.__mittoPerf = true;
    global.fetch = mock(() => Promise.reject(new Error("network down")));

    await expect(
      dumpPerfBufferToServer("scenario", "ios-safari"),
    ).resolves.toBe(false);
  });
});

describe("exposePerfDumpForConsole", () => {
  test("does not set window.mittoPerfDump when perf is disabled", () => {
    exposePerfDumpForConsole();
    expect(window.mittoPerfDump).toBeUndefined();
  });

  test("exposes dumpPerfBufferToFile as window.mittoPerfDump when perf is enabled", () => {
    window.__mittoPerf = true;
    exposePerfDumpForConsole();
    expect(window.mittoPerfDump).toBe(dumpPerfBufferToFile);
  });

  test("exposes dumpPerfBufferToServer as window.mittoPerfDumpServer when perf is enabled", () => {
    window.__mittoPerf = true;
    exposePerfDumpForConsole();
    expect(window.mittoPerfDumpServer).toBe(dumpPerfBufferToServer);
  });
});
