// Mitto Web Interface - Unified Session Panel
// Fixed overlay panel on the RIGHT side with tabs for Properties and User Data

const { html, useState, useEffect, useCallback, useRef, useMemo, Fragment } =
  window.preact;

import {
  CloseIcon,
  EditIcon,
  CheckIcon,
  FolderIcon,
  PeriodicFilledIcon,
  ChevronDownIcon,
} from "./Icons.js";
import { apiUrl } from "../utils/api.js";
import { secureFetch, authFetch } from "../utils/csrf.js";
import { ConfirmDialog } from "./ConfirmDialog.js";
import { formatTimeAgo, looksLikeFilePath } from "../lib.js";
import { canRevealInFinder, revealInFinder } from "../utils/native.js";
import { isNativeApp, getAPIPrefix } from "../utils/index.js";

// ---------------------------------------------------------------------------
// Helpers (copied from ConversationPropertiesPanel)
// ---------------------------------------------------------------------------

function formatTokenCount(count) {
  if (count === undefined || count === null) return "—";
  if (count >= 1000000) return `${(count / 1000000).toFixed(1)}M`;
  if (count >= 1000) return `${(count / 1000).toFixed(1)}k`;
  return count.toString();
}

const MODEL_CONTEXT_WINDOWS = {
  "gemini-2.5": 1048576,
  "gemini-2.0": 1048576,
  "gemini-1.5": 1048576,
  "gemini": 1048576,
  "o4-mini": 200000,
  "opus": 200000,
  "sonnet": 200000,
  "haiku": 200000,
  "claude": 200000,
  "o1": 200000,
  "o3": 200000,
  "gpt-4o": 128000,
  "gpt-4-turbo": 128000,
  "gpt-4": 8192,
  "gpt-3.5": 16385,
};

function getContextWindowSize(modelId) {
  if (!modelId) return null;
  const lower = modelId.toLowerCase();
  const sortedKeys = Object.keys(MODEL_CONTEXT_WINDOWS).sort(
    (a, b) => b.length - a.length,
  );
  for (const key of sortedKeys) {
    if (lower.includes(key)) return MODEL_CONTEXT_WINDOWS[key];
  }
  return null;
}

function utcToLocalTimeDisplay(utcTime) {
  if (!utcTime) return "";
  const [hours, minutes] = utcTime.split(":").map(Number);
  const now = new Date();
  const utcDate = new Date(
    Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate(), hours, minutes, 0),
  );
  return utcDate.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
}

function formatFrequency(frequency) {
  if (!frequency) return "";
  const { value, unit, at } = frequency;
  let text = "";
  if (value === 1) {
    switch (unit) {
      case "minutes": text = "Every minute"; break;
      case "hours": text = "Every hour"; break;
      case "days": text = "Every day"; break;
      default: text = `Every ${unit}`;
    }
  } else {
    text = `Every ${value} ${unit}`;
  }
  if (unit === "days" && at) {
    text += ` at ${utcToLocalTimeDisplay(at)}`;
  }
  return text;
}

function formatRelativeTime(targetDate) {
  if (!targetDate) return "";
  const target = targetDate instanceof Date ? targetDate : new Date(targetDate);
  const now = new Date();
  const diffMs = target.getTime() - now.getTime();
  if (diffMs <= 0) return "now";
  const diffMinutes = Math.floor(diffMs / (1000 * 60));
  const diffHours = Math.floor(diffMs / (1000 * 60 * 60));
  const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24));
  if (diffMinutes < 60) return diffMinutes === 1 ? "in 1 minute" : `in ${diffMinutes} minutes`;
  if (diffHours < 24) return diffHours === 1 ? "in 1 hour" : `in ${diffHours} hours`;
  return diffDays === 1 ? "in 1 day" : `in ${diffDays} days`;
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function TriStateCheckbox({ value, onChange, disabled = false, title = "" }) {
  const handleClick = useCallback(() => {
    if (disabled) return;
    if (value === null || value === undefined) onChange(true);
    else onChange(!value);
  }, [value, onChange, disabled]);

  const isUnset = value === null || value === undefined;
  const isEnabled = value === true;

  return html`
    <button
      type="button"
      class="relative w-5 h-5 rounded border-2 transition-colors flex items-center justify-center
        ${disabled ? "opacity-50 cursor-not-allowed" : "cursor-pointer"}
        ${isUnset ? "border-slate-500 bg-slate-700" : isEnabled ? "border-blue-500 bg-blue-500" : "border-slate-500 bg-slate-700"}"
      onClick=${handleClick}
      disabled=${disabled}
      title=${title}
    >
      ${isUnset
        ? html`<span class="text-slate-500 text-xs font-medium">—</span>`
        : isEnabled
          ? html`<svg class="w-3 h-3 text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="3" d="M5 13l4 4L19 7" />
            </svg>`
          : null}
    </button>
  `;
}


function ConfigOptionSelect({ configOption, onSetConfigOption, isStreaming }) {
  const [localValue, setLocalValue] = useState(configOption.current_value);

  useEffect(() => {
    setLocalValue(configOption.current_value);
  }, [configOption.current_value]);

  const handleChange = useCallback(
    (e) => {
      const newValue = e.target.value;
      setLocalValue(newValue);
      onSetConfigOption?.(configOption.id, newValue);
    },
    [configOption.id, onSetConfigOption],
  );

  const selectedOpt = configOption.options?.find((o) => o.value === localValue);

  return html`
    <select
      class="w-full bg-slate-700 text-slate-200 rounded-lg px-3 py-2 text-sm border border-slate-600 focus:border-blue-500 focus:ring-1 focus:ring-blue-500 outline-none cursor-pointer"
      value=${localValue || ""}
      onChange=${handleChange}
      disabled=${isStreaming}
      title=${isStreaming
        ? `Cannot change ${configOption.name.toLowerCase()} while streaming`
        : configOption.description || `Select ${configOption.name.toLowerCase()}`}
    >
      ${configOption.options?.map(
        (opt) => html`
          <option value=${opt.value} title=${opt.description || ""}>${opt.name}</option>
        `,
      )}
    </select>
    ${selectedOpt?.description &&
    html`<p class="mt-1 text-xs text-slate-500">${selectedOpt.description}</p>`}
  `;
}

// ---------------------------------------------------------------------------
// Main SessionPanel component
// ---------------------------------------------------------------------------

