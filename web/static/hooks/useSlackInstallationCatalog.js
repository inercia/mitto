// web/static/hooks/useSlackInstallationCatalog.js
// Loads the Slack app/installation catalog for a given SDK client (mitto-uqq.2):
// iterates client.slack.listApps() -> client.slack.listInstallations(appId),
// decorating each installation with app_name + app_token_configured. Also
// listens for SLACK_INTEGRATIONS_UPDATED_EVENT (credentials/installations
// changed elsewhere) and clears the shared channel cache before reloading.
// Extracted from SlackSubscriptionEditor.js so both the Loop-settings editor
// and the prompt-dialog slackChannel field share one catalog loader.
const { useEffect, useState } = window.preact;

import { clearChannelCache, isAbortError } from "../utils/slackChannels.js";
import { SLACK_INTEGRATIONS_UPDATED_EVENT } from "../utils/slackEvents.js";

/**
 * @param {object} client - SDK client exposing `.slack`.
 * @returns {{apps: object[], installations: object[], loading: boolean, error: string}}
 */
export function useSlackInstallationCatalog(client) {
  const [catalog, setCatalog] = useState({
    apps: [],
    installations: [],
    loading: true,
    error: "",
  });
  const [catalogVersion, setCatalogVersion] = useState(0);

  useEffect(() => {
    const refresh = () => {
      clearChannelCache(client);
      setCatalogVersion((current) => current + 1);
    };
    window.addEventListener(SLACK_INTEGRATIONS_UPDATED_EVENT, refresh);
    return () =>
      window.removeEventListener(SLACK_INTEGRATIONS_UPDATED_EVENT, refresh);
  }, [client]);

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

  return catalog;
}
