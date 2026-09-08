# `mitto config get` / `mitto config set` — CLI Contract

> **Status:** `mitto config get [PATH]` is wired and live (bead `mitto-4rz.4`,
> composing the pure parser from `mitto-4rz.1`, the snapshot service from
> `mitto-4rz.2`, and the authenticated snapshot resource from `mitto-4rz.3`).
> `mitto config set` is still contract-only — no persistence, no server
> calls, no CLI flags exist for it yet. The parser living in
> `internal/config/configpath` implements only the path/value grammar
> described below; it never reads files, calls the network, or touches CLI
> globals.

## `mitto config get [PATH]`

Reads one value by dotted/indexed `PATH` (same grammar as `config set`
below), or the whole config when `PATH` is omitted.

- **Default (live)**: talks to a running `mitto web` server, resolved via
  `--url`/`--token`/`--api-prefix` flags, `$MITTO_URL`/`$MITTO_TOKEN`/
  `$MITTO_API_PREFIX`, or `instance.json` — same precedence as `mitto
  conversation`/`mitto auth` (`docs/devel/cli-conversation.md` §2). Returns
  the server's `settings.json`, always redacted server-side. A remote/auth/
  transport error is reported as-is and never silently falls back to
  reading local disk. If `--url`/`$MITTO_URL` is given explicitly without an
  explicit `--token`/`$MITTO_TOKEN`, the command refuses to attach the local
  `instance.json` bearer token to that target (exit 2) rather than risk
  sending this machine's credential to an unrelated host.
- **`--offline`**: reads local `settings.json` directly — no server, no
  network, no Keychain access, no first-run creation/migration. Required for
  `--effective` and `--explain` (below), since the live snapshot resource
  reports only the redacted stored document with no per-path provenance.
- **`--effective`**: when a path has no stored value but this service knows
  a compile-time default for it (`web.port`, `web.external_port`,
  `mcp.host`, `mcp.port`), show that default instead of a not-found error.
  Without `--effective`, an unstored-but-defaultable path is still
  "not found" — the default view is always "stored". Offline only.
- **`--explain`**: alongside the value, show `provenance`
  (`stored`/`effective`) and whether the value was redacted. Requires
  `--offline` and a `PATH` (there is nothing to explain for the whole doc).
- **`--raw`**: print a bare scalar with no JSON/YAML quoting (for shell
  scripting); errors (exit 2) if the resolved value is an object or array.
- **`--output json|yaml|table`**: defaults to `json`. Table output is
  unstable and not meant to be parsed by scripts (same convention as
  `mitto conversation`).

Every mode always redacts secrets (`web.auth.simple.password`,
`web.auth.shared_token`, the whole `mcp` subtree) — there is no
`--show-secrets` escape hatch.

**Exit codes** (shared with `mitto conversation`/`mitto auth`, see
`docs/devel/cli-conversation.md` §5): `0` success, `1` generic error, `2`
usage error (bad path, invalid flag combination, `--effective`/`--explain`
without `--offline`), `3` server unreachable, `4` auth failure, `5` path (or
whole config) not found. A path that resolves to a stored JSON `null` is a
**successful** read (exit 0, prints `null`) — distinct from a missing path
(exit 5).

**Examples:**

```zsh
mitto config get web.port                                # live, JSON
mitto config get --raw web.port                           # bare scalar: 8080
mitto config get --offline --effective mcp.port           # compile-time default
mitto config get --offline --explain 'task_label_colors[0].color'
mitto config get --offline --output yaml                  # whole config as YAML
```

Quote array-indexed paths in zsh (`[0]` is glob-special), same as `config
set` below.

## Vocabulary: settings.json JSON tags

All paths use the **JSON tag vocabulary of `settings.json`** (the file
written/read by the Web UI and `internal/config.Settings`), never Go field
names and never the web frontend's DTO shape. Examples used throughout this
document are real settings.json paths:

- `web.port` — nested scalar
- `shortcuts` — top-level map keyed by section id
- `task_label_colors[0].color` — indexed array of objects

## Supported syntax

### Paths

