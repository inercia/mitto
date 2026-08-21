/**
 * Process-global Slack integration catalog resource.
 * Bot and delegated-user credentials use the generic write-only `token`
 * request field; every response is non-secret identity metadata plus
 * token_configured booleans.
 */
import { request } from "../core/transport.js";

const enc = encodeURIComponent;

/** @param {import("../core/config.js").ResolvedConfig} config */
export function createSlackResource(config) {
  const call = (method, path, opts = {}) =>
    request(config, { method, path, ...opts });
  const appPath = (id) => `/api/slack/apps/${enc(id)}`;
  const installationPath = (id) => `/api/slack/installations/${enc(id)}`;
  const oauthFlowPath = (id) => `/api/slack/oauth/flows/${enc(id)}`;

  return {
    environmentStatus: (opts) =>
      call("GET", "/api/slack/environment-import", opts),
    importEnvironment: (body, opts) =>
      call("POST", "/api/slack/environment-import", { body, ...opts }),
    listApps: (opts) => call("GET", "/api/slack/apps", opts),
    connections: (opts) => call("GET", "/api/slack/connections", opts),
    createApp: (body, opts) =>
      call("POST", "/api/slack/apps", { body, ...opts }),
    getApp: (id, opts) => call("GET", appPath(id), opts),
    renameApp: (id, name, opts) =>
      call("PATCH", appPath(id), { body: { name }, ...opts }),
    replaceAppToken: (id, token, opts) =>
      call("PUT", `${appPath(id)}/token`, { body: { token }, ...opts }),
    oauthConfig: (opts) => call("GET", "/api/slack/oauth/config", opts),
    configureOAuthClient: (id, body, opts) =>
      call("PUT", `${appPath(id)}/oauth-client`, { body, ...opts }),
    startOAuthInstallation: (id, body, opts) =>
      call("POST", `${appPath(id)}/oauth/start`, { body, ...opts }),
    validateApp: (id, opts) =>
      call("POST", `${appPath(id)}/validate`, { body: {}, ...opts }),
    prepareDeleteApp: (id, opts) =>
      call("GET", `${appPath(id)}/prepare-delete`, opts),
    removeAppReferences: (id, opts) =>
      call("DELETE", `${appPath(id)}/references`, opts),
    deleteApp: (id, opts) => call("DELETE", appPath(id), opts),

    listInstallations: (appId, opts) =>
      call("GET", `${appPath(appId)}/installations`, opts),
    createInstallation: (appId, body, opts) =>
      call("POST", `${appPath(appId)}/installations`, { body, ...opts }),
    getInstallation: (id, opts) => call("GET", installationPath(id), opts),
    renameInstallation: (id, name, opts) =>
      call("PATCH", installationPath(id), { body: { name }, ...opts }),
    replaceInstallationToken: (id, token, opts) =>
      call("PUT", `${installationPath(id)}/token`, {
        body: { token },
        ...opts,
      }),
    startOAuthReplacement: (id, opts) =>
      call("POST", `${installationPath(id)}/oauth/start`, {
        body: {},
        ...opts,
      }),
    oauthFlowStatus: (id, opts) => call("GET", oauthFlowPath(id), opts),
    validateInstallation: (id, opts) =>
      call("POST", `${installationPath(id)}/validate`, {
        body: {},
        ...opts,
      }),
    prepareDeleteInstallation: (id, opts) =>
      call("GET", `${installationPath(id)}/prepare-delete`, opts),
    removeInstallationReferences: (id, opts) =>
      call("DELETE", `${installationPath(id)}/references`, opts),
    deleteInstallation: (id, opts) =>
      call("DELETE", installationPath(id), opts),
    listChannels: (id, params, opts) =>
      call("GET", `${installationPath(id)}/channels`, {
        query: params,
        ...opts,
      }),
  };
}
