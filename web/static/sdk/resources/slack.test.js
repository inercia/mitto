import { MittoApiError } from "../core/errors.js";
import { fakeResponse, resourceMounter } from "../testing/fake-server.js";
import { createSlackResource } from "./slack.js";

const mk = resourceMounter((config) => ({
  slack: createSlackResource(config),
}));

function parsedBody(call) {
  return call.init.body ? JSON.parse(call.init.body) : null;
}

describe("Slack catalog resource", () => {
  test("inspects and imports environment configuration without token fields", async () => {
    const { slack, calls, respondWith } = mk();
    respondWith(() =>
      fakeResponse({
        body: { present: true, complete: true, team_id: "T123" },
      }),
    );
    expect(await slack.environmentStatus()).toEqual({
      present: true,
      complete: true,
      team_id: "T123",
    });
    await slack.importEnvironment({
      app_id: "app-1",
      installation_id: "inst-1",
    });
    expect(calls.map((call) => [call.init.method, call.url])).toEqual([
      ["GET", "/api/slack/environment-import"],
      ["POST", "/api/slack/environment-import"],
    ]);
    expect(parsedBody(calls[1])).toEqual({
      app_id: "app-1",
      installation_id: "inst-1",
    });
    expect(JSON.stringify(parsedBody(calls[1]))).not.toContain("token");
  });

  test("lists apps and forwards AbortSignal through the SDK transport", async () => {
    const { slack, calls, respondWith } = mk();
    const controller = new AbortController();
    respondWith(() => fakeResponse({ body: { apps: [{ id: "app-1" }] } }));
    expect(await slack.listApps({ signal: controller.signal })).toEqual({
      apps: [{ id: "app-1" }],
    });
    expect(calls[0].url).toBe("/api/slack/apps");
    expect(calls[0].init.method).toBe("GET");
    expect(calls[0].init.signal).toBe(controller.signal);
  });

  test("creates an app with a write-only app_token request", async () => {
    const { slack, calls, respondWith } = mk();
    respondWith(() =>
      fakeResponse({
        status: 201,
        body: { id: "app-1", name: "Primary", token_configured: true },
      }),
    );
    const result = await slack.createApp({
      name: "Primary",
      app_token: "test-app-token",
    });
    expect(calls[0].init.method).toBe("POST");
    expect(parsedBody(calls[0])).toEqual({
      name: "Primary",
      app_token: "test-app-token",
    });
    expect(result.token_configured).toBe(true);
    expect(result.app_token).toBeUndefined();
  });

  test("encodes app IDs for get, rename, token replacement and validation", async () => {
    const { slack, calls } = mk();
    await slack.getApp("app 1/x");
    await slack.renameApp("app 1/x", "Renamed");
    await slack.replaceAppToken("app 1/x", "test-replacement");
    await slack.validateApp("app 1/x");
    expect(calls.map((call) => [call.init.method, call.url])).toEqual([
      ["GET", "/api/slack/apps/app%201%2Fx"],
      ["PATCH", "/api/slack/apps/app%201%2Fx"],
      ["PUT", "/api/slack/apps/app%201%2Fx/token"],
      ["POST", "/api/slack/apps/app%201%2Fx/validate"],
    ]);
    expect(parsedBody(calls[1])).toEqual({ name: "Renamed" });
    expect(parsedBody(calls[2])).toEqual({ token: "test-replacement" });
    expect(parsedBody(calls[3])).toEqual({});
  });

  test("uses value-free OAuth start and status contracts", async () => {
    const { slack, calls } = mk();
    await slack.oauthConfig();
    await slack.configureOAuthClient("app 1/x", {
      client_id: "123.456",
      client_secret: "write-only-secret",
    });
    await slack.startOAuthInstallation("app 1/x", { name: "Workspace" });
    await slack.startOAuthReplacement("inst 1/x");
    await slack.oauthFlowStatus("flow 1/x");
    expect(calls.map((call) => [call.init.method, call.url])).toEqual([
      ["GET", "/api/slack/oauth/config"],
      ["PUT", "/api/slack/apps/app%201%2Fx/oauth-client"],
      ["POST", "/api/slack/apps/app%201%2Fx/oauth/start"],
      ["POST", "/api/slack/installations/inst%201%2Fx/oauth/start"],
      ["GET", "/api/slack/oauth/flows/flow%201%2Fx"],
    ]);
    expect(parsedBody(calls[1])).toEqual({
      client_id: "123.456",
      client_secret: "write-only-secret",
    });
    expect(parsedBody(calls[2])).toEqual({ name: "Workspace" });
    expect(parsedBody(calls[3])).toEqual({});
  });

  test("prepares and deletes app profiles through distinct routes", async () => {
    const { slack, calls } = mk();
    await slack.prepareDeleteApp("app-1");
    await slack.removeAppReferences("app-1");
    await slack.deleteApp("app-1");
    expect(calls.map((call) => [call.init.method, call.url])).toEqual([
      ["GET", "/api/slack/apps/app-1/prepare-delete"],
      ["DELETE", "/api/slack/apps/app-1/references"],
      ["DELETE", "/api/slack/apps/app-1"],
    ]);
  });

  test("lists and creates multiple installations under the selected app", async () => {
    const { slack, calls } = mk();
    await slack.listInstallations("app 1/x");
    await slack.createInstallation("app 1/x", {
      name: "Workspace One",
      team_id: "T1",
      token: "test-installation-token",
    });
    expect(calls.map((call) => [call.init.method, call.url])).toEqual([
      ["GET", "/api/slack/apps/app%201%2Fx/installations"],
      ["POST", "/api/slack/apps/app%201%2Fx/installations"],
    ]);
    expect(parsedBody(calls[1])).toEqual({
      name: "Workspace One",
      team_id: "T1",
      token: "test-installation-token",
    });
  });

  test("encodes installation IDs for CRUD, validation and deletion preview", async () => {
    const { slack, calls } = mk();
    await slack.getInstallation("inst 1/x");
    await slack.renameInstallation("inst 1/x", "Renamed team");
    await slack.replaceInstallationToken("inst 1/x", "test-bot-replacement");
    await slack.validateInstallation("inst 1/x");
    await slack.prepareDeleteInstallation("inst 1/x");
    await slack.removeInstallationReferences("inst 1/x");
    await slack.deleteInstallation("inst 1/x");
    expect(calls.map((call) => [call.init.method, call.url])).toEqual([
      ["GET", "/api/slack/installations/inst%201%2Fx"],
      ["PATCH", "/api/slack/installations/inst%201%2Fx"],
      ["PUT", "/api/slack/installations/inst%201%2Fx/token"],
      ["POST", "/api/slack/installations/inst%201%2Fx/validate"],
      ["GET", "/api/slack/installations/inst%201%2Fx/prepare-delete"],
      ["DELETE", "/api/slack/installations/inst%201%2Fx/references"],
      ["DELETE", "/api/slack/installations/inst%201%2Fx"],
    ]);
    expect(parsedBody(calls[1])).toEqual({ name: "Renamed team" });
    expect(parsedBody(calls[2])).toEqual({ token: "test-bot-replacement" });
    expect(parsedBody(calls[3])).toEqual({});
  });

  test("channel discovery encodes installation ID and query values", async () => {
    const { slack, calls } = mk();
    await slack.listChannels("inst/x", { cursor: "next value", limit: 50 });
    expect(calls[0].init.method).toBe("GET");
    expect(calls[0].url).toBe(
      "/api/slack/installations/inst%2Fx/channels?cursor=next+value&limit=50",
    );
  });

  test("apiPrefix appears exactly once for nested Slack routes", async () => {
    const { slack, calls } = mk({ apiPrefix: "/mitto" });
    await slack.prepareDeleteInstallation("inst-1");
    expect(calls[0].url).toBe(
      "/mitto/api/slack/installations/inst-1/prepare-delete",
    );
    expect(calls[0].url.split("/mitto").length - 1).toBe(1);
  });

  test("dependency conflicts surface as MittoApiError without client-side force", async () => {
    const { slack, respondWith } = mk();
    respondWith(() =>
      fakeResponse({
        status: 409,
        body: {
          error: {
            code: "conflict",
            message: "Slack integration is referenced",
          },
        },
      }),
    );
    await expect(slack.deleteApp("app-1")).rejects.toBeInstanceOf(
      MittoApiError,
    );
  });

  test("delegated-user create rejection surfaces a canonical conflict code and safe message", async () => {
    const { slack, respondWith } = mk();
    const canary = "write-only-user-token-missing-app-id";
    respondWith(() =>
      fakeResponse({
        status: 409,
        body: {
          error: {
            code: "conflict",
            message:
              "Slack did not return the app identity needed to safely bind this delegated-user credential. Manual delegated-user setup is unavailable until Slack OAuth provenance is supported; use a bot token instead.",
          },
        },
      }),
    );
    let caught;
    try {
      await slack.createInstallation("app-1", {
        name: "Team",
        team_id: "T123",
        token: canary,
      });
    } catch (error) {
      caught = error;
    }
    expect(caught).toBeInstanceOf(MittoApiError);
    expect(caught.status).toBe(409);
    expect(caught.code).toBe("conflict");
    expect(caught.message).toContain("OAuth");
    expect(caught.message).toContain("delegated-user");
    expect(caught.message).not.toContain(canary);
  });
});
