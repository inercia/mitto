/**
 * Drift-detection tests for web/static/sdk/realtime/events.js (mitto-7gta.16).
 *
 * Reads the Go WSMsgType* constant registry and the protocol spec's
 * markdown headings straight from disk (same pattern as
 * session-stream.test.js's whole-directory purity scan) and asserts this
 * file's EVENTS/COMMANDS/LEGACY_EVENTS never silently diverge from either.
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  EVENTS,
  COMMANDS,
  LEGACY_EVENTS,
  isKnownEventType,
  isCommandType,
} from "./events.js";

const REPO_ROOT = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..", "..");

const GO_CONSTANT_FILES = [
  join(REPO_ROOT, "internal", "web", "ws_messages.go"),
  join(REPO_ROOT, "internal", "conversation", "ws_events.go"),
];

const SPEC_FILE = join(REPO_ROOT, "docs", "devel", "websockets", "protocol-spec.md");

const EVENTS_FILE = join(dirname(fileURLToPath(import.meta.url)), "events.js");

/** Every `@typedef {...} XxxPayload` name declared in events.js. */
function declaredPayloadTypedefs() {
  const src = readFileSync(EVENTS_FILE, "utf8");
  const names = new Set();
  const re = /@typedef\s+\{[^}]*\}\s+([A-Za-z0-9_]+Payload)\b/g;
  let m;
  while ((m = re.exec(src))) names.add(m[1]);
  return names;
}

/** `session_ui_prompt` -> `SessionUiPromptPayload`. */
function payloadTypedefName(value) {
  return (
    value
      .split("_")
      .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
      .join("") + "Payload"
  );
}

/** Every `WSMsgTypeXxx = "..."` string value declared in the Go registry. */
function goConstantValues() {
  const values = new Set();
  for (const file of GO_CONSTANT_FILES) {
    const src = readFileSync(file, "utf8");
    const re = /WSMsgType[A-Za-z]+\s*=\s*"([a-z_]+)"/g;
    let m;
    while ((m = re.exec(src))) values.add(m[1]);
  }
  return values;
}

/**
 * Every backtick-quoted `type` string in a `###`/`####` heading of the
 * protocol spec, e.g. `#### \`session_archive_pending\` — ...` or
 * `#### \`file_read\` / \`file_write\` — ...` (both backtick tokens count).
 */
