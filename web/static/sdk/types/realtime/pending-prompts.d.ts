/** Generates a unique prompt ID for delivery tracking. */
export function generatePromptId(): string;
/** In-memory pending-prompt store; the default when no `pendingPromptStore` is injected. */
export function createMemoryPendingPromptStore(options?: {}): {
    save(sessionId: any, promptId: any, message: any, imageIds?: any[], fileIds?: any[]): void;
    remove(promptId: any): void;
    getForSession(sessionId: any): any[];
};
/**
 * Pending-prompt store backed by an injected storage adapter (the same
 * getItem/setItem/removeItem contract as `config.storage`).
 */
export function createStoragePendingPromptStore(storage: any, options?: {}): {
    save(sessionId: any, promptId: any, message: any, imageIds?: any[], fileIds?: any[]): void;
    remove(promptId: any): void;
    getForSession(sessionId: any): any[];
};
export namespace PENDING_PROMPTS_CONSTANTS {
    export { PROMPT_EXPIRY_MS };
    export { DEFAULT_STORAGE_KEY };
}
/**
 * Injectable pending-prompt queue — a host-agnostic replacement for the
 * `mitto_pending_prompts` localStorage queue in web/static/lib.js. Used by
 * SessionStream.sendPrompt()'s delivery-verification path to persist an
 * outbound prompt before the ACK arrives, and by retryPendingPrompts() to
 * resend anything still outstanding after a reconnect.
 *
 * Never touches `localStorage` directly — the storage-backed variant is
 * built on the injected storage contract from sdk/core/config.js
 * (getItem/setItem/removeItem).
 */
declare const PROMPT_EXPIRY_MS: number;
declare const DEFAULT_STORAGE_KEY: "mitto_sdk_pending_prompts";
export {};
