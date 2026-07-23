package cel

import (
	"reflect"
	"testing"
)

// TestDocsTemplateAccessorsExistOnContext pins the docs↔code contract for
// mitto-mi4: every template accessor advertised in docs/config/prompts.md and
// docs/devel/prompt-templates.md under Iteration.* and Trigger.OnTasks.Changes.*
// must resolve to a real exported field on the corresponding context struct.
// A silent rename in internal/cel/context.go breaks this test instead of the
// docs drifting.
func TestDocsTemplateAccessorsExistOnContext(t *testing.T) {
	cases := []struct {
		name   string
		typ    reflect.Type
		fields []string
	}{
		{
			name: "IterationContext",
			typ:  reflect.TypeOf(IterationContext{}),
			fields: []string{
				"Number", "Max", "IsLoop", "IsFirst", "IsLast", "IsUninterrupted",
			},
		},
		{
			name: "TasksChangesView",
			typ:  reflect.TypeOf(TasksChangesView{}),
			fields: []string{
				"Added", "Updated", "Removed", "Closed", "Reopened", "LabelAdded", "Touched",
			},
		},
		{
			name:   "TriggerContext",
			typ:    reflect.TypeOf(TriggerContext{}),
			fields: []string{"OnTasks"},
		},
		{
			name:   "TriggerOnTasksContext",
			typ:    reflect.TypeOf(TriggerOnTasksContext{}),
			fields: []string{"Changes"},
		},
	}

	for _, c := range cases {
		for _, f := range c.fields {
			if _, ok := c.typ.FieldByName(f); !ok {
				t.Errorf("%s: docs reference .%s but no such exported field on %s (fix docs or restore field)", c.name, f, c.typ.Name())
			}
		}
	}
}
