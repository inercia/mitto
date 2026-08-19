/**
 * Creates a Mitto API client from environment-agnostic, injectable config.
 * See docs/devel/js-client-library.md §4 for the full contract.
 */
export function createClient(options?: {}): {
    config: Readonly<import("./core/config.js").ResolvedConfig>;
    endpoints: any;
    sessions: {
        list: (opts: any) => Promise<any>;
        running: (opts: any) => Promise<any>;
        get: (id: any, opts: any) => Promise<any>;
        create: (body?: object, opts?: import("./core/transport.js").RequestOptions) => Promise<any>;
        update: (id: any, patch: object, opts: any) => Promise<any>;
        remove: (id: any, opts: any) => Promise<any>;
        events: (id: any, params?: object, opts?: import("./core/transport.js").RequestOptions) => Promise<any>;
        changes: (id: any, opts: any) => Promise<any>;
        getSettings: (id: any, opts: any) => Promise<any>;
        updateSettings: (id: any, settings: object, opts: any) => Promise<any>;
        flush: (id: any, opts: any) => Promise<any>;
        prune: (id: any, keepLast?: number, opts?: import("./core/transport.js").RequestOptions) => Promise<any>;
        getCallback: (id: any, opts: any) => Promise<any>;
        createCallback: (id: any, opts: any) => Promise<any>;
        revokeCallback: (id: any, opts: any) => Promise<any>;
        getUserData: (id: any, opts: any) => Promise<any>;
        setUserData: (id: any, body: object, opts: any) => Promise<any>;
        promptArgCache: (id: any, promptName: any, opts: any) => Promise<any>;
        acknowledgeUIPrompt: (id: any, requestId: any, opts: any) => Promise<any>;
        images: {
            list: (id: any, opts: any) => Promise<any>;
            upload: (id: any, form: FormData, opts: any) => Promise<any>;
            uploadFromPath: (id: any, paths: string[], opts: any) => Promise<any>;
            url: (id: any, imageId: any) => string;
            fetchImage: (id: any, imageId: any, opts: any) => Promise<Response>;
            remove: (id: any, imageId: any, opts: any) => Promise<any>;
        };
        queue: {
            list: (id: any, opts: any) => Promise<{
                messages: object[];
                count: number;
            }>;
            add: (id: any, body: object, opts: any) => Promise<any>;
            addNamed: (id: any, promptName: string, args?: object, extra?: object, opts?: import("./core/transport.js").RequestOptions) => Promise<any>;
            get: (id: any, msgId: any, opts: any) => Promise<any>;
            remove: (id: any, msgId: any, opts: any) => Promise<any>;
            clear: (id: any, opts: any) => Promise<any>;
            move: (id: any, msgId: any, direction: "up" | "down", opts: any) => Promise<any>;
            config: (id: any, opts: any) => Promise<any>;
        };
        loop: {
            get: (id: any, opts: any) => Promise<any>;
            set: (id: any, body: object, opts: any) => Promise<any>;
            update: (id: any, patch: object, opts: any) => Promise<any>;
            detach: (id: any, opts: any) => Promise<any>;
            restore: (id: any, opts: any) => Promise<any>;
            runNow: (id: any, resetTimer?: boolean, opts?: import("./core/transport.js").RequestOptions) => Promise<any>;
            suggestFromRecent: (id: any, opts: any) => Promise<any>;
            acknowledgeStoppedReason: (id: any, opts: any) => Promise<any>;
            enable: (id: any, opts: any) => Promise<any>;
            disable: (id: any, opts: any) => Promise<any>;
        };
    };
    prompts: {
        list: (params?: object, opts?: import("./core/transport.js").RequestOptions) => Promise<any>;
        create: (body: object, opts: any) => Promise<any>;
        remove: (params: object, opts: any) => Promise<any>;
        setEnabled: (name: string, workingDir: string, enabled: boolean, opts: any) => Promise<any>;
        rememberedArgs: (params: object, opts: any) => Promise<any>;
    };
    processors: {
        list: (uuid: string, opts: any) => Promise<any>;
        setEnabled: (uuid: string, name: string, enabled: boolean, opts: any) => Promise<any>;
        setArguments: (uuid: string, name: string, argumentsMap: {
            [x: string]: string;
        }, opts: any) => Promise<any>;
    };
    shortcuts: {
        getGlobal: (params?: object, opts?: import("./core/transport.js").RequestOptions) => Promise<any>;
        setGlobal: (body: object, opts: any) => Promise<any>;
        getFolder: (params: object, opts: any) => Promise<any>;
        setFolder: (workingDir: string, body: object, opts: any) => Promise<any>;
    };
    taskLabelColors: {
        getGlobal: (opts?: import("./core/transport.js").RequestOptions) => Promise<import("./resources/task-label-colors.js").TaskLabelColorsBody>;
        setGlobal: (body: import("./resources/task-label-colors.js").TaskLabelColorsBody, opts?: import("./core/transport.js").RequestOptions) => Promise<import("./resources/task-label-colors.js").TaskLabelColorsBody>;
    };
    issues: {
        list: (params: any, opts: any) => Promise<any>;
        stats: (params: any, opts: any) => Promise<any>;
        labelsAll: (params: any, opts: any) => Promise<any>;
        config: (params: any, opts: any) => Promise<any>;
        setConfig: (params: any, body: object, opts: any) => Promise<any>;
        deleteConfig: (params: any, opts: any) => Promise<any>;
        upstream: (params: any, opts: any) => Promise<any>;
        setUpstream: (params: any, body: object, opts: any) => Promise<any>;
        databaseMode: (params: any, opts: any) => Promise<any>;
        setDatabaseMode: (params: any, body: object, opts: any) => Promise<any>;
        show: (id: any, params: any, opts: any) => Promise<any>;
        create: (params: any, body: object, opts: any) => Promise<any>;
        update: (id: any, params: any, patch: object, opts: any) => Promise<any>;
        remove: (id: any, params: any, opts: any) => Promise<any>;
        status: (id: any, params: any, body: object, opts: any) => Promise<any>;
        comment: (id: any, params: any, body: object, opts: any) => Promise<any>;
        dependency: (id: any, params: any, body: object, opts: any) => Promise<any>;
        label: (id: any, params: any, body: object, opts: any) => Promise<any>;
        cleanup: (params: any, opts: any) => Promise<any>;
        sync: (params: any, body: object, opts: any) => Promise<any>;
        migrate: (body: object, opts: any) => Promise<any>;
    };
    serverConfig: {
        get: (params?: object, opts?: import("./core/transport.js").RequestOptions) => Promise<any>;
        save: (body: object, opts: any) => Promise<any>;
        advancedFlags: (opts: any) => Promise<any>;
        externalStatus: (opts: any) => Promise<any>;
        supportedRunners: (opts: any) => Promise<any>;
        runnerDefaults: (opts: any) => Promise<any>;
    };
    files: {
        list: (id: any, opts: any) => Promise<any>;
        upload: (id: any, form: FormData, opts: any) => Promise<any>;
        uploadFromPath: (id: any, paths: string[], opts: any) => Promise<any>;
        url: (id: any, fileId: any) => string;
        fetchFile: (id: any, fileId: any, opts: any) => Promise<Response>;
        remove: (id: any, fileId: any, opts: any) => Promise<any>;
        contentUrl: (params: object) => string;
        fetchContent: (params: object, opts: any) => Promise<Response>;
        workspaceFiles: {
            list: (params: any, opts: any) => Promise<any>;
        };
        workspaceDirs: {
            list: (params: any, opts: any) => Promise<any>;
        };
    };
    images: {
        list: (id: any, opts: any) => Promise<any>;
        upload: (id: any, form: FormData, opts: any) => Promise<any>;
        uploadFromPath: (id: any, paths: string[], opts: any) => Promise<any>;
        url: (id: any, imageId: any) => string;
        fetchImage: (id: any, imageId: any, opts: any) => Promise<Response>;
        remove: (id: any, imageId: any, opts: any) => Promise<any>;
    };
    dashboard: {
        summary: (params?: object, opts?: import("./core/transport.js").RequestOptions) => Promise<any>;
        timeseries: (params?: object, opts?: import("./core/transport.js").RequestOptions) => Promise<any>;
    };
    misc: {
        uiPreferences: {
            get: (opts: any) => Promise<any>;
            save: (prefs: object, opts: any) => Promise<any>;
        };
        csrfToken: (opts: any) => Promise<any>;
        authInfo: (opts: any) => Promise<{
            simple: boolean;
            cloudflare: boolean;
        }>;
        login: (credentials: {
            username: string;
            password: string;
        }, opts: any) => Promise<any>;
        checkFileExists: (path: string, opts: any) => Promise<{
            exists: boolean;
        }>;
        saveFileToPath: (path: string, content: string, opts: any) => Promise<any>;
        improvePrompt: (prompt: string, workspaceUUID: string, opts: any) => Promise<{
            improved_prompt: string;
        }>;
        badgeClick: (body: object, opts: any) => Promise<any>;
        folderPin: {
            get: (params: object, opts: any) => Promise<{
                pinned: boolean;
            }>;
            set: (params: object, body: object, opts: any) => Promise<{
                pinned: boolean;
            }>;
        };
        advancedFlags: any;
        externalStatus: any;
        supportedRunners: any;
        runnerDefaults: any;
    };
    workspaces: {
        list: (params?: object, opts?: import("./core/transport.js").RequestOptions) => Promise<{
            workspaces: object[];
            acp_servers: object[];
        }>;
        create: (body: object, opts: any) => Promise<any>;
        remove: (uuid: string, opts: any) => Promise<any>;
        getMetadata: (uuid: any, opts: any) => Promise<any>;
        setMetadata: (uuid: any, body: object, opts: any) => Promise<any>;
        getUserDataSchema: (uuid: any, opts: any) => Promise<any>;
        setUserDataSchema: (uuid: any, body: any, opts: any) => Promise<any>;
        getEffectiveRunnerConfig: (uuid: any, opts: any) => Promise<any>;
        getAcpStatus: (uuid: any, opts: any) => Promise<{
            alive: boolean;
        }>;
        restartAcp: (uuid: any, opts: any) => Promise<any>;
        setFolderGroup: (uuid: string, group: string, opts: any) => Promise<any>;
        listMcpTools: (uuid: string, acpServer: string, opts: any) => Promise<any>;
        installMcpTool: (uuid: any, body: object, opts: any) => Promise<any>;
        removeMcpTool: (uuid: any, body: object, opts: any) => Promise<any>;
    };
    acpServers: {
        prepareDelete: (name: string, opts: any) => Promise<any>;
        reassignAndDelete: (name: string, body: object, opts: any) => Promise<any>;
    };
    agents: {
        types: (opts: any) => Promise<{
            agent_types: string[];
        }>;
        scan: (opts: any) => Promise<object[]>;
        confirm: (agentsList: object[], opts: any) => Promise<any>;
    };
    slack: {
        environmentStatus: (opts: any) => Promise<any>;
        importEnvironment: (body: any, opts: any) => Promise<any>;
        listApps: (opts: any) => Promise<any>;
        createApp: (body: any, opts: any) => Promise<any>;
        getApp: (id: any, opts: any) => Promise<any>;
        renameApp: (id: any, name: any, opts: any) => Promise<any>;
        replaceAppToken: (id: any, token: any, opts: any) => Promise<any>;
        validateApp: (id: any, opts: any) => Promise<any>;
        prepareDeleteApp: (id: any, opts: any) => Promise<any>;
        deleteApp: (id: any, opts: any) => Promise<any>;
        listInstallations: (appId: any, opts: any) => Promise<any>;
        createInstallation: (appId: any, body: any, opts: any) => Promise<any>;
        getInstallation: (id: any, opts: any) => Promise<any>;
        renameInstallation: (id: any, name: any, opts: any) => Promise<any>;
        replaceInstallationToken: (id: any, token: any, opts: any) => Promise<any>;
        validateInstallation: (id: any, opts: any) => Promise<any>;
        prepareDeleteInstallation: (id: any, opts: any) => Promise<any>;
        deleteInstallation: (id: any, opts: any) => Promise<any>;
        listChannels: (id: any, params: any, opts: any) => Promise<any>;
    };
    sessionStream: (sessionId: any, streamOptions: any) => import("./index.js").SessionStream;
    eventsStream: (streamOptions: any) => import("./index.js").EventsStream;
};
/**
 * The embedded copy ships lockstep with the server (§6): its version is the
 * Mitto release tag it is served inside.
 */
