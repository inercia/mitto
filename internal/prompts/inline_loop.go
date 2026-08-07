package prompts

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/inercia/mitto/internal/prompts/migrate"
)

// DecodeInlineLoop decodes a loop: value node that was embedded inline in a
// non-.prompt.yaml config file (a workspace .mittorc "prompts:" entry, or a
// settings.yaml "prompts:"/ACP-server "prompts:" entry) into a *PromptLoop.
//
// Unlike the on-disk .prompt.yaml loader (ParsePromptFile), these call sites
// previously decoded loop: directly into a *PromptLoop field, so a pre-r6j
// flat-schema block hit PromptLoop.UnmarshalYAML's strict rejection and
// hard-failed the *entire* file's load (mitto-opoh). DecodeInlineLoop instead
// runs the same mitto-r6j.3 migration registry used for prompt files against
// a synthetic "loop:" document built around node, so a legacy block is
// rewritten in memory (like ParsePromptFile does) instead of hard-failing.
//
// There is no on-disk write-back here: the migration registry's line-splice
// write-back (migrate.MigrateYAML) only locates a *top-level* loop: key,
// which does not apply to a loop: block nested inside a prompts: sequence
// entry, and rewriting a multi-section user config file (.mittorc,
// settings.yaml) surgically is out of scope. Callers should log a WARN when
// migrated.Changed is true, naming the source file/prompt so the operator
// can hand-migrate the on-disk block onto the grouped schema.
//
// Returns an error if node is not a mapping or fails strict validation after
// migration; callers should treat that as "drop this prompt's loop config"
// (or, for the two hard-fail-prone sites, WARN + fall back to no loop)
// rather than failing the whole file load.
func DecodeInlineLoop(node *yaml.Node) (*PromptLoop, migrate.Result, error) {
	if node == nil {
		return nil, migrate.Result{}, nil
	}

	// Wrap node in a synthetic top-level "loop:" document so the migration
	// registry's findTopLevelPair (which expects a DocumentNode whose
	// Content[0] is a MappingNode with the target key at the root) can see
	// it, exactly as it would for a real .prompt.yaml file's top-level loop:.
	loopKey := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "loop"}
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{loopKey, node}}
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}

	result, err := migrate.Migrate(doc)
	if err != nil {
		return nil, migrate.Result{}, fmt.Errorf("loop migration failed: %w", err)
	}

	var loop PromptLoop
	if err := node.Decode(&loop); err != nil {
		return nil, result, err
	}
	if err := ValidatePromptLoop("", &loop); err != nil {
		return nil, result, err
	}
	return &loop, result, nil
}
