// Settings > Slack integration catalog manager.
const { useEffect, useMemo, useState, html } = window.preact;

import { getSdkClient } from "../utils/sdkClient.js";
import { openExternalURL } from "../utils/native.js";
import { ConfirmDialog } from "./ConfirmDialog.js";
import { PlusIcon, SpinnerIcon, TrashIcon } from "./Icons.js";

export const SLACK_APPS_URL = "https://api.slack.com/apps";
export const SLACK_CREATE_APP_URL = "https://api.slack.com/apps?new_app=1";
export const SLACK_SETUP_URL =
  "https://github.com/inercia/mitto/blob/main/docs/devel/slack-bridge.md#slack-app-setup";

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

export function slackHealth(record, attempt = "") {
  if (attempt === "failed")
    return { label: "Validation failed", className: "badge-error" };
  if (!record?.token_configured)
    return { label: "Not configured", className: "badge-warning" };
  if (formatSlackValidation(record.validated_at) !== "Never")
    return { label: "Connected", className: "badge-success" };
  return { label: "Configured", className: "badge-info" };
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

export function SlackSettingsTab({ showToast }) {
  const client = getSdkClient();
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

  const [showNewApp, setShowNewApp] = useState(false);
  const [newAppName, setNewAppName] = useState("");
  const [newAppToken, setNewAppToken] = useState("");
  const [appName, setAppName] = useState("");
  const [appToken, setAppToken] = useState("");
  const [appValidation, setAppValidation] = useState("");

  const [showNewInstallation, setShowNewInstallation] = useState(false);
  const [newInstallationName, setNewInstallationName] = useState("");
  const [newTeamId, setNewTeamId] = useState("");
  const [newBotToken, setNewBotToken] = useState("");
  const [installationName, setInstallationName] = useState("");
  const [botToken, setBotToken] = useState("");
  const [installationValidation, setInstallationValidation] = useState("");
  const [deletePlan, setDeletePlan] = useState(null);

  const selectedApp = useMemo(
    () => apps.find((app) => app.id === selectedAppId) || null,
    [apps, selectedAppId],
  );
  const selectedInstallation = useMemo(
    () =>
      installations.find((item) => item.id === selectedInstallationId) || null,
    [installations, selectedInstallationId],
  );

  const notify = (message, style = "success") =>
    showToast?.({ message, style });

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
  }, [refreshApps]);

  useEffect(() => {
    setAppName(selectedApp?.name || "");
    setAppToken("");
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
  }, [selectedAppId]);

  useEffect(() => {
    setInstallationName(selectedInstallation?.name || "");
    setBotToken("");
    setInstallationValidation("");
    setActionError("");
  }, [selectedInstallationId]);

  const chooseApp = (id) => {
    setAppToken("");
    setNewBotToken("");
    setBotToken("");
    setSelectedAppId(id);
  };

  const chooseInstallation = (id) => {
    setBotToken("");
    setSelectedInstallationId(id);
  };

  const openURL = (url, fallback = SLACK_APPS_URL) => {
    try {
      openExternalURL(url);
    } catch {
      if (url !== fallback) openExternalURL(fallback);
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
    if (!id || !appName.trim()) return;
    setBusy("rename-app");
    setActionError("");
    try {
      const updated = await client.slack.renameApp(id, appName.trim());
      setApps((current) => replaceById(current, updated));
      setAppName(updated.name);
      notify("Slack app profile renamed.");
    } catch {
      setActionError("Slack app profile could not be renamed.");
    } finally {
      setBusy("");
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
      if (selectedAppId === id) setAppToken("");
      setAppValidation("success");
      notify("Slack app token replaced and validated.");
    } catch {
      setAppValidation("failed");
      setActionError(
        "The replacement app token was rejected; the configured credential was not changed.",
      );
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
      setAppValidation("success");
      notify("Slack app connection is healthy.");
    } catch {
      setAppValidation("failed");
      setActionError("Slack app connection validation failed.");
    } finally {
      setBusy("");
    }
  };

  const prepareDeleteApp = async () => {
    if (!selectedApp) return;
    setBusy("prepare-delete-app");
    setActionError("");
    try {
      const preview = await client.slack.prepareDeleteApp(selectedApp.id);
      setDeletePlan({ kind: "app", target: selectedApp, preview });
    } catch {
      setActionError("Slack app deletion could not be prepared.");
    } finally {
      setBusy("");
    }
  };

  const createInstallation = async (event) => {
    event.preventDefault();
    if (!selectedApp || !newInstallationName.trim() || !newBotToken) return;
    setBusy("create-installation");
    setActionError("");
    try {
      const created = await client.slack.createInstallation(selectedApp.id, {
        name: newInstallationName.trim(),
        team_id: newTeamId.trim(),
        bot_token: newBotToken,
      });
      setNewBotToken("");
      setNewInstallationName("");
      setNewTeamId("");
      setShowNewInstallation(false);
      setInstallations((current) => [...current, created]);
      setSelectedInstallationId(created.id);
      notify("Slack workspace installation created.");
    } catch {
      setActionError("Slack workspace installation could not be created.");
    } finally {
      setBusy("");
    }
  };

  const renameInstallation = async () => {
    const id = selectedInstallation?.id;
    if (!id || !installationName.trim()) return;
    setBusy("rename-installation");
    setActionError("");
    try {
      const updated = await client.slack.renameInstallation(
        id,
        installationName.trim(),
      );
      setInstallations((current) => replaceById(current, updated));
      setInstallationName(updated.name);
      notify("Slack workspace installation renamed.");
    } catch {
      setActionError("Slack workspace installation could not be renamed.");
    } finally {
      setBusy("");
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
      if (selectedInstallationId === id) setBotToken("");
      setInstallationValidation("success");
      notify("Slack bot token replaced and validated.");
    } catch {
      setInstallationValidation("failed");
      setActionError(
        "The replacement bot token was rejected; the configured credential was not changed.",
      );
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
      setInstallationValidation("success");
      notify("Slack workspace connection is healthy.");
    } catch {
      setInstallationValidation("failed");
      setActionError("Slack workspace connection validation failed.");
    } finally {
      setBusy("");
    }
  };

  const prepareDeleteInstallation = async () => {
    if (!selectedInstallation) return;
    setBusy("prepare-delete-installation");
    setActionError("");
    try {
      const preview = await client.slack.prepareDeleteInstallation(
        selectedInstallation.id,
      );
      setDeletePlan({
        kind: "installation",
        target: selectedInstallation,
        preview,
      });
    } catch {
      setActionError("Slack workspace deletion could not be prepared.");
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
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="space-y-1">
          <h4 class="font-semibold">Slack integrations</h4>
          <p class="text-sm text-mitto-text-muted">
            A Slack workspace is a Slack team. It is not a Mitto project
            workspace. One Slack app can have several Slack workspace
            installations.
          </p>
        </div>
        <div class="flex flex-wrap gap-2">
          <button
            type="button"
            class="btn btn-sm btn-ghost"
            data-testid="slack-setup-guide"
            onClick=${() => openURL(SLACK_SETUP_URL)}
          >
            Setup and scopes
          </button>
          <button
            type="button"
            class="btn btn-sm btn-primary"
            data-testid="slack-create-app-external"
            onClick=${() => openURL(SLACK_CREATE_APP_URL)}
          >
            Create Slack app
          </button>
        </div>
      </div>

      <div role="alert" class="alert alert-info alert-soft text-sm">
        App and bot tokens are write-only and stored in Mitto's credential vault.
        On Linux the vault is an atomic file restricted to mode 0600 inside a
        mode 0700 directory.
      </div>
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

      <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
        <section class="rounded-lg border border-mitto-border bg-mitto-surface-2">
          <div class="flex flex-col p-3 gap-3">
            <div class="flex items-center justify-between gap-2">
              <h5 class="font-semibold text-sm">Slack apps</h5>
              <button
                type="button"
                class="btn btn-sm btn-ghost"
                data-testid="slack-add-app-profile"
                onClick=${() => setShowNewApp((value) => !value)}
              >
                <${PlusIcon} className="w-4 h-4" /> Add profile
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
                    Save profile
                  </button>
                </div>
              </form>`
            }
            ${
              apps.length === 0
                ? html`<div class="text-center py-6 space-y-3">
                    <p class="text-sm text-mitto-text-muted">
                      No Slack app profiles are configured.
                    </p>
                    <button
                      class="btn btn-sm btn-primary"
                      onClick=${() => openURL(SLACK_CREATE_APP_URL)}
                    >
                      Create Slack app
                    </button>
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

        <section class="flex-1 min-w-0 space-y-4">
          ${
            selectedApp
              ? html`<div
                    class="rounded-lg border border-mitto-border bg-mitto-surface-2"
                  >
                    <div class="flex flex-col p-4 gap-4">
                      <div
                        class="flex flex-wrap items-start justify-between gap-2"
                      >
                        <div>
                          <h5 class="font-semibold text-base">
                            ${selectedApp.name}
                          </h5>
                          <${StatusSummary}
                            record=${selectedApp}
                            attempt=${appValidation}
                            identityLabel="Slack App ID"
                            identityValue=${selectedApp.slack_app_id}
                          />
                        </div>
                        <div class="flex flex-wrap gap-2">
                          <button
                            class="btn btn-sm btn-ghost"
                            data-testid="slack-open-app-settings"
                            onClick=${() =>
                              openURL(
                                slackAppSettingsURL(selectedApp.slack_app_id),
                              )}
                          >
                            Open app settings
                          </button>
                          <button
                            class="btn btn-sm"
                            disabled=${busy === "validate-app"}
                            onClick=${validateApp}
                          >
                            Test connection
                          </button>
                          <button
                            class="btn btn-sm btn-ghost text-error"
                            disabled=${busy === "prepare-delete-app"}
                            onClick=${prepareDeleteApp}
                            aria-label="Delete Slack app profile"
                          >
                            <${TrashIcon} className="w-4 h-4" />
                          </button>
                        </div>
                      </div>

                      <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
                        <fieldset class="fieldset">
                          <legend class="fieldset-legend">Friendly name</legend>
                          <div class="join w-full">
                            <input
                              class="input input-sm join-item flex-1"
                              value=${appName}
                              onInput=${(event) =>
                                setAppName(event.target.value)}
                            />
                            <button
                              class="btn btn-sm join-item"
                              disabled=${busy === "rename-app"}
                              onClick=${renameApp}
                            >
                              Rename
                            </button>
                          </div>
                        </fieldset>
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
                          <h5 class="font-semibold text-base">
                            Slack workspaces
                          </h5>
                          <p class="text-xs text-mitto-text-muted">
                            Installations of this app in Slack teams.
                          </p>
                        </div>
                        <button
                          class="btn btn-sm btn-ghost"
                          data-testid="slack-add-installation"
                          onClick=${() =>
                            setShowNewInstallation((value) => !value)}
                        >
                          <${PlusIcon} className="w-4 h-4" /> Add workspace
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
                            <legend class="fieldset-legend">Bot token</legend>
                            <input
                              type="password"
                              autocomplete="off"
                              class="input input-sm w-full"
                              placeholder="xoxb-…"
                              value=${newBotToken}
                              onInput=${(event) =>
                                setNewBotToken(event.target.value)}
                              required
                            />
                          </fieldset>
                          <button
                            class="btn btn-sm btn-primary"
                            disabled=${busy === "create-installation"}
                          >
                            Save Slack workspace
                          </button>
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
                              This app has no Slack workspace installations.
                            </p>`
                          : html`<ul
                              class="menu flex-wrap p-0"
                              data-testid="slack-installation-list"
                            >
                              ${installations.map(
                                (item) =>
                                  html`<li key=${item.id}>
                                    <button
                                      class=${item.id === selectedInstallationId
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
                            </ul>`}
                      ${selectedInstallation &&
                      html`<div
                        class="rounded-lg border border-mitto-border bg-base-200"
                        data-testid="slack-installation-detail"
                      >
                        <div class="flex flex-col p-3 gap-3">
                          <div
                            class="flex flex-wrap items-start justify-between gap-2"
                          >
                            <div>
                              <h6 class="font-medium">
                                ${selectedInstallation.name}
                              </h6>
                              <${StatusSummary}
                                record=${selectedInstallation}
                                attempt=${installationValidation}
                                identityLabel="Slack Team ID"
                                identityValue=${selectedInstallation.team_id}
                              />
                              ${selectedInstallation.team_name &&
                              html`<p class="text-xs text-mitto-text-muted">
                                Slack team: ${selectedInstallation.team_name} ·
                                Bot: ${selectedInstallation.bot_id || "Unknown"}
                              </p>`}
                            </div>
                            <div class="flex gap-2">
                              <button
                                class="btn btn-sm"
                                disabled=${busy === "validate-installation"}
                                onClick=${validateInstallation}
                              >
                                Test connection
                              </button>
                              <button
                                class="btn btn-sm btn-ghost text-error"
                                disabled=${busy ===
                                "prepare-delete-installation"}
                                onClick=${prepareDeleteInstallation}
                                aria-label="Delete Slack workspace installation"
                              >
                                <${TrashIcon} className="w-4 h-4" />
                              </button>
                            </div>
                          </div>
                          <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
                            <fieldset class="fieldset">
                              <legend class="fieldset-legend">
                                Friendly name
                              </legend>
                              <div class="join w-full">
                                <input
                                  class="input input-sm join-item flex-1"
                                  value=${installationName}
                                  onInput=${(event) =>
                                    setInstallationName(event.target.value)}
                                />
                                <button
                                  class="btn btn-sm join-item"
                                  disabled=${busy === "rename-installation"}
                                  onClick=${renameInstallation}
                                >
                                  Rename
                                </button>
                              </div>
                            </fieldset>
                            <fieldset class="fieldset">
                              <legend class="fieldset-legend">
                                Replace bot token
                              </legend>
                              <div class="join w-full">
                                <input
                                  type="password"
                                  autocomplete="off"
                                  class="input input-sm join-item flex-1"
                                  placeholder="New xoxb-… token"
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
                      Select or add a Slack app profile to manage it.
                    </p>
                  </div>
                </div>`
          }
        </section>
      </div>

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
          html`<ul class="list mt-3" data-testid="slack-delete-references">
            ${deleteReferences.map(
              (reference) =>
                html`<li class="list-row" key=${reference.session_id}>
                  <span>${reference.name || "Conversation"}</span>
                  <code class="text-xs">${reference.session_id}</code>
                </li>`,
            )}
          </ul>`
        }
      </${ConfirmDialog}>
    </div>
  `;
}