export const VERSION: "0.3.0";
export type TaskLabelColorsBody = import("./resources/task-label-colors.js").TaskLabelColorsBody;
import { MittoError } from "./core/errors.js";
import { ConfigError } from "./core/errors.js";
import { MittoApiError } from "./core/errors.js";
import { MittoAuthError } from "./core/errors.js";
import { MittoNetworkError } from "./core/errors.js";
import { noneAuth } from "./auth/index.js";
import { sharedTokenAuth } from "./auth/index.js";
import { browserCookieAuth } from "./auth/index.js";
import { createSeqTracker } from "./realtime/seq.js";
import { isSeqDuplicate } from "./realtime/seq.js";
import { markSeqSeen } from "./realtime/seq.js";
import { getMaxSeq } from "./realtime/seq.js";
import { isStaleClientState } from "./realtime/seq.js";
import { isTerminalSessionError } from "./realtime/seq.js";
import { createMemorySeqStore } from "./realtime/seq.js";
import { createStorageSeqStore } from "./realtime/seq.js";
import { generatePromptId } from "./realtime/pending-prompts.js";
import { createMemoryPendingPromptStore } from "./realtime/pending-prompts.js";
import { createStoragePendingPromptStore } from "./realtime/pending-prompts.js";
import { EVENTS } from "./realtime/events.js";
import { COMMANDS } from "./realtime/events.js";
import { LEGACY_EVENTS } from "./realtime/events.js";
import { isKnownEventType } from "./realtime/events.js";
import { isCommandType } from "./realtime/events.js";
import { createTtlCache } from "./cache/ttl-cache.js";
import { keyForParams } from "./cache/ttl-cache.js";
import { withIssueCaches } from "./resources/issues.js";
export { MittoError, ConfigError, MittoApiError, MittoAuthError, MittoNetworkError, noneAuth, sharedTokenAuth, browserCookieAuth, createSeqTracker, isSeqDuplicate, markSeqSeen, getMaxSeq, isStaleClientState, isTerminalSessionError, createMemorySeqStore, createStorageSeqStore, generatePromptId, createMemoryPendingPromptStore, createStoragePendingPromptStore, EVENTS, COMMANDS, LEGACY_EVENTS, isKnownEventType, isCommandType, createTtlCache, keyForParams, withIssueCaches };
export { browserEnv, browserCookieReader } from "./env/browser.js";
export { createSessionStream, SessionStream } from "./realtime/session-stream.js";
export { createEventsStream, EventsStream } from "./realtime/events-stream.js";
