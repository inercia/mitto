/**
 * Unit tests for the SDK-native endpoint registry (mitto-7gta.6).
 *
 * Ported from the legacy web/static/utils/endpoints.test.js suite: instead
 * of mutating `window.mittoApiPrefix` between assertions, each test injects
 * a config object directly into `createEndpoints()`. Same coverage: prefix
 * handling, query-string encoding, path-param encoding, null/undefined/""
 * param omission, and a representative builder from each resource group —
 * plus new ws-builder cases (explicit wsBaseUrl, https->wss/http->ws scheme
 * mapping, missing-wsBaseUrl ConfigError) that the window-based shim test
 * could not express directly against createEndpoints().
 */
import { createEndpoints } from "./endpoints.js";
import { ConfigError } from "./errors.js";

/** Shorthand: registry with a relative baseUrl (the common browser case)
 *  and the given apiPrefix; no wsBaseUrl unless passed. */
function mk(apiPrefix = "", options) {
  return createEndpoints({ baseUrl: "", apiPrefix }, options);
}

describe("endpoints registry", () => {
  // ---------------------------------------------------------------------
  // Prefix handling
  // ---------------------------------------------------------------------
  describe("prefix handling", () => {
    test("applies /mitto prefix when set", () => {
      expect(mk("/mitto").sessions.list()).toBe("/mitto/api/sessions");
    });

    test("no prefix when apiPrefix is empty string", () => {
      expect(mk("").sessions.list()).toBe("/api/sessions");
    });

    test("no prefix when apiPrefix is undefined", () => {
      expect(mk(undefined).sessions.list()).toBe("/api/sessions");
    });

    test("path-param builder also respects prefix", () => {
      expect(mk("/mitto").sessions.get("20260101-120000-deadbeef")).toBe(
        "/mitto/api/sessions/20260101-120000-deadbeef",
      );
    });
  });

  // ---------------------------------------------------------------------
  // Query-string building (qs helper via builders)
  // ---------------------------------------------------------------------
  describe("query-string encoding", () => {
    const endpoints = mk("");

    test("omits '?' when no params object", () => {
      expect(endpoints.issues.list()).toBe("/api/issues");
    });

    test("omits '?' when params object is empty", () => {
      expect(endpoints.issues.list({})).toBe("/api/issues");
    });

    test("omits null param values", () => {
      expect(endpoints.issues.list({ working_dir: null })).toBe("/api/issues");
    });

    test("omits undefined param values", () => {
      expect(endpoints.issues.list({ working_dir: undefined })).toBe(
        "/api/issues",
      );
    });

    test("omits empty-string param values", () => {
      expect(endpoints.issues.list({ working_dir: "" })).toBe("/api/issues");
    });

    test("encodes special chars in param values via URLSearchParams", () => {
      const url = endpoints.issues.list({
        working_dir: "/home/user/my project",
      });
      expect(url).toBe("/api/issues?working_dir=%2Fhome%2Fuser%2Fmy+project");
    });

    test("encodes '&' in param value", () => {
      const url = endpoints.misc.checkFileExists({ path: "a&b" });
      expect(url).toContain("path=a%26b");
    });

    test("multiple params produce '&'-joined query string", () => {
      const url = endpoints.issues.config({ working_dir: "/x", key: "k" });
      expect(url).toContain("working_dir=");
      expect(url).toContain("key=k");
      expect(url).toContain("?");
    });

    test("keeps params whose value is 0 or false", () => {
      const url = endpoints.workspaces.list({ page: 0 });
      expect(url).toContain("page=0");
    });
  });

  // ---------------------------------------------------------------------
  // Path-param encoding
  // ---------------------------------------------------------------------
  describe("path-param encoding", () => {
    const endpoints = mk("");

    test("encodes slashes in issue id", () => {
      const url = endpoints.issues.show("proj/issue-1", { working_dir: "/x" });
      expect(url).toContain("/api/issues/proj%2Fissue-1");
    });

    test("encodes spaces in workspace uuid", () => {
      const url = endpoints.workspaces.metadata("uuid with space");
      expect(url).toBe("/api/workspaces/uuid%20with%20space/metadata");
    });

    test("encodes special chars in session id", () => {
      const url = endpoints.sessions.queueMove("sess?id", "msg&1");
      expect(url).toBe("/api/sessions/sess%3Fid/queue/msg%261/move");
    });

    test("encodes prompt name with slash", () => {
      const url = endpoints.workspacePrompts.get("team/my-prompt");
      expect(url).toBe("/api/workspace-prompts/team%2Fmy-prompt");
    });
  });

  // ---------------------------------------------------------------------
  // Representative builder from each resource group
  // ---------------------------------------------------------------------
  describe("issues group", () => {
    const endpoints = mk("");

    test("list — base path", () =>
      expect(endpoints.issues.list()).toBe("/api/issues"));
    test("list — with working_dir", () =>
      expect(endpoints.issues.list({ working_dir: "/w" })).toBe(
        "/api/issues?working_dir=%2Fw",
      ));
    test("stats", () =>
      expect(endpoints.issues.stats({ working_dir: "/w" })).toBe(
        "/api/issues/stats?working_dir=%2Fw",
      ));
    test("show", () =>
      expect(endpoints.issues.show("abc-1")).toBe("/api/issues/abc-1"));
    test("show — with working_dir", () =>
      expect(endpoints.issues.show("abc-1", { working_dir: "/w" })).toBe(
        "/api/issues/abc-1?working_dir=%2Fw",
      ));
    test("create — with working_dir", () =>
      expect(endpoints.issues.create({ working_dir: "/w" })).toBe(
        "/api/issues?working_dir=%2Fw",
      ));
    test("update — with working_dir", () =>
      expect(endpoints.issues.update("abc-1", { working_dir: "/w" })).toBe(
        "/api/issues/abc-1?working_dir=%2Fw",
      ));
    test("remove — with working_dir", () =>
      expect(endpoints.issues.remove("abc-1", { working_dir: "/w" })).toBe(
        "/api/issues/abc-1?working_dir=%2Fw",
      ));
    test("status sub-resource", () =>
      expect(endpoints.issues.status("abc-1")).toBe(
        "/api/issues/abc-1/status",
      ));
    test("status — with working_dir", () =>
      expect(endpoints.issues.status("abc-1", { working_dir: "/w" })).toBe(
        "/api/issues/abc-1/status?working_dir=%2Fw",
      ));
    test("comments sub-resource", () =>
      expect(endpoints.issues.comments("abc-1")).toBe(
        "/api/issues/abc-1/comments",
      ));
    test("comments — with working_dir", () =>
      expect(endpoints.issues.comments("abc-1", { working_dir: "/w" })).toBe(
        "/api/issues/abc-1/comments?working_dir=%2Fw",
      ));
    test("dependencies sub-resource", () =>
      expect(endpoints.issues.dependencies("x")).toBe(
        "/api/issues/x/dependencies",
      ));
    test("dependencies — with working_dir", () =>
      expect(endpoints.issues.dependencies("x", { working_dir: "/w" })).toBe(
        "/api/issues/x/dependencies?working_dir=%2Fw",
      ));
    test("cleanup", () =>
      expect(endpoints.issues.cleanup()).toBe("/api/issues/cleanup"));
    test("cleanup — with working_dir", () =>
      expect(endpoints.issues.cleanup({ working_dir: "/w" })).toBe(
        "/api/issues/cleanup?working_dir=%2Fw",
      ));
    test("config — base", () =>
      expect(endpoints.issues.config()).toBe("/api/issues/config"));
    test("config — with working_dir + key (DELETE scenario)", () => {
      const url = endpoints.issues.config({
        working_dir: "/w",
        key: "jira.url",
      });
      expect(url).toContain("working_dir=");
      expect(url).toContain("key=jira.url");
    });
    test("upstream", () =>
      expect(endpoints.issues.upstream()).toBe("/api/issues/upstream"));
    test("upstream — with working_dir", () =>
      expect(endpoints.issues.upstream({ working_dir: "/w" })).toBe(
        "/api/issues/upstream?working_dir=%2Fw",
      ));
    test("database mode — with working_dir", () =>
      expect(endpoints.issues.databaseMode({ working_dir: "/w" })).toBe(
        "/api/issues/database-mode?working_dir=%2Fw",
      ));
    test("sync", () =>
      expect(endpoints.issues.sync()).toBe("/api/issues/sync"));
    test("sync — with working_dir", () =>
      expect(endpoints.issues.sync({ working_dir: "/w" })).toBe(
        "/api/issues/sync?working_dir=%2Fw",
      ));
  });

  describe("sessions group", () => {
    const endpoints = mk("");

    test("running", () =>
      expect(endpoints.sessions.running()).toBe("/api/sessions/running"));
    test("get(id)", () =>
      expect(endpoints.sessions.get("s1")).toBe("/api/sessions/s1"));
    test("loop", () =>
      expect(endpoints.sessions.loop("s1")).toBe("/api/sessions/s1/loop"));
    test("loopRunNow", () =>
      expect(endpoints.sessions.loopRunNow("s1")).toBe(
        "/api/sessions/s1/loop/run-now",
      ));
    test("queueMove", () =>
      expect(endpoints.sessions.queueMove("s1", "m1")).toBe(
        "/api/sessions/s1/queue/m1/move",
      ));
    test("images", () =>
      expect(endpoints.sessions.images("s1")).toBe("/api/sessions/s1/images"));
    test("prune", () =>
      expect(endpoints.sessions.prune("s1")).toBe("/api/sessions/s1/prune"));
    test("image(id, imageId)", () =>
      expect(endpoints.sessions.image("s1", "img1")).toBe(
        "/api/sessions/s1/images/img1",
      ));
    test("filesFromPath", () =>
      expect(endpoints.sessions.filesFromPath("s1")).toBe(
        "/api/sessions/s1/files/from-path",
      ));

    describe("promptArgCache", () => {
      test("produces correct path with prompt query param", () => {
        const url = endpoints.sessions.promptArgCache("sess-1", "my-prompt");
        expect(url).toBe(
          "/api/sessions/sess-1/prompt-arg-cache?prompt=my-prompt",
        );
      });

      test("encodes special chars in session id", () => {
        const url = endpoints.sessions.promptArgCache("sess/id", "p");
        expect(url).toContain("/api/sessions/sess%2Fid/prompt-arg-cache");
      });

      test("encodes special chars in prompt name", () => {
        const url = endpoints.sessions.promptArgCache("s1", "team/my prompt");
        expect(url).toContain("prompt=team%2Fmy+prompt");
      });

      test("respects apiPrefix", () => {
        const url = mk("/mitto").sessions.promptArgCache("s1", "p");
        expect(url).toContain("/mitto/api/sessions/s1/prompt-arg-cache");
      });
    });
  });

  describe("workspaces group", () => {
    const endpoints = mk("");

    test("list", () =>
      expect(endpoints.workspaces.list()).toBe("/api/workspaces"));
    test("mcpTools", () =>
      expect(endpoints.workspaces.mcpTools("uuid-1")).toBe(
        "/api/workspaces/uuid-1/mcp-tools",
      ));
    test("mcpToolsInstall", () =>
      expect(endpoints.workspaces.mcpToolsInstall("u")).toBe(
        "/api/workspaces/u/mcp-tools/install",
      ));
    test("processor", () =>
      expect(endpoints.workspaces.processor("u", "myproc")).toBe(
        "/api/workspaces/u/processors/myproc",
      ));
    test("processorArguments", () =>
      expect(endpoints.workspaces.processorArguments("u", "myproc")).toBe(
        "/api/workspaces/u/processors/myproc/arguments",
      ));
    test("processorArguments encodes special chars in name", () =>
      expect(endpoints.workspaces.processorArguments("u", "my proc/v2")).toBe(
        "/api/workspaces/u/processors/my%20proc%2Fv2/arguments",
      ));
  });

  describe("workspacePrompts group", () => {
    const endpoints = mk("");

    test("list", () =>
      expect(endpoints.workspacePrompts.list()).toBe("/api/workspace-prompts"));
    test("get", () =>
      expect(endpoints.workspacePrompts.get("p")).toBe(
        "/api/workspace-prompts/p",
      ));
  });

  describe("other groups", () => {
    const endpoints = mk("");

    test("config.get", () =>
      expect(endpoints.config.get()).toBe("/api/config"));
    test("config.get with acp_server", () =>
      expect(endpoints.config.get({ acp_server: "server-a" })).toBe(
        "/api/config?acp_server=server-a",
      ));
    test("config.get with acp_server and session_id", () => {
      const url = endpoints.config.get({
        acp_server: "server-a",
        session_id: "s1",
      });
      expect(url).toContain("acp_server=server-a");
      expect(url).toContain("session_id=s1");
    });
    test("config.get skips null params", () =>
      expect(endpoints.config.get({ acp_server: null, session_id: null })).toBe(
        "/api/config",
      ));
    test("config.update", () =>
      expect(endpoints.config.update()).toBe("/api/config"));
    test("agents.types", () =>
      expect(endpoints.agents.types()).toBe("/api/agents/types"));
    test("agents.scan", () =>
      expect(endpoints.agents.scan()).toBe("/api/agents/scan"));
    test("acpServers.prepareDelete encodes name", () =>
      expect(endpoints.acpServers.prepareDelete("Auggie (Gemini Pro)")).toBe(
        "/api/acp-servers/Auggie%20(Gemini%20Pro)/prepare-delete",
      ));
    test("acpServers.reassignAndDelete encodes name", () =>
      expect(
        endpoints.acpServers.reassignAndDelete("Auggie (Gemini Pro)"),
      ).toBe("/api/acp-servers/Auggie%20(Gemini%20Pro)/reassign-and-delete"));
    test("slack app and installation builders encode IDs", () => {
      expect(endpoints.slack.apps()).toBe("/api/slack/apps");
      expect(endpoints.slack.app("app 1/x")).toBe(
        "/api/slack/apps/app%201%2Fx",
      );
      expect(endpoints.slack.appToken("app 1/x")).toBe(
        "/api/slack/apps/app%201%2Fx/token",
      );
      expect(endpoints.slack.installations("app 1/x")).toBe(
        "/api/slack/apps/app%201%2Fx/installations",
      );
      expect(endpoints.slack.installation("inst 1/x")).toBe(
        "/api/slack/installations/inst%201%2Fx",
      );
      expect(
        endpoints.slack.installationChannels("inst 1/x", {
          cursor: "next value",
        }),
      ).toBe(
        "/api/slack/installations/inst%201%2Fx/channels?cursor=next+value",
      );
    });
    test("aux.improvePrompt", () =>
      expect(endpoints.aux.improvePrompt()).toBe("/api/aux/improve-prompt"));
    test("runners.supported", () =>
      expect(endpoints.runners.supported()).toBe("/api/supported-runners"));
    test("runners.defaults", () =>
      expect(endpoints.runners.defaults()).toBe("/api/runner-defaults"));
    test("misc.advancedFlags", () =>
      expect(endpoints.misc.advancedFlags()).toBe("/api/advanced-flags"));
    test("misc.externalStatus", () =>
      expect(endpoints.misc.externalStatus()).toBe("/api/external-status"));
    test("misc.uiPreferences", () =>
      expect(endpoints.misc.uiPreferences()).toBe("/api/ui-preferences"));
    test("misc.csrfToken", () =>
      expect(endpoints.misc.csrfToken()).toBe("/api/csrf-token"));
    test("misc.saveFileToPath", () =>
      expect(endpoints.misc.saveFileToPath()).toBe("/api/save-file-to-path"));
    // mitto-7gta.19.1: pre-auth endpoints used by auth.js.
    test("misc.authInfo", () =>
      expect(endpoints.misc.authInfo()).toBe("/api/auth-info"));
    test("misc.login", () => expect(endpoints.misc.login()).toBe("/api/login"));
  });

  // ---------------------------------------------------------------------
  // WebSocket builders (sessions.ws / events.ws) — delegate to wsUrlFor()
  // ---------------------------------------------------------------------
  describe("ws builders", () => {
    test("throws ConfigError when baseUrl is relative and no wsBaseUrl given", () => {
      const endpoints = mk("");
      expect(() => endpoints.events.ws()).toThrow(ConfigError);
      expect(() => endpoints.sessions.ws("abc")).toThrow(ConfigError);
    });

    test("events.ws returns the given wsBaseUrl origin + /api/events", () => {
      const endpoints = mk("", { wsBaseUrl: "ws://localhost:1234" });
      expect(endpoints.events.ws()).toBe("ws://localhost:1234/api/events");
    });

    test("sessions.ws returns the given wsBaseUrl origin + session path", () => {
      const endpoints = mk("", { wsBaseUrl: "ws://localhost:1234" });
      expect(endpoints.sessions.ws("abc")).toBe(
        "ws://localhost:1234/api/sessions/abc/ws",
      );
    });

    test("events.ws includes apiPrefix when set", () => {
      const endpoints = mk("/mitto", { wsBaseUrl: "wss://host" });
      const url = endpoints.events.ws();
      expect(url).toBe("wss://host/mitto/api/events");
    });

    test("sessions.ws encodes special chars in id", () => {
      const endpoints = mk("", { wsBaseUrl: "ws://host" });
      expect(endpoints.sessions.ws("a/b")).toBe(
        "ws://host/api/sessions/a%2Fb/ws",
      );
    });

    test("maps an absolute https:// baseUrl to wss:// without a wsBaseUrl option", () => {
      const endpoints = createEndpoints({
        baseUrl: "https://example.com",
        apiPrefix: "",
      });
      expect(endpoints.events.ws()).toBe("wss://example.com/api/events");
    });

    test("maps an absolute http:// baseUrl to ws:// without a wsBaseUrl option", () => {
      const endpoints = createEndpoints({
        baseUrl: "http://example.com",
        apiPrefix: "",
      });
      expect(endpoints.events.ws()).toBe("ws://example.com/api/events");
    });

    test("an explicit wsBaseUrl option takes precedence over an absolute baseUrl", () => {
      const endpoints = createEndpoints(
        { baseUrl: "https://example.com", apiPrefix: "" },
        { wsBaseUrl: "ws://override:9" },
      );
      expect(endpoints.events.ws()).toBe("ws://override:9/api/events");
    });
  });
});
