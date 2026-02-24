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
  getAPIPrefix,
} from "./native.js";

export {
  getLastActiveSessionId,
  setLastActiveSessionId,
  getQueueDropdownHeight,
  setQueueDropdownHeight,
  getQueueHeightConstraints,
  getGroupingMode,
  setGroupingMode,
  cycleGroupingMode,
  isGroupExpanded,
  setGroupExpanded,
  getExpandedGroups,
  getSingleExpandedGroupMode,
  setSingleExpandedGroupMode,
  initUIPreferences,
  onUIPreferencesLoaded,
  FILTER_TAB,
  getFilterTab,
  setFilterTab,
  getFilterTabGrouping,
  setFilterTabGrouping,
  cycleFilterTabGrouping,
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
