package cel

import "testing"

func TestCELEvaluator_BeadsDatabaseMode(t *testing.T) {
	e := newTestEvaluator(t)
	local := compile(t, e, `Workspace.BeadsDatabaseMode == "local"`)
	shared := compile(t, e, `Workspace.BeadsDatabaseMode == "shared"`)

	for _, tc := range []struct {
		mode       string
		wantLocal  bool
		wantShared bool
	}{
		{mode: "local", wantLocal: true},
		{mode: "shared", wantShared: true},
		{mode: ""},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			ctx := &PromptEnabledContext{Workspace: WorkspaceContext{BeadsDatabaseMode: tc.mode}}
			if got := evaluate(t, e, local, ctx); got != tc.wantLocal {
				t.Errorf("local expression = %v, want %v", got, tc.wantLocal)
			}
			if got := evaluate(t, e, shared, ctx); got != tc.wantShared {
				t.Errorf("shared expression = %v, want %v", got, tc.wantShared)
			}
		})
	}
}
