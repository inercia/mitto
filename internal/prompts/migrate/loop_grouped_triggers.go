package migrate

import "gopkg.in/yaml.v3"

// loopLegacyKeyBlock maps each pre-r6j flat key (formerly valid directly
// under loop:) to the grouped trigger block it now belongs to. Mirrors
// internal/prompts.legacyPromptLoopFlatKeys, which is what rejects any of
// these keys still present at loop-load time once this migration has NOT
// been run against a file (e.g. because the migration itself is buggy).
var loopLegacyKeyBlock = map[string]string{
	"value":              "schedule",
	"unit":               "schedule",
	"at":                 "schedule",
	"delay":              "onCompletion",
	"condition":          "onTasks",
	"conditionPreset":    "onTasks",
	"coalesceDuringBusy": "onTasks",
	"settleWindow":       "onTasks",
	"cooldown":           "onTasks",
}

// loopTriggerBlocks lists the three trigger-block keys, in the canonical
// output order used when rebuilding loop:'s mapping content.
var loopTriggerBlocks = []string{"schedule", "onCompletion", "onTasks"}

// loopWideKeys enumerates the loop-wide fields that stay directly under
// loop: regardless of which trigger(s) are active.
var loopWideKeys = map[string]bool{
	"maxIterations": true,
	"maxDuration":   true,
	"freshContext":  true,
	"runOnStart":    true,
	"mode":          true,
	"default":       true,
}

func init() {
	Register(loopGroupedTriggersMigration{})
}

// loopGroupedTriggersMigration is migration "0001-loop-grouped-triggers": it
// rewrites the pre-r6j flat loop: schema (single implicit trigger, flat
// value/unit/at/delay/condition/... siblings) onto the grouped multi-trigger
// schema (trigger: [...] plus nested schedule/onCompletion/onTasks blocks)
// landed by mitto-r6j.1. See internal/prompts.PromptLoop doc comment for the
// full schema description.
type loopGroupedTriggersMigration struct{}

// ID implements Migration.
func (loopGroupedTriggersMigration) ID() string { return "0001-loop-grouped-triggers" }

// Applies implements Migration.
func (loopGroupedTriggersMigration) Applies(doc *yaml.Node) bool {
	_, val := findTopLevelPair(doc, "loop")
	if val == nil || val.Kind != yaml.MappingNode {
		return false
	}
	return loopNeedsMigration(val)
}

// Apply implements Migration.
func (loopGroupedTriggersMigration) Apply(doc *yaml.Node) (bool, error) {
	_, val := findTopLevelPair(doc, "loop")
	if val == nil || val.Kind != yaml.MappingNode {
		return false, nil
	}
	if !loopNeedsMigration(val) {
		return false, nil
	}
	migrateLoopNode(val)
	return true, nil
}

// loopNeedsMigration reports whether loop (the loop: mapping value node)
// still carries any pre-r6j flat key, or an unconverted scalar trigger:.
func loopNeedsMigration(loop *yaml.Node) bool {
	for i := 0; i+1 < len(loop.Content); i += 2 {
		key := loop.Content[i]
		if key.Kind != yaml.ScalarNode {
			continue
		}
		if _, ok := loopLegacyKeyBlock[key.Value]; ok {
			return true
		}
		if key.Value == "trigger" && loop.Content[i+1].Kind == yaml.ScalarNode {
			return true
		}
	}
	return false
}

