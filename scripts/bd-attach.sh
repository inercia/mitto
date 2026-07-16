#!/bin/bash
# bd-attach.sh — Manage file-path attachments on beads issues.
#
# Attachments are stored as structured JSON under the `attachments` metadata key
# on each issue, so they travel with `bd dolt push`/`pull` like any other
# metadata. See .augment/rules/44-beads-attachments.md for the full convention.
#
# Requires: bd, jq.

set -euo pipefail

usage() {
  cat >&2 <<'EOF'
Usage:
  bd-attach.sh add    <issue-id> <path> [--name <name>] [--note <note>]
  bd-attach.sh list   <issue-id>
  bd-attach.sh remove <issue-id> <path>
  bd-attach.sh clear  <issue-id>

Paths should be repo-relative when possible (portable across clones).
Absolute paths are allowed for out-of-repo references.
EOF
  exit 2
}

require() {
  command -v "$1" >/dev/null 2>&1 || { echo "bd-attach: '$1' is required but not installed" >&2; exit 3; }
}
require bd
require jq

# Print the issue's current metadata object (defaulting to {}).
get_metadata() {
  local id="$1"
  bd show "$id" --json | jq -e '.[0].metadata // {}'
}

# Send a metadata JSON blob to the issue.
# `bd update --metadata` MERGES at the top level with existing metadata, so
# other metadata keys on the issue are preserved automatically.
put_metadata() {
  local id="$1" json="$2"
  local tmp; tmp=$(mktemp)
  printf '%s' "$json" > "$tmp"
  bd update "$id" --metadata "@$tmp" >/dev/null
  rm -f "$tmp"
}

# Replace the `attachments` array on the issue (other metadata keys preserved
# by the top-level merge in `bd update --metadata`).
put_attachments() {
  local id="$1" attachments="$2"
  local body; body=$(jq -nc --argjson a "$attachments" '{attachments: $a}')
  put_metadata "$id" "$body"
}

cmd_add() {
  local id="$1" path="$2"; shift 2
  local name="" note=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --name) name="$2"; shift 2 ;;
      --note) note="$2"; shift 2 ;;
      *) echo "bd-attach add: unknown flag: $1" >&2; usage ;;
    esac
  done
  [[ -z "$path" ]] && { echo "bd-attach add: <path> is required" >&2; usage; }
  local existing; existing=$(get_metadata "$id" | jq -c '.attachments // []')
  local entry; entry=$(jq -nc --arg p "$path" --arg n "$name" --arg no "$note" \
    '{path: $p} + (if $n == "" then {} else {name: $n} end) + (if $no == "" then {} else {note: $no} end)')
  local updated; updated=$(jq -c --argjson e "$entry" '. + [$e]' <<<"$existing")
  put_attachments "$id" "$updated"
  echo "✓ attached $path to $id"
  if [[ ! -e "$path" ]]; then
    echo "  ⚠ note: $path does not exist on disk (attachment recorded anyway)" >&2
  fi
}

cmd_list() {
  local id="$1"
  local attachments; attachments=$(get_metadata "$id" | jq -c '.attachments // []')
  local count; count=$(jq 'length' <<<"$attachments")
  if [[ "$count" -eq 0 ]]; then
    echo "no attachments on $id"
    return 0
  fi
  echo "$id: $count attachment(s):"
  local i=0
  while IFS= read -r entry; do
    i=$((i+1))
    local p n no
    p=$(jq -r '.path' <<<"$entry")
    n=$(jq -r '.name // ""' <<<"$entry")
    no=$(jq -r '.note // ""' <<<"$entry")
    local marker="✓"
    if [[ ! -e "$p" ]]; then marker="✗"; fi
    printf '  %d. %s %s' "$i" "$marker" "$p"
    if [[ -n "$n"  ]]; then printf '  [%s]' "$n"; fi
    if [[ -n "$no" ]]; then printf '  — %s' "$no"; fi
    printf '\n'
  done < <(jq -c '.[]' <<<"$attachments")
  echo "  (✓ present on disk, ✗ missing)"
}

cmd_remove() {
  local id="$1" path="$2"
  [[ -z "$path" ]] && { echo "bd-attach remove: <path> is required" >&2; usage; }
  local existing; existing=$(get_metadata "$id" | jq -c '.attachments // []')
  local before; before=$(jq 'length' <<<"$existing")
  local updated; updated=$(jq -c --arg p "$path" 'map(select(.path != $p))' <<<"$existing")
  local after; after=$(jq 'length' <<<"$updated")
  if [[ "$before" == "$after" ]]; then
    echo "bd-attach remove: no attachment with path=$path on $id" >&2
    exit 1
  fi
  put_attachments "$id" "$updated"
  echo "✓ removed $path from $id (was $before, now $after)"
}

cmd_clear() {
  local id="$1"
  bd update "$id" --unset-metadata attachments >/dev/null
  echo "✓ cleared all attachments from $id"
}

[[ $# -lt 2 ]] && usage
subcmd="$1"; shift
case "$subcmd" in
  add)    cmd_add    "$@" ;;
  list)   cmd_list   "$@" ;;
  remove) cmd_remove "$@" ;;
  clear)  cmd_clear  "$@" ;;
  -h|--help|help) usage ;;
  *) echo "bd-attach: unknown sub-command: $subcmd" >&2; usage ;;
esac
