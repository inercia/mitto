// Mitto Web Interface - Utilities Index
// Re-exports all utility functions for convenient importing

export {
    openExternalURL,
    hasNativeFolderPicker,
    pickFolder,
    pickImages,
    hasNativeImagePicker,
    isNativeApp
} from './native.js';

export {
    getLastSeenSeq,
    setLastSeenSeq,
    getLastActiveSessionId,
    setLastActiveSessionId
} from './storage.js';

export {
    playAgentCompletedSound
} from './audio.js';

export {
    getCSRFToken,
    clearCSRFToken,
    secureFetch,
    initCSRF,
    checkAuth,
    authFetch
} from './csrf.js';

export {
    getApiPrefix,
    apiUrl,
    wsUrl
} from './api.js';