// migrateLoopNode rewrites loop's Content in place onto the grouped schema:
//   - a scalar trigger: becomes a single-element flow-style list; an absent
//     trigger: is synthesized from whichever block ends up non-empty
//     (schedule > onCompletion > onTasks precedence, matching the mapping
//     table order), or left absent if none apply;
//   - each legacy flat key is moved into its target block (merged into an
//     existing block mapping if one is already present, never replacing it);
//   - loop-wide keys (maxIterations, mode, ...) and any unrecognized key are
//     left as direct loop: children, in their original relative order;
//   - the final key order is: trigger, schedule, onCompletion, onTasks, then
//     the remaining loop-wide/unknown keys.
//
// Every moved/converted node keeps its original *yaml.Node object (so
// HeadComment/LineComment/FootComment and scalar Style/Tag are preserved
// verbatim) — only the containing mapping/sequence wrappers are freshly
// created.
func migrateLoopNode(loop *yaml.Node) {
	var triggerKey, triggerVal *yaml.Node
	blockKey := map[string]*yaml.Node{}
	blockVal := map[string]*yaml.Node{}
	movedPairs := map[string][]*yaml.Node{}
	var otherPairs []*yaml.Node // loop-wide + unrecognized keys, original order

	for i := 0; i+1 < len(loop.Content); i += 2 {
		key, val := loop.Content[i], loop.Content[i+1]
		name := key.Value
		switch {
		case name == "trigger":
			triggerKey, triggerVal = key, val
		case name == "schedule" || name == "onCompletion" || name == "onTasks":
			blockKey[name] = key
			blockVal[name] = val
		case loopWideKeys[name]:
			otherPairs = append(otherPairs, key, val)
		default:
			if block, ok := loopLegacyKeyBlock[name]; ok {
				movedPairs[block] = append(movedPairs[block], key, val)
			} else {
				otherPairs = append(otherPairs, key, val)
			}
		}
	}

	for _, block := range loopTriggerBlocks {
		pairs := movedPairs[block]
		if len(pairs) == 0 {
			continue
		}
		if existing := blockVal[block]; existing != nil {
			existing.Content = append(existing.Content, pairs...)
			continue
		}
		blockVal[block] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: pairs}
		blockKey[block] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: block}
	}

	triggerKey, triggerVal = normalizeTrigger(triggerKey, triggerVal, blockVal)

	out := make([]*yaml.Node, 0, len(loop.Content))
	if triggerKey != nil {
		out = append(out, triggerKey, triggerVal)
	}
	for _, block := range loopTriggerBlocks {
		if blockVal[block] != nil {
			out = append(out, blockKey[block], blockVal[block])
		}
	}
	out = append(out, otherPairs...)
	loop.Content = out
}

// normalizeTrigger converts an existing scalar trigger: value into a
// single-element flow-style sequence (preserving its comments), or — when
// trigger: is absent — synthesizes one from whichever block is non-empty,
// per the mapping table's precedence (schedule, then onCompletion, then
// onTasks). Returns (nil, nil) when no trigger: should be added.
func normalizeTrigger(key, val *yaml.Node, blockVal map[string]*yaml.Node) (*yaml.Node, *yaml.Node) {
	if val != nil {
		if val.Kind == yaml.ScalarNode {
			val = wrapAsSingletonList(val)
		}
		return key, val
	}

	inferred := ""
	for _, block := range loopTriggerBlocks {
		if blockVal[block] != nil {
			inferred = block
			break
		}
	}
	if inferred == "" {
		return nil, nil
	}
	key = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "trigger"}
	item := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: inferred}
	val = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle, Content: []*yaml.Node{item}}
	return key, val
}

// wrapAsSingletonList wraps a scalar node into a single-element flow-style
// sequence, carrying the scalar's comments onto the new sequence node (a
// mapping value's comments live on the value node itself) so they still
// render on the same logical line/position after the shape change.
func wrapAsSingletonList(scalar *yaml.Node) *yaml.Node {
	item := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: scalar.Value}
	seq := &yaml.Node{
		Kind:        yaml.SequenceNode,
		Tag:         "!!seq",
		Style:       yaml.FlowStyle,
		Content:     []*yaml.Node{item},
		HeadComment: scalar.HeadComment,
		LineComment: scalar.LineComment,
		FootComment: scalar.FootComment,
	}
	return seq
}

// findTopLevelPair returns the key and value nodes for name at the document
// root, or (nil, nil) if doc is not a single-document mapping or name is
// absent.
func findTopLevelPair(doc *yaml.Node, name string) (key, val *yaml.Node) {
	if doc == nil || doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Kind == yaml.ScalarNode && root.Content[i].Value == name {
			return root.Content[i], root.Content[i+1]
		}
	}
	return nil, nil
}
