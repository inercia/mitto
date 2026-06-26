/**
 * Unit tests for the centralized API endpoint registry.
 *
 * Covers: prefix handling, query-string encoding, path-param encoding,
 * null/undefined/"" param omission, and a representative builder from each
 * resource group.
 */
import { endpoints } from "./endpoints.js";

describe("endpoints registry", () => {
  let originalMittoApiPrefix;

  beforeEach(() => {
    originalMittoApiPrefix = window.mittoApiPrefix;
  });

  afterEach(() => {
    window.mittoApiPrefix = originalMittoApiPrefix;
  });

  // ---------------------------------------------------------------------------
  // Prefix handling
  // ---------------------------------------------------------------------------

  describe("prefix handling", () => {
    test("applies /mitto prefix when set", () => {
      window.mittoApiPrefix = "/mitto";
      expect(endpoints.sessions.list()).toBe("/mitto/api/sessions");
    });

    test("no prefix when mittoApiPrefix is empty string", () => {
      window.mittoApiPrefix = "";
      expect(endpoints.sessions.list()).toBe("/api/sessions");
    });

    test("no prefix when mittoApiPrefix is undefined", () => {
      delete window.mittoApiPrefix;
      expect(endpoints.sessions.list()).toBe("/api/sessions");
    });

    test("path-param builder also respects prefix", () => {
      window.mittoApiPrefix = "/mitto";
      expect(endpoints.sessions.get("20260101-120000-deadbeef"))
        .toBe("/mitto/api/sessions/20260101-120000-deadbeef");
    });
  });

  // ---------------------------------------------------------------------------
  // Query-string building (qs helper via builders)
  // ---------------------------------------------------------------------------

  describe("query-string encoding", () => {
    beforeEach(() => { window.mittoApiPrefix = ""; });

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
      expect(endpoints.issues.list({ working_dir: undefined })).toBe("/api/issues");
    });

    test('omits empty-string param values', () => {
      expect(endpoints.issues.list({ working_dir: "" })).toBe("/api/issues");
    });

    test("encodes special chars in param values via URLSearchParams", () => {
      const url = endpoints.issues.list({ working_dir: "/home/user/my project" });
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
      // 0 and false are valid param values — only null/undefined/"" are omitted
      const url = endpoints.workspaces.list({ page: 0 });
      expect(url).toContain("page=0");
    });
  });

  // ---------------------------------------------------------------------------
  // Path-param encoding
  // ---------------------------------------------------------------------------

  describe("path-param encoding", () => {
    beforeEach(() => { window.mittoApiPrefix = ""; });

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

  // ---------------------------------------------------------------------------
  // Representative builder from each resource group
  // ---------------------------------------------------------------------------

  describe("issues group", () => {
    beforeEach(() => { window.mittoApiPrefix = ""; });

    test("list — base path", () => expect(endpoints.issues.list()).toBe("/api/issues"));
    test("list — with working_dir", () => expect(endpoints.issues.list({ working_dir: "/w" })).toBe("/api/issues?working_dir=%2Fw"));
    test("stats", () => expect(endpoints.issues.stats({ working_dir: "/w" })).toBe("/api/issues/stats?working_dir=%2Fw"));
    test("show", () => expect(endpoints.issues.show("abc-1")).toBe("/api/issues/abc-1"));
    test("show — with working_dir", () => expect(endpoints.issues.show("abc-1", { working_dir: "/w" })).toBe("/api/issues/abc-1?working_dir=%2Fw"));
    test("create — with working_dir", () => expect(endpoints.issues.create({ working_dir: "/w" })).toBe("/api/issues?working_dir=%2Fw"));
    test("update — with working_dir", () => expect(endpoints.issues.update("abc-1", { working_dir: "/w" })).toBe("/api/issues/abc-1?working_dir=%2Fw"));
    test("remove — with working_dir", () => expect(endpoints.issues.remove("abc-1", { working_dir: "/w" })).toBe("/api/issues/abc-1?working_dir=%2Fw"));
    test("status sub-resource", () => expect(endpoints.issues.status("abc-1")).toBe("/api/issues/abc-1/status"));
    test("status — with working_dir", () => expect(endpoints.issues.status("abc-1", { working_dir: "/w" })).toBe("/api/issues/abc-1/status?working_dir=%2Fw"));
    test("comments sub-resource", () => expect(endpoints.issues.comments("abc-1")).toBe("/api/issues/abc-1/comments"));
    test("comments — with working_dir", () => expect(endpoints.issues.comments("abc-1", { working_dir: "/w" })).toBe("/api/issues/abc-1/comments?working_dir=%2Fw"));
    test("dependencies sub-resource", () => expect(endpoints.issues.dependencies("x")).toBe("/api/issues/x/dependencies"));
    test("dependencies — with working_dir", () => expect(endpoints.issues.dependencies("x", { working_dir: "/w" })).toBe("/api/issues/x/dependencies?working_dir=%2Fw"));
    test("cleanup", () => expect(endpoints.issues.cleanup()).toBe("/api/issues/cleanup"));
    test("cleanup — with working_dir", () => expect(endpoints.issues.cleanup({ working_dir: "/w" })).toBe("/api/issues/cleanup?working_dir=%2Fw"));
    test("config — base", () => expect(endpoints.issues.config()).toBe("/api/issues/config"));
    test("config — with working_dir + key (DELETE scenario)", () => {
      const url = endpoints.issues.config({ working_dir: "/w", key: "jira.url" });
      expect(url).toContain("working_dir=");
      expect(url).toContain("key=jira.url");
    });
    test("upstream", () => expect(endpoints.issues.upstream()).toBe("/api/issues/upstream"));
    test("upstream — with working_dir", () => expect(endpoints.issues.upstream({ working_dir: "/w" })).toBe("/api/issues/upstream?working_dir=%2Fw"));
    test("sync", () => expect(endpoints.issues.sync()).toBe("/api/issues/sync"));
    test("sync — with working_dir", () => expect(endpoints.issues.sync({ working_dir: "/w" })).toBe("/api/issues/sync?working_dir=%2Fw"));
  });

  describe("sessions group", () => {
    beforeEach(() => { window.mittoApiPrefix = ""; });

    test("running", () => expect(endpoints.sessions.running()).toBe("/api/sessions/running"));
    test("get(id)", () => expect(endpoints.sessions.get("s1")).toBe("/api/sessions/s1"));
    test("periodic", () => expect(endpoints.sessions.periodic("s1")).toBe("/api/sessions/s1/periodic"));
    test("periodicRunNow", () => expect(endpoints.sessions.periodicRunNow("s1")).toBe("/api/sessions/s1/periodic/run-now"));
    test("queueMove", () => expect(endpoints.sessions.queueMove("s1", "m1")).toBe("/api/sessions/s1/queue/m1/move"));
    test("images", () => expect(endpoints.sessions.images("s1")).toBe("/api/sessions/s1/images"));
    test("filesFromPath", () => expect(endpoints.sessions.filesFromPath("s1")).toBe("/api/sessions/s1/files/from-path"));
  });

  describe("workspaces group", () => {
    beforeEach(() => { window.mittoApiPrefix = ""; });

    test("list", () => expect(endpoints.workspaces.list()).toBe("/api/workspaces"));
    test("mcpTools", () => expect(endpoints.workspaces.mcpTools("uuid-1")).toBe("/api/workspaces/uuid-1/mcp-tools"));
    test("mcpToolsInstall", () => expect(endpoints.workspaces.mcpToolsInstall("u")).toBe("/api/workspaces/u/mcp-tools/install"));
    test("processor", () => expect(endpoints.workspaces.processor("u", "myproc")).toBe("/api/workspaces/u/processors/myproc"));
  });

  describe("workspacePrompts group", () => {
    beforeEach(() => { window.mittoApiPrefix = ""; });

    test("list", () => expect(endpoints.workspacePrompts.list()).toBe("/api/workspace-prompts"));
    test("get", () => expect(endpoints.workspacePrompts.get("p")).toBe("/api/workspace-prompts/p"));
  });

  describe("other groups", () => {
    beforeEach(() => { window.mittoApiPrefix = ""; });

    test("config.get", () => expect(endpoints.config.get()).toBe("/api/config"));
    test("config.get with acp_server", () => expect(endpoints.config.get({ acp_server: "server-a" })).toBe("/api/config?acp_server=server-a"));
    test("config.get with acp_server and session_id", () => {
      const url = endpoints.config.get({ acp_server: "server-a", session_id: "s1" });
      expect(url).toContain("acp_server=server-a");
      expect(url).toContain("session_id=s1");
    });
    test("config.get skips null params", () => expect(endpoints.config.get({ acp_server: null, session_id: null })).toBe("/api/config"));
    test("config.update", () => expect(endpoints.config.update()).toBe("/api/config"));
    test("agents.types", () => expect(endpoints.agents.types()).toBe("/api/agents/types"));
    test("agents.scan", () => expect(endpoints.agents.scan()).toBe("/api/agents/scan"));
    test("aux.improvePrompt", () => expect(endpoints.aux.improvePrompt()).toBe("/api/aux/improve-prompt"));
    test("runners.supported", () => expect(endpoints.runners.supported()).toBe("/api/supported-runners"));
    test("runners.defaults", () => expect(endpoints.runners.defaults()).toBe("/api/runner-defaults"));
    test("misc.advancedFlags", () => expect(endpoints.misc.advancedFlags()).toBe("/api/advanced-flags"));
    test("misc.externalStatus", () => expect(endpoints.misc.externalStatus()).toBe("/api/external-status"));
    test("misc.uiPreferences", () => expect(endpoints.misc.uiPreferences()).toBe("/api/ui-preferences"));
    test("misc.csrfToken", () => expect(endpoints.misc.csrfToken()).toBe("/api/csrf-token"));
    test("misc.saveFileToPath", () => expect(endpoints.misc.saveFileToPath()).toBe("/api/save-file-to-path"));

    test("events.ws returns ws(s):// URL ending in /api/events", () => {
      window.mittoApiPrefix = "";
      const url = endpoints.events.ws();
      expect(url).toMatch(/^wss?:\/\//);
      expect(url).toMatch(/\/api\/events$/);
    });
    test("sessions.ws returns ws(s):// URL ending in /api/sessions/abc/ws", () => {
      window.mittoApiPrefix = "";
      const url = endpoints.sessions.ws("abc");
      expect(url).toMatch(/^wss?:\/\//);
      expect(url).toMatch(/\/api\/sessions\/abc\/ws$/);
    });
    test("events.ws includes prefix when set", () => {
      window.mittoApiPrefix = "/mitto";
      const url = endpoints.events.ws();
      expect(url).toContain("/mitto");
      expect(url).toMatch(/\/api\/events$/);
      window.mittoApiPrefix = "";
    });
    test("sessions.ws encodes special chars in id", () => {
      window.mittoApiPrefix = "";
      const url = endpoints.sessions.ws("a/b");
      expect(url).toMatch(/\/api\/sessions\/a%2Fb\/ws$/);
    });
  });
});
