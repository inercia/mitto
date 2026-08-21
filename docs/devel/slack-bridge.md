# Slack Socket Mode Bridge

The process-scoped Slack manager receives Slack Events API traffic over Socket
Mode and routes each event to every matching canonical `onSlack` loop.
OAuth flow-status polling is used only to finish delegated-user setup; message
delivery never polls Slack and continues through the app profile's `xapp` Socket
Mode worker, authorization routing, durable journal, and `onSlack` dispatch.

Package: `internal/slackbridge`. The manager pools one Socket Mode connection
per Slack app profile. App and installation credentials remain in the Mitto
credential vault; loops persist only installation and channel IDs.

## Production architecture

- `Manager` rebuilds its subscription index from enabled, unarchived `onSlack`
  loops at startup and reconciles loop edit/pause/archive/delete transitions.
- A Slack app profile owns one worker and one bounded durable event journal,
  regardless of how many installations, channels, or loops reference it.
- For envelopes with an `event_context`, the worker resolves the complete
  installation set through `apps.event.authorizations.list`. Routing matches
  each bot or delegated-user authorization to the catalog installation's
  credential kind and authorized Slack identity before applying app, team,
  channel, event-mode, and thread-policy filters. A target session is included
  at most once even when multiple authorizations match it.
- An explicitly empty authorization set fails closed, including after credential
  revocation or user deactivation. Authorization lookup failures leave the
  Socket Mode envelope unacknowledged for Slack redelivery. Authorization
  identities are transient routing data and are not written to the journal.
- Human messages from subscribed public and private channels are accepted by
  default. Message subtypes, direct messages, bot events, and events authored
  by the installation's bot identity are ignored.
- Attachments and files are not copied into normalized events and are never
  fetched automatically in v1.
- App tokens are resolved only when a worker starts. Successful replacement
  restarts that app's worker; failed catalog transactions do not disturb it.
- `connected` is emitted only after Slack's Socket Mode `hello` frame. Statuses
  and logs also expose value-free connection/envelope timestamps, Events API,
  accepted/ignored/delivered counts, authorization/journal failure timestamps,
  retry timing, and pending/failed/dead-letter counts—never credentials or
  message text. A worker that starts without completing the handshake remains
  `connecting`.
- Unused app workers stop after a grace period; shutdown cancels and joins all
  workers before loop/session shutdown.

## Durable delivery

Catalog-backed workers normalize and filter an Events API envelope, snapshot
all matching loop recipients, and atomically persist it before acknowledging
the Socket Mode request. A persistence or capacity failure leaves the envelope
unacknowledged so Slack can redeliver it. Durable `event_id` tombstones make
those redeliveries idempotent across process restarts.

Each recipient advances independently through `pending`, `delivering`, and
`delivered`. Busy, workspace-capped, or coalesced dispatches return to pending
and drain when that conversation next becomes idle. Other dispatch failures use
bounded retry backoff. A two-second profile settle window groups events into
ordered batches of at most 20 events and 32 KiB; each batch consumes one loop
iteration. Startup recovery changes interrupted `delivering` recipients back to
pending before workers begin accepting traffic.

The journal retains delivered event-ID tombstones for 24 hours. Undelivered
records older than 24 hours become content-free expired/dead-letter tombstones,
and Slack text is erased immediately once every snapshotted recipient is
terminal. Files are bounded to 2,000 records and 8 MiB per app profile and are
stored with mode `0600` under Mitto's app-data `slack-event-journal` directory.

Delivery is **at least once**. A process can crash after the loop dispatch is
accepted but before the journal records `delivered`; startup recovery retries
that recipient, so this narrow ambiguity window can produce a duplicate turn.

## Legacy environment compatibility

The original single-target environment bridge remains available as a deprecated
adapter for one compatibility release. Settings > Slack exposes a value-free
import action when any legacy variable is present. The import validates both
credentials, creates or selects managed app/installation records, adds the
canonical `onSlack` channel subscription to the target loop, and writes tokens
directly to the vault without returning them to the browser.

Persisted configuration wins. If the target loop already references the same
Slack team/channel, including while paused, Mitto suppresses the environment
listener at startup. During import Mitto stops and joins the legacy listener
before committing the handoff, starts managed routing only after catalog and
loop persistence succeed, and restores every prior value plus the legacy
listener if any step fails. Remove the environment variables after a successful
import. The adapter is planned for removal in the next breaking release after
this compatibility period.

