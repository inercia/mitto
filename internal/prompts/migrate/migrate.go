// Package migrate implements a generic, versioned migration registry for
// .prompt.yaml documents (mitto-r6j.3). Each Migration is a small, idempotent
// rewrite applied to the decoded YAML document (as a *yaml.Node tree) before
// it is unmarshalled into the typed PromptFile struct, so a prompt authored
// against an older schema loads cleanly (with a WARN) instead of hard-failing.
//
// This package intentionally does NOT import internal/prompts — the
// dependency is one-way (internal/prompts imports internal/prompts/migrate)
// to avoid an import cycle, since migrations only ever need the generic
// gopkg.in/yaml.v3 node API, never the typed prompt structs.
//
// Migrations are registered in an ordered, append-only list via Register
// (called from each migration's own init()); Migrate runs every registered
// migration in order, re-checking Applies before each one since an earlier
// migration may change what a later one sees. See writeback.go for the
// companion line-splice logic that turns a changed document back into
// minimally-modified file bytes.
package migrate

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Migration is a single, idempotent, versioned rewrite of a prompt-file YAML
// document. Implementations must be safe to run more than once: once Apply
// has fired, a subsequent Applies call on the same document must return
// false.
type Migration interface {
	// ID is a stable identifier, e.g. "0001-loop-grouped-triggers". Used in
	// WARN log lines and CLI output; never reused or renumbered once shipped.
	ID() string
	// Applies reports whether this migration has anything to do on doc.
	// doc is the root *yaml.Node returned by yaml.Unmarshal into a
	// yaml.Node (i.e. a DocumentNode whose Content[0] is the top-level
	// mapping).
	Applies(doc *yaml.Node) bool
	// Apply rewrites doc in place. changed reports whether anything was
	// actually modified; it should normally mirror Applies, but Apply's
	// return value is authoritative for callers deciding whether to persist
	// a write-back.
	Apply(doc *yaml.Node) (changed bool, err error)
}

// registry is the ordered, append-only list of registered migrations. Order
// matters: later migrations may assume earlier ones already ran.
var registry []Migration

// Register adds a migration to the ordered registry. Intended to be called
// from an init() function in the file that defines the migration, so the
// registry is fully populated before any caller touches it.
func Register(m Migration) {
	registry = append(registry, m)
}

// All returns a copy of the ordered list of registered migrations, e.g. for
// CLI listing or tests.
func All() []Migration {
	out := make([]Migration, len(registry))
	copy(out, registry)
	return out
}

// Result describes the outcome of running Migrate against a document.
type Result struct {
	// Changed is true if at least one migration modified the document.
	Changed bool
	// Fired lists the IDs of migrations that fired, in registration order.
	Fired []string
}

// Migrate runs every registered migration against doc, in registration
// order, mutating it in place. Applies is re-checked immediately before each
// Apply call (not just once up front), since an earlier migration in the
// list may change what a later one sees. Returns which migrations fired.
func Migrate(doc *yaml.Node) (Result, error) {
	var res Result
	for _, m := range registry {
		if !m.Applies(doc) {
			continue
		}
		changed, err := m.Apply(doc)
		if err != nil {
			return res, fmt.Errorf("migration %s: %w", m.ID(), err)
		}
		if changed {
			res.Changed = true
			res.Fired = append(res.Fired, m.ID())
		}
	}
	return res, nil
}
