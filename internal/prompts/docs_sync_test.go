package prompts

import (
	"reflect"
	"strings"
	"testing"
)

// TestDocsFrontmatterFieldsExistOnStructs pins the docs↔code contract for
// mitto-mi4: every YAML frontmatter field advertised in docs/config/prompts.md
// must resolve to a real yaml: tag on the corresponding struct, so a silent
// rename in the Go source breaks this test instead of the docs drifting.
func TestDocsFrontmatterFieldsExistOnStructs(t *testing.T) {
	cases := []struct {
		name   string
		typ    reflect.Type
		fields []string
	}{
		{
			name: "PromptFile",
			typ:  reflect.TypeOf(PromptFile{}),
			fields: []string{
				"target", "preferredModels", "loop", "parameters",
				"singleton", "enabledWhen",
			},
		},
		{
			name:   "PromptTarget",
			typ:    reflect.TypeOf(PromptTarget{}),
			fields: []string{"title", "reuse"},
		},
		{
			// mitto-6b3: nested reuse block under target.reuse.
			name:   "PromptTargetReuse",
			typ:    reflect.TypeOf(PromptTargetReuse{}),
			fields: []string{"issue", "title", "coalesce"},
		},
		{
			name: "PromptLoop",
			typ:  reflect.TypeOf(PromptLoop{}),
			fields: []string{
				"trigger", "schedule", "onCompletion", "onTasks", "onChild",
				"freshContext", "runOnStart",
			},
		},
		{
			// mitto-r6j.1: grouped schedule-trigger attributes.
			name:   "PromptLoopSchedule",
			typ:    reflect.TypeOf(PromptLoopSchedule{}),
			fields: []string{"value", "unit", "at"},
		},
		{
			// mitto-r6j.1: grouped onCompletion-trigger attributes.
			name:   "PromptLoopOnCompletion",
			typ:    reflect.TypeOf(PromptLoopOnCompletion{}),
			fields: []string{"delay"},
		},
		{
			// mitto-r6j.1: grouped onTasks-trigger attributes.
			name: "PromptLoopOnTasks",
			typ:  reflect.TypeOf(PromptLoopOnTasks{}),
			fields: []string{
				"condition", "conditionPreset", "coalesceDuringBusy",
				"settleWindow", "cooldown",
			},
		},
		{
			// mitto-987y.2: grouped onChild-trigger attributes.
			name:   "PromptLoopOnChild",
			typ:    reflect.TypeOf(PromptLoopOnChild{}),
			fields: []string{"when"},
		},
		{
			// mitto-boio: closes a docs-sync coverage gap for the parameters:
			// entry schema documented under "parameters (Typed Inputs &
			// Type-Based Gating)" — this struct had no case here before.
			name: "PromptParameter",
			typ:  reflect.TypeOf(PromptParameter{}),
			fields: []string{
				"name", "type", "description", "required", "show", "default",
				"dir", "glob", "multiLine", "options", "remember",
				"collectInnerArgs", "group",
			},
		},
	}

	for _, c := range cases {
		tags := yamlTagSet(c.typ)
		for _, f := range c.fields {
			if _, ok := tags[f]; !ok {
				t.Errorf("%s: docs/config/prompts.md references yaml:%q but no such tag on %s (fix docs or restore tag)", c.name, f, c.typ.Name())
			}
		}
	}
}

// yamlTagSet returns the set of yaml tag names (before any `,omitempty`) on
// the given struct type. Anonymous embeds are traversed one level; skipped
// tags ("-" or empty) are omitted.
func yamlTagSet(t reflect.Type) map[string]struct{} {
	out := map[string]struct{}{}
	if t.Kind() != reflect.Struct {
		return out
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			for k := range yamlTagSet(f.Type) {
				out[k] = struct{}{}
			}
			continue
		}
		tag := f.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.SplitN(tag, ",", 2)[0]
		if name == "" {
			continue
		}
		out[name] = struct{}{}
	}
	return out
}