- **Dotted map keys**: `a.b.c`
- **Array indexes**: nonnegative integers only, `task_label_colors[0].color`
- **Escaping**: `\.`, `\,`, `\\` (and `\=`, `\{`, `\}`, `\[`, `\]`, `\"`) inside
  a key literal-escape the following character, e.g.
  `nodeSelector.kubernetes\.io/role` is the single key
  `kubernetes.io/role` under `nodeSelector`.

### Assignments

- **One flag, multiple assignments**: comma-separated, `--set a=1,b=2`
- **Brace lists**: `--set colors={red,green,blue}` sets a flat list; each
  element is typed the same way a scalar would be. Nested lists
  (`{a,{b,c}}`) are rejected — use `--set-json` for nested structures.
- **Comma-containing values must escape the comma**: e.g. an rgb string —
  `--set-string color=rgb(0\,128\,255)`. In zsh, quote the whole
  assignment so the shell doesn't interpret the backslash or parens:
  `--set-string 'color=rgb(0\,128\,255)'`.

### Value modes

| Mode           | Flag            | Behavior                                                             |
| -------------- | --------------- | ---------------------------------------------------------------------|
| Typed          | `--set`         | Auto-detects bool / int / float / `null` / string (see below)        |
| Forced string  | `--set-string`  | Value is always a string, even if it looks like a bool/number        |
| Strict JSON    | `--set-json`    | Value must be valid JSON: scalar, object, or array                   |
| File / stdin   | `--set-file`    | Value is literal file/stdin content (see "set-file semantics" below) |

**`--set` typing rules:**

- `true` / `false` → bool
- `null` → **the JSON/YAML null value**, not a delete or reset instruction
  (see "null, empty list, and absent" below)
- integers (`42`, `-7`, `0`) → int
- floats (`3.14`, `1e10`) → float
- **leading-zero tokens are preserved as strings**: `007` stays the string
  `"007"` rather than being parsed as an integer, because leading-zero
  numeric-looking tokens are usually meant to be strings (version suffixes,
  zero-padded codes, etc.)
- empty value (`a=`) → the empty string, distinct from `null` and from
  omitting the assignment entirely
- anything else → string, literally (after unescaping)

Integers outside the `int64` range fall back to string; use `--set-json`
for exact large-number handling.

Prefer `--set-json` whenever the intended type is ambiguous or the value is
structured (objects/arrays) — it has no auto-detection surprises.

### `--set-file` semantics

The **parser itself stays pure**: `ParseSetFile` takes a `path=content`
string where `content` has *already been read* by the CLI layer and is
preserved **literally** — no comma-splitting (content may contain commas or
newlines), no escape processing, no type detection.

Reading the file (or the one explicit stdin source, e.g. `--set-file
key=-`) is a **client-side CLI concern for a later bead**: bounded read
size, exactly one stdin source per invocation, and no implicit "read
whatever is on this path" fallback. This document intentionally does not
specify that reading behavior beyond the bound already reserved in the
parser (`MaxFileValueBytes`, currently 1 MiB) — the reading bead should
treat that as the hard ceiling, not a target.

## Precedence

- **Same exact path, repeated**: last occurrence wins, in the order
  assignments were supplied on the command line (across possibly multiple
  `--set`/`--set-string`/`--set-json` flags).
- **Mixed modes at the same exact path**: the same rule applies —
  precedence is purely by **argument order**, not by mode. `--set a=1
  --set-json a=2` results in `a` being the JSON-decoded `2`; reversing the
  flag order reverses the winner.
- **Ancestor/descendant conflicts are rejected, not resolved**: assigning
  both `a.b=1` and `a=2` (in either order) is an error — it's ambiguous
  whether `a` should be a scalar or a map containing `b`. This also covers
  scalar-vs-object conflicts at the same path family. There is no implicit
  "deepest wins" or "shallowest wins" rule; the CLI must reject and ask the
  user to disambiguate.

## `null`, empty list, and absent — three distinct outcomes

- **Absent**: the path was never mentioned; the stored value (if any) is
  untouched.
- **`null`**: an explicit value, `--set foo=null` (or `--set-json
  foo=null`). This **sets** the field to `null` — it does **not** delete
  the key or reset it to a schema default. Deleting a key is out of scope
  for this contract (no delete verb exists in this design).
