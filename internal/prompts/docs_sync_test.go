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
			fields: []string{"reuseIssue", "title", "reuseTitle", "reuseCoalesce"},
		},
		{
			name: "PromptLoop",
			typ:  reflect.TypeOf(PromptLoop{}),
			fields: []string{
				"coalesceDuringBusy", "freshContext", "runOnStart",
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
