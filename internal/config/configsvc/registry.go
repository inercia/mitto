package configsvc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/config/configpath"
)

// Mutability classifies how a registered field may be touched through this
// service. Paths that don't match any registry entry are implicitly
// "unregistered" and always fail as ErrKindUnknownField on write (reads
// simply pass them through unredacted).
type Mutability int

const (
	// MutUnregistered is the zero value; never assigned to a real entry.
	MutUnregistered Mutability = iota
	// MutSettableV1 fields may be written via Mutate in this bead's scope.
	MutSettableV1
	// MutReadOnlyEffective fields are valid, known paths but only available
	// in the effective (merged) view — not stored, not settable in v1
	// (e.g. acp_servers, which the RC file can override at load time).
	MutReadOnlyEffective
	// MutRejected fields are credential/system fields never writable here.
	MutRejected
)

// Liveness classifies whether a settable field's change is picked up by a
// running server without a restart. Purely informational metadata for
// future consumers (e.g. a CLI/REST layer warning the user); this package
// never talks to a running process.
type Liveness int

const (
	LivenessUnspecified Liveness = iota
	LivenessLive
	LivenessRequiresRestart
)

// FieldEntry is one row of the supported-field registry.
type FieldEntry struct {
	// Path is the canonical dotted settings.json path (json-tag vocabulary,
	// e.g. "web.port", "task_label_colors"), matching docs/config/config-cli.md.
	Path string
	// Dynamic entries match the exact root key at ANY depth/index below it
	// (used for map/list/object-shaped fields: shortcuts, task_label_colors,
	// ui, mcp, acp_servers). Non-dynamic entries match only the exact path.
	Dynamic    bool
	Mutability Mutability
	// Redact marks this field's value as a secret: never returned unredacted
	// by a snapshot read, and its presence forces reject-on-write regardless
	// of Mutability (defense in depth; Mutability is also MutRejected).
	Redact   bool
	Liveness Liveness
	// Validate checks a candidate new value (already JSON-shaped:
	// nil/bool/int64/float64/string/[]interface{}/map[string]interface{})
	// for a MutSettableV1 entry. It must itself reject unknown nested keys.
	// Nil for entries that are never validated (e.g. MutRejected).
	Validate func(v interface{}) error
}

// Registry is the single source of truth for which settings.json paths this
// service knows about, and what may be done with each.
type Registry struct {
	exact        map[string]*FieldEntry
	dynamicRoots map[string]*FieldEntry
}

// NewRegistry builds the v1 supported-field registry.
func NewRegistry() *Registry {
	r := &Registry{exact: map[string]*FieldEntry{}, dynamicRoots: map[string]*FieldEntry{}}

	r.addDynamic("task_label_colors", MutSettableV1, false, LivenessLive, validateTaskLabelColors)
	r.addDynamic("shortcuts", MutSettableV1, false, LivenessLive, validateShortcuts)
	r.addDynamic("ui", MutSettableV1, false, LivenessLive, validateUIConfig)

	r.addExact("web.port", MutSettableV1, false, LivenessRequiresRestart, validateWebPort)
	r.addExact("web.external_port", MutSettableV1, false, LivenessRequiresRestart, validateWebExternalPort)

	// Credential/system fields: redact on read, reject on write.
	r.addExact("web.auth.simple.password", MutRejected, true, LivenessUnspecified, nil)
	r.addExact("web.auth.shared_token", MutRejected, true, LivenessUnspecified, nil)
	// MCPConfig has no credential fields today, but is reserved system
	// config (bind host/port for the always-on MCP server); redact+reject
	// the whole subtree defensively per the bead spec ("MCP creds").
	r.addDynamic("mcp", MutRejected, true, LivenessUnspecified, nil)

	// Read-only effective: valid paths, but the RC file can override
	// acp_servers at load time and this service never writes it in v1.
	r.addDynamic("acp_servers", MutReadOnlyEffective, false, LivenessUnspecified, nil)

	return r
}

func (r *Registry) addExact(path string, m Mutability, redact bool, live Liveness, validate func(interface{}) error) {
	r.exact[path] = &FieldEntry{Path: path, Mutability: m, Redact: redact, Liveness: live, Validate: validate}
}

func (r *Registry) addDynamic(root string, m Mutability, redact bool, live Liveness, validate func(interface{}) error) {
	r.dynamicRoots[root] = &FieldEntry{Path: root, Dynamic: true, Mutability: m, Redact: redact, Liveness: live, Validate: validate}
}

// Lookup returns the registry entry governing path, if any. Exact matches
// (fixed leaf fields like "web.port") win over a dynamic root match on the
// same first segment.
func (r *Registry) Lookup(path configpath.Path) (*FieldEntry, bool) {
	if len(path) == 0 {
		return nil, false
	}
	if e, ok := r.exact[path.String()]; ok {
		return e, true
	}
	if path[0].IsIndex {
		return nil, false
	}
	if e, ok := r.dynamicRoots[path[0].Key]; ok {
		return e, true
	}
	return nil, false
}

// DynamicRoot returns the dynamic entry registered exactly at root key, if any.
func (r *Registry) DynamicRoot(rootKey string) (*FieldEntry, bool) {
	e, ok := r.dynamicRoots[rootKey]
	return e, ok
}

var hexColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func decodeStrict(v interface{}, out interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("invalid value: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	return nil
}

func validateTaskLabelColors(v interface{}) error {
	var entries []config.TaskLabelColor
	if err := decodeStrict(v, &entries); err != nil {
		return fmt.Errorf("must be a list of {label, color} objects: %w", err)
	}
	for _, e := range entries {
		if strings.TrimSpace(e.Label) == "" {
			return fmt.Errorf("label must not be empty")
		}
		if !hexColorRE.MatchString(e.Color) {
			return fmt.Errorf("color %q must be a 6-digit hex code (#rrggbb)", e.Color)
		}
	}
	return nil
}

func validateShortcuts(v interface{}) error {
	var sections map[string][]config.ShortcutButton
	if err := decodeStrict(v, &sections); err != nil {
		return fmt.Errorf("must be a map of section -> [{icon, prompt}]: %w", err)
	}
	for section, buttons := range sections {
		if strings.TrimSpace(section) == "" {
			return fmt.Errorf("shortcut section name must not be empty")
		}
		for _, b := range buttons {
			if strings.TrimSpace(b.Prompt) == "" {
				return fmt.Errorf("shortcut in section %q missing prompt", section)
			}
		}
	}
	return nil
}

func validateUIConfig(v interface{}) error {
	var ui config.UIConfig
	if err := decodeStrict(v, &ui); err != nil {
		return fmt.Errorf("invalid ui config: %w", err)
	}
	return nil
}

func validateWebPort(v interface{}) error {
	n, ok := asInt(v)
	if !ok {
		return fmt.Errorf("must be an integer")
	}
	if n < 1 || n > 65535 {
		return fmt.Errorf("must be between 1 and 65535")
	}
	return nil
}

func validateWebExternalPort(v interface{}) error {
	n, ok := asInt(v)
	if !ok {
		return fmt.Errorf("must be an integer")
	}
	if n < -1 || n > 65535 {
		return fmt.Errorf("must be -1 (disabled), 0 (random), or between 1 and 65535")
	}
	return nil
}

func asInt(v interface{}) (int64, bool) {
	switch t := v.(type) {
	case int64:
		return t, true
	case float64:
		if t == float64(int64(t)) {
			return int64(t), true
		}
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n, true
		}
	}
	return 0, false
}