function specHeadingTypes() {
  const src = readFileSync(SPEC_FILE, "utf8");
  const types = new Set();
  for (const line of src.split("\n")) {
    if (!/^#{3,4}\s/.test(line)) continue;
    const re = /`([a-z_]+)`/g;
    let m;
    while ((m = re.exec(line))) types.add(m[1]);
  }
  return types;
}

/**
 * All values this SDK recognizes as valid wire types. Includes
 * LEGACY_EVENTS: the four aliases (permission, permission_answer,
 * sync_session, session_sync) still have live Go constants and are still
 * handled by the frontend switch for back-compat, so they must count for
 * the Go-registry comparison even though the spec documents them only in
 * the "Legacy Messages" table, not as their own `###`/`####` heading.
 */
function sdkValues() {
  return new Set([
    ...Object.values(EVENTS),
    ...Object.values(COMMANDS),
    ...Object.values(LEGACY_EVENTS),
  ]);
}

describe("events.js vs Go WSMsgType* constants (source of truth)", () => {
  test("every Go constant value is a known SDK event or command", () => {
    const go = goConstantValues();
    const sdk = sdkValues();
    const missing = [...go].filter((v) => !sdk.has(v));
    expect(missing).toEqual([]);
  });

  test("every SDK event/command value has a matching Go constant", () => {
    const go = goConstantValues();
    const sdk = sdkValues();
    const extra = [...sdk].filter((v) => !go.has(v));
    expect(extra).toEqual([]);
  });

  test("Go registry is non-trivial (sanity check the parser found something)", () => {
    expect(goConstantValues().size).toBeGreaterThan(50);
  });
});

describe("events.js vs docs/devel/websockets/protocol-spec.md", () => {
  test("every documented heading type is a known SDK type", () => {
    const spec = specHeadingTypes();
    const known = sdkValues();
    const undocumentedInSdk = [...spec].filter((v) => !known.has(v));
    expect(undocumentedInSdk).toEqual([]);
  });

  test("every non-legacy SDK event is documented in the spec", () => {
    const spec = specHeadingTypes();
    const undocumented = Object.values(EVENTS).filter((v) => !spec.has(v));
    expect(undocumented).toEqual([]);
  });
});

describe("EVENTS / COMMANDS / LEGACY_EVENTS shape", () => {
  test("no duplicate values within or across EVENTS and COMMANDS", () => {
    const all = [...Object.values(EVENTS), ...Object.values(COMMANDS)];
    expect(new Set(all).size).toBe(all.length);
  });

  test("EVENTS, COMMANDS, and LEGACY_EVENTS are frozen", () => {
    expect(Object.isFrozen(EVENTS)).toBe(true);
    expect(Object.isFrozen(COMMANDS)).toBe(true);
    expect(Object.isFrozen(LEGACY_EVENTS)).toBe(true);
  });

  test("isKnownEventType / isCommandType recognize current and legacy types", () => {
    expect(isKnownEventType(EVENTS.AGENT_MESSAGE)).toBe(true);
    expect(isKnownEventType(LEGACY_EVENTS.PERMISSION)).toBe(true);
    expect(isKnownEventType("not_a_real_type")).toBe(false);

    expect(isCommandType(COMMANDS.PROMPT)).toBe(true);
    expect(isCommandType(LEGACY_EVENTS.SYNC_SESSION)).toBe(true);
    expect(isCommandType("not_a_real_type")).toBe(false);
  });

  test("isKnownEventType / isCommandType do not cross-recognize non-legacy values", () => {
    // A command-only value (never emitted server->client) must not read as a
    // known *event*, and an event-only value (never sent client->server)
    // must not read as a known *command* — otherwise a host could route a
    // request type through response-handling logic (or vice versa) without
    // either predicate catching it.
    for (const value of Object.values(COMMANDS)) {
      expect(isKnownEventType(value)).toBe(false);
    }
    for (const value of Object.values(EVENTS)) {
      expect(isCommandType(value)).toBe(false);
    }
  });

  test("no key collisions between EVENTS, COMMANDS, and LEGACY_EVENTS", () => {
    const keySets = [Object.keys(EVENTS), Object.keys(COMMANDS), Object.keys(LEGACY_EVENTS)];
    const allKeys = keySets.flat();
    expect(new Set(allKeys).size).toBe(allKeys.length);
  });

  test("every key is the SCREAMING_SNAKE_CASE form of its own value", () => {
    // Guards against copy-paste drift where a value is updated but the key
    // is left stale (or vice versa), which would silently break the
    // predictable key<->wire-string mapping hosts rely on for autocomplete.
    const toKey = (value) => value.toUpperCase();
    for (const [maps, name] of [
      [EVENTS, "EVENTS"],
      [COMMANDS, "COMMANDS"],
      [LEGACY_EVENTS, "LEGACY_EVENTS"],
    ]) {
      const mismatches = Object.entries(maps)
        .filter(([key, value]) => key !== toKey(value))
        .map(([key, value]) => `${name}.${key} = "${value}"`);
      expect(mismatches).toEqual([]);
    }
  });
});

describe("payload typedefs", () => {
  test("every event, command, and legacy type has a payload typedef", () => {
    const declared = declaredPayloadTypedefs();
    const missing = [...sdkValues()]
      .map(payloadTypedefName)
      .filter((name) => !declared.has(name));
    expect(missing).toEqual([]);
  });

  test("every payload typedef corresponds to a known wire type", () => {
    const expected = new Set([...sdkValues()].map(payloadTypedefName));
    const orphaned = [...declaredPayloadTypedefs()].filter((name) => !expected.has(name));
    expect(orphaned).toEqual([]);
  });
});
