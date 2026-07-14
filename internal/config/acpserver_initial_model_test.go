package config

import (
	"encoding/json"
	"testing"
)

// ---- ACPServerSettings InitialModel tests (mitto-fqj) ----

func TestACPServerSettings_InitialModel_JSONRoundTrip(t *testing.T) {
	s := ACPServerSettings{
		Name:                "auggie",
		Command:             "auggie acp",
		InitialModelProfile: "Claude Opus",
		InitialModelTag:     "Coding",
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var got ACPServerSettings
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got.InitialModelProfile != "Claude Opus" {
		t.Errorf("InitialModelProfile = %q, want %q", got.InitialModelProfile, "Claude Opus")
	}
	if got.InitialModelTag != "Coding" {
		t.Errorf("InitialModelTag = %q, want %q", got.InitialModelTag, "Coding")
	}
}

func TestACPServerSettings_InitialModel_JSONOmitempty(t *testing.T) {
	// When both fields are empty, they should be omitted from JSON.
	s := ACPServerSettings{
		Name:    "auggie",
		Command: "auggie acp",
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if _, ok := raw["initial_model_profile"]; ok {
		t.Error("initial_model_profile should be omitted from JSON when empty")
	}
	if _, ok := raw["initial_model_tag"]; ok {
		t.Error("initial_model_tag should be omitted from JSON when empty")
	}
}

// TestACPServerSettings_InitialModel_ToConfigRoundTrip verifies that the new
// initial-model fields survive the Settings → Config → Settings round-trip
// (the ACPServer(srv) / ACPServerSettings(srv) casts in ToConfig /
// ConfigToSettings).
func TestACPServerSettings_InitialModel_ToConfigRoundTrip(t *testing.T) {
	s := &Settings{
		ACPServers: []ACPServerSettings{{
			Name:                "auggie",
			Command:             "auggie acp",
			InitialModelProfile: "Claude Opus",
			InitialModelTag:     "Smart",
		}},
	}
	cfg := s.ToConfig()
	if len(cfg.ACPServers) != 1 {
		t.Fatalf("ToConfig: got %d servers, want 1", len(cfg.ACPServers))
	}
	if cfg.ACPServers[0].InitialModelProfile != "Claude Opus" {
		t.Errorf("ToConfig: InitialModelProfile = %q, want %q", cfg.ACPServers[0].InitialModelProfile, "Claude Opus")
	}
	if cfg.ACPServers[0].InitialModelTag != "Smart" {
		t.Errorf("ToConfig: InitialModelTag = %q, want %q", cfg.ACPServers[0].InitialModelTag, "Smart")
	}
	back := ConfigToSettings(cfg)
	if back.ACPServers[0].InitialModelProfile != "Claude Opus" {
		t.Errorf("ConfigToSettings: InitialModelProfile = %q, want %q", back.ACPServers[0].InitialModelProfile, "Claude Opus")
	}
	if back.ACPServers[0].InitialModelTag != "Smart" {
		t.Errorf("ConfigToSettings: InitialModelTag = %q, want %q", back.ACPServers[0].InitialModelTag, "Smart")
	}
}

func TestACPServer_GetInitialModelPreference(t *testing.T) {
	tests := []struct {
		name    string
		srv     *ACPServer
		want    []PromptPreferredModel
		wantNil bool
	}{
		{
			name:    "nil receiver",
			srv:     nil,
			wantNil: true,
		},
		{
			name:    "no preference configured",
			srv:     &ACPServer{Name: "auggie"},
			wantNil: true,
		},
		{
			name: "profile only",
			srv:  &ACPServer{InitialModelProfile: "Claude Opus"},
			want: []PromptPreferredModel{{ModelName: "Claude Opus"}},
		},
		{
			name: "tag only",
			srv:  &ACPServer{InitialModelTag: "Coding"},
			want: []PromptPreferredModel{{ModelTag: "Coding"}},
		},
		{
			name: "profile wins over tag when both set",
			srv: &ACPServer{
				InitialModelProfile: "Claude Opus",
				InitialModelTag:     "Cheap",
			},
			want: []PromptPreferredModel{{ModelName: "Claude Opus"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.srv.GetInitialModelPreference()
			if tt.wantNil {
				if got != nil {
					t.Errorf("got %v, want nil", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d entries, want %d: %v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("entry %d: got %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestInitialModelPreference_ResolverPrecedence documents the workspace →
// ACP-server → nil fallback precedence used by
// conversation.SessionManager.CreateSessionWithWorkspace (mitto-fqj). Since
// that precedence lives inline in session_manager.go (which cannot be
// imported here), this table test exercises the same shape by directly
// calling GetInitialModelPreference() on both tiers and applying the same
// "workspace wins if non-nil, else server, else nil" rule.
func TestInitialModelPreference_ResolverPrecedence(t *testing.T) {
	tests := []struct {
		name string
		ws   *WorkspaceSettings
		srv  *ACPServer
		want []PromptPreferredModel
	}{
		{
			name: "workspace wins when both set",
			ws:   &WorkspaceSettings{InitialModelProfile: "WS Opus"},
			srv:  &ACPServer{InitialModelProfile: "Server Sonnet"},
			want: []PromptPreferredModel{{ModelName: "WS Opus"}},
		},
		{
			name: "server fallback when workspace unset",
			ws:   &WorkspaceSettings{},
			srv:  &ACPServer{InitialModelTag: "Coding"},
			want: []PromptPreferredModel{{ModelTag: "Coding"}},
		},
		{
			name: "server fallback with server profile",
			ws:   &WorkspaceSettings{},
			srv:  &ACPServer{InitialModelProfile: "Server Sonnet"},
			want: []PromptPreferredModel{{ModelName: "Server Sonnet"}},
		},
		{
			name: "nil when both unset",
			ws:   &WorkspaceSettings{},
			srv:  &ACPServer{Name: "auggie"},
			want: nil,
		},
		{
			name: "nil workspace + nil server → nil",
			ws:   nil,
			srv:  nil,
			want: nil,
		},
		{
			name: "workspace tag wins over server profile",
			ws:   &WorkspaceSettings{InitialModelTag: "Smart"},
			srv:  &ACPServer{InitialModelProfile: "Server Sonnet"},
			want: []PromptPreferredModel{{ModelTag: "Smart"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pref := tt.ws.GetInitialModelPreference()
			if pref == nil {
				pref = tt.srv.GetInitialModelPreference()
			}
			if len(pref) != len(tt.want) {
				t.Fatalf("got %d entries, want %d: %v", len(pref), len(tt.want), pref)
			}
			for i := range pref {
				if pref[i] != tt.want[i] {
					t.Errorf("entry %d: got %+v, want %+v", i, pref[i], tt.want[i])
				}
			}
		})
	}
}
