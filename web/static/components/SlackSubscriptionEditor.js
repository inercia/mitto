// Slack channel subscriptions for the staged loop-settings editor.
const { html, useCallback, useEffect, useMemo, useRef, useState } =
  window.preact;

import { PlusIcon, TrashIcon } from "./Icons.js";
import { getSdkClient } from "../utils/sdkClient.js";
import {
  DEFAULT_SLACK_EVENT_MODE,
  DEFAULT_SLACK_THREAD_POLICY,
} from "../utils/loopSettings.js";
import {
  SLACK_INTEGRATIONS_UPDATED_EVENT,
  openSettingsTab,
} from "../utils/slackEvents.js";

const CHANNEL_PAGE_SIZE = 100;

function isAbortError(error) {
  return error?.name === "AbortError" || error?.cause?.name === "AbortError";
}

function installationLabel(installation) {
  const parts = [
    installation?.name,
    installation?.team_name || installation?.team_id,
    installation?.app_name,
  ].filter(Boolean);
  return [...new Set(parts)].join(" · ") || installation?.id || "Unknown";
}

function mergeChannels(current, incoming) {
  const byID = new Map((current || []).map((channel) => [channel.id, channel]));
  for (const channel of incoming || []) byID.set(channel.id, channel);
  return [...byID.values()];
}

function blankSubscription() {
  return {
    installationId: "",
    channelId: "",
    eventMode: DEFAULT_SLACK_EVENT_MODE,
    threadPolicy: DEFAULT_SLACK_THREAD_POLICY,
  };
}

