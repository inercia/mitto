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
- Human messages from subscribed public and private channels are accepted by
  default. Message subtypes, direct messages, bot events, and events authored
  by the installation's bot identity are ignored.
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

1. Create a Slack app (https://api.slack.com/apps) in a development workspace.
2. **Socket Mode**: enable it and generate an app-level token with the
   `connections:write` scope (`xapp-...`).
3. **OAuth & Permissions**: add bot token scopes `users:read` for app identity
   validation, `channels:read` and `groups:read` for public/private channel
   discovery, and `channels:history` plus `groups:history` for their message
   flows. Add `app_mentions:read` only when using mention mode. `chat:write` is
   not required. Existing apps must apply the current manifest and be
   reauthorized before their bot token carries newly-added scopes.
4. **Event Subscriptions**: enable events and subscribe to `message.channels` and
   `message.groups`. Subscribe to `app_mention` only when mention mode is used.
5. Invite the bot to every target channel (`/invite @your-bot`). Private
   channels are visible to discovery only after the bot becomes a member.
6. Note the workspace's **Team ID** and the target **Channel ID**.

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

| Scenario                          | Action                                                                                                   | Expected result                                                                                                                                        |
| --------------------------------- | -------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Multi-team routing                | Post one synthetic human message in each subscribed channel.                                             | Every matching loop receives exactly one turn; loops for other teams/channels receive none. Record event-to-turn latency.                              |
| Public/private routing            | Post one synthetic human message in a subscribed public channel and one in a subscribed private channel. | Both `message.channels` and `message.groups` reach only their matching loops.                                                                          |
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
