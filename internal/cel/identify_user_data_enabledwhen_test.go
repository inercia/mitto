package cel

import "testing"

// TestCELEvaluator_IdentifyUserDataEnabledWhen is a regression test for
// mitto-685 ("Gate identify-user-data auxiliary processor on missing work").
// It pins the exact literal enabledWhen expression from
// config/processors/builtin/identify-user-data.yaml and exercises every
// clause of the conjunction:
//
//   - Workspace.HasUserDataSchema must be true (no schema → nothing to
//     identify).
//   - !Session.IsLoop — loop prompts never trigger the auxiliary processor.
//   - Workspace.MissingUserDataFieldCount > 0 — the new admission-control
//     gate: once every schema field already has a value, the processor must
//     NOT be dispatched, even though a schema exists and this isn't a loop.
//
// A future edit that drops the new clause (or flips its sense) would
// silently reintroduce the wasted-token dispatch this bead fixes; this test
// fails loudly instead.
func TestCELEvaluator_IdentifyUserDataEnabledWhen(t *testing.T) {
	const identifyUserDataExpr = `Workspace.HasUserDataSchema && !Session.IsLoop && Workspace.MissingUserDataFieldCount > 0`

	e := newTestEvaluator(t)
	ce := compile(t, e, identifyUserDataExpr)

	schemaWS := WorkspaceContext{HasUserDataSchema: true, MissingUserDataFieldCount: 2}
	schemaResolvedWS := WorkspaceContext{HasUserDataSchema: true, MissingUserDataFieldCount: 0}
	noSchemaWS := WorkspaceContext{HasUserDataSchema: false, MissingUserDataFieldCount: 0}

	tests := []struct {
		name string
		ctx  *PromptEnabledContext
		want bool
	}{
		{
			name: "schema present, unresolved fields remain, not a loop -> dispatch",
			ctx:  &PromptEnabledContext{Workspace: schemaWS, Session: SessionContext{IsLoop: false}},
			want: true,
		},
		{
			name: "schema present but every field already resolved -> skip (mitto-685 gate)",
			ctx:  &PromptEnabledContext{Workspace: schemaResolvedWS, Session: SessionContext{IsLoop: false}},
			want: false,
		},
		{
			name: "schema present, unresolved fields remain, but this is a loop prompt -> skip",
			ctx:  &PromptEnabledContext{Workspace: schemaWS, Session: SessionContext{IsLoop: true}},
			want: false,
		},
		{
			name: "no schema at all -> skip regardless of MissingUserDataFieldCount",
			ctx:  &PromptEnabledContext{Workspace: noSchemaWS, Session: SessionContext{IsLoop: false}},
			want: false,
		},
		{
			name: "no schema, loop prompt, no missing fields -> skip (all clauses false)",
			ctx:  &PromptEnabledContext{Workspace: noSchemaWS, Session: SessionContext{IsLoop: true}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evaluate(t, e, ce, tt.ctx)
			if got != tt.want {
				t.Errorf("Evaluate(identifyUserDataExpr) = %v, want %v", got, tt.want)
			}
		})
	}
}
