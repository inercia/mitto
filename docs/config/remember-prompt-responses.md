# Per-conversation remembered prompt responses

The prompt parameter dialog (and its callers) can persist the responses you
submit for a prompt so the same dialog in the same conversation can prefill
them later. This page documents the behavior and the API contract added by
that feature. Prompt declarations themselves are documented in
[prompts.md](prompts.md) (`remember:` declarations).

## Dialog behavior

When a prompt dialog opens for a conversation and a destination session id is
known, a footer checkbox **Remember responses** with helper text *“For this
prompt in this conversation only.”* is shown:

- First open with no saved preference: unchecked.
- Checking the box and submitting: the submitted dialog values replace any
  previously saved snapshot and the preference becomes **enabled**.
- Unchecking and submitting: the saved values are deleted and the preference
  becomes **disabled** (a tombstone; disabled is *not* the same as never
  having used the control).
- Cancel, closing the dialog, or a rejected submission persist nothing.

Reopening: when an enabled snapshot exists, the dialog prefills the saved
values and the checkbox is checked. Untouched with no saved preference, the
checkbox state is unset and nothing is persisted on submit.

Hydration precedence: caller/contextual values (for example linked bead
arguments) and fields you already edited always win; a late remembered-args
response never overwrites them. Saved remembered values fill only remaining
fields. Untouched without an enabled snapshot, dialog behavior falls back to
the legacy `remember: folder|conversation` prefill.

Precedence when the preference is enabled: caller/contextual values → last
submitted conversation responses → folder remembered values → declared
defaults.

## API

### Read — `GET /api/workspace-prompts/remembered-args?working_dir=&prompt=&session_id=`

Response is tri-state via an optional `remember_responses` field:

- omitted or unset: legacy behavior — folder values overlaid by saved
  conversation values (filtered to declared parameters).
- `true`: enabled — the saved conversation snapshot (filtered to declared
  parameters) is returned; folder values are not merged. Checkbox presets to
  checked.
- `false`: disabled tombstone — `{"arguments": {}, "remember_responses": false}`.

### Submission — `remember_responses` request field

Both prompt-dispatch endpoints accept an optional boolean
`remember_responses` in the request body:

- `POST /api/sessions/{id}/queue` and `POST /api/sessions` (fresh session with
  an initial prompt; also honored when a request is coalesced into an existing
  conversation).
- omitted: legacy behavior — `remember: conversation` declarations continue to
  save non-empty values, unless a disabled tombstone exists for that
  prompt/session (then nothing is saved).
- `true`: after the message is successfully enqueued, the submitted dialog
  response snapshot is saved (declared parameters only, excluding
  `remember: never`).
- `false`: values are deleted and the tombstone is persisted.

Persistence failures never block the send; failures are logged server-side
without response contents.

## Storage

- One versioned file per session inside the session folder
  (`remembered-prompt-responses.json`), namespaced per workspace UUID and
  write-locked per conversation.
- Deleting a conversation removes the file with it; archiving keeps it.
- Legacy pre-existing `$MITTO_DIR/remembered-args-conversation/<session>.json`
  files are migrated on first access as **unset** (values keep their legacy
  prefill behavior, no consent is inferred); the legacy file is removed only
  after a durable session-local write, and never merged again afterwards.
