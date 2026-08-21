export const OPEN_SETTINGS_EVENT = "mitto:open_settings";
export const SLACK_INTEGRATIONS_UPDATED_EVENT =
  "mitto:slack_integrations_updated";

export function openSettingsTab(tab) {
  window.dispatchEvent(
    new CustomEvent(OPEN_SETTINGS_EVENT, { detail: { tab } }),
  );
}

export function notifySlackIntegrationsUpdated() {
  window.dispatchEvent(new CustomEvent(SLACK_INTEGRATIONS_UPDATED_EVENT));
}
