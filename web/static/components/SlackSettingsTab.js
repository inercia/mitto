// Settings > Slack integration catalog manager.
const { useEffect, useMemo, useRef, useState, html } = window.preact;

import { getSdkClient } from "../utils/sdkClient.js";
import { openExternalURL } from "../utils/native.js";
import { notifySlackIntegrationsUpdated } from "../utils/slackEvents.js";
import { copyToClipboard } from "../lib.js";
import { ConfirmDialog } from "./ConfirmDialog.js";
import { Tooltip } from "./Tooltip.js";
import {
  CheckIcon,
  CloseIcon,
  CopyIcon,
  EditIcon,
  LinkIcon,
  PlusIcon,
  RefreshIcon,
  SpinnerIcon,
  TrashIcon,
} from "./Icons.js";

export const SLACK_APPS_URL = "https://api.slack.com/apps";
export const SLACK_CREATE_APP_URL = "https://api.slack.com/apps?new_app=1";
export const SLACK_SETUP_URL =
  "https://github.com/inercia/mitto/blob/main/docs/devel/slack-bridge.md#slack-app-setup";

// Slack app manifest for "Create from an app manifest" (mitto-kqpl). Fields
// mirror docs/devel/slack-bridge.md#slack-app-setup exactly: Socket Mode plus
// bot and delegated-user scopes/events. Bot mention mode is optional but
// included so one manifest covers message and mention routing. User scopes
// pre-authorize delegated operation without implying backend support is active.
// The app-level token (connections:write) is not part of the manifest schema
// and must still be generated manually from the app's Basic Information page.
export const SLACK_APP_MANIFEST_YAML = `display_information:
  name: Mitto
features:
  bot_user:
    display_name: Mitto
    always_online: false
oauth_config:
  scopes:
    bot:
      - channels:read
      - channels:history
      - groups:read
      - groups:history
      - app_mentions:read
      - users:read
    user:
      - channels:read
      - channels:history
      - groups:read
      - groups:history
settings:
  event_subscriptions:
    bot_events:
      - message.channels
      - message.groups
      - app_mention
    user_events:
      - message.channels
      - message.groups
  socket_mode_enabled: true
`;

export function slackAppManifestYAML(redirectURI = "") {
  if (!redirectURI) return SLACK_APP_MANIFEST_YAML;
  return SLACK_APP_MANIFEST_YAML.replace(
    "oauth_config:\n",
    `oauth_config:\n  redirect_urls:\n    - ${JSON.stringify(redirectURI)}\n`,
  );
}

export function slackAppSettingsURL(slackAppId) {
  const id = String(slackAppId || "").trim();
  return id ? `${SLACK_APPS_URL}/${encodeURIComponent(id)}` : SLACK_APPS_URL;
}

export function formatSlackValidation(value) {
  if (!value) return "Never";
  const date = new Date(value);
  if (!Number.isFinite(date.getTime()) || date.getUTCFullYear() <= 1)
    return "Never";
  return date.toLocaleString();
}

// Compact relative-time label for "Last checked" metadata (2s ago / 2m ago /
// 2h ago / 2d ago). Pure and deterministic given `now`, so tests don't race
// the wall clock. Mirrors formatSlackValidation's "Never" handling for
// unset/zero-value timestamps; a timestamp at or after `now` (clock skew,
// or `now` not advanced yet) reads as "just now" rather than a negative
// duration.
export function formatSlackRelativeTime(value, now = new Date()) {
  if (!value) return "Never";
  const date = new Date(value);
  if (!Number.isFinite(date.getTime()) || date.getUTCFullYear() <= 1)
    return "Never";
  const reference = now instanceof Date ? now : new Date(now);
  if (!Number.isFinite(reference.getTime())) return "Never";
  const diffMs = reference.getTime() - date.getTime();
  if (diffMs <= 0) return "just now";
  const diffSec = Math.floor(diffMs / 1000);
  if (diffSec < 60) return `${diffSec}s ago`;
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHour = Math.floor(diffMin / 60);
  if (diffHour < 24) return `${diffHour}h ago`;
  const diffDay = Math.floor(diffHour / 24);
  return `${diffDay}d ago`;
}

export function slackHealth(record, attempt = "") {
  if (attempt === "failed")
    return { label: "Validation failed", className: "badge-error" };
  if (!record?.token_configured)
    return { label: "Not configured", className: "badge-warning" };
  if (formatSlackValidation(record.validated_at) !== "Never")
    return { label: "Connected", className: "badge-success" };
  return { label: "Configured", className: "badge-info" };
}

export function slackCredentialKind(record) {
  return record?.credential_kind === "user"
    ? { label: "Delegated user", className: "badge-info" }
    : { label: "Bot", className: "badge-primary" };
}

function isAbortError(error) {
  return error?.name === "AbortError" || error?.cause?.name === "AbortError";
}

function replaceById(items, value) {
  return items.map((item) => (item.id === value.id ? value : item));
}

function referencesOf(preview) {
  return Array.isArray(preview?.references) ? preview.references : [];
}

function StatusSummary({ record, attempt, identityLabel, identityValue }) {
  const health = slackHealth(record, attempt);
  return html`
    <div class="flex flex-wrap items-center gap-2 text-sm">
      <span class="badge badge-sm badge-soft ${health.className}"
        >${health.label}</span
      >
      ${identityValue &&
      html`<span class="text-mitto-text-muted"
        >${identityLabel}: <code>${identityValue}</code></span
      >`}
      <span class="text-mitto-text-muted">
        Last validated: ${formatSlackValidation(record?.validated_at)}
      </span>
    </div>
  `;
}