- **Empty list**: `--set foo={}` or `--set-json foo=[]` sets an explicit
  empty array, distinct from both `null` and "field absent".

## Stored vs. effective view (target semantics)

This is the intended contract for `config get`, not implemented in this
bead:

- **Default view is "stored"**: `config get web.port` reads back exactly
  what is persisted in the configuration store, with no merging of
  defaults, environment overrides, or in-memory runtime state.
- **`--effective` is opt-in**: `config get web.port --effective` reads the
  fully merged, currently-active value (defaults + file + environment +
  runtime overrides), matching what the running server actually uses.
- Both views use the same path grammar described in this document.

## Output and exit-code contract (target)

- **Output modes**: `--output json|yaml`, or an explicitly requested scalar
  mode for single-leaf values (e.g. printing a bare string/number without
  JSON/YAML quoting). Default output mode is left to the implementing bead,
  but must be one of these three — never a bespoke ad hoc format.
- **Exit codes**:
  - `0`: success (value found and printed, or write applied)
  - nonzero, distinct code: path not found (`config get` on a stored path
    that doesn't exist and `--effective` wasn't requested)
  - nonzero, distinct code: parse or validation error (malformed
    `--set`/`--set-string`/`--set-json`/`--set-file` syntax, limit
    exceeded, or an ancestor/descendant conflict)

The exact numeric codes are left to the implementing bead; the contract is
that these three outcomes are **distinguishable** by exit code, not just by
stderr text.

## Limits (enforced by the parser, fuzz-tested)

| Limit                  | Constant               | Value      |
| ----------------------- | ---------------------- | ---------- |
| Raw `--set*` flag bytes | `MaxRawInputBytes`      | 16 KiB     |
| `--set-file` content    | `MaxFileValueBytes`     | 1 MiB      |
| Path segments           | `MaxPathDepth`          | 32         |
| One key segment length  | `MaxKeySegmentLength`   | 256        |
| Array index value       | `MaxArrayIndex`         | 10000      |
| Brace-list / JSON array | `MaxListLength`         | 1000       |
| JSON object keys        | `MaxJSONObjectKeys`     | 1000       |
| JSON nesting depth      | `MaxJSONNestingDepth`   | 32         |
| Assignments per OpSet   | `MaxOperations`         | 500        |

Exceeding any limit produces a typed limit error rather than panicking or
allocating unboundedly; this is exercised by the package's native Go fuzz
tests.

## Deliberate deviations from Helm's `--set` grammar

Mitto does **not** depend on `helm.sh/helm/v3/pkg/strvals` (out of scope,
would need separate dependency approval). The implemented subset is
Helm-*inspired*, not Helm-*compatible* in every corner case:

- **No implicit list-append or map-merge operators** — Helm's `--set` has
  none either, but some Helm-adjacent tools do; this parser doesn't.
- **`null` is a value, never a delete**. (Helm's `--set` uses `null` to
  *remove* a key when merging with `--values` files — a values-merge
  concept that doesn't apply here, since there is no base values file being
  overlaid.)
- **Leading-zero tokens are always strings**, never silently reinterpreted
  as octal or stripped of the leading zero.
- **Negative and sparse array indexes are rejected outright** rather than
  silently padding the array with nulls up to the requested index (Helm
  pads sparse indexes).
- **Ancestor/descendant path conflicts are hard errors**, not "last
  assignment wins" the way Helm generally overlays maps as it goes.
- **Explicit operation/byte/nesting limits** are enforced; Helm's parser has
  no such caps (git blame shows the closest analog is Kubernetes'
  general apiserver request-size ceiling, but that's a caller-side bound,
  not part of the strvals grammar itself).

## zsh quoting

Brackets and braces are shell-special in zsh (glob patterns). Always quote
`--set`/`--set-string`/`--set-json` arguments:

```zsh
mitto config set --set 'task_label_colors[0].color=#ef4444'
mitto config set --set 'colors={red,green,blue}'
mitto config set --set-json 'shortcuts={"icon":"star","prompt":"review"}'
mitto config set --set-string 'shortcuts.color=rgb(0\,128\,255)'
```

Without quoting, zsh will attempt to expand `[0]` and `{red,green,blue}` as
glob/brace patterns before Mitto ever sees the argument.
