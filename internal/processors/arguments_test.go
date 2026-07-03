package processors

import (
	"testing"

	"github.com/inercia/mitto/internal/config"
)

func TestResolveProcessorArgs(t *testing.T) {
	tests := []struct {
		name      string
		params    []config.PromptParameter
		overrides map[string]string
		want      map[string]string
	}{
		{
			name:      "nil params and nil overrides returns nil",
			params:    nil,
			overrides: nil,
			want:      nil,
		},
		{
			name:      "empty params and empty overrides returns nil",
			params:    []config.PromptParameter{},
			overrides: map[string]string{},
			want:      nil,
		},
		{
			name:   "default seeded from params",
			params: []config.PromptParameter{{Name: "ENV", Default: "prod"}},
			want:   map[string]string{"ENV": "prod"},
		},
		{
			name:      "override wins over default when non-empty",
			params:    []config.PromptParameter{{Name: "ENV", Default: "prod"}},
			overrides: map[string]string{"ENV": "staging"},
			want:      map[string]string{"ENV": "staging"},
		},
		{
			name:      "empty override falls back to default",
			params:    []config.PromptParameter{{Name: "ENV", Default: "prod"}},
			overrides: map[string]string{"ENV": ""},
			want:      map[string]string{"ENV": "prod"},
		},
		{
			name:      "extra key in overrides added to map",
			params:    []config.PromptParameter{},
			overrides: map[string]string{"EXTRA": "val"},
			want:      map[string]string{"EXTRA": "val"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveProcessorArgs(tt.params, tt.overrides)
			if len(got) != len(tt.want) {
				t.Fatalf("ResolveProcessorArgs len = %d, want %d; got=%v, want=%v", len(got), len(tt.want), got, tt.want)
			}
			for k, wv := range tt.want {
				if gv := got[k]; gv != wv {
					t.Errorf("key %q = %q, want %q", k, gv, wv)
				}
			}
		})
	}
}