## Slack app setup

1. Create a Slack app (https://api.slack.com/apps) from the manifest copied from
   Mitto's Slack settings. For an existing app, apply the current manifest, then
   reinstall or reauthorize it so newly-added bot and user scopes are granted.
2. **App-level token**: enable Socket Mode and separately generate an `xapp-...`
   token with `connections:write` and `authorizations:read`. The latter permits
   `apps.event.authorizations.list` to resolve every installation that may see
   one shared Events API envelope. This token is not an OAuth bot or user token
   and cannot be declared in the Slack manifest.
3. **Bot token**: the manifest requests `users:read` for app identity validation,
   `channels:read` and `groups:read` for discovery, and `channels:history` plus
   `groups:history` for message flows. `app_mentions:read` supports optional
   mention mode; `chat:write` is not required. The installed app supplies the
   `xoxb-...` bot token currently accepted by Mitto's integration catalog.
4. **Delegated user OAuth**: the manifest also requests least-privilege user scopes
   `channels:read`, `groups:read`, `channels:history`, and `groups:history` for
   channels visible to the authorizing user. Configure an HTTPS
   `web.hooks.external_address`, copy the exact redirect URI from Settings > Slack
   into the Slack app, then store its client ID and write-only client secret. Use
   **Authorize delegated user** instead of pasting a user token; Mitto binds the
   returned token to `oauth.v2.access` app/team/user provenance.
5. **Event Subscriptions**: bot events include `message.channels`,
   `message.groups`, and optional `app_mention`; user events include
   `message.channels` and `message.groups`. Apply the current manifest and
   reauthorize existing delegated-user installations whenever these events or
   scopes change; a Socket Mode connection alone does not prove event delivery.
6. In bot mode, invite the bot to every target channel (`/invite @your-bot`). A
   private channel is visible to bot discovery only after the bot becomes a
   member. Delegated-user mode will instead use channels visible to its user.
7. Note the workspace's **Team ID** and the target **Channel ID**.

## Legacy adapter runtime configuration

The deprecated single-target adapter is configured via environment variables,
read once at Mitto web server startup (`internal/slackbridge.LoadConfigFromEnv`).
These values are not used by the production catalog-backed manager or written
to project files. Token values are never exposed via Settings/UI/REST/MCP or
logged. The value-free migration status exposes only the non-secret team,
channel, and target session IDs; startup logging includes only channel and
target session IDs:

| Variable                        | Description                                   |
| ------------------------------- | --------------------------------------------- |
| `MITTO_SLACK_APP_TOKEN`         | Socket Mode app-level token (`xapp-...`)      |
| `MITTO_SLACK_BOT_TOKEN`         | Bot token (`xoxb-...`)                        |
| `MITTO_SLACK_TEAM_ID`           | Slack workspace/team ID to accept events from |
| `MITTO_SLACK_CHANNEL_ID`        | Slack channel ID to listen on                 |
| `MITTO_SLACK_TARGET_SESSION_ID` | Mitto conversation ID to trigger              |

All five must be set together or the feature stays disabled. A partial set
(some but not all present) fails safely: the server logs a warning naming
only the **missing variable names** (never any configured value) and does
not start the listener.

The target conversation must already exist and have an **enabled** loop
prompt (any trigger, or none — the bridge fires it directly via
`LoopRunner.TriggerNowWithSlackEvent`, independent of the loop's own armed
triggers). It must be **idle** (not currently prompting) when an event
arrives, or the fire is dropped (`ErrSessionBusy`) — see
[Remaining gaps](#remaining-gaps).

For the legacy adapter, render the guarded trigger context. Mitto has already
JSON-escaped the message inside an explicit authority-free, untrusted delimiter:

```text
{{ with .Trigger }}{{ with .Slack }}
## Slack event (UNTRUSTED EXTERNAL CONTENT)
- Event ID: {{ .EventID }}
- Channel: {{ .ChannelID }}
- Author: {{ .AuthorID }}
- Timestamp: {{ .Timestamp }}
- Thread timestamp: {{ .ThreadTimestamp }}

{{ .Text }}
{{ end }}{{ end }}
```

The enforced delimiters are `<!-- SLACK_UNTRUSTED_START -->` and
`<!-- SLACK_UNTRUSTED_END -->`. The JSON payload escapes Slack-controlled marker
text so it cannot forge the closing delimiter. The complete event batch is capped
at 20 events and 32 KiB; every metadata field is also capped. Prompt contexts
never contain attachments, files, credentials, or raw Slack SDK structures.

## Production validation

Automated coverage must stay credential-free: use `FakeSource`, fake catalog
and credential providers, temporary journals/session stores, and the mock ACP
server. CI must not read a developer Keychain, require `MITTO_SLACK_*`, or call
Slack. A real development workspace smoke is separate because only Slack can
validate Socket Mode latency, disconnect behavior, and credential replacement.

### Managed development-workspace smoke

Use synthetic messages in isolated public and private development channels. Configure one Slack
app profile in **Settings > Slack**, install it into two development Slack
workspaces, and create one installation record per team. Invite the bot to two
channels in each team. Create at least two enabled loops: one subscribed across
both teams and another sharing one of those subscriptions. Arm a second trigger
on one loop to exercise dispatch contention.

Never paste tokens into an issue, chat, terminal argument, URL, screenshot, or
results file. Enter them only into Mitto's write-only localhost Settings fields.
Use aliases such as `team-a/channel-1` in evidence instead of copying credential
material or message bodies.

For a focused receipt smoke, first wait for `state=connected` with a non-zero
`connected_at`, then record the aggregate counters and post one unique synthetic
human message in a subscribed development channel. Verify `events_api_received`,
`last_envelope_at`, and `accepted_count` advance; the journal record reaches a
content-free delivered tombstone; and the resulting `events.jsonl` prompt records
`onSlack` provenance. If the transport is connected but no envelope counter moves,
reapply the current manifest and reauthorize the installation before debugging
Mitto routing. An advancing `ignored_count`, authorization timestamp, or journal
timestamp instead identifies the corresponding in-process failure boundary.

| Scenario                          | Action                                                                                                   | Expected result                                                                                                                                        |
| --------------------------------- | -------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Multi-team routing                | Post one synthetic human message in each subscribed channel.                                             | Every matching loop receives exactly one turn; loops for other teams/channels receive none. Record event-to-turn latency.                              |
| Public/private routing            | Post one synthetic human message in a subscribed public channel and one in a subscribed private channel. | Both `message.channels` and `message.groups` reach only their matching loops.                                                                          |
| Bot/user authorization routing    | Subscribe separate bot- and user-mode installations, then post events visible to each identity.          | Each event reaches only installations present in Slack's resolved authorization set; equivalent human messages produce equivalent loop deliveries.     |
| Overlapping authorizations        | Subscribe one loop through bot and user installations that can both see the same event.                  | The loop receives one turn, not one per matching authorization.                                                                                        |
| Authorization loss                | Revoke one credential or deactivate its delegated user, then post in the formerly visible channel.       | The affected installation receives no turn; no authorization identities or unmatched message text remain in the durable journal.                       |
| Filter boundary                   | Post from an unsubscribed channel and from the app's bot identity; exercise a non-empty message subtype. | No loop turn is created.                                                                                                                               |
| Pause/resume                      | Pause one shared-channel loop, post once, resume it, then post a new event.                              | Only enabled recipients receive the first event; both receive the new event after resume.                                                              |
| Busy and mixed-trigger contention | Keep one loop prompting (or fire its other trigger), then post a subscribed event.                       | The Slack recipient remains pending and is delivered exactly once after idle; another trigger never causes the Slack event to disappear.               |
| Socket reconnect                  | Briefly interrupt Socket Mode or local network access, restore it, then post again.                      | Connection state recovers and the next event is delivered. Record disconnect-to-connected duration.                                                    |
| App credential rotation           | Generate a replacement app-level token and use **Replace app token**.                                    | The app worker restarts without changing subscriptions; a subsequent event is delivered. Record success/failure only.                                  |
| Bot credential rotation           | Replace one installation's bot token with a token for the same app/team.                                 | Validation succeeds, routing reconciles, and that team's next event is delivered without affecting the other team.                                     |
| Process restart recovery          | Make a subscribed recipient busy, accept an event, restart Mitto, and let the conversation become idle.  | The persisted pending recipient drains after startup. Normal-path recipients are not duplicated; the documented crash ambiguity remains at least once. |

### Value-free results record

Store only the following evidence. Leave a cell blank rather than attaching raw
logs when sanitization is uncertain.

| Field                                                  | Result |
| ------------------------------------------------------ | ------ |
| Mitto revision and platform                            |        |
| UTC test window                                        |        |
| Synthetic topology aliases (apps/teams/channels/loops) |        |
| Fan-out expected / observed counts                     |        |
| Event-to-turn latency: min / median / max              |        |
| Reconnect duration                                     |        |
| App-token rotation outcome and recovery duration       |        |
| Bot-token rotation outcome and recovery duration       |        |
| Busy/mixed-trigger pending then delivered              |        |
| Restart pending then delivered                         |        |
| Value-free connection states/error classes             |        |
| Overall pass/fail and follow-up issue IDs              |        |

Do **not** record tokens, authorization headers, cookies, vault files, Keychain
output, full request/response bodies, Slack message text, or raw provider errors.
Connection-state snapshots and aggregate pending/failed/dead-letter counts are
the authoritative operational evidence.

### Deprecated environment-adapter smoke

The legacy single-target adapter may still be checked during its compatibility
period. Supply all five `MITTO_SLACK_*` values through a protected local process
environment rather than shell history, start Mitto, and verify the value-free
`Deprecated Slack environment adapter enabled` startup message. A subscribed
human event should create one turn, while another channel and the bot identity
must not. Reconnect should emit the sanitized `slackbridge: event source
disconnected, reconnecting` message and deliver the next event. This adapter
does not validate managed fan-out, durable busy recovery, or rotation.

## Troubleshooting: connected but 0 events received

Settings > Slack surfaces a "Connected, but 0 events received." warning
(mitto-yn5) when an app's Socket Mode connection is `state=connected`, has at
least one active `onSlack` loop subscription referencing it (so it is not
simply idle by design), and `events_api_received` is still zero roughly 90
seconds after `connected_at` (a short grace window so a freshly-connected app
is not flagged before Slack could plausibly deliver a first envelope). The
initial value comes from `GET /api/slack/connections`; live updates arrive via
the `slack_connection_status` global-events broadcast (both value-free —
see [Value-free results record](#value-free-results-record)).

The most common root cause is a **delegated-user installation** whose Slack
app is not actually subscribed to the events it needs: bot-token installations
receive `bot_events`, but user-token (delegated-user) installations require
`user_events` under Slack's "Subscribe to events on behalf of users" section.
If that block is missing `message.channels` / `message.groups`, Slack never
sends Events API envelopes for that identity even though the Socket Mode
connection itself is healthy.

To resolve:

1. Open the app in [api.slack.com/apps](https://api.slack.com/apps) and check
   **Event Subscriptions > Subscribe to events on behalf of users**.
2. Add `message.channels` and `message.groups` (see the manifest in
   [Slack app setup](#slack-app-setup)) if either is missing.
3. Reinstall/reauthorize the workspace so the new user-scope subscriptions
   take effect, then re-test with a synthetic message per the
   [managed development-workspace smoke](#managed-development-workspace-smoke).

If subscriptions are already correct, treat this the same as any other
zero-envelope symptom in the smoke test above: reapply the manifest and
reauthorize before debugging Mitto's own routing.

## Remaining gaps

- **Legacy adapter limitations**: the environment path remains single-target
  and does not participate in catalog credential rotation or the durable
  journal. Its busy-event and in-memory-dedupe limitations remain unchanged.
- **Single-process journal ownership**: journal files coordinate goroutines in
  one Mitto process, not multiple Mitto replicas sharing the same app-data
  directory.
- **OAuth requires an external HTTPS redirect**: plaintext localhost callbacks are
  intentionally not synthesized. Development installations need a configured HTTPS
  tunnel or reverse proxy whose base URL is `web.hooks.external_address`.
- **No public HTTP Events API fallback**: Socket Mode only.
- **Reconnect backoff is fixed/simple**: `Bridge.Run`'s outer retry loop uses
  a bounded exponential backoff (1s→30s) as a second line of defense on top
  of `slack-go`'s own Socket Mode reconnect; neither implements Slack's
  documented rate-limit/backoff guidance beyond this.
