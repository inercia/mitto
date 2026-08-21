/**
 * Table-driven negative-path matrix across every SDK resource module
 * (mitto-7gta.23).
 *
 * The per-module test files (config/dashboard/files/issues/misc/processors/
 * prompts/sessions/shortcuts .test.js) already assert MittoApiError and
 * AbortSignal forwarding for their own methods, but none of them exercise a
 * network failure or an auth (401) failure through the resource layer —
 * those were previously only covered indirectly in core/transport.test.js
 * and core/errors.test.js, never through an actual resource method. This
 * file closes that gap with ONE declarative table (one representative
 * no-required-args method per module) instead of duplicating the same five
 * negative-path blocks nine times — the "declarative fake-server fixtures"
 * requirement made structural rather than aspirational. Adding a new
 * resource module later is a one-line entry in MODULES below.
 */
import { MittoApiError, MittoAuthError, MittoNetworkError } from "../core/errors.js";
import { apiFailure, authFailure, createFakeServer, networkFailure } from "../testing/fake-server.js";
import { createConfigResource } from "./config.js";
import { createDashboardResource } from "./dashboard.js";
import { createFilesResource } from "./files.js";
import { createIssuesResource } from "./issues.js";
import { createMiscResource } from "./misc.js";
import { createProcessorsResource } from "./processors.js";
import { createPromptsResource } from "./prompts.js";
import { createSessionsResource } from "./sessions.js";
import { createShortcutsResource } from "./shortcuts.js";
import { createWorkspacesResource } from "./workspaces.js";
import { createAcpServersResource } from "./acp-servers.js";
import { createAgentsResource } from "./agents.js";

/**
 * One representative, no-required-args (or trivially-satisfiable) call per
 * resource module. `build(config)` returns the resource; `call(resource,
 * opts)` invokes the representative method — forwarding `opts` (e.g.
 * `{signal}`) to the resource method's own `opts` parameter — and returns
 * its promise.
 */
const MODULES = [
  { name: "config", build: createConfigResource, call: (r, opts) => r.get(undefined, opts) },
  { name: "dashboard", build: createDashboardResource, call: (r, opts) => r.summary(undefined, opts) },
  { name: "files", build: createFilesResource, call: (r, opts) => r.list("s1", opts) },
  { name: "issues", build: createIssuesResource, call: (r, opts) => r.list(undefined, opts) },
  {
    name: "misc",
    build: (config) => createMiscResource(config, createConfigResource(config)),
    call: (r, opts) => r.csrfToken(opts),
  },
  { name: "processors", build: createProcessorsResource, call: (r, opts) => r.list("u1", opts) },
  { name: "prompts", build: createPromptsResource, call: (r, opts) => r.list(undefined, opts) },
  { name: "sessions", build: createSessionsResource, call: (r, opts) => r.list(opts) },
  { name: "shortcuts", build: createShortcutsResource, call: (r, opts) => r.getGlobal(undefined, opts) },
  { name: "workspaces", build: createWorkspacesResource, call: (r, opts) => r.list(undefined, opts) },
  { name: "acpServers", build: createAcpServersResource, call: (r, opts) => r.prepareDelete("srv", opts) },
  { name: "agents", build: createAgentsResource, call: (r, opts) => r.types(opts) },
];

describe.each(MODULES)("$name resource — negative paths", ({ build, call }) => {
  test("a fetch rejection surfaces as MittoNetworkError with the cause preserved", async () => {
    const { config, respondWith } = createFakeServer();
    respondWith(networkFailure("offline"));
    const resource = build(config);
    try {
      await call(resource);
      throw new Error("expected call() to reject");
    } catch (err) {
      expect(err).toBeInstanceOf(MittoNetworkError);
      expect(err.cause).toBeInstanceOf(Error);
      expect(err.cause.message).toBe("offline");
    }
  });

  test("a 401 response surfaces as MittoAuthError (which is also a MittoApiError)", async () => {
    const { config, respondWith } = createFakeServer();
    respondWith(authFailure());
    const resource = build(config);
    const err = await call(resource).catch((e) => e);
    expect(err).toBeInstanceOf(MittoAuthError);
    expect(err).toBeInstanceOf(MittoApiError);
    expect(err.status).toBe(401);
    expect(err.code).toBe("unauthenticated");
  });

  test("a structured {error:{code,message}} envelope maps status/code/message onto MittoApiError", async () => {
    const { config, respondWith } = createFakeServer();
    respondWith(apiFailure(409, "conflict", "already exists"));
    const resource = build(config);
    const err = await call(resource).catch((e) => e);
    expect(err).toBeInstanceOf(MittoApiError);
    expect(err.status).toBe(409);
    expect(err.code).toBe("conflict");
    expect(err.message).toBe("already exists");
  });

  test("a non-JSON error body falls back to the status-derived code", async () => {
    const { config, respondWith } = createFakeServer();
    respondWith(() => ({
      ok: false,
      status: 503,
      headers: { get: () => null },
      text: async () => "Service Unavailable",
    }));
    const resource = build(config);
    const err = await call(resource).catch((e) => e);
    expect(err).toBeInstanceOf(MittoApiError);
    expect(err.status).toBe(503);
    expect(err.code).toBe("unavailable");
  });

  test("an AbortSignal passed in opts reaches fetch's init", async () => {
    const { config, calls } = createFakeServer();
    const resource = build(config);
    const controller = new AbortController();
    await call(resource, { signal: controller.signal });
    expect(calls[0].init.signal).toBe(controller.signal);
  });
});
