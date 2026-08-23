// Slack channel subscriptions for the staged loop-settings editor.
const { html, useEffect, useMemo, useState } = window.preact;

import { PlusIcon, SearchIcon, TrashIcon } from "./Icons.js";
import { NativeSelectWithChevron } from "./NativeSelectWithChevron.js";
import { SlackChannelPickerModal } from "./slack/SlackChannelPickerModal.js";
import { getSdkClient } from "../utils/sdkClient.js";
import {
  DEFAULT_SLACK_EVENT_MODE,
  DEFAULT_SLACK_THREAD_POLICY,
} from "../utils/loopSettings.js";
import { useSlackInstallationCatalog } from "../hooks/useSlackInstallationCatalog.js";
import {
  fetchAllChannelsInBackground,
  getChannelCacheEntry,
  installationLabel,
  isDelegatedUserInstallation,
  subscribeChannelCache,
} from "../utils/slackChannels.js";

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
  const catalog = useSlackInstallationCatalog(client);
  // Picker modal state lives here (not per-row) so exactly one modal instance
  // exists; `index` identifies the active subscription and is always read
  // fresh from state at render/handler time, so re-renders or row
  // additions/removals never leave a handler closing over a stale index.
  const [picker, setPicker] = useState({ open: false, index: -1 });
  // Forces a re-render when a subscribed installation's cached channels
  // change (see the subscribeChannelCache effect below); channel data itself
  // lives in the client-scoped channel cache, not component state, so it
  // survives this component remounting (as long as the same `client` — the
  // production singleton from getSdkClient() — is reused).
  const [, forceChannelCacheRerender] = useState(0);

  const installationIDs = useMemo(
    () =>
      [
        ...new Set(
          subscriptions.map((item) => item.installationId).filter(Boolean),
        ),
      ].sort(),
    [subscriptions],
  );
  const installationIDsKey = installationIDs.join("\u0000");

  // Subscribes this component to the shared channel cache for every
  // installation currently referenced by a subscription row, re-rendering
  // when a background page (fetched here or by another mounted editor
  // instance) lands. Unsubscribing (row removed / component unmounted) lets
  // subscribeChannelCache abort the fetch once no editor still cares.
  useEffect(() => {
    const unsubscribes = installationIDs.map((installationId) =>
      subscribeChannelCache(client, installationId, () =>
        forceChannelCacheRerender((count) => count + 1),
      ),
    );
    return () => {
      for (const unsubscribe of unsubscribes) unsubscribe();
    };
  }, [installationIDsKey, client]);

  // Kicks off (or resumes) the sequential background page fetch for every
  // referenced installation with a configured bot or delegated-user credential.
  // A no-op for installations already complete or already being fetched.
  useEffect(() => {
    for (const installationId of installationIDs) {
      const installation = catalog.installations.find(
        (item) => item.id === installationId,
      );
      if (installation?.token_configured === true) {
        fetchAllChannelsInBackground(client, installationId);
      }
    }
  }, [installationIDsKey, catalog.installations, client]);

  // appMention is delivered only through the bot event stream. A credential
  // replacement can change an installation's mode without changing its stable
  // ID, so normalize stale drafts after every catalog refresh while preserving
  // their installation, channel, thread policy, and unknown future fields.
  useEffect(() => {
    if (catalog.loading) return;
    let changed = false;
    const next = subscriptions.map((subscription) => {
      const installation = catalog.installations.find(
        (item) => item.id === subscription.installationId,
      );
      if (
        isDelegatedUserInstallation(installation) &&
        subscription.eventMode === "appMention"
      ) {
        changed = true;
        return { ...subscription, eventMode: DEFAULT_SLACK_EVENT_MODE };
      }
      return subscription;
    });
    if (changed) onChange?.(next);
  }, [catalog.loading, catalog.installations, subscriptions, onChange]);

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
    if (picker.index === index) {
      setPicker({ open: false, index: -1 });
    }
  };

  const openChannelPicker = (index) => {
    setPicker({ open: true, index });
  };

  const closeChannelPicker = () => {
    setPicker((current) => ({ ...current, open: false }));
  };

  const selectChannel = (channelId) => {
    if (picker.index < 0) return;
    updateSubscription(picker.index, { channelId });
    closeChannelPicker();
  };

  // Derived from `picker` + current props/state on every render (never
  // memoized against a stale index), so they always reflect the actively
  // open row even if subscriptions are added/removed while the modal is open.
  const pickerSubscription =
    picker.index >= 0 ? subscriptions[picker.index] : null;
  const pickerInstallationId = pickerSubscription?.installationId || "";
  const pickerInstallation = catalog.installations.find(
    (item) => item.id === pickerInstallationId,
  );
  const pickerUsesDelegatedUser =
    isDelegatedUserInstallation(pickerInstallation);

  return html`
    <div class="flex flex-col gap-3" data-testid="slack-subscription-editor">
      <p class="text-xs text-mitto-text-muted">
        New human-authored messages are included by default. Bot, self, and
        subtype messages remain excluded.
      </p>

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
        const usesDelegatedUser = isDelegatedUserInstallation(installation);
        const page = getChannelCacheEntry(client, subscription.installationId);
        const channels = page.channels || [];
        const selectedChannel = channels.find(
          (channel) => channel.id === subscription.channelId,
        );
        const selectedChannelNeedsInvite =
          !!selectedChannel && selectedChannel.is_member !== true;
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
              <${NativeSelectWithChevron}
                ariaLabel="Slack workspace"
                value=${subscription.installationId}
                testId="slack-installation-${index}"
                onChange=${(event) =>
                  updateSubscription(index, {
                    installationId: event.target.value,
                    channelId:
                      event.target.value === subscription.installationId
                        ? subscription.channelId
                        : "",
                    eventMode: isDelegatedUserInstallation(
                      catalog.installations.find(
                        (item) => item.id === event.target.value,
                      ),
                    )
                      ? DEFAULT_SLACK_EVENT_MODE
                      : subscription.eventMode,
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
              <//>
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
                >This integration needs configured app and installation
                credentials before the trigger can run. Configure the
                ${usesDelegatedUser ? "delegated-user" : "bot"} credential to
                load channels.</span
              >
            </div>`}
            ${installation &&
            html`<div
              class="flex flex-wrap items-center gap-2 text-xs text-mitto-text-muted"
              data-testid="slack-credential-mode-${index}"
              role="status"
            >
              <span
                class="badge badge-sm badge-soft ${usesDelegatedUser
                  ? "badge-info"
                  : "badge-primary"}"
                >${usesDelegatedUser ? "Delegated user" : "Bot"}</span
              >
              <span>
                ${usesDelegatedUser
                  ? "Channel visibility follows the authorizing user's membership and lasts only while that authorization remains active. Mention-only mode is unavailable; stale mention-only drafts reset to human messages."
                  : "Channel visibility follows the bot's membership. Invite the bot to private channels before using them."}
              </span>
            </div>`}

            <label class="fieldset">
              <span class="fieldset-legend">Channel ID</span>
              <div class="join w-full">
                <input
                  type="text"
                  class="input input-sm join-item flex-1"
                  value=${subscription.channelId}
                  placeholder="C0123456789"
                  data-testid="slack-channel-id-${index}"
                  onInput=${(event) =>
                    updateSubscription(index, {
                      channelId: event.target.value,
                    })}
                />
                <button
                  type="button"
                  class="btn btn-sm join-item btn-square"
                  aria-label="Search Slack channels"
                  title="Search Slack channels"
                  disabled=${!installation || channelCredentialMissing}
                  data-testid="slack-channel-picker-open-${index}"
                  onClick=${() => openChannelPicker(index)}
                >
                  <${SearchIcon} className="w-4 h-4" />
                </button>
              </div>
              ${field("channelId") &&
              html`<span class="label text-mitto-danger"
                >${field("channelId")}</span
              >`}
            </label>

            ${selectedChannelNeedsInvite &&
            html`<div
              role="alert"
              class="alert alert-warning alert-soft text-sm"
              data-testid="slack-channel-invite-guidance-${index}"
            >
              <span
                >${usesDelegatedUser
                  ? `The authorizing user must join #${selectedChannel.name} and keep the authorization active before enabling this trigger.`
                  : `Invite the bot to #${selectedChannel.name} before enabling this trigger so Slack can deliver its messages.`}</span
              >
            </div>`}

            <div class="flex flex-col gap-3">
              <label class="fieldset">
                <span class="fieldset-legend">Message mode</span>
                <${NativeSelectWithChevron}
                  ariaLabel="Message mode"
                  value=${subscription.eventMode}
                  testId="slack-event-mode-${index}"
                  onChange=${(event) =>
                    updateSubscription(index, {
                      eventMode: event.target.value,
                    })}
                >
                  <option value="anyHumanMessage">Any new human message</option>
                  ${!usesDelegatedUser &&
                  html`<option value="appMention">
                    Only messages mentioning the app
                  </option>`}
                <//>
                ${field("eventMode") &&
                html`<span class="label text-mitto-danger"
                  >${field("eventMode")}</span
                >`}
              </label>
              <label class="fieldset">
                <span class="fieldset-legend">Threads</span>
                <${NativeSelectWithChevron}
                  ariaLabel="Threads"
                  value=${subscription.threadPolicy}
                  testId="slack-thread-policy-${index}"
                  onChange=${(event) =>
                    updateSubscription(index, {
                      threadPolicy: event.target.value,
                    })}
                >
                  <option value="any">Root messages and replies</option>
                  <option value="rootOnly">Root messages only</option>
                  <option value="repliesOnly">Thread replies only</option>
                <//>
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

      <${SlackChannelPickerModal}
        client=${client}
        installationId=${pickerInstallationId}
        usesDelegatedUser=${pickerUsesDelegatedUser}
        open=${picker.open}
        onClose=${closeChannelPicker}
        onSelect=${selectChannel}
      />
    </div>
  `;
}
