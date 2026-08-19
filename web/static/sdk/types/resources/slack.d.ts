/** @param {import("../core/config.js").ResolvedConfig} config */
export function createSlackResource(config: import("../core/config.js").ResolvedConfig): {
    environmentStatus: (opts: any) => Promise<any>;
    importEnvironment: (body: any, opts: any) => Promise<any>;
    listApps: (opts: any) => Promise<any>;
    createApp: (body: any, opts: any) => Promise<any>;
    getApp: (id: any, opts: any) => Promise<any>;
    renameApp: (id: any, name: any, opts: any) => Promise<any>;
    replaceAppToken: (id: any, token: any, opts: any) => Promise<any>;
    oauthConfig: (opts: any) => Promise<any>;
    configureOAuthClient: (id: any, body: any, opts: any) => Promise<any>;
    startOAuthInstallation: (id: any, body: any, opts: any) => Promise<any>;
    validateApp: (id: any, opts: any) => Promise<any>;
    prepareDeleteApp: (id: any, opts: any) => Promise<any>;
    removeAppReferences: (id: any, opts: any) => Promise<any>;
    deleteApp: (id: any, opts: any) => Promise<any>;
    listInstallations: (appId: any, opts: any) => Promise<any>;
    createInstallation: (appId: any, body: any, opts: any) => Promise<any>;
    getInstallation: (id: any, opts: any) => Promise<any>;
    renameInstallation: (id: any, name: any, opts: any) => Promise<any>;
    replaceInstallationToken: (id: any, token: any, opts: any) => Promise<any>;
    startOAuthReplacement: (id: any, opts: any) => Promise<any>;
    oauthFlowStatus: (id: any, opts: any) => Promise<any>;
    validateInstallation: (id: any, opts: any) => Promise<any>;
    prepareDeleteInstallation: (id: any, opts: any) => Promise<any>;
    removeInstallationReferences: (id: any, opts: any) => Promise<any>;
    deleteInstallation: (id: any, opts: any) => Promise<any>;
    listChannels: (id: any, params: any, opts: any) => Promise<any>;
};