export function SlackSubscriptionEditor({
  subscriptions = [],
  onChange,
  fieldErrors = {},
  client: clientOverride,
}) {
  const client = clientOverride || getSdkClient();
  const [catalog, setCatalog] = useState({
    apps: [],
    installations: [],
    loading: true,
    error: "",
  });
  const [catalogVersion, setCatalogVersion] = useState(0);
  const [channelPages, setChannelPages] = useState({});
  const [searchTerms, setSearchTerms] = useState([]);
  const channelPagesRef = useRef(channelPages);
  const channelControllersRef = useRef(new Map());
  channelPagesRef.current = channelPages;

  useEffect(() => {
    setSearchTerms((current) =>
      subscriptions.map((_subscription, index) => current[index] || ""),
    );
  }, [subscriptions.length]);

  useEffect(() => {
    const refresh = () => {
      for (const controller of channelControllersRef.current.values()) {
        controller.abort();
      }
      channelControllersRef.current.clear();
      setChannelPages({});
      setCatalogVersion((current) => current + 1);
    };
    window.addEventListener(SLACK_INTEGRATIONS_UPDATED_EVENT, refresh);
    return () =>
      window.removeEventListener(SLACK_INTEGRATIONS_UPDATED_EVENT, refresh);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;
    setCatalog((current) => ({ ...current, loading: true, error: "" }));

    const load = async () => {
      try {
        const appData = await client.slack.listApps({
          signal: controller.signal,
        });
        const apps = Array.isArray(appData?.apps) ? appData.apps : [];
        const results = await Promise.all(
          apps.map(async (app) => {
            try {
              const data = await client.slack.listInstallations(app.id, {
                signal: controller.signal,
              });
              return {
                failed: false,
                values: (data?.installations || []).map((installation) => ({
                  ...installation,
                  app_name: app.name,
                  app_token_configured: app.token_configured === true,
                })),
              };
            } catch (error) {
              if (isAbortError(error)) throw error;
              return { failed: true, values: [] };
            }
          }),
        );
        if (cancelled) return;
        setCatalog({
          apps,
          installations: results.flatMap((result) => result.values),
          loading: false,
          error: results.some((result) => result.failed)
            ? "Some Slack workspace installations could not be loaded."
            : "",
        });
      } catch (error) {
        if (!cancelled && !isAbortError(error)) {
          setCatalog((current) => ({
            ...current,
            loading: false,
            error: "Slack integrations could not be loaded.",
          }));
        }
      }
    };
    load();
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [catalogVersion, client]);

  useEffect(
    () => () => {
      for (const controller of channelControllersRef.current.values()) {
        controller.abort();
      }
      channelControllersRef.current.clear();
    },
    [],
  );

  const loadChannels = useCallback(
    async (installationID, append = false) => {
      if (!installationID) return;
      const current = channelPagesRef.current[installationID] || {};
      if (current.loading || (append && !current.nextCursor)) return;

      channelControllersRef.current.get(installationID)?.abort();
      const controller = new AbortController();
      channelControllersRef.current.set(installationID, controller);
      setChannelPages((pages) => ({
        ...pages,
        [installationID]: {
          ...(pages[installationID] || {}),
          loading: true,
          error: "",
        },
      }));
      try {
        const data = await client.slack.listChannels(
          installationID,
          {
            cursor: append ? current.nextCursor || "" : "",
            limit: CHANNEL_PAGE_SIZE,
          },
          { signal: controller.signal },
        );
        if (channelControllersRef.current.get(installationID) !== controller)
          return;
        setChannelPages((pages) => {
          const previous = pages[installationID] || {};
          return {
            ...pages,
            [installationID]: {
              channels: append
                ? mergeChannels(previous.channels, data?.channels)
                : mergeChannels([], data?.channels),
              nextCursor: data?.next_cursor || "",
              loading: false,
              loaded: true,
              error: "",
            },
          };
        });
      } catch (error) {
        if (isAbortError(error)) return;
        setChannelPages((pages) => ({
          ...pages,
          [installationID]: {
            ...(pages[installationID] || {}),
            loading: false,
            loaded: true,
            error:
              "Public channels could not be loaded. Check channels:read and retry.",
          },
        }));
      } finally {
        if (channelControllersRef.current.get(installationID) === controller) {
          channelControllersRef.current.delete(installationID);
        }
      }
    },
    [client],
  );

  const installationIDs = useMemo(
    () =>
      [
        ...new Set(
          subscriptions.map((item) => item.installationId).filter(Boolean),
        ),
      ].sort(),
    [subscriptions],
  );

  useEffect(() => {
    for (const installationID of installationIDs) {
      const installation = catalog.installations.find(
        (item) => item.id === installationID,
      );
      const page = channelPages[installationID];
      if (
        installation?.token_configured === true &&
        !page?.loaded &&
        !page?.loading
      ) {
        loadChannels(installationID);
      }
    }
  }, [installationIDs, catalog.installations, channelPages, loadChannels]);

  const updateSubscription = (index, patch) => {
    onChange?.(
      subscriptions.map((subscription, itemIndex) =>
        itemIndex === index ? { ...subscription, ...patch } : subscription,
      ),
    );
  };

  const removeSubscription = (index) => {
    onChange?.(
      subscriptions.filter((_subscription, itemIndex) => itemIndex !== index),
    );
    setSearchTerms((current) =>
      current.filter((_term, itemIndex) => itemIndex !== index),
    );
  };

  return html`
    <div class="flex flex-col gap-3" data-testid="slack-subscription-editor">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <p class="text-xs text-mitto-text-muted">
          New human-authored messages are included by default. Bot, self, and
          subtype messages remain excluded.
        </p>
        <button
          type="button"
          class="btn btn-link btn-xs"
          data-testid="slack-manage-integrations"
          onClick=${() => openSettingsTab("slack")}
        >
          Manage Slack integrations
        </button>
      </div>

      ${catalog.loading &&
      html`<div class="flex items-center gap-2 text-xs text-mitto-text-muted">
        <span class="loading loading-spinner loading-xs"></span>
        Loading Slack workspaces…
      </div>`}
      ${catalog.error &&
      html`<div role="alert" class="alert alert-warning alert-soft text-sm">
        <span>${catalog.error} Existing draft values are preserved.</span>
      </div>`}
      ${!catalog.loading &&
      catalog.apps.length === 0 &&
      html`<div role="alert" class="alert alert-warning alert-soft text-sm">
        <span
          >Configure a Slack app and workspace installation before enabling this
          trigger.</span
        >
      </div>`}
      ${fieldErrors.slackSubscriptions &&
      html`<span class="label text-mitto-danger"
        >${fieldErrors.slackSubscriptions}</span
      >`}
      ${subscriptions.map((subscription, index) => {
        const installation = catalog.installations.find(
          (item) => item.id === subscription.installationId,
        );
        const installationMissing =
          !!subscription.installationId && !catalog.loading && !installation;
        const credentialsMissing =
          !!installation &&
          (installation.token_configured !== true ||
            installation.app_token_configured !== true);
        const channelCredentialMissing =
          !!installation && installation.token_configured !== true;
        const page = channelPages[subscription.installationId] || {};
        const channels = page.channels || [];
        const selectedChannel = channels.find(
          (channel) => channel.id === subscription.channelId,
        );
        const search = (searchTerms[index] || "").trim().toLowerCase();
        const visibleChannels = channels.filter(
          (channel) =>
            channel.id === subscription.channelId ||
            !search ||
            channel.name?.toLowerCase().includes(search) ||
            channel.id?.toLowerCase().includes(search),
        );
        const channelMissing =
          !!subscription.channelId &&
          page.loaded &&
          !page.nextCursor &&
          !selectedChannel;
        const channelUnresolved =
          !!subscription.channelId &&
          page.loaded &&
          !!page.nextCursor &&
          !selectedChannel;
        const field = (name) =>
          fieldErrors[`slackSubscription.${index}.${name}`];

        return html`
          <div
            key=${index}
            class="border border-mitto-border rounded-box bg-mitto-surface-2 p-3 flex flex-col gap-3"
            data-testid="slack-subscription-row-${index}"
          >
            <div class="flex items-center justify-between gap-2">
              <span class="font-medium text-sm">Channel ${index + 1}</span>
              <button
                type="button"
                class="btn btn-ghost btn-square btn-xs"
                aria-label=${`Remove Slack channel ${index + 1}`}
                data-testid="slack-subscription-remove-${index}"
                onClick=${() => removeSubscription(index)}
              >
                <${TrashIcon} className="w-4 h-4" />
              </button>
            </div>

            <label class="fieldset">
              <span class="fieldset-legend">Slack workspace</span>
              <select
                class="select select-sm w-full"
                value=${subscription.installationId}
                data-testid="slack-installation-${index}"
                onChange=${(event) =>
                  updateSubscription(index, {
                    installationId: event.target.value,
                    channelId:
                      event.target.value === subscription.installationId
                        ? subscription.channelId
                        : "",
                  })}
              >
                <option value="">Select a configured workspace</option>
                ${installationMissing &&
                html`<option value=${subscription.installationId}>
                  Missing integration · ${subscription.installationId}
                </option>`}
                ${catalog.installations.map(
                  (item) =>
                    html`<option key=${item.id} value=${item.id}>
                      ${installationLabel(item)}
                      ${item.token_configured && item.app_token_configured
                        ? ""
                        : " · credentials required"}
                    </option>`,
                )}
              </select>
              ${field("installationId") &&
              html`<span class="label text-mitto-danger"
                >${field("installationId")}</span
              >`}
            </label>

            ${installationMissing &&
            html`<div
              role="alert"
              class="alert alert-warning alert-soft text-sm"
            >
              <span
                >This saved installation no longer exists. Choose another
                workspace or manage integrations.</span
              >
            </div>`}
            ${credentialsMissing &&
            html`<div
              role="alert"
              class="alert alert-warning alert-soft text-sm"
            >
              <span
                >This integration needs configured app and bot credentials
                before the trigger can run. Configure the bot credential to load
                channels.</span
              >
            </div>`}

            <label class="fieldset">
              <span class="fieldset-legend">Search loaded public channels</span>
              <input
                type="search"
                class="input input-sm w-full"
                value=${searchTerms[index] || ""}
                placeholder="Channel name or ID"
                disabled=${!installation || channelCredentialMissing}
                data-testid="slack-channel-search-${index}"
                onInput=${(event) =>
                  setSearchTerms((current) => {
                    const next = [...current];
                    next[index] = event.target.value;
                    return next;
                  })}
              />
            </label>

            <label class="fieldset">
              <span class="fieldset-legend">Public channel</span>
              <select
                class="select select-sm w-full"
                value=${subscription.channelId}
                disabled=${!installation || channelCredentialMissing}
                data-testid="slack-channel-${index}"
                onChange=${(event) =>
                  updateSubscription(index, { channelId: event.target.value })}
              >
                <option value="">Select a public channel</option>
                ${subscription.channelId &&
                !selectedChannel &&
                html`<option value=${subscription.channelId}>
                  ${channelMissing ? "Missing channel" : "Saved channel"} ·
                  ${subscription.channelId}
                </option>`}
                ${visibleChannels.map(
                  (channel) =>
                    html`<option key=${channel.id} value=${channel.id}>
                      #${channel.name} · ${channel.id}
                    </option>`,
                )}
              </select>
              ${field("channelId") &&
              html`<span class="label text-mitto-danger"
                >${field("channelId")}</span
              >`}
            </label>

            ${page.loading &&
            html`<div
              class="flex items-center gap-2 text-xs text-mitto-text-muted"
            >
              <span class="loading loading-spinner loading-xs"></span>
              Loading public channels…
            </div>`}
            ${page.error &&
            html`<div
              role="alert"
              class="alert alert-warning alert-soft text-sm"
            >
              <span>${page.error}</span>
              <button
                type="button"
                class="btn btn-sm"
                onClick=${() => loadChannels(subscription.installationId)}
              >
                Retry
              </button>
            </div>`}
            ${channelMissing &&
            html`<div
              role="alert"
              class="alert alert-warning alert-soft text-sm"
            >
              <span
                >The saved channel could not be found. Choose an available
                public channel; the saved ID remains in the draft until
                then.</span
              >
            </div>`}
            ${channelUnresolved &&
            html`<div role="status" class="alert alert-info alert-soft text-sm">
              <span
                >The saved channel is not in the loaded pages yet. Load more to
                continue checking.</span
              >
            </div>`}
            ${page.nextCursor &&
            html`<button
              type="button"
              class="btn btn-sm"
              disabled=${page.loading}
              data-testid="slack-channel-load-more-${index}"
              onClick=${() => loadChannels(subscription.installationId, true)}
            >
              Load more channels
            </button>`}

            <div class="flex flex-col gap-3">
              <label class="fieldset">
                <span class="fieldset-legend">Message mode</span>
                <select
                  class="select select-sm w-full"
                  value=${subscription.eventMode}
                  data-testid="slack-event-mode-${index}"
                  onChange=${(event) =>
                    updateSubscription(index, {
                      eventMode: event.target.value,
                    })}
                >
                  <option value="anyHumanMessage">Any new human message</option>
                  <option value="appMention">
                    Only messages mentioning the app
                  </option>
                </select>
                ${field("eventMode") &&
                html`<span class="label text-mitto-danger"
                  >${field("eventMode")}</span
                >`}
              </label>
              <label class="fieldset">
                <span class="fieldset-legend">Threads</span>
                <select
                  class="select select-sm w-full"
                  value=${subscription.threadPolicy}
                  data-testid="slack-thread-policy-${index}"
                  onChange=${(event) =>
                    updateSubscription(index, {
                      threadPolicy: event.target.value,
                    })}
                >
                  <option value="any">Root messages and replies</option>
                  <option value="rootOnly">Root messages only</option>
                  <option value="repliesOnly">Thread replies only</option>
                </select>
                ${field("threadPolicy") &&
                html`<span class="label text-mitto-danger"
                  >${field("threadPolicy")}</span
                >`}
              </label>
            </div>
          </div>
        `;
      })}

      <button
        type="button"
        class="btn btn-sm"
        data-testid="slack-subscription-add"
        onClick=${() => onChange?.([...subscriptions, blankSubscription()])}
      >
        <${PlusIcon} className="w-4 h-4" />
        Add channel
      </button>
    </div>
  `;
}
