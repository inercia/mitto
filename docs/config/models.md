# Model Profiles (`models:`)

Model profiles pair a **model-selection criteria** with **capability tags**, configured
under a top-level `models:` list. This is currently an **interface-only** feature:
profiles are parsed, stored, and exposed through an internal Go API, but Mitto does
**not yet** branch on model tags at runtime — there is no prompt-template function,
CEL macro, or processor that consumes them. The Go API below is the intended extension
point for future work.

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

## Not yet consumed at runtime

> **Note:** Profiles are parsed and round-tripped through `Config`/`Settings` and
> exposed via the Go API above, but **nothing in Mitto currently consumes model tags
> at runtime**. There is no prompt-template function, CEL macro, or processor that
> branches on them. This is the intended extension point for future work; contributors
> adding runtime consumption should build on `ResolveModelTags`.

## See also

- [ACP Servers / Model Selection Constraints](acp.md) — shares the same match engine
