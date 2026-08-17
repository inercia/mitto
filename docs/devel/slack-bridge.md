# Slack Socket Mode Bridge

The process-scoped Slack manager receives Slack Events API traffic over Socket
Mode and routes each event to every matching canonical `onSlack` loop.

Package: `internal/slackbridge`. The manager pools one Socket Mode connection
per Slack app profile. App and installation credentials remain in the Mitto
credential vault; loops persist only installation and channel IDs.

## Production architecture

- `Manager` rebuilds its subscription index from enabled, unarchived `onSlack`
  loops at startup and reconciles loop edit/pause/archive/delete transitions.
- A Slack app profile owns one worker and one bounded durable event journal,
  regardless of how many installations, channels, or loops reference it.
- Routing first matches app, team, and channel, then event mode and thread
  policy. A target session is included at most once per event.
- Human messages are accepted by default. Message subtypes, bot events, and
  events authored by the installation's bot identity are ignored.
- Attachments and files are not copied into normalized events and are never
  fetched automatically in v1.
- App tokens are resolved only when a worker starts. Successful replacement
  restarts that app's worker; failed catalog transactions do not disturb it.
- Connection statuses and logs contain app IDs, state, reference counts, retry
  timing, pending/failed/dead-letter counts, and sanitized error classes
  only—never credentials or message text.
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
adapter. It may run alongside catalog-backed subscriptions during migration,
but new deployments should configure Slack profiles and canonical `onSlack`
subscriptions instead.

## Slack app setup

1. Create a Slack app (https://api.slack.com/apps) in a development workspace.
2. **Socket Mode**: enable it and generate an app-level token with the
   `connections:write` scope (`xapp-...`).
3. **OAuth & Permissions**: add bot token scopes `channels:read` for the public
   channel picker and `channels:history` for the default message flow. Add
   `app_mentions:read` only when using mention mode. `chat:write` and private
   channel scopes are not required or supported in v1. Install the app to obtain
   the bot token (`xoxb-...`).
4. **Event Subscriptions**: enable events and subscribe to `message.channels`.
   Subscribe to `app_mention` only when mention mode is used.
5. Invite the bot to the target channel (`/invite @your-bot`).
6. Note the workspace's **Team ID** and the target **Channel ID**.

## Legacy adapter runtime configuration

The deprecated single-target adapter is configured via environment variables,
read once at Mitto web server startup (`internal/slackbridge.LoadConfigFromEnv`).
These values are not used by the production catalog-backed manager, written to
disk, or exposed via Settings/UI/REST/MCP. Token values are never logged;
startup logging includes only the non-secret channel and target session IDs:

| Variable                         | Description                                   |
| --------------------------------- | ---------------------------------------------- |
| `MITTO_SLACK_APP_TOKEN`          | Socket Mode app-level token (`xapp-...`)      |
| `MITTO_SLACK_BOT_TOKEN`          | Bot token (`xoxb-...`)                        |
| `MITTO_SLACK_TEAM_ID`            | Slack workspace/team ID to accept events from |
| `MITTO_SLACK_CHANNEL_ID`         | Slack channel ID to listen on                 |
| `MITTO_SLACK_TARGET_SESSION_ID`  | Mitto conversation ID to trigger              |

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

## Running the smoke test

```bash
export MITTO_SLACK_APP_TOKEN=xapp-...
export MITTO_SLACK_BOT_TOKEN=xoxb-...
export MITTO_SLACK_TEAM_ID=T...
export MITTO_SLACK_CHANNEL_ID=C...
export MITTO_SLACK_TARGET_SESSION_ID=<conversation-id-with-an-enabled-loop>
./mitto web
```

Look for `"Slack event source enabled"` in the log at startup. Then, in the
configured Slack channel:

1. Post a plain message, or `@mention` the bot — the target conversation
   should receive a new turn within ~10s (Socket Mode delivery is normally
   sub-second; the bound accounts for Mitto's own dispatch/resume latency).
2. Post the same test twice from a **different** channel, or have the
   bridge's own bot post a message — neither should trigger anything.
3. Force a Socket Mode disconnect (e.g. toggle the app's Socket Mode off and
   back on, or kill/restore local network briefly) and post again — the
   bridge should reconnect (see `"slackbridge: event source disconnected,
   reconnecting"` in the log) and still deliver the next event.

Record observed event→loop latency and reconnect time when validating
against a real Slack workspace; this repository's automated tests
(`internal/slackbridge/*_test.go`) exercise the same filter/dedupe/reconnect
logic against a **fake** source and require no credentials, but cannot
themselves prove real Slack Socket Mode latency.

## Remaining gaps

- **Legacy adapter limitations**: the environment path remains single-target
  and does not participate in catalog credential rotation or the durable
  journal. Its busy-event and in-memory-dedupe limitations remain unchanged.
- **Single-process journal ownership**: journal files coordinate goroutines in
  one Mitto process, not multiple Mitto replicas sharing the same app-data
  directory.
- **No OAuth installation flow**: credentials are still entered and validated
  through Mitto's integration catalog rather than installed through OAuth.
- **No public HTTP Events API fallback**: Socket Mode only.
- **Reconnect backoff is fixed/simple**: `Bridge.Run`'s outer retry loop uses
  a bounded exponential backoff (1s→30s) as a second line of defense on top
  of `slack-go`'s own Socket Mode reconnect; neither implements Slack's
  documented rate-limit/backoff guidance beyond this.
