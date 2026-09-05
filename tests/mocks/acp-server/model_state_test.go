package main

import "testing"

func TestModelChangesAreSessionScoped(t *testing.T) {
	s := &MockACPServer{sessions: map[string]*SessionState{
		"a": {ConfigOptions: []SessionConfigOption{buildModelConfigOption(defaultModelId)}},
		"b": {ConfigOptions: []SessionConfigOption{buildModelConfigOption(defaultModelId)}},
	}}
	for _, tc := range []struct{ session, model, other string }{
		{"a", "claude-opus-4-6", "b"},
		{"b", "claude-haiku-4-5", "a"},
	} {
		before := s.sessionModel(tc.other)
		if err := s.applyModelChange(JSONRPCRequest{}, tc.session, tc.model, "set_model"); err != nil {
			t.Fatal(err)
		}
		if got := s.sessionModel(tc.session); got != tc.model {
			t.Fatalf("session %s model=%q, want %q", tc.session, got, tc.model)
		}
		if got := s.sessionModel(tc.other); got != before {
			t.Fatalf("changing %s affected %s: %q -> %q", tc.session, tc.other, before, got)
		}
	}
}
