// Mitto Web Interface - Utilities Index
// Re-exports all utility functions for convenient importing

export {
  openExternalURL,
  openFileURL,
  convertFileURLToHTTP,
  convertHTTPFileURLToFile,
  convertHTTPFileURLToViewer,
  setCurrentWorkspace,
  hasNativeFolderPicker,
  pickFolder,
  pickImages,
  hasNativeImagePicker,
  isNativeApp,
  fixViewerURLIfNeeded,
} from "./native.js";

export {
  getLastSeenSeq,
  setLastSeenSeq,
  getLastActiveSessionId,
  setLastActiveSessionId,
  getQueueDropdownHeight,
  setQueueDropdownHeight,
  getQueueHeightConstraints,
} from "./storage.js";

export { playAgentCompletedSound } from "./audio.js";

export {
  getCSRFToken,
  clearCSRFToken,
  secureFetch,
  initCSRF,
  checkAuth,
  authFetch,
} from "./csrf.js";

export { getApiPrefix, apiUrl, wsUrl } from "./api.js";