export function SlackSettingsTab({ showToast, client: clientOverride }) {
  const client = clientOverride || getSdkClient();
  const [apps, setApps] = useState([]);
  const [selectedAppId, setSelectedAppId] = useState("");
  const [installations, setInstallations] = useState([]);
  const [selectedInstallationId, setSelectedInstallationId] = useState("");
  const [loadingApps, setLoadingApps] = useState(true);
  const [loadingInstallations, setLoadingInstallations] = useState(false);
  const [loadError, setLoadError] = useState("");
  const [actionError, setActionError] = useState("");
  const [busy, setBusy] = useState("");
  const [refreshApps, setRefreshApps] = useState(0);
  const [environmentStatus, setEnvironmentStatus] = useState(null);
  const [environmentImportOpen, setEnvironmentImportOpen] = useState(false);
  const [importAppMode, setImportAppMode] = useState("create");
  const [importInstallationMode, setImportInstallationMode] =
    useState("create");
  const [importAppName, setImportAppName] = useState("Imported Slack app");
  const [importInstallationName, setImportInstallationName] = useState(
    "Imported Slack workspace",
  );

  const [showNewApp, setShowNewApp] = useState(false);
  const [newAppName, setNewAppName] = useState("");
  const [newAppToken, setNewAppToken] = useState("");
  const [appName, setAppName] = useState("");
  const [isEditingAppName, setIsEditingAppName] = useState(false);
  const [appToken, setAppToken] = useState("");
  const [oauthConfig, setOAuthConfig] = useState(null);
  const [oauthClientId, setOAuthClientId] = useState("");
  const [oauthClientSecret, setOAuthClientSecret] = useState("");
  const [oauthFlowId, setOAuthFlowId] = useState("");
  const [appValidation, setAppValidation] = useState("");

  const [showNewInstallation, setShowNewInstallation] = useState(false);
  const [newInstallationName, setNewInstallationName] = useState("");
  const [newTeamId, setNewTeamId] = useState("");
  const [newBotToken, setNewBotToken] = useState("");
  const [installationName, setInstallationName] = useState("");
  const [isEditingInstallationName, setIsEditingInstallationName] =
    useState(false);
  const [botToken, setBotToken] = useState("");
  const [installationValidation, setInstallationValidation] = useState("");
  const [deletePlan, setDeletePlan] = useState(null);
  const [manifestCopied, setManifestCopied] = useState(false);
  const selectedAppIdRef = useRef(selectedAppId);
  const selectedInstallationIdRef = useRef(selectedInstallationId);
  const appNameInputRef = useRef(null);
  const installationNameInputRef = useRef(null);
  selectedAppIdRef.current = selectedAppId;
  selectedInstallationIdRef.current = selectedInstallationId;

  const selectedApp = useMemo(
    () => apps.find((app) => app.id === selectedAppId) || null,
    [apps, selectedAppId],
  );
  const selectedInstallation = useMemo(
    () =>
      installations.find((item) => item.id === selectedInstallationId) || null,
    [installations, selectedInstallationId],
  );
  const selectedAppHealth = slackHealth(selectedApp, appValidation);
  const selectedCredentialKind = slackCredentialKind(selectedInstallation);

  const notify = (message, style = "success") => {
    notifySlackIntegrationsUpdated();
    showToast?.({ message, style });
  };

  useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;
    setLoadingApps(true);
    setLoadError("");
    client.slack
      .listApps({ signal: controller.signal })
      .then((data) => {
        if (cancelled) return;
        const next = Array.isArray(data?.apps) ? data.apps : [];
        setApps(next);
        setSelectedAppId((current) =>
          next.some((app) => app.id === current) ? current : next[0]?.id || "",
        );
      })
      .catch((error) => {
        if (!cancelled && !isAbortError(error))
          setLoadError("Slack integrations could not be loaded.");
      })
      .finally(() => {
        if (!cancelled) setLoadingApps(false);
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [refreshApps, client]);

  const loadEnvironmentStatus = async (signal) => {
    try {
      const status = await client.slack.environmentStatus({ signal });
      setEnvironmentStatus(status);
    } catch (error) {
      if (!isAbortError(error)) setEnvironmentStatus(null);
    }
  };

  useEffect(() => {
    const controller = new AbortController();
    loadEnvironmentStatus(controller.signal);
    return () => controller.abort();
  }, [client]);

  useEffect(() => {
    if (typeof client.slack.oauthConfig !== "function") return;
    let cancelled = false;
    client.slack
      .oauthConfig()
      .then((config) => {
        if (!cancelled) setOAuthConfig(config);
      })
      .catch(() => {
        if (!cancelled)
          setOAuthConfig({
            available: false,
            message: "Slack OAuth configuration could not be loaded.",
          });
      });
    return () => {
      cancelled = true;
    };
  }, [client]);

  useEffect(() => {
    setAppName(selectedApp?.name || "");
    setIsEditingAppName(false);
    setAppToken("");
    setOAuthClientId(selectedApp?.oauth_client_id || "");
    setOAuthClientSecret("");
    setAppValidation("");
    setActionError("");
    setShowNewInstallation(false);
    setNewBotToken("");
    setSelectedInstallationId("");
    setInstallations([]);
    if (!selectedAppId) return;

    const controller = new AbortController();
    let cancelled = false;
    setLoadingInstallations(true);
    client.slack
      .listInstallations(selectedAppId, { signal: controller.signal })
      .then((data) => {
        if (cancelled) return;
        const next = Array.isArray(data?.installations)
          ? data.installations
          : [];
        setInstallations(next);
        setSelectedInstallationId(next[0]?.id || "");
      })
      .catch((error) => {
        if (!cancelled && !isAbortError(error))
          setActionError("Slack workspace installations could not be loaded.");
      })
      .finally(() => {
        if (!cancelled) setLoadingInstallations(false);
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [selectedAppId, client]);

  useEffect(() => {
    if (!oauthFlowId || typeof client.slack.oauthFlowStatus !== "function")
      return;
    let cancelled = false;
    const poll = async () => {
      try {
        const status = await client.slack.oauthFlowStatus(oauthFlowId);
        if (cancelled || status?.status === "pending") return;
        setOAuthFlowId("");
        if (status?.status === "succeeded") {
          const data = await client.slack.listInstallations(selectedAppId);
          if (cancelled) return;
          const next = Array.isArray(data?.installations)
            ? data.installations
            : [];
          setInstallations(next);
          setSelectedInstallationId(
            status.installation_id || next[0]?.id || "",
          );
          notify("Slack delegated-user authorization completed.");
        } else {
          setActionError(
            status?.message ||
              "Slack authorization was not completed. Start again.",
          );
        }
      } catch {
        if (!cancelled)
          setActionError("Slack authorization status could not be checked.");
      }
    };
    poll();
    const timer = setInterval(poll, 1000);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [oauthFlowId, selectedAppId, client]);

  useEffect(() => {
    setInstallationName(selectedInstallation?.name || "");
    setIsEditingInstallationName(false);
    setBotToken("");
    setInstallationValidation("");
    setActionError("");
  }, [selectedInstallationId]);

  useEffect(() => {
    if (!isEditingInstallationName || !installationNameInputRef.current) return;
    installationNameInputRef.current.focus();
    installationNameInputRef.current.select();
  }, [isEditingInstallationName]);

  useEffect(() => {
    if (!isEditingAppName || !appNameInputRef.current) return;
    appNameInputRef.current.focus();
    appNameInputRef.current.select();
  }, [isEditingAppName]);

  const chooseApp = (id) => {
    setAppToken("");
    setNewBotToken("");
    setBotToken("");
    selectedAppIdRef.current = id;
    setSelectedAppId(id);
  };

  const chooseInstallation = (id) => {
    setBotToken("");
    selectedInstallationIdRef.current = id;
    setSelectedInstallationId(id);
  };

  const openURL = (url, fallback = SLACK_APPS_URL) => {
    try {
      openExternalURL(url);
    } catch {
      if (url !== fallback) openExternalURL(fallback);
    }
  };

  const copyManifest = async () => {
    const ok = await copyToClipboard(
      slackAppManifestYAML(oauthConfig?.redirect_uri),
    );
    if (ok) {
      setManifestCopied(true);
      setTimeout(() => setManifestCopied(false), 1500);
    }
    showToast?.({
      style: ok ? "success" : "error",
      title: ok
        ? "Slack app manifest copied to clipboard."
        : "Slack app manifest could not be copied.",
      duration: ok ? 3000 : 4000,
    });
  };

  const configureOAuthClient = async () => {
    if (!selectedApp?.id || !oauthClientId.trim()) return;
    setBusy("configure-oauth");
    setActionError("");
    try {
      const updated = await client.slack.configureOAuthClient(selectedApp.id, {
        client_id: oauthClientId.trim(),
        client_secret: oauthClientSecret,
      });
      setApps((current) => replaceById(current, updated));
      setOAuthClientSecret("");
      notify("Slack OAuth client configured.");
    } catch {
      setActionError("Slack OAuth client could not be configured.");
    } finally {
      setBusy("");
    }
  };

  const startOAuth = async (installationId = "") => {
    if (!selectedApp?.id || !oauthConfig?.available || oauthFlowId) return;
    if (!installationId && !newInstallationName.trim()) return;
    setBusy("start-oauth");
    setActionError("");
    try {
      const start = installationId
        ? await client.slack.startOAuthReplacement(installationId)
        : await client.slack.startOAuthInstallation(selectedApp.id, {
            name: newInstallationName.trim(),
            team_id: newTeamId.trim(),
          });
      setOAuthFlowId(start.flow_id);
      openURL(start.authorization_url);
    } catch {
      setActionError(
        "Slack authorization could not be started. Check the OAuth client and redirect URL.",
      );
    } finally {
      setBusy("");
    }
  };

  const createApp = async (event) => {
    event.preventDefault();
    if (!newAppName.trim() || !newAppToken) return;
    setBusy("create-app");
    setActionError("");
    try {
      const created = await client.slack.createApp({
        name: newAppName.trim(),
        app_token: newAppToken,
      });
      setNewAppToken("");
      setNewAppName("");
      setShowNewApp(false);
      setApps((current) => [...current, created]);
      setSelectedAppId(created.id);
      notify("Slack app profile created.");
    } catch {
      setActionError("Slack app profile could not be created.");
    } finally {
      setBusy("");
    }
  };

  const renameApp = async () => {
    const id = selectedApp?.id;
    const name = appName.trim();
    if (!id || !name || busy === "rename-app") return;
    if (name === selectedApp.name) {
      setIsEditingAppName(false);
      return;
    }
    setBusy("rename-app");
    setActionError("");
    try {
      const updated = await client.slack.renameApp(id, name);
      setApps((current) => replaceById(current, updated));
      if (selectedAppIdRef.current === id) {
        setAppName(updated.name);
        setIsEditingAppName(false);
      }
      notify("Slack app profile renamed.");
    } catch {
      if (selectedAppIdRef.current === id)
        setActionError("Slack app profile could not be renamed.");
    } finally {
      setBusy("");
    }
  };

  const cancelAppRename = () => {
    setAppName(selectedApp?.name || "");
    setIsEditingAppName(false);
  };

  const handleAppNameKeyDown = (event) => {
    if (event.key === "Enter") {
      event.preventDefault();
      renameApp();
    } else if (event.key === "Escape") {
      event.preventDefault();
      cancelAppRename();
    }
  };

  const replaceAppToken = async () => {
    const id = selectedApp?.id;
    if (!id || !appToken) return;
    setBusy("replace-app-token");
    setActionError("");
    try {
      const updated = await client.slack.replaceAppToken(id, appToken);
      setApps((current) => replaceById(current, updated));
      if (selectedAppIdRef.current === id) {
        setAppToken("");
        setAppValidation("success");
      }
      notify("Slack app token replaced and validated.");
    } catch {
      if (selectedAppIdRef.current === id) {
        setAppValidation("failed");
        setActionError(
          "The replacement app token was rejected; the configured credential was not changed.",
        );
      }
    } finally {
      setBusy("");
    }
  };

  const validateApp = async () => {
    const id = selectedApp?.id;
    if (!id) return;
    setBusy("validate-app");
    setActionError("");
    try {
      const updated = await client.slack.validateApp(id);
      setApps((current) => replaceById(current, updated));
      if (selectedAppIdRef.current === id) setAppValidation("success");
      notify("Slack app connection is healthy.");
    } catch {
      if (selectedAppIdRef.current === id) {
        setAppValidation("failed");
        setActionError("Slack app connection validation failed.");
      }
    } finally {
      setBusy("");
    }
  };

  const prepareDeleteApp = async () => {
    if (!selectedApp) return;
    const target = selectedApp;
    setBusy("prepare-delete-app");
    setActionError("");
    try {
      const preview = await client.slack.prepareDeleteApp(target.id);
      if (selectedAppIdRef.current === target.id)
        setDeletePlan({ kind: "app", target, preview });
    } catch {
      if (selectedAppIdRef.current === target.id)
        setActionError("Slack app deletion could not be prepared.");
    } finally {
      setBusy("");
    }
  };

  const createInstallation = async (event) => {
    event.preventDefault();
    if (!selectedApp || !newInstallationName.trim() || !newBotToken) return;
    const appId = selectedApp.id;
    setBusy("create-installation");
    setActionError("");
    try {
      const created = await client.slack.createInstallation(appId, {
        name: newInstallationName.trim(),
        team_id: newTeamId.trim(),
        token: newBotToken,
      });
      if (selectedAppIdRef.current === appId) {
        setNewBotToken("");
        setNewInstallationName("");
        setNewTeamId("");
        setShowNewInstallation(false);
        setInstallations((current) => [...current, created]);
        selectedInstallationIdRef.current = created.id;
        setSelectedInstallationId(created.id);
      }
      notify("Slack workspace installation created.");
    } catch (error) {
      if (selectedAppIdRef.current === appId)
        setActionError(
          error?.message ||
            "Slack workspace installation could not be created.",
        );
    } finally {
      setBusy("");
    }
  };

  const renameInstallation = async () => {
    const id = selectedInstallation?.id;
    const name = installationName.trim();
    if (!id || !name || busy === "rename-installation") return;
    if (name === selectedInstallation.name) {
      setIsEditingInstallationName(false);
      return;
    }
    setBusy("rename-installation");
    setActionError("");
    try {
      const updated = await client.slack.renameInstallation(id, name);
      setInstallations((current) => replaceById(current, updated));
      if (selectedInstallationIdRef.current === id) {
        setInstallationName(updated.name);
        setIsEditingInstallationName(false);
      }
      notify("Slack workspace installation renamed.");
    } catch {
      if (selectedInstallationIdRef.current === id)
        setActionError("Slack workspace installation could not be renamed.");
    } finally {
      setBusy("");
    }
  };

  const cancelInstallationRename = () => {
    setInstallationName(selectedInstallation?.name || "");
    setIsEditingInstallationName(false);
  };

  const handleInstallationNameKeyDown = (event) => {
    if (event.key === "Enter") {
      event.preventDefault();
      renameInstallation();
    } else if (event.key === "Escape") {
      event.preventDefault();
      cancelInstallationRename();
    }
  };

  const replaceBotToken = async () => {
    const id = selectedInstallation?.id;
    if (!id || !botToken) return;
    setBusy("replace-bot-token");
    setActionError("");
    try {
      const updated = await client.slack.replaceInstallationToken(id, botToken);
      setInstallations((current) => replaceById(current, updated));
      if (selectedInstallationIdRef.current === id) {
        setBotToken("");
        setInstallationValidation("success");
      }
      notify("Slack installation credential replaced and validated.");
    } catch (error) {
      if (selectedInstallationIdRef.current === id) {
        setInstallationValidation("failed");
        setActionError(
          error?.message ||
            "The replacement credential was rejected; the configured credential was not changed.",
        );
      }
    } finally {
      setBusy("");
    }
  };

  const validateInstallation = async () => {
    const id = selectedInstallation?.id;
    if (!id) return;
    setBusy("validate-installation");
    setActionError("");
    try {
      const updated = await client.slack.validateInstallation(id);
      setInstallations((current) => replaceById(current, updated));
      if (selectedInstallationIdRef.current === id)
        setInstallationValidation("success");
      notify("Slack workspace connection is healthy.");
    } catch {
      if (selectedInstallationIdRef.current === id) {
        setInstallationValidation("failed");
        setActionError("Slack workspace connection validation failed.");
      }
    } finally {
      setBusy("");
    }
  };

  const prepareDeleteInstallation = async () => {
    if (!selectedInstallation) return;
    const target = selectedInstallation;
    setBusy("prepare-delete-installation");
    setActionError("");
    try {
      const preview = await client.slack.prepareDeleteInstallation(target.id);
      if (selectedInstallationIdRef.current === target.id)
        setDeletePlan({ kind: "installation", target, preview });
    } catch {
      if (selectedInstallationIdRef.current === target.id)
        setActionError("Slack workspace deletion could not be prepared.");
    } finally {
      setBusy("");
    }
  };

  const openEnvironmentImport = () => {
    setImportAppMode(selectedApp ? "selected" : "create");
    setImportInstallationMode(selectedInstallation ? "selected" : "create");
    setEnvironmentImportOpen(true);
  };

  const importEnvironment = async () => {
    if (!environmentStatus?.complete || environmentStatus.shadowed) return;
    setBusy("environment-import");
    setActionError("");
    try {
      await client.slack.importEnvironment({
        app_id: importAppMode === "selected" ? selectedApp?.id || "" : "",
        app_name: importAppMode === "create" ? importAppName.trim() : "",
        installation_id:
          importAppMode === "selected" && importInstallationMode === "selected"
            ? selectedInstallation?.id || ""
            : "",
        installation_name:
          importAppMode === "create" || importInstallationMode === "create"
            ? importInstallationName.trim()
            : "",
      });
      setEnvironmentImportOpen(false);
      setRefreshApps((value) => value + 1);
      await loadEnvironmentStatus();
      notify(
        "Slack environment configuration imported. Remove the deprecated environment variables after restart.",
      );
    } catch {
      setActionError(
        "Slack environment import failed; managed credentials and loop settings were left unchanged.",
      );
    } finally {
      setBusy("");
    }
  };

  const confirmDelete = async () => {
    if (!deletePlan || referencesOf(deletePlan.preview).length > 0) return;
    const { kind, target } = deletePlan;
    setBusy("delete");
    setActionError("");
    try {
      if (kind === "app") {
        await client.slack.deleteApp(target.id);
        setApps((current) => current.filter((app) => app.id !== target.id));
        setSelectedAppId("");
        notify("Slack app profile deleted.");
      } else {
        await client.slack.deleteInstallation(target.id);
        setInstallations((current) =>
          current.filter((item) => item.id !== target.id),
        );
        setSelectedInstallationId("");
        notify("Slack workspace installation deleted.");
      }
      setDeletePlan(null);
      setRefreshApps((value) => value + 1);
    } catch {
      setDeletePlan(null);
      setActionError(
        "Deletion was blocked because the integration is unavailable or now referenced by a loop.",
      );
    } finally {
      setBusy("");
    }
  };

  const removeDeleteReferences = async () => {
    if (!deletePlan || referencesOf(deletePlan.preview).length === 0) return;
    const { kind, target } = deletePlan;
    setBusy("remove-delete-references");
    setActionError("");
    try {
      const result =
        kind === "app"
          ? await client.slack.removeAppReferences(target.id)
          : await client.slack.removeInstallationReferences(target.id);
      setDeletePlan((current) =>
        current?.kind === kind && current?.target?.id === target.id
          ? { ...current, preview: result.preview }
          : current,
      );
      notify("Removed on Slack from the affected conversations.");
    } catch {
      setActionError(
        "On Slack could not be removed from every affected conversation; refreshed the list below.",
      );
      // A partial failure may still have mutated some sessions on disk, so
      // re-fetch the preview instead of leaving stale references displayed.
      try {
        const preview =
          kind === "app"
            ? await client.slack.prepareDeleteApp(target.id)
            : await client.slack.prepareDeleteInstallation(target.id);
        setDeletePlan((current) =>
          current?.kind === kind && current?.target?.id === target.id
            ? { ...current, preview }
            : current,
        );
      } catch {
        // Preview refresh failed too; keep the previous (possibly stale)
        // preview rather than clearing state the user is looking at.
      }
    } finally {
      setBusy("");
    }
  };

  const deleteReferences = referencesOf(deletePlan?.preview);
  const deleteBlocked = deleteReferences.length > 0;
  const cascadingInstallations = Array.isArray(
    deletePlan?.preview?.installation_ids,
  )
    ? deletePlan.preview.installation_ids
    : [];

  if (loadingApps)
    return html`<div class="flex justify-center py-12">
      <${SpinnerIcon} className="w-8 h-8 text-mitto-accent" />
    </div>`;

  return html`
    <div class="space-y-4" data-testid="slack-settings-tab">
      <div>
        <div class="space-y-1">
          <h4 class="font-semibold">Slack integrations</h4>
          <p class="text-sm text-mitto-text-muted">
            A Slack workspace is a Slack team. It is not a Mitto project
            workspace. One Slack app can have several Slack workspace
            installations. Review the
            <a
              href=${SLACK_SETUP_URL}
              target="_blank"
              rel="noopener noreferrer"
              class="link link-primary"
              data-testid="slack-setup-guide"
              onClick=${(event) => {
                event.preventDefault();
                openURL(SLACK_SETUP_URL);
              }}
            >setup and scopes</a> before configuring the integration.
          </p>
        </div>
      </div>

      <section
        class="rounded-lg border border-mitto-border bg-mitto-surface-2"
        data-testid="slack-create-app-guide"
      >
        <div class="flex flex-col p-4 gap-3">
          <div class="flex flex-col gap-2">
            <h5 class="font-semibold text-base">App manifest</h5>
            <p class="text-sm text-mitto-text-muted">
              <a
                href=${SLACK_CREATE_APP_URL}
                target="_blank"
                rel="noopener noreferrer"
                class="link link-primary"
                data-testid="slack-create-app-external"
                onClick=${(event) => {
                  event.preventDefault();
                  openURL(SLACK_CREATE_APP_URL);
                }}
              >Create a Slack app</a>, choose “From an app manifest,” and copy the
              manifest below. Socket Mode also needs an app-level token:
              ${" "}<code>connections:write</code>.
            </p>
            <p class="text-sm text-mitto-text-muted">
              <span class="font-medium text-mitto-text">1. Bot:</span>${" "}Use a
              bot token. Invite the bot to a private channel before selecting that
              channel in a trigger.
            </p>
            <p class="text-sm text-mitto-text-muted">
              <span class="font-medium text-mitto-text"
                >2. User delegation:</span
              >${" "}Configure the OAuth client on the app profile, then authorize a
              delegated user. Manual user tokens remain disabled because they do not
              carry verifiable app provenance.
            </p>
						${
              oauthConfig?.redirect_uri &&
              html`<p class="text-sm text-mitto-text-muted">
                Slack OAuth redirect URL:
                <code>${oauthConfig.redirect_uri}</code>
              </p>`
            }
            <p class="text-sm text-mitto-text-muted">
              Existing app? Apply the current manifest and reauthorize it.
            </p>
          </div>
          <div class="flex flex-wrap gap-2">
            <button
              type="button"
              class="btn btn-sm"
              data-testid="slack-copy-manifest"
              onClick=${copyManifest}
            >
              ${
                manifestCopied
                  ? html`<${CheckIcon} className="w-4 h-4" /> Copied!`
                  : html`<${CopyIcon} className="w-4 h-4" /> Copy manifest`
              }
            </button>
          </div>
        </div>
      </section>

			${
        environmentStatus?.present &&
        html`
          <section
            class="card card-border border-warning bg-warning/10"
            data-testid="slack-environment-import"
          >
            <div class="card-body gap-3 p-4">
              <div class="flex flex-wrap items-start justify-between gap-3">
                <div>
                  <h5 class="card-title text-base">
                    Deprecated environment configuration
                  </h5>
                  <p class="text-sm text-mitto-text-muted">
                    Import into the credential vault and a managed onSlack
                    subscription. No token values are sent to this page.
                  </p>
                </div>
                <button
                  type="button"
                  class="btn btn-sm btn-warning"
                  disabled=${!environmentStatus.complete ||
                  environmentStatus.shadowed}
                  onClick=${openEnvironmentImport}
                  data-testid="slack-import-environment"
                >
                  ${environmentStatus.shadowed
                    ? "Already managed"
                    : "Import from environment"}
                </button>
              </div>
              <div class="flex flex-wrap gap-2 text-xs">
                ${environmentStatus.team_id &&
                html`<span class="badge badge-outline"
                  >Team ${environmentStatus.team_id}</span
                >`}
                ${environmentStatus.channel_id &&
                html`<span class="badge badge-outline"
                  >Channel ${environmentStatus.channel_id}</span
                >`}
                ${environmentStatus.target_session_id &&
                html`<span class="badge badge-outline"
                  >Conversation ${environmentStatus.target_session_id}</span
                >`}
                ${environmentStatus.active &&
                html`<span class="badge badge-warning"
                  >Legacy listener active</span
                >`}
              </div>
              ${!environmentStatus.complete &&
              html`
                <p class="text-sm text-warning-content">
                  Missing:
                  ${(environmentStatus.missing_variables || []).join(", ")}
                </p>
              `}
            </div>
          </section>
        `
      }
      ${
        loadError &&
        html`<div role="alert" class="alert alert-error alert-soft text-sm">
          <span>${loadError}</span>
          <button
            class="btn btn-sm"
            onClick=${() => setRefreshApps((value) => value + 1)}
          >
            Retry
          </button>
        </div>`
      }
      ${
        actionError &&
        html`<div
          role="alert"
          class="alert alert-error alert-soft text-sm"
          data-testid="slack-action-error"
        >
          ${actionError}
        </div>`
      }

      <div
        class="grid grid-cols-1 gap-4 md:grid-cols-3"
        data-testid="slack-catalog-layout"
      >
        <section class="rounded-lg border border-mitto-border bg-mitto-surface-2">
          <div class="flex flex-col p-3 gap-3">
            <div class="flex items-center justify-between gap-2">
              <h5 class="font-semibold text-sm">Apps</h5>
              <button
                type="button"
                class="btn btn-sm btn-ghost"
                data-testid="slack-add-app-profile"
                aria-label="Add Slack app profile"
                title="Add Slack app profile"
                onClick=${() => setShowNewApp((value) => !value)}
              >
                <${PlusIcon} className="w-4 h-4" /> Add
              </button>
            </div>
            ${
              showNewApp &&
              html`<form
                class="rounded-lg border border-mitto-border bg-base-200"
                data-testid="slack-new-app-form"
                onSubmit=${createApp}
              >
                <div class="flex flex-col p-3 gap-2">
                  <fieldset class="fieldset">
                    <legend class="fieldset-legend">Friendly name</legend>
                    <input
                      class="input input-sm w-full"
                      value=${newAppName}
                      onInput=${(event) => setNewAppName(event.target.value)}
                      required
                    />
                  </fieldset>
                  <fieldset class="fieldset">
                    <legend class="fieldset-legend">App token</legend>
                    <input
                      type="password"
                      autocomplete="off"
                      class="input input-sm w-full"
                      placeholder="xapp-…"
                      value=${newAppToken}
                      onInput=${(event) => setNewAppToken(event.target.value)}
                      required
                    />
                    <p class="label">Write-only; never returned by Mitto.</p>
                  </fieldset>
                  <button
                    class="btn btn-sm btn-primary"
                    disabled=${busy === "create-app"}
                  >
                    Save
                  </button>
                </div>
              </form>`
            }
            ${
              apps.length === 0
                ? html`<div class="text-center py-6 space-y-3">
                    <p class="text-sm text-mitto-text-muted">
                      No app profiles are configured. Use the guide above to
                      create a Slack app, then add its token here.
                    </p>
                  </div>`
                : html`<ul
                    class="menu menu-sm p-0"
                    data-testid="slack-app-list"
                  >
                    ${apps.map((app) => {
                      const health = slackHealth(
                        app,
                        app.id === selectedAppId ? appValidation : "",
                      );
                      return html`<li key=${app.id}>
                        <button
                          type="button"
                          class=${app.id === selectedAppId ? "menu-active" : ""}
                          data-testid=${`slack-app-${app.id}`}
                          onClick=${() => chooseApp(app.id)}
                        >
                          <span class="min-w-0 flex-1 text-left">
                            <span class="block truncate font-medium"
                              >${app.name}</span
                            >
                            <span class="block truncate text-xs opacity-70"
                              >${app.slack_app_id || "Identity pending"}</span
                            >
                          </span>
                          <span
                            class="badge badge-xs badge-soft ${health.className}"
                            >${health.label}</span
                          >
                        </button>
                      </li>`;
                    })}
                  </ul>`
            }
          </div>
        </section>

        <section class="flex-1 min-w-0 space-y-4 md:col-span-2">
          ${
            selectedApp
              ? html`<div
                    class="rounded-lg border border-mitto-border bg-mitto-surface-2"
                  >
                    <div class="flex flex-col p-4 gap-4">
                      <div class="space-y-1">
                        <div
                          class="flex flex-wrap items-center gap-2"
                          data-testid="slack-app-header"
                        >
                          ${isEditingAppName
                            ? html`<div class="flex items-center gap-1">
                                <input
                                  ref=${appNameInputRef}
                                  class="input input-sm min-w-0"
                                  data-testid="slack-app-name-input"
                                  value=${appName}
                                  disabled=${busy === "rename-app"}
                                  onInput=${(event) =>
                                    setAppName(event.target.value)}
                                  onKeyDown=${handleAppNameKeyDown}
                                />
                                <${Tooltip} tip="Save app name">
                                  <button
                                    class="btn btn-xs btn-ghost btn-square"
                                    aria-label="Save app name"
                                    title="Save app name"
                                    disabled=${busy === "rename-app" ||
                                    !appName.trim()}
                                    onClick=${renameApp}
                                  >
                                    ${busy === "rename-app"
                                      ? html`<${SpinnerIcon}
                                          className="w-4 h-4"
                                        />`
                                      : html`<${CheckIcon}
                                          className="w-4 h-4"
                                        />`}
                                  </button>
                                <//>
                                <${Tooltip} tip="Cancel app rename">
                                  <button
                                    class="btn btn-xs btn-ghost btn-square"
                                    aria-label="Cancel app rename"
                                    title="Cancel app rename"
                                    disabled=${busy === "rename-app"}
                                    onClick=${cancelAppRename}
                                  >
                                    <${CloseIcon} className="w-4 h-4" />
                                  </button>
                                <//>
                              </div>`
                            : html`<div class="flex items-center gap-1">
                                <h5 class="font-semibold text-base">
                                  ${selectedApp.name}
                                </h5>
                                <${Tooltip} tip="Rename Slack app profile">
                                  <button
                                    class="btn btn-xs btn-ghost btn-square"
                                    aria-label="Rename Slack app profile"
                                    title="Rename Slack app profile"
                                    onClick=${() => setIsEditingAppName(true)}
                                  >
                                    <${EditIcon} className="w-4 h-4" />
                                  </button>
                                <//>
                              </div>`}
                          <span
                            class="badge badge-sm badge-soft ${selectedAppHealth.className}"
                            >${selectedAppHealth.label}</span
                          >
                          <div class="ml-auto flex items-center gap-1">
                            <${Tooltip} tip="Open app settings in Slack">
                              <button
                                class="btn btn-sm btn-ghost btn-square"
                                data-testid="slack-open-app-settings"
                                aria-label="Open app settings"
                                title="Open app settings"
                                onClick=${() =>
                                  openURL(
                                    slackAppSettingsURL(
                                      selectedApp.slack_app_id,
                                    ),
                                  )}
                              >
                                <${LinkIcon} className="w-4 h-4" />
                              </button>
                            <//>
                            <${Tooltip} tip="Test app connection">
                              <button
                                class="btn btn-sm btn-square"
                                data-testid="slack-validate-app"
                                aria-label="Test app connection"
                                title="Test app connection"
                                disabled=${busy === "validate-app"}
                                onClick=${validateApp}
                              >
                                ${busy === "validate-app"
                                  ? html`<${SpinnerIcon} className="w-4 h-4" />`
                                  : html`<${RefreshIcon}
                                      className="w-4 h-4"
                                    />`}
                              </button>
                            <//>
                            <${Tooltip} tip="Delete Slack app profile">
                              <button
                                class="btn btn-sm btn-square btn-ghost text-error"
                                disabled=${busy === "prepare-delete-app"}
                                onClick=${prepareDeleteApp}
                                aria-label="Delete Slack app profile"
                                title="Delete Slack app profile"
                              >
                                <${TrashIcon} className="w-4 h-4" />
                              </button>
                            <//>
                          </div>
                        </div>
                        <p
                          class="flex flex-wrap items-center gap-x-2 text-xs text-mitto-text-muted"
                          data-testid="slack-app-last-check"
                        >
                          ${selectedApp.slack_app_id &&
                          html`<span
                            >App ID:${" "}
                            <code>${selectedApp.slack_app_id}</code></span
                          >`}
                          ${selectedApp.slack_app_id &&
                          html`<span aria-hidden="true">·</span>`}
                          <span
                            title=${formatSlackValidation(
                              selectedApp.validated_at,
                            )}
                            >Last checked:
                            ${formatSlackRelativeTime(
                              selectedApp.validated_at,
                            )}</span
                          >
                        </p>
                      </div>

                      <div>
                        <fieldset class="fieldset">
                          <legend class="fieldset-legend">
                            Replace app token
                          </legend>
                          <div class="join w-full">
                            <input
                              type="password"
                              autocomplete="off"
                              class="input input-sm join-item flex-1"
                              placeholder="New xapp-… token"
                              value=${appToken}
                              onInput=${(event) =>
                                setAppToken(event.target.value)}
                            />
                            <button
                              class="btn btn-sm join-item"
                              disabled=${busy === "replace-app-token" ||
                              !appToken}
                              onClick=${replaceAppToken}
                            >
                              Replace
                            </button>
                          </div>
                        </fieldset>
                      </div>
                      <div
                        class="rounded-lg border border-mitto-border bg-base-200 p-3 space-y-2"
                        data-testid="slack-oauth-client-config"
                      >
                        <div>
                          <h6 class="font-medium">Delegated-user OAuth</h6>
                          <p class="text-xs text-mitto-text-muted">
                            Only required for delegated-user authorization—not
                            bot-token installations. The client secret is
                            write-only and stored in the credential vault.
                          </p>
                        </div>
                        ${oauthConfig &&
                        !oauthConfig.available &&
                        html`<div
                          role="alert"
                          class="alert alert-warning alert-soft text-sm"
                        >
                          ${oauthConfig.message}
                        </div>`}
                        <div class="grid grid-cols-1 gap-2 md:grid-cols-2">
                          <fieldset class="fieldset">
                            <legend class="fieldset-legend">Client ID</legend>
                            <input
                              class="input input-sm w-full"
                              value=${oauthClientId}
                              onInput=${(event) =>
                                setOAuthClientId(event.target.value)}
                            />
                          </fieldset>
                          <fieldset class="fieldset">
                            <legend class="fieldset-legend">
                              Client secret
                            </legend>
                            <input
                              type="password"
                              autocomplete="off"
                              class="input input-sm w-full"
                              placeholder=${selectedApp.oauth_client_secret_configured
                                ? "Configured — leave blank to keep"
                                : "Required for first setup"}
                              value=${oauthClientSecret}
                              onInput=${(event) =>
                                setOAuthClientSecret(event.target.value)}
                            />
                          </fieldset>
                        </div>
                        <button
                          type="button"
                          class="btn btn-sm"
                          disabled=${busy === "configure-oauth" ||
                          !oauthClientId.trim()}
                          onClick=${configureOAuthClient}
                        >
                          Save OAuth client
                        </button>
                      </div>
                    </div>
                  </div>

                  <div
                    class="rounded-lg border border-mitto-border bg-mitto-surface-2"
                  >
                    <div class="flex flex-col p-4 gap-3">
                      <div
                        class="flex flex-wrap items-center justify-between gap-2"
                      >
                        <div>
                          <h5 class="font-semibold text-base">Workspaces</h5>
                          <p class="text-xs text-mitto-text-muted">
                            Installations of this app in workspaces.
                          </p>
                        </div>
                        <button
                          class="btn btn-sm btn-ghost"
                          data-testid="slack-add-installation"
                          aria-label="Add Slack workspace"
                          title="Add Slack workspace"
                          onClick=${() =>
                            setShowNewInstallation((value) => !value)}
                        >
                          <${PlusIcon} className="w-4 h-4" /> Add
                        </button>
                      </div>

                      ${showNewInstallation &&
                      html`<form
                        class="rounded-lg border border-mitto-border bg-base-200"
                        data-testid="slack-new-installation-form"
                        onSubmit=${createInstallation}
                      >
                        <div class="flex flex-col p-3 gap-2">
                          <div class="grid grid-cols-1 gap-2 md:grid-cols-2">
                            <fieldset class="fieldset">
                              <legend class="fieldset-legend">
                                Friendly name
                              </legend>
                              <input
                                class="input input-sm w-full"
                                value=${newInstallationName}
                                onInput=${(event) =>
                                  setNewInstallationName(event.target.value)}
                                required
                              />
                            </fieldset>
                            <fieldset class="fieldset">
                              <legend class="fieldset-legend">
                                Team ID (optional)
                              </legend>
                              <input
                                class="input input-sm w-full"
                                placeholder="T…"
                                value=${newTeamId}
                                onInput=${(event) =>
                                  setNewTeamId(event.target.value)}
                              />
                            </fieldset>
                          </div>
                          <fieldset class="fieldset">
                            <legend class="fieldset-legend">
                              Installation credential
                            </legend>
                            <input
                              type="password"
                              autocomplete="off"
                              class="input input-sm w-full"
                              placeholder="Bot token"
                              value=${newBotToken}
                              onInput=${(event) =>
                                setNewBotToken(event.target.value)}
                            />
                          </fieldset>
                          <div class="flex flex-wrap gap-2">
                            <button
                              class="btn btn-sm"
                              disabled=${busy === "create-installation" ||
                              !newBotToken}
                            >
                              Save
                            </button>
                            <button
                              type="button"
                              class="btn btn-sm btn-primary"
                              disabled=${busy === "start-oauth" ||
                              !!oauthFlowId ||
                              !oauthConfig?.available ||
                              !selectedApp.oauth_client_secret_configured ||
                              !newInstallationName.trim()}
                              onClick=${() => startOAuth()}
                            >
                              Authorize delegated user
                            </button>
                          </div>
                        </div>
                      </form>`}
                      ${loadingInstallations
                        ? html`<div class="flex justify-center py-6">
                            <${SpinnerIcon}
                              className="w-6 h-6 text-mitto-accent"
                            />
                          </div>`
                        : installations.length === 0
                          ? html`<p class="text-sm text-mitto-text-muted py-3">
                              This app has no workspace installations.
                            </p>`
                          : installations.length > 1
                            ? html`<ul
                                class="menu flex-wrap p-0"
                                data-testid="slack-installation-list"
                              >
                                ${installations.map(
                                  (item) =>
                                    html`<li key=${item.id}>
                                      <button
                                        class=${item.id ===
                                        selectedInstallationId
                                          ? "menu-active"
                                          : ""}
                                        data-testid=${`slack-installation-${item.id}`}
                                        onClick=${() =>
                                          chooseInstallation(item.id)}
                                      >
                                        ${item.name}
                                      </button>
                                    </li>`,
                                )}
                              </ul>`
                            : null}
                      ${selectedInstallation &&
                      html`<div
                        class="rounded-lg border border-mitto-border bg-base-200"
                        data-testid="slack-installation-detail"
                      >
                        <div class="flex flex-col p-3 gap-3">
                          <div
                            class="flex flex-wrap items-start justify-between gap-2"
                          >
                            <div class="min-w-0 flex-1">
                              ${isEditingInstallationName
                                ? html`<div class="flex items-center gap-1">
                                    <input
                                      ref=${installationNameInputRef}
                                      class="input input-sm min-w-0 flex-1"
                                      data-testid="slack-installation-name-input"
                                      value=${installationName}
                                      disabled=${busy === "rename-installation"}
                                      onInput=${(event) =>
                                        setInstallationName(event.target.value)}
                                      onKeyDown=${handleInstallationNameKeyDown}
                                    />
                                    <${Tooltip} tip="Save workspace name">
                                      <button
                                        class="btn btn-xs btn-ghost btn-square"
                                        aria-label="Save workspace name"
                                        title="Save workspace name"
                                        disabled=${busy ===
                                          "rename-installation" ||
                                        !installationName.trim()}
                                        onClick=${renameInstallation}
                                      >
                                        ${busy === "rename-installation"
                                          ? html`<${SpinnerIcon}
                                              className="w-4 h-4"
                                            />`
                                          : html`<${CheckIcon}
                                              className="w-4 h-4"
                                            />`}
                                      </button>
                                    <//>
                                    <${Tooltip} tip="Cancel workspace rename">
                                      <button
                                        class="btn btn-xs btn-ghost btn-square"
                                        aria-label="Cancel workspace rename"
                                        title="Cancel workspace rename"
                                        disabled=${busy ===
                                        "rename-installation"}
                                        onClick=${cancelInstallationRename}
                                      >
                                        <${CloseIcon} className="w-4 h-4" />
                                      </button>
                                    <//>
                                  </div>`
                                : html`<div
                                    class="flex items-center gap-1 group"
                                  >
                                    <h6 class="font-medium truncate">
                                      ${selectedInstallation.name}
                                    </h6>
                                    <${Tooltip} tip="Rename Slack workspace">
                                      <button
                                        class="btn btn-xs btn-ghost btn-square"
                                        aria-label="Rename Slack workspace"
                                        title="Rename Slack workspace"
                                        onClick=${() =>
                                          setIsEditingInstallationName(true)}
                                      >
                                        <${EditIcon} className="w-4 h-4" />
                                      </button>
                                    <//>
                                  </div>`}
                              <${StatusSummary}
                                record=${selectedInstallation}
                                attempt=${installationValidation}
                                identityLabel="Team ID"
                                identityValue=${selectedInstallation.team_id}
                              />
                              <span
                                class="badge badge-sm badge-soft ${selectedCredentialKind.className}"
                                data-testid="slack-credential-kind"
                                >${selectedCredentialKind.label}</span
                              >
                              ${selectedInstallation.team_name &&
                              html`<p class="text-xs text-mitto-text-muted">
                                Slack team: ${selectedInstallation.team_name} ·
                                ${selectedInstallation.credential_kind ===
                                "user"
                                  ? `User: ${selectedInstallation.user_id || "Unknown"}`
                                  : `Bot: ${selectedInstallation.bot_id || "Unknown"}`}
                              </p>`}
                            </div>
                            <div class="flex gap-2">
                              <${Tooltip} tip="Test workspace connection">
                                <button
                                  class="btn btn-sm btn-square"
                                  data-testid="slack-validate-installation"
                                  aria-label="Test workspace connection"
                                  title="Test workspace connection"
                                  disabled=${busy === "validate-installation"}
                                  onClick=${validateInstallation}
                                >
                                  ${busy === "validate-installation"
                                    ? html`<${SpinnerIcon}
                                        className="w-4 h-4"
                                      />`
                                    : html`<${RefreshIcon}
                                        className="w-4 h-4"
                                      />`}
                                </button>
                              <//>
                              <${Tooltip}
                                tip="Delete Slack workspace installation"
                              >
                                <button
                                  class="btn btn-sm btn-square btn-ghost text-error"
                                  disabled=${busy ===
                                  "prepare-delete-installation"}
                                  onClick=${prepareDeleteInstallation}
                                  aria-label="Delete Slack workspace installation"
                                  title="Delete Slack workspace installation"
                                >
                                  <${TrashIcon} className="w-4 h-4" />
                                </button>
                              <//>
                            </div>
                          </div>
                          <div>
                            <fieldset class="fieldset">
                              <legend class="fieldset-legend">
                                Replace installation credential
                              </legend>
                              <div class="join w-full">
                                <input
                                  type="password"
                                  autocomplete="off"
                                  class="input input-sm join-item flex-1"
                                  placeholder="New bot token"
                                  value=${botToken}
                                  onInput=${(event) =>
                                    setBotToken(event.target.value)}
                                />
                                <button
                                  class="btn btn-sm join-item"
                                  disabled=${busy === "replace-bot-token" ||
                                  !botToken}
                                  onClick=${replaceBotToken}
                                >
                                  Replace
                                </button>
                              </div>
                              <button
                                type="button"
                                class="btn btn-sm mt-2"
                                disabled=${busy === "start-oauth" ||
                                !!oauthFlowId ||
                                !oauthConfig?.available ||
                                !selectedApp.oauth_client_secret_configured}
                                onClick=${() =>
                                  startOAuth(selectedInstallation.id)}
                              >
                                ${oauthFlowId
                                  ? "Waiting for Slack…"
                                  : "Authorize delegated user"}
                              </button>
                            </fieldset>
                          </div>
                        </div>
                      </div>`}
                    </div>
                  </div>`
              : html`<div
                  class="rounded-lg border border-mitto-border bg-mitto-surface-2"
                >
                  <div class="flex flex-col items-center text-center py-12">
                    <p class="text-mitto-text-muted">
                      Select or add an app profile to manage it.
                    </p>
                  </div>
                </div>`
          }
        </section>
      </div>

      <div
        role="alert"
        class="alert alert-info alert-soft text-sm"
        data-testid="slack-credential-storage-note"
      >
        App and installation credentials are write-only and stored in Mitto's credential vault.
        On Linux the vault is an atomic file restricted to mode 0600 inside a
        mode 0700 directory.
      </div>

      <${ConfirmDialog}
			isOpen=${environmentImportOpen}
			title="Import Slack environment configuration"
			message="This validates the environment credentials, stores them in the vault, updates the target loop, and hands off the listener atomically."
			confirmLabel="Import and hand off"
			isLoading=${busy === "environment-import"}
			confirmDisabled=${
        (importAppMode === "create" && !importAppName.trim()) ||
        ((importAppMode === "create" || importInstallationMode === "create") &&
          !importInstallationName.trim())
      }
			onCancel=${() => setEnvironmentImportOpen(false)}
			onConfirm=${importEnvironment}
		>
			<div class="mt-4 space-y-3" data-testid="slack-environment-import-dialog">
				<fieldset class="fieldset">
					<legend class="fieldset-legend">App profile</legend>
					<select class="select select-sm w-full" value=${importAppMode}
						onChange=${(event) => setImportAppMode(event.target.value)}>
						${selectedApp && html`<option value="selected">Use selected: ${selectedApp.name}</option>`}
						<option value="create">Create or match by Slack identity</option>
					</select>
					${
            importAppMode === "create" &&
            html`<input
              class="input input-sm w-full"
              value=${importAppName}
              onInput=${(event) => setImportAppName(event.target.value)}
              aria-label="Imported app name"
            />`
          }
				</fieldset>
				<fieldset class="fieldset">
					<legend class="fieldset-legend">Workspace installation</legend>
					<select class="select select-sm w-full" value=${importAppMode === "create" ? "create" : importInstallationMode}
						disabled=${importAppMode === "create"}
						onChange=${(event) => setImportInstallationMode(event.target.value)}>
						${selectedInstallation && html`<option value="selected">Use selected: ${selectedInstallation.name}</option>`}
						<option value="create">Create or match by Slack team</option>
					</select>
					${
            (importAppMode === "create" ||
              importInstallationMode === "create") &&
            html`
              <input
                class="input input-sm w-full"
                value=${importInstallationName}
                onInput=${(event) =>
                  setImportInstallationName(event.target.value)}
                aria-label="Imported workspace name"
              />
            `
          }
				</fieldset>
			</div>
		</${ConfirmDialog}>

		<${ConfirmDialog}
        isOpen=${!!deletePlan}
        title=${deleteBlocked ? "Integration is in use" : "Delete Slack integration"}
        message=${
          deleteBlocked
            ? "Resolve the loop dependencies below before deleting this Slack integration."
            : deletePlan?.kind === "app" && cascadingInstallations.length > 0
              ? `Deleting this app also deletes ${cascadingInstallations.length} Slack workspace installation(s).`
              : "This removes the selected Slack integration and its stored credential."
        }
        confirmLabel="Delete"
        cancelLabel=${deleteBlocked ? "Close" : "Cancel"}
        confirmVariant="danger"
        confirmDisabled=${deleteBlocked}
        isLoading=${busy === "delete"}
        onCancel=${() => setDeletePlan(null)}
        onConfirm=${confirmDelete}
      >
        ${
          deleteBlocked &&
          html`<div class="mt-3 space-y-3">
            <ul class="list" data-testid="slack-delete-references">
              ${deleteReferences.map(
                (reference) =>
                  html`<li class="list-row" key=${reference.session_id}>
                    <span class="font-medium"
                      >${reference.name || "Untitled conversation"}</span
                    >
                  </li>`,
              )}
            </ul>
            <button
              class="btn btn-error btn-sm"
              disabled=${busy === "remove-delete-references"}
              onClick=${removeDeleteReferences}
              data-testid="slack-remove-delete-references"
            >
              ${busy === "remove-delete-references" &&
              html`<${SpinnerIcon} className="w-4 h-4" />`}
              Remove on Slack
            </button>
          </div>`
        }
      </${ConfirmDialog}>
    </div>
  `;
}
