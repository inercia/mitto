---
description: Beads issue file-path attachments — convention, metadata schema, and helper script
globs:
  - "scripts/bd-attach.sh"
  - ".beads/**/*"
keywords:
  - beads attachments
  - bd attach
  - attachment
  - issue attachment
  - bd metadata
  - attachments metadata
  - bd-attach.sh
---

# Beads Issue Attachments

`bd` (beads 1.0.x) has no native attachment command. This project attaches file
paths to issues using the `attachments` key inside each issue's structured JSON
metadata column. Metadata syncs with `bd dolt push` / `pull` like any other
issue field, so attachments travel with the ticket to every collaborator.

## Schema

Each attachment is an object; `attachments` is a JSON array of them:

```json
{
  "attachments": [
    {
      "path": "docs/screenshot.png",
      "name": "Failure state",
      "note": "Reproduces after clicking Save twice"
    },
    { "path": "logs/2026-07-16-crash.log" }
  ]
}
```

- `path` (**required**, string): repo-relative path is **preferred** (portable
  across clones); absolute paths are allowed for out-of-repo references.
- `name` (optional, string): short human label; defaults to the basename.
- `note` (optional, string): free-form description of what the attachment shows.

No write-time validation is performed (bd has no filesystem awareness); the
helper script warns on missing files at **read** time only.

## Usage — helper (preferred)

Use `scripts/bd-attach.sh` for day-to-day work; it hides the metadata-merge
plumbing and preserves other metadata keys:

```bash
# Attach a file
scripts/bd-attach.sh add mitto-aiq docs/screenshot.png \
  --name "Failure state" --note "Reproduces after clicking Save twice"

# List (marks missing-on-disk entries with ✗)
scripts/bd-attach.sh list mitto-aiq

# Remove one entry by path
scripts/bd-attach.sh remove mitto-aiq docs/screenshot.png

# Wipe all attachments from an issue
scripts/bd-attach.sh clear mitto-aiq
```

Requires `bd` and `jq` on `PATH`.

## Usage — raw `bd` (advanced)

The helper is a thin wrapper around `bd update --metadata`. If you must go raw:

```bash
# Write (whole-blob replace — YOU are responsible for preserving other keys)
echo '{"attachments":[{"path":"docs/foo.png","name":"screenshot"}]}' \
  > /tmp/att.json
bd update mitto-aiq --metadata @/tmp/att.json

# Read
bd show mitto-aiq --json | jq '.[0].metadata.attachments'
```

> ⚠ **Do not** use `bd update <id> --set-metadata 'attachments=[...]'` — that
> form stores the value as a **string**, not a parsed JSON array, and readers
> then have to double-decode it. Always use `--metadata @file.json` for
> structured attachments, or use the helper.

## Conventions

- Prefer **repo-relative** paths — they resolve on any clone of the repo.
- Keep `note` short; the description or a `bd comment` is the place for a
  longer write-up.
- Multiple attachments per issue is fine; the array preserves insertion order.
- Attachments record the *path only*, not the file bytes. If the referenced
  file may disappear, capture the relevant content in a comment or copy it into
  the repo before attaching.

## Non-goals

The attachment mechanism is deliberately minimal:

- No byte storage or uploads.
- No MIME/type validation.
- No per-attachment access control.
- No UI integration (attachments are visible via `bd show --json`, the helper's
  `list` sub-command, or direct inspection of the metadata column).

Byte-storage or UI features are separate proposals; open a new bead if you need
them.