/**
 * SessionPanel - Unified right-side overlay panel with tabs for Properties and User Data.
 */
export function SessionPanel({
  isOpen,
  onClose,
  activeTab = "changes",
  onTabChange,
  sessionId,
  sessionInfo,
  onRename,
  isStreaming = false,
  configOptions = [],
  onSetConfigOption,
  mcpTools = [],
}) {
  // --- Tab state ---
  const [currentTab, setCurrentTab] = useState(activeTab);
  useEffect(() => setCurrentTab(activeTab), [activeTab]);

  const handleTabChange = useCallback(
    (tab) => {
      setCurrentTab(tab);
      if (onTabChange) onTabChange(tab);
    },
    [onTabChange],
  );

  // --- Animation state ---
  const [isClosing, setIsClosing] = useState(false);
  const [shouldRender, setShouldRender] = useState(isOpen);

  useEffect(() => {
    if (isOpen) {
      setShouldRender(true);
      setIsClosing(false);
    } else if (shouldRender) {
      setIsClosing(true);
      const timer = setTimeout(() => {
        setShouldRender(false);
        setIsClosing(false);
      }, 150);
      return () => clearTimeout(timer);
    }
  }, [isOpen, shouldRender]);

  const handleClose = useCallback(() => {
    setIsClosing(true);
    setTimeout(() => onClose(), 150);
  }, [onClose]);

  // --- Properties tab state ---
  const [isEditingTitle, setIsEditingTitle] = useState(false);
  const [editedTitle, setEditedTitle] = useState("");
  const [isSavingTitle, setIsSavingTitle] = useState(false);
  const titleInputRef = useRef(null);
  const [periodicConfig, setPeriodicConfig] = useState(null);
  const [callbackConfig, setCallbackConfig] = useState(null);
  const [callbackCopied, setCallbackCopied] = useState(false);
  const [confirmDialog, setConfirmDialog] = useState(null);
  const [isMcpToolsExpanded, setIsMcpToolsExpanded] = useState(false);
  const [isAdvancedExpanded, setIsAdvancedExpanded] = useState(false);
  const [availableFlags, setAvailableFlags] = useState([]);
  const [sessionSettings, setSessionSettings] = useState({});
  const [isLoadingFlags, setIsLoadingFlags] = useState(false);
  const [savingFlags, setSavingFlags] = useState({});
  const [flagsError, setFlagsError] = useState(null);
  const [, setTimeNow] = useState(Date.now());

  const currentModelId = useMemo(() => {
    if (!configOptions?.length) return null;
    const modelOpt = configOptions.find((opt) => opt.id === "model");
    return modelOpt?.current_value || null;
  }, [configOptions]);

  // --- Changes tab state ---
  const [changesData, setChangesData] = useState(null);
  const [isLoadingChanges, setIsLoadingChanges] = useState(false);
  const [changesError, setChangesError] = useState(null);

  // --- User Data tab state ---
  const [userData, setUserData] = useState({ attributes: [] });
  const [userDataSchema, setUserDataSchema] = useState(null);
  const [isLoadingUserData, setIsLoadingUserData] = useState(false);
  const [editingAttribute, setEditingAttribute] = useState(null);
  const [editedAttributeValue, setEditedAttributeValue] = useState("");
  const [isSavingAttribute, setIsSavingAttribute] = useState(false);
  const [userDataError, setUserDataError] = useState(null);
  const attributeInputRef = useRef(null);


  // --- Effects: reset on session change ---
  useEffect(() => {
    setIsEditingTitle(false);
    setPeriodicConfig(null);
    setCallbackConfig(null);
    setCallbackCopied(false);
    setFlagsError(null);
    setSavingFlags({});
    setEditingAttribute(null);
    setUserDataError(null);
  }, [sessionId, isOpen]);

  // --- Effects: fetch properties data when open ---
  useEffect(() => {
    if (!isOpen || !sessionId) return;

    const fetchData = async () => {
      setIsLoadingFlags(true);
      setFlagsError(null);

      try {
        const [periodicRes, callbackRes, flagsRes, settingsRes] = await Promise.all([
          authFetch(apiUrl(`/api/sessions/${sessionId}/periodic`)),
          authFetch(apiUrl(`/api/sessions/${sessionId}/callback`)),
          authFetch(apiUrl("/api/advanced-flags")),
          authFetch(apiUrl(`/api/sessions/${sessionId}/settings`)),
        ]);

        if (periodicRes.ok) setPeriodicConfig(await periodicRes.json());
        else setPeriodicConfig(null);

        if (callbackRes.ok) setCallbackConfig(await callbackRes.json());
        else setCallbackConfig(null);

        if (flagsRes.ok) {
          const flagsData = await flagsRes.json();
          setAvailableFlags(flagsData.flags || flagsData || []);
        }

        if (settingsRes.ok) {
          const settingsData = await settingsRes.json();
          setSessionSettings(settingsData.settings || {});
        }
      } catch (err) {
        console.error("[SessionPanel] Failed to fetch properties data:", err);
        setFlagsError("Failed to load settings");
      } finally {
        setIsLoadingFlags(false);
      }
    };

    fetchData();
  }, [isOpen, sessionId]);

  // --- Effects: fetch user data when open ---
  useEffect(() => {
    if (!isOpen || !sessionId || !sessionInfo?.working_dir) return;

    const fetchUserData = async () => {
      setIsLoadingUserData(true);
      setUserDataError(null);

      try {
        const [userDataRes, schemaRes] = await Promise.all([
          authFetch(apiUrl(`/api/sessions/${sessionId}/user-data`)),
          authFetch(
            apiUrl(
              `/api/workspace/user-data-schema?working_dir=${encodeURIComponent(sessionInfo.working_dir)}`,
            ),
          ),
        ]);

        if (userDataRes.ok) setUserData(await userDataRes.json());

        if (schemaRes.ok) setUserDataSchema(await schemaRes.json());
        else if (schemaRes.status === 404) setUserDataSchema({ fields: [] });
      } catch (err) {
        console.error("[SessionPanel] Failed to fetch user data:", err);
        setUserDataError("Failed to load user data");
      } finally {
        setIsLoadingUserData(false);
      }
    };

    fetchUserData();
  }, [isOpen, sessionId, sessionInfo?.working_dir]);

  // --- Effects: fetch changes when changes tab is active ---
  useEffect(() => {
    if (!isOpen || !sessionId || currentTab !== "changes") return;

    const fetchChanges = async () => {
      setIsLoadingChanges(true);
      setChangesError(null);
      try {
        const resp = await authFetch(apiUrl(`/api/sessions/${sessionId}/changes`));
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
        const data = await resp.json();
        setChangesData(data);
      } catch (err) {
        setChangesError(err.message);
      } finally {
        setIsLoadingChanges(false);
      }
    };

    fetchChanges();
  }, [isOpen, sessionId, currentTab]);

  // --- Effects: periodic relative time ticker ---
  useEffect(() => {
    if (!isOpen || !periodicConfig?.next_scheduled_at) return;
    const id = setInterval(() => setTimeNow(Date.now()), 30000);
    return () => clearInterval(id);
  }, [isOpen, periodicConfig?.next_scheduled_at]);

  // --- Effects: WebSocket settings sync ---
  useEffect(() => {
    if (!isOpen || !sessionId) return;
    const handler = (event) => {
      const { session_id, settings } = event.detail || {};
      if (session_id === sessionId && settings) setSessionSettings(settings);
    };
    window.addEventListener("mitto:session_settings_updated", handler);
    return () => window.removeEventListener("mitto:session_settings_updated", handler);
  }, [isOpen, sessionId]);

  // --- Effects: focus inputs ---
  useEffect(() => {
    if (isEditingTitle && titleInputRef.current) {
      titleInputRef.current.focus();
      titleInputRef.current.select();
    }
  }, [isEditingTitle]);

  useEffect(() => {
    if (editingAttribute && attributeInputRef.current) {
      attributeInputRef.current.focus();
      attributeInputRef.current.select();
    }
  }, [editingAttribute]);


  // --- Handlers: title editing ---
  const handleStartEditTitle = useCallback(() => {
    setEditedTitle(sessionInfo?.name || "");
    setIsEditingTitle(true);
  }, [sessionInfo?.name]);

  const handleSaveTitle = useCallback(async () => {
    if (!sessionId || isSavingTitle) return;
    const newTitle = editedTitle.trim();
    if (!newTitle || newTitle === sessionInfo?.name) {
      setIsEditingTitle(false);
      return;
    }
    setIsSavingTitle(true);
    try {
      await onRename(sessionId, newTitle);
      setIsEditingTitle(false);
    } catch (err) {
      console.error("Failed to save title:", err);
    } finally {
      setIsSavingTitle(false);
    }
  }, [sessionId, editedTitle, sessionInfo?.name, onRename, isSavingTitle]);

  const handleTitleKeyDown = useCallback(
    (e) => {
      if (e.key === "Enter") { e.preventDefault(); handleSaveTitle(); }
      else if (e.key === "Escape") setIsEditingTitle(false);
    },
    [handleSaveTitle],
  );

  // --- Handlers: feature flags ---
  const handleFlagChange = useCallback(
    async (flagName, newValue) => {
      if (!sessionId) return;
      setSavingFlags((prev) => ({ ...prev, [flagName]: true }));
      setFlagsError(null);
      try {
        const res = await secureFetch(apiUrl(`/api/sessions/${sessionId}/settings`), {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ settings: { [flagName]: newValue } }),
        });
        if (res.ok) {
          const data = await res.json();
          setSessionSettings(data.settings || {});
        } else {
          const errorData = await res.json().catch(() => ({}));
          setFlagsError(errorData.message || "Failed to save setting");
        }
      } catch (err) {
        console.error("Failed to save flag:", err);
        setFlagsError("Failed to save setting");
      } finally {
        setSavingFlags((prev) => ({ ...prev, [flagName]: false }));
      }
    },
    [sessionId],
  );

  // --- Handlers: callback URL ---
  const handleEnableCallback = useCallback(async () => {
    const res = await secureFetch(apiUrl(`/api/sessions/${sessionId}/callback`), { method: "POST" });
    if (res.ok) {
      const data = await res.json();
      setCallbackConfig(data);
      try {
        await navigator.clipboard.writeText(data.callback_url);
        setCallbackCopied(true);
        setTimeout(() => setCallbackCopied(false), 2000);
      } catch (e) { /* clipboard may not be available */ }
    }
  }, [sessionId]);

  const handleCopyCallbackUrl = useCallback(async () => {
    if (callbackConfig?.callback_url) {
      try {
        await navigator.clipboard.writeText(callbackConfig.callback_url);
        setCallbackCopied(true);
        setTimeout(() => setCallbackCopied(false), 2000);
      } catch (e) { /* clipboard may not be available */ }
    }
  }, [callbackConfig]);

  const handleRotateCallback = useCallback(() => {
    setConfirmDialog({
      title: "Rotate Callback URL",
      message: "Rotate callback URL? The old URL will stop working immediately.",
      confirmLabel: "Rotate",
      confirmVariant: "danger",
      onConfirm: async () => {
        setConfirmDialog(null);
        const res = await secureFetch(apiUrl(`/api/sessions/${sessionId}/callback`), { method: "POST" });
        if (res.ok) {
          const data = await res.json();
          setCallbackConfig(data);
          try {
            await navigator.clipboard.writeText(data.callback_url);
            setCallbackCopied(true);
            setTimeout(() => setCallbackCopied(false), 2000);
          } catch (e) { /* clipboard may not be available */ }
        }
      },
    });
  }, [sessionId]);

  const handleRevokeCallback = useCallback(() => {
    setConfirmDialog({
      title: "Revoke Callback URL",
      message: "Revoke callback URL? It will stop working immediately.",
      confirmLabel: "Revoke",
      confirmVariant: "danger",
      onConfirm: async () => {
        setConfirmDialog(null);
        const res = await secureFetch(apiUrl(`/api/sessions/${sessionId}/callback`), { method: "DELETE" });
        if (res.ok) setCallbackConfig(null);
      },
    });
  }, [sessionId]);

  // --- Handlers: user data editing ---
  const handleStartEditAttribute = useCallback((attr) => {
    setEditingAttribute(attr.name);
    setEditedAttributeValue(attr.value || "");
  }, []);

  const getAttributeValue = useCallback(
    (name) => {
      const attr = userData.attributes.find((a) => a.name === name);
      return attr?.value || "";
    },
    [userData.attributes],
  );

  const handleSaveAttribute = useCallback(async () => {
    if (!sessionId || isSavingAttribute || !editingAttribute) return;
    setIsSavingAttribute(true);
    setUserDataError(null);
    try {
      const updatedAttributes = [...userData.attributes];
      const existingIndex = updatedAttributes.findIndex((a) => a.name === editingAttribute);
      if (existingIndex >= 0) {
        updatedAttributes[existingIndex] = { name: editingAttribute, value: editedAttributeValue };
      } else {
        updatedAttributes.push({ name: editingAttribute, value: editedAttributeValue });
      }
      const res = await secureFetch(apiUrl(`/api/sessions/${sessionId}/user-data`), {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ attributes: updatedAttributes }),
      });
      if (res.ok) {
        setUserData(await res.json());
        setEditingAttribute(null);
      } else {
        const errorData = await res.json().catch(() => ({}));
        setUserDataError(errorData.message || "Failed to save attribute");
      }
    } catch (err) {
      console.error("Failed to save attribute:", err);
      setUserDataError("Failed to save attribute");
    } finally {
      setIsSavingAttribute(false);
    }
  }, [sessionId, editingAttribute, editedAttributeValue, userData.attributes, isSavingAttribute]);

  const handleAttributeKeyDown = useCallback(
    (e) => {
      if (e.key === "Enter") { e.preventDefault(); handleSaveAttribute(); }
      else if (e.key === "Escape") setEditingAttribute(null);
    },
    [handleSaveAttribute],
  );


  if (!shouldRender) return null;

  return html`
    <${Fragment}>
      <div
        class="fixed inset-0 z-50 flex"
        onClick=${(e) => { if (e.target === e.currentTarget) handleClose(); }}
      >
        <!-- Backdrop -->
        <div
          class="flex-1 bg-black/50 properties-backdrop ${isClosing ? "closing" : ""}"
          onClick=${handleClose}
        />
        <!-- Panel -->
        <div
          class="w-80 bg-mitto-sidebar flex-shrink-0 shadow-2xl h-full flex flex-col border-l border-slate-700 properties-panel ${isClosing ? "closing" : ""}"
        >
          <!-- Header -->
          <div class="p-4 border-b border-slate-700 flex items-center justify-between flex-shrink-0">
            <h2 class="font-semibold text-lg">Conversation</h2>
            <button
              class="p-1 hover:bg-slate-700 rounded transition-colors"
              onClick=${handleClose}
              title="Close"
            >
              <${CloseIcon} className="w-5 h-5" />
            </button>
          </div>

          <!-- Tab switcher -->
          <div class="flex border-b border-slate-700 flex-shrink-0">
            <button
              class="flex-1 px-4 py-2.5 text-sm font-medium transition-colors ${currentTab === "changes" ? "text-blue-400 border-b-2 border-blue-400" : "text-slate-400 hover:text-slate-300"}"
              onClick=${() => handleTabChange("changes")}
            >
              Changes
            </button>
            <button
              class="flex-1 px-4 py-2.5 text-sm font-medium transition-colors ${currentTab === "properties" ? "text-blue-400 border-b-2 border-blue-400" : "text-slate-400 hover:text-slate-300"}"
              onClick=${() => handleTabChange("properties")}
            >
              Properties
            </button>
            <button
              class="flex-1 px-4 py-2.5 text-sm font-medium transition-colors ${currentTab === "userdata" ? "text-blue-400 border-b-2 border-blue-400" : "text-slate-400 hover:text-slate-300"}"
              onClick=${() => handleTabChange("userdata")}
            >
              User Data
            </button>
          </div>

          <!-- Tab content -->
          <div class="flex-1 overflow-y-auto">
            ${currentTab === "changes" ? renderChangesContent() : currentTab === "properties" ? renderPropertiesContent() : renderUserDataContent()}
          </div>
        </div>
      </div>

      <${ConfirmDialog}
        isOpen=${!!confirmDialog}
        title=${confirmDialog?.title || "Confirm"}
        message=${confirmDialog?.message || ""}
        confirmLabel=${confirmDialog?.confirmLabel || "Yes"}
        cancelLabel=${confirmDialog?.cancelLabel || "Cancel"}
        confirmVariant=${confirmDialog?.confirmVariant || "primary"}
        onConfirm=${confirmDialog?.onConfirm}
        onCancel=${() => setConfirmDialog(null)}
      />
    <//>
  `;

  // ---------------------------------------------------------------------------
  // Changes tab content
  // ---------------------------------------------------------------------------
  function renderChangesContent() {
    // Helper to build viewer URL with diff mode
    const buildDiffViewerUrl = (filePath) => {
      const apiPrefix = window.mittoApiPrefix || "";
      const workspaceUUID = window.mittoCurrentWorkspaceUUID || "";
      const wsPath = sessionInfo?.working_dir || window.mittoCurrentWorkspace || "";
      const relativePath = filePath.replace(/^\.\//, "");
      if (!workspaceUUID) return null;
      let url = `${apiPrefix}/viewer.html?ws=${encodeURIComponent(workspaceUUID)}&path=${encodeURIComponent(relativePath)}&view=diff`;
      if (wsPath) url += `&ws_path=${encodeURIComponent(wsPath)}`;
      return url;
    };

    const openFileInViewer = (filePath, e) => {
      if (e) { e.preventDefault(); e.stopPropagation(); }
      const viewerUrl = buildDiffViewerUrl(filePath);
      if (!viewerUrl) return;
      if (isNativeApp() && typeof window.mittoOpenViewer === "function") {
        const fullUrl = new URL(viewerUrl, window.location.origin).href;
        window.mittoOpenViewer(fullUrl);
      } else {
        window.open(viewerUrl, "_blank", "noopener,noreferrer");
      }
    };

    const statusColors = {
      "A": "bg-green-600 text-white",
      "M": "bg-amber-600 text-white",
      "D": "bg-red-600 text-white",
      "R": "bg-blue-600 text-white",
      "C": "bg-purple-600 text-white",
      "?": "bg-slate-500 text-white",
    };

    const handleRefreshChanges = async () => {
      if (!sessionId) return;
      setIsLoadingChanges(true);
      setChangesError(null);
      try {
        const resp = await authFetch(apiUrl(`/api/sessions/${sessionId}/changes`));
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
        const data = await resp.json();
        setChangesData(data);
      } catch (err) {
        setChangesError(err.message);
      } finally {
        setIsLoadingChanges(false);
      }
    };

    if (isLoadingChanges && !changesData) {
      return html`
        <div class="p-4 text-center text-slate-500">
          <div class="animate-spin inline-block w-5 h-5 border-2 border-slate-500 border-t-transparent rounded-full mb-2"></div>
          <p class="text-sm">Loading changes...</p>
        </div>
      `;
    }

    if (changesError) {
      return html`
        <div class="p-4">
          <div class="p-3 bg-red-500/20 border border-red-500/50 rounded-lg text-red-400 text-sm">
            Failed to load changes: ${changesError}
          </div>
          <button
            class="mt-3 px-3 py-1.5 text-xs text-slate-400 hover:text-slate-200 hover:bg-slate-700 rounded transition-colors"
            onClick=${handleRefreshChanges}
          >Retry</button>
        </div>
      `;
    }

    if (!changesData || !changesData.is_git_repo) {
      return html`
        <div class="p-4 text-center text-slate-500 text-sm">
          <p>Not a git repository</p>
        </div>
      `;
    }

    const files = changesData.files || [];

    return html`
      <div class="p-4 space-y-3">
        <!-- Header with branch and refresh -->
        <div class="flex items-center justify-between">
          <div class="flex items-center gap-2 text-sm text-slate-400">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M6 3v12M18 9a3 3 0 01-3 3H9m9-3a3 3 0 00-3-3H9m0 0V3" />
            </svg>
            <span class="font-medium">${changesData.branch || "detached"}</span>
            <span class="text-slate-600">·</span>
            <span>${files.length} file${files.length !== 1 ? "s" : ""}</span>
          </div>
          <button
            class="p-1.5 text-slate-400 hover:text-slate-200 hover:bg-slate-700 rounded transition-colors ${isLoadingChanges ? "animate-spin" : ""}"
            onClick=${handleRefreshChanges}
            title="Refresh changes"
            disabled=${isLoadingChanges}
          >
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
            </svg>
          </button>
        </div>

        ${files.length === 0
          ? html`
              <div class="text-center text-slate-500 text-sm py-6">
                No uncommitted changes
              </div>
            `
          : html`
              <div class="space-y-0.5">
                ${files.map(
                  (file) => html`
                    <a
                      key=${file.path}
                      href="#"
                      class="flex items-center gap-2 px-2 py-1.5 rounded hover:bg-slate-700/50 transition-colors cursor-pointer group no-underline"
                      onClick=${(e) => openFileInViewer(file.path, e)}
                      title=${file.old_path ? file.old_path + " → " + file.path : file.path}
                    >
                      <span
                        class="flex-shrink-0 w-5 h-5 rounded text-[10px] font-bold flex items-center justify-center ${statusColors[file.status] || "bg-slate-600 text-white"}"
                      >${file.status}</span>
                      <span class="flex-1 text-sm text-slate-300 truncate group-hover:text-slate-100">${file.path}</span>
                      ${(file.additions > 0 || file.deletions > 0) &&
                      html`
                        <span class="flex-shrink-0 text-xs font-mono whitespace-nowrap">
                          ${file.additions > 0 && html`<span class="text-green-400">+${file.additions}</span>`}
                          ${file.additions > 0 && file.deletions > 0 && html`<span class="text-slate-600">/</span>`}
                          ${file.deletions > 0 && html`<span class="text-red-400">-${file.deletions}</span>`}
                        </span>
                      `}
                    </a>
                  `,
                )}
              </div>
            `}
      </div>
    `;
  }

  // ---------------------------------------------------------------------------
  // Properties tab content
  // ---------------------------------------------------------------------------
  function renderPropertiesContent() {
    return html`
      <div class="p-4 space-y-6">
        <!-- Title -->
        <div>
          <label class="block text-sm font-medium text-slate-400 mb-2">Title</label>
          ${isEditingTitle
            ? html`
                <div class="flex items-center gap-2">
                  <input
                    ref=${titleInputRef}
                    type="text"
                    class="flex-1 bg-slate-800 border border-slate-600 rounded px-3 py-2 text-sm focus:outline-none focus:border-blue-500"
                    value=${editedTitle}
                    onInput=${(e) => setEditedTitle(e.target.value)}
                    onKeyDown=${handleTitleKeyDown}
                    onBlur=${() => { setTimeout(() => { if (isEditingTitle && !isSavingTitle) setIsEditingTitle(false); }, 150); }}
                    disabled=${isSavingTitle}
                  />
                  <button class="p-2 hover:bg-slate-700 rounded transition-colors text-green-400" onClick=${handleSaveTitle} title="Save" disabled=${isSavingTitle}>
                    <${CheckIcon} className="w-4 h-4" />
                  </button>
                </div>
              `
            : html`
                <div class="flex items-center gap-2 group">
                  <span class="flex-1 text-sm truncate cursor-pointer hover:text-blue-400 transition-colors" onClick=${handleStartEditTitle} title="Click to edit title">
                    ${sessionInfo?.name || "New conversation"}
                  </span>
                  <button class="p-1 hover:bg-slate-700 rounded transition-colors opacity-0 group-hover:opacity-100" onClick=${handleStartEditTitle} title="Edit title">
                    <${EditIcon} className="w-4 h-4" />
                  </button>
                </div>
              `}
        </div>

        <!-- Status Badges -->
        <div class="flex items-center gap-2 flex-wrap">
          ${isStreaming
            ? html`<span class="inline-flex items-center gap-1.5 px-2 py-1 rounded-full bg-blue-500/20 text-blue-400 text-xs"><span class="w-2 h-2 bg-blue-400 rounded-full streaming-indicator"></span>Streaming</span>`
            : sessionInfo?.archived
              ? html`<span class="inline-flex items-center gap-1.5 px-2 py-1 rounded-full bg-slate-700 text-slate-400 text-xs"><span class="w-2 h-2 bg-slate-500 rounded-full"></span>Archived</span>`
              : sessionInfo?.status === "active"
                ? html`<span class="inline-flex items-center gap-1.5 px-2 py-1 rounded-full bg-green-500/20 text-green-400 text-xs"><span class="w-2 h-2 bg-green-400 rounded-full"></span>Active</span>`
                : html`<span class="inline-flex items-center gap-1.5 px-2 py-1 rounded-full bg-slate-700 text-slate-400 text-xs">Stored</span>`}
          ${sessionInfo?.acp_server && html`<span class="inline-flex items-center px-2 py-1 rounded bg-blue-500/20 text-blue-400 text-xs" title="ACP Server">${sessionInfo.acp_server}</span>`}
          ${sessionInfo?.runner_type && html`<span class="inline-flex items-center px-2 py-1 rounded ${sessionInfo.runner_restricted ? "bg-yellow-500/20 text-yellow-400" : "bg-purple-500/20 text-purple-400"} text-xs" title="${sessionInfo.runner_restricted ? "Restricted execution mode" : "Sandbox type"}">${sessionInfo.runner_type}</span>`}
        </div>

        <!-- Statistics Section -->
        <div>
          <label class="block text-sm font-medium text-slate-400 mb-1">Statistics</label>
          <div class="text-xs text-slate-400 space-y-0.5">
            ${sessionInfo?.messageCount !== undefined && html`
              <div class="flex justify-between">
                <span>Messages</span>
                <span class="text-slate-300">${sessionInfo.messageCount}</span>
              </div>
            `}
            ${sessionInfo?.created_at && html`
              <div class="flex justify-between">
                <span>Created</span>
                <span class="text-slate-300" title=${new Date(sessionInfo.created_at).toLocaleString()}>
                  ${formatTimeAgo(sessionInfo.created_at)}
                </span>
              </div>
            `}
            ${(sessionInfo?.processor_count > 0) && html`
              <div
                class="flex justify-between"
                title=${sessionInfo?.processor_last_names?.length
                  ? `Last applied: ${sessionInfo.processor_last_names.join(", ")}`
                  : "No processors applied yet"}
              >
                <span>Processors</span>
                <span class="text-slate-300">${sessionInfo.processor_count}${sessionInfo?.processor_activations > 0 ? ` (${sessionInfo.processor_activations} runs)` : ""}</span>
              </div>
            `}
          </div>

          ${sessionInfo?.usage && html`
            <div class="mt-2 pt-2 border-t border-slate-700/50">
              ${(() => {
                const contextTokens = sessionInfo.usage.input_tokens;
                const contextWindow = getContextWindowSize(currentModelId);
                const pct = contextWindow ? Math.min((contextTokens / contextWindow) * 100, 100) : null;
                const barColor = pct === null ? "bg-blue-500" : pct > 80 ? "bg-red-500" : pct > 50 ? "bg-yellow-500" : "bg-green-500";
                const textColor = pct === null ? "text-slate-300" : pct > 80 ? "text-red-400" : pct > 50 ? "text-yellow-400" : "text-green-400";
                return html`
                  <div class="mb-2">
                    <div class="flex justify-between items-baseline mb-1">
                      <span class="text-xs font-medium text-slate-400">Context</span>
                      <span class="text-xs ${textColor}">
                        ${formatTokenCount(contextTokens)}${contextWindow ? html` / ${formatTokenCount(contextWindow)}` : ""}
                      </span>
                    </div>
                    <div class="w-full h-1.5 bg-slate-700 rounded-full overflow-hidden">
                      <div class="h-full ${barColor} rounded-full transition-all duration-300" style="width: ${pct !== null ? pct : 0}%" />
                    </div>
                    ${pct !== null && html`<div class="text-right mt-0.5"><span class="text-[10px] text-slate-500">${pct.toFixed(0)}%</span></div>`}
                  </div>
                `;
              })()}
              <label class="block text-xs font-medium text-slate-500 mb-1">Last Turn Tokens</label>
              <div class="text-xs text-slate-400 space-y-0.5">
                <div class="flex justify-between"><span>Input</span><span class="text-slate-300">${formatTokenCount(sessionInfo.usage.input_tokens)}</span></div>
                <div class="flex justify-between"><span>Output</span><span class="text-slate-300">${formatTokenCount(sessionInfo.usage.output_tokens)}</span></div>
                <div class="flex justify-between"><span>Total</span><span class="text-slate-300 font-medium">${formatTokenCount(sessionInfo.usage.total_tokens)}</span></div>
                ${sessionInfo.usage.cached_read_tokens !== undefined && html`<div class="flex justify-between"><span>Cache Read</span><span class="text-slate-300">${formatTokenCount(sessionInfo.usage.cached_read_tokens)}</span></div>`}
                ${sessionInfo.usage.cached_write_tokens !== undefined && html`<div class="flex justify-between"><span>Cache Write</span><span class="text-slate-300">${formatTokenCount(sessionInfo.usage.cached_write_tokens)}</span></div>`}
                ${sessionInfo.usage.thought_tokens !== undefined && html`<div class="flex justify-between"><span>Thinking</span><span class="text-slate-300">${formatTokenCount(sessionInfo.usage.thought_tokens)}</span></div>`}
              </div>
            </div>
          `}
        </div>

        <!-- Workspace Section -->
        <div>
          <label class="block text-sm font-medium text-slate-400 mb-2">Workspace</label>
          <div class="flex items-center gap-2 text-sm text-slate-300">
            <${FolderIcon} className="w-4 h-4 flex-shrink-0 text-slate-500" />
            ${canRevealInFinder() && sessionInfo?.working_dir
              ? html`<button type="button" class="truncate text-left hover:text-blue-400 hover:underline transition-colors cursor-pointer" title="Open in Finder: ${sessionInfo.working_dir}" onClick=${() => revealInFinder(sessionInfo.working_dir)}>${sessionInfo.working_dir}</button>`
              : html`<span class="truncate" title=${sessionInfo?.working_dir || ""}>${sessionInfo?.working_dir || "Unknown"}</span>`}
          </div>
        </div>

        <!-- Session Config Options -->
        ${configOptions?.length > 0 && configOptions.map((configOption) => html`
          <div key=${configOption.id}>
            <label class="block text-sm font-medium text-slate-400 mb-2">${configOption.name}</label>
            ${configOption.type === "select" && html`
              <${ConfigOptionSelect} configOption=${configOption} onSetConfigOption=${onSetConfigOption} isStreaming=${isStreaming} />
            `}
            ${configOption.type === "toggle" && html`
              <div class="flex items-center justify-between">
                <button
                  class="relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out ${configOption.current_value === "true" ? "bg-blue-600" : "bg-slate-600"}"
                  role="switch"
                  aria-checked=${configOption.current_value === "true"}
                  onClick=${() => onSetConfigOption?.(configOption.id, configOption.current_value === "true" ? "false" : "true")}
                  disabled=${isStreaming}
                  title=${isStreaming ? `Cannot change ${configOption.name.toLowerCase()} while streaming` : configOption.description || `Toggle ${configOption.name.toLowerCase()}`}
                >
                  <span class="pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out ${configOption.current_value === "true" ? "translate-x-5" : "translate-x-0"}" />
                </button>
              </div>
              ${configOption.description && html`<p class="mt-1 text-xs text-slate-500">${configOption.description}</p>`}
            `}
            ${configOption.type !== "select" && configOption.type !== "toggle" && html`
              <div class="w-full bg-slate-700/50 text-slate-400 rounded-lg px-3 py-2 text-sm border border-slate-600" title=${`Unsupported config type: ${configOption.type}`}>
                ${configOption.current_value || "(not set)"}
              </div>
              ${configOption.description && html`<p class="mt-1 text-xs text-slate-500">${configOption.description}</p>`}
            `}
          </div>
        `)}

        <!-- Periodic Prompts Section -->
        ${periodicConfig?.enabled && html`
          <div>
            <label class="block text-sm font-medium text-slate-400 mb-2">Periodic Prompts</label>
            <div class="flex items-center gap-2 text-sm text-slate-300">
              <${PeriodicFilledIcon} className="w-4 h-4 flex-shrink-0 text-blue-400" />
              <span>${formatFrequency(periodicConfig.frequency)}</span>
            </div>
            ${periodicConfig.last_sent_at && html`<p class="mt-1 text-xs text-slate-500">Last run: ${new Date(periodicConfig.last_sent_at).toLocaleString()}</p>`}
            ${periodicConfig.next_scheduled_at && html`
              <p class="mt-1 text-xs text-slate-500">
                Next run: ${new Date(periodicConfig.next_scheduled_at).toLocaleString()}
                <span class="text-slate-400 ml-1">(${formatRelativeTime(periodicConfig.next_scheduled_at)})</span>
              </p>
            `}
            ${callbackConfig?.callback_url && html`
              <div class="mt-3 pt-3 border-t border-slate-700/50">
                <label class="block text-xs font-medium text-slate-500 mb-1.5">Callback URL</label>
                <div class="flex items-center gap-1.5">
                  <button onClick=${handleCopyCallbackUrl} class="text-xs px-2 py-1 rounded bg-slate-700 hover:bg-slate-600 text-slate-300 transition-colors" title="Copy callback URL to clipboard">
                    ${callbackCopied ? "✓ Copied!" : "📋 Copy URL"}
                  </button>
                  <button onClick=${handleRotateCallback} class="text-xs px-2 py-1 rounded bg-slate-700 hover:bg-slate-600 text-slate-300 transition-colors" title="Generate new callback URL (invalidates old one)">🔄 Rotate</button>
                  <button onClick=${handleRevokeCallback} class="text-xs px-2 py-1 rounded bg-slate-700 hover:bg-red-900/50 text-slate-400 hover:text-red-300 transition-colors" title="Revoke callback URL">✕</button>
                </div>
              </div>
            `}
            ${!callbackConfig?.callback_url && html`
              <div class="mt-3 pt-3 border-t border-slate-700/50">
                <label class="block text-xs font-medium text-slate-500 mb-1.5">Callback URL</label>
                <button onClick=${handleEnableCallback} class="text-xs px-2 py-1 rounded bg-slate-700 hover:bg-slate-600 text-slate-300 transition-colors" title="Generate a callback URL for triggering this periodic conversation externally">
                  🔗 Enable Callback URL
                </button>
              </div>
            `}
          </div>
        `}

        <!-- Callback URL when periodic is disabled -->
        ${!periodicConfig?.enabled && callbackConfig?.callback_url && html`
          <div class="mt-2">
            <label class="block text-xs font-medium text-slate-500 mb-1">Callback URL</label>
            <p class="text-xs text-slate-600 mb-1.5 italic">Preserved but inactive while periodic is disabled</p>
            <div class="flex items-center gap-1.5">
              <button onClick=${handleCopyCallbackUrl} class="text-xs px-2 py-1 rounded bg-slate-800 text-slate-500 hover:text-slate-400 transition-colors">${callbackCopied ? "✓ Copied!" : "📋 Copy URL"}</button>
              <button onClick=${handleRevokeCallback} class="text-xs px-2 py-1 rounded bg-slate-800 text-slate-500 hover:text-red-400 transition-colors">✕ Revoke</button>
            </div>
          </div>
        `}

        <!-- MCP Tools Section (Collapsible) -->
        ${mcpTools && mcpTools.length > 0 && html`
          <div class="pt-4">
            <button type="button" class="w-full flex items-center gap-2 text-sm font-medium text-slate-400 hover:text-slate-300 transition-colors" style="background: transparent; border: none; padding: 0; cursor: pointer;" onClick=${() => setIsMcpToolsExpanded(!isMcpToolsExpanded)}>
              <span class="transition-transform ${isMcpToolsExpanded ? "" : "-rotate-90"}">
                <${ChevronDownIcon} className="w-4 h-4" />
              </span>
              <span>MCP Tools</span>
              <span class="text-xs text-slate-500">(${mcpTools.length})</span>
            </button>
            ${isMcpToolsExpanded && html`
              <div class="mt-3 space-y-1 max-h-64 overflow-y-auto">
                ${mcpTools.map((tool) => html`
                  <div key=${tool.name} class="text-xs text-slate-300 bg-slate-700/50 rounded px-2 py-1" title=${tool.description || tool.name}>
                    <span class="font-mono">${tool.name}</span>
                    ${tool.description && html`<p class="text-slate-500 mt-0.5 truncate">${tool.description}</p>`}
                  </div>
                `)}
              </div>
            `}
          </div>
        `}

        <!-- Advanced Section (Collapsible) -->
        ${renderAdvancedSection()}
      </div>
    `;
  }


  // ---------------------------------------------------------------------------
  // Advanced section (feature flags)
  // ---------------------------------------------------------------------------
  function renderAdvancedSection() {
    if (!availableFlags || availableFlags.length === 0) return null;

    return html`
      <div class="pt-4">
        <button
          type="button"
          class="w-full flex items-center gap-2 text-sm font-medium text-slate-400 hover:text-slate-300 transition-colors"
          style="background: transparent; border: none; padding: 0; cursor: pointer;"
          onClick=${() => setIsAdvancedExpanded(!isAdvancedExpanded)}
        >
          <span class="transition-transform ${isAdvancedExpanded ? "" : "-rotate-90"}">
            <${ChevronDownIcon} className="w-4 h-4" />
          </span>
          <span>Advanced</span>
        </button>

        ${isAdvancedExpanded && html`
          <div class="mt-3 space-y-3">
            ${isLoadingFlags
              ? html`<div class="text-sm text-slate-500">Loading...</div>`
              : html`
                  ${flagsError && html`<div class="text-sm text-red-400 bg-red-900/20 rounded px-2 py-1">${flagsError}</div>`}
                  ${availableFlags.map((flag) => {
                    const currentValue = sessionSettings[flag.name];
                    const isSaving = savingFlags[flag.name];
                    return html`
                      <div key=${flag.name} class="flex items-start gap-3">
                        <div class="pt-0.5">
                          ${isSaving
                            ? html`<div class="w-5 h-5 flex items-center justify-center"><div class="w-3 h-3 border-2 border-blue-500 border-t-transparent rounded-full animate-spin"></div></div>`
                            : html`<${TriStateCheckbox} value=${currentValue} onChange=${(newValue) => handleFlagChange(flag.name, newValue)} title=${flag.description || flag.label} />`}
                        </div>
                        <div class="flex-1 min-w-0">
                          <label
                            class="block text-sm text-slate-300 cursor-pointer"
                            onClick=${() => !isSaving && handleFlagChange(flag.name, currentValue === true ? false : true)}
                          >
                            ${flag.label}
                          </label>
                          ${flag.description && html`<p class="text-xs text-slate-500 mt-0.5">${flag.description}</p>`}
                        </div>
                      </div>
                    `;
                  })}
                `}
          </div>
        `}
      </div>
    `;
  }

  // ---------------------------------------------------------------------------
  // User Data tab content
  // ---------------------------------------------------------------------------
  function renderUserDataContent() {
    const hasSchema = userDataSchema && userDataSchema.fields?.length > 0;

    if (isLoadingUserData) {
      return html`<div class="p-4 text-sm text-slate-500">Loading...</div>`;
    }

    if (!hasSchema) {
      return html`
        <div class="p-4 text-sm text-slate-500 italic">
          No user data schema configured for this workspace.
        </div>
      `;
    }

    return html`
      <div class="p-4 space-y-3">
        ${userDataError && html`<div class="text-sm text-red-400 bg-red-900/20 rounded px-2 py-1">${userDataError}</div>`}
        ${userDataSchema.fields.map((field) => {
          const value = getAttributeValue(field.name);
          const isEditing = editingAttribute === field.name;

          return html`
            <div key=${field.name}>
              <label class="block text-xs text-slate-500 mb-1" title=${field.description || ""}>${field.name}</label>
              ${isEditing
                ? html`
                    <div class="flex items-center gap-2">
                      <input
                        ref=${attributeInputRef}
                        type=${field.type === "url" ? "url" : "text"}
                        class="flex-1 bg-slate-800 border border-slate-600 rounded px-2 py-1 text-sm focus:outline-none focus:border-blue-500"
                        value=${editedAttributeValue}
                        onInput=${(e) => setEditedAttributeValue(e.target.value)}
                        onKeyDown=${handleAttributeKeyDown}
                        onBlur=${() => { setTimeout(() => { if (editingAttribute === field.name && !isSavingAttribute) setEditingAttribute(null); }, 150); }}
                        disabled=${isSavingAttribute}
                        placeholder=${field.description ? field.description : field.type === "url" ? "https://..." : "Enter value..."}
                      />
                      <button class="p-1 hover:bg-slate-700 rounded transition-colors text-green-400" onClick=${handleSaveAttribute} title="Save" disabled=${isSavingAttribute}>
                        <${CheckIcon} className="w-4 h-4" />
                      </button>
                    </div>
                  `
                : html`
                    <div class="flex items-center gap-2 group">
                      ${field.type === "url" && value
                        ? html`<a href=${value} target="_blank" rel="noopener noreferrer" class="flex-1 text-sm text-blue-400 hover:underline truncate" title=${value}>${value}</a>`
                        : (() => {
                            if (value && looksLikeFilePath(value)) {
                              const apiPrefix = getAPIPrefix();
                              const workspaceUUID = window.mittoCurrentWorkspaceUUID || "";
                              const wsPath = window.mittoCurrentWorkspace || "";
                              const relativePath = value.replace(/^\.\//, "");
                              let viewerUrl = null;
                              if (workspaceUUID) {
                                viewerUrl = `${apiPrefix}/viewer.html?ws=${encodeURIComponent(workspaceUUID)}&path=${encodeURIComponent(relativePath)}`;
                                if (wsPath) viewerUrl += `&ws_path=${encodeURIComponent(wsPath)}`;
                              }
                              const href = viewerUrl || "#";
                              return html`
                                <a
                                  href=${href}
                                  class="file-link flex-1 text-sm text-blue-400 hover:underline truncate"
                                  title=${value}
                                  onClick=${(e) => {
                                    e.preventDefault();
                                    e.stopPropagation();
                                    if (!viewerUrl) return;
                                    if (isNativeApp() && typeof window.mittoOpenViewer === "function") {
                                      const fullUrl = new URL(viewerUrl, window.location.origin).href;
                                      window.mittoOpenViewer(fullUrl);
                                    } else {
                                      window.open(viewerUrl, "_blank", "noopener,noreferrer");
                                    }
                                  }}
                                >${value}</a>
                              `;
                            }
                            return html`<span class="flex-1 text-sm truncate ${!value ? "text-slate-500 italic" : ""}" title=${value}>${value || "Not set"}</span>`;
                          })()}
                      <button class="p-1 hover:bg-slate-700 rounded transition-colors opacity-0 group-hover:opacity-100" onClick=${() => handleStartEditAttribute({ name: field.name, value })} title="Edit">
                        <${EditIcon} className="w-4 h-4" />
                      </button>
                    </div>
                  `}
            </div>
          `;
        })}
      </div>
    `;
  }

}
