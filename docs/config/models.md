# Model Profiles (`models:`)

Model profiles pair a **model-selection criteria** with **capability tags**, configured
under a top-level `models:` list. Profiles are parsed, stored, exposed through an
internal Go API, **and consumed at runtime**: the current model's capability tags are
available to prompts and processors via the `Model("tag")` template function, the
`Session.HasModelTag("tag")` CEL macro, and the `"tag" in Session.ModelTags` membership
expression (see [Consumed at runtime](#consumed-at-runtime) below). All three are
populated from `config.ResolveModelTags`.

## Shipped defaults (first install only)

New installs are seeded with a set of well-known profiles from the embedded
`config/config.default.yaml` (written to `settings.json` on the first run; existing
installs are left untouched). All use `matchMode: contains`, so they are
version-agnostic, and their tags **union** across overlapping matches:

| Profile | Pattern | Tags |
|---------|---------|------|
| Claude | `Claude` | `Anthropic` |
| Claude Opus | `Opus` | `Smartest`, `Reasoning`, `Expensive` |
| Claude Sonnet | `Sonnet` | `Smart`, `Coding` |
| Claude Haiku | `Haiku` | `Fast`, `Cheap` |
| GPT-5 | `GPT-5` | `Smart`, `Reasoning`, `Coding` |
| GPT-4 | `GPT-4` | `Smart`, `Coding` |
| Gemini | `Gemini` | `Smart`, `LongContext` |

Because matching is additive, a name like `Claude Opus 4.x` resolves to the union of
the vendor-level `Claude` profile and the `Claude Opus` profile
(`Anthropic`, `Smartest`, `Reasoning`, `Expensive`). Edit or remove these in your
`settings.json` (or the Models settings tab) to suit the models you use.

## YAML Configuration

The `models:` section is a top-level list in your configuration (settings or YAML).
Each entry is a profile with a `name`, a `criteria` block, and a list of `tags`:

```yaml
models:
  - name: Opus
    criteria:
      matchMode: contains
      pattern: Opus
    tags: [Smartest, Expensive]
  - name: Sonnet
    criteria:
      matchMode: contains
      pattern: Sonnet
    tags: [Smart, Cheap]
```

### Fields

| Field | Required | Type | Description |
|-------|----------|------|-------------|
| `name` | Yes | string | Display name of the profile. Profiles without a name are skipped. |
| `criteria` | No | object | How to match a model. A profile with no criteria never matches (and so contributes no tags). |
| `criteria.matchMode` | Yes (if criteria set) | string | One of `contains`, `exact`, `startsWith`, `regex`, `lookAlike`. |
| `criteria.pattern` | Yes (if criteria set) | string | The pattern compared against the model's display name. |
| `tags` | No | list of string | Capability tags associated with this profile. |

### Match Modes

Matching is **case-insensitive** and reuses `config.ConstraintMatchesName` — the same
engine as ACP-server model constraints (see [ACP Servers](acp.md)):

| Mode | Matches when the model name… |
|------|------------------------------|
| `contains` | contains the pattern as a substring |
| `exact` | equals the pattern exactly |
| `startsWith` | starts with the pattern |
| `regex` | matches the pattern as a regular expression (`(?i)` applied) |
| `lookAlike` | contains every whitespace-separated word of the pattern |

## Internal Go API

The following methods are available on `*config.Config`
(defined in `internal/config/config.go`):

**`ModelProfileByName(name string) (*ModelProfile, bool)`**
Case-insensitive lookup by profile name. Returns the matching profile and `true`,
or `nil, false` when no profile has that name.

**`ModelProfilesByTag(tag string) []ModelProfile`**
Returns all profiles carrying the given tag (case-insensitive). Returns an empty
slice when no profiles match.

**`ResolveModelTags(modelName string) []string`**
Matches `modelName` against every profile's `criteria` and returns the de-duplicated
(case-insensitive) union of the matching profiles' tags. Returns an empty slice when
the model is unknown or no profile matches; never errors.

**`config.ConstraintMatchesName(c *ACPServerConstraint, name string) bool`**
The shared match engine used by `ResolveModelTags`. Returns `false` when `c` is nil.

## Consumed at runtime

The current model's capability tags (resolved via `ResolveModelTags`) are exposed to
both **prompts** and **processors**, populated at menu time and at send time so the two
agree:

- **Template function** — `{{ Model "tag" }}` returns `true` when the current model
  carries `tag` (case-insensitive); `false` when the model is unknown or no profile
  matches.
- **CEL macro** — `Session.HasModelTag("tag")` (mirrors `Tools.HasPattern`), usable in
  `enabledWhen` to gate a prompt or processor on the active model.
- **CEL membership** — `"tag" in Session.ModelTags`, where `Session.ModelTags` is the
  list of the current model's tags (`[]` when unknown).

Tags reflect the session's **baseline/active** model at render time, not a prompt's
`preferredModels` (which apply after render). Membership is case-insensitive and
degrades to an empty set when the model is unknown (cold start / suspended session) or
no profile matches.

See [prompt-templates.md](../devel/prompt-templates.md) (context schema table and the
`Model` function) and [prompts.md](prompts.md) (`enabledWhen` with
`Session.HasModelTag`) for the canonical reference.

## Referenced by prompts (`preferredModels`)

Prompts may declare a `preferredModels:` list to steer model selection at prompt
dispatch. Each entry is a **structured reference to a profile** with **exactly one**
of `modelName` / `modelTag`:

```yaml
preferredModels:
  - modelName: Claude Sonnet   # matches a profile by its `name` (case-insensitive)
  - modelTag: Coding           # selects any profile carrying this tag
```

- **`modelName`** — case-insensitive equality against the profile's `name`.
- **`modelTag`** — matches any profile carrying that tag. When several profiles share
  the tag, resolution is **deterministic by profile order** in the `models:` list
  (first profile with the tag wins). Given the shipped defaults above, the tag-based
  entries in the builtin prompts resolve as follows:
  - `Coding` → first hit is `Claude Sonnet` (also on `GPT-5`, `GPT-4`).
  - `Cheap` → `Claude Haiku`.
  - `Smart`, `Smartest`, `Reasoning`, `Fast`, `LongContext`, `Anthropic`,
    `Expensive` are also available; see the shipped defaults table.
- Entries are **ordered, first-match-wins**. The backend tries each entry in order
  and stops at the first that resolves to a profile whose `criteria` match an
  available model on the session's ACP server.
- If the current model **already satisfies** the resolved profile, it is kept — no
  needless model switch. Otherwise the preference is applied.

The old glob-pattern form (`- "*sonnet*"`) has been removed. See
[.augment/rules/07-prompts.md § preferredModels Field](../../.augment/rules/07-prompts.md)
for the internal implementation notes.

## See also

- [ACP Servers / Model Selection Constraints](acp.md) — shares the same match engine
- [Prompt Templates](../devel/prompt-templates.md) — context schema, `Model` function,
  `Session.ModelTags` / `Session.HasModelTag`
- [Prompts](prompts.md) — `enabledWhen` gating with `Session.HasModelTag` /
  `"tag" in Session.ModelTags`
