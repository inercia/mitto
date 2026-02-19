package session

import "testing"

func TestAvailableFlags_HasCanDoIntrospection(t *testing.T) {
	// Verify can_do_introspection flag is defined
	found := false
	for _, flag := range AvailableFlags {
		if flag.Name == FlagCanDoIntrospection {
			found = true
			if flag.Label == "" {
				t.Error("FlagCanDoIntrospection should have a label")
			}
			if flag.Description == "" {
				t.Error("FlagCanDoIntrospection should have a description")
			}
			// Default should be false (opt-in feature)
			if flag.Default != false {
				t.Errorf("FlagCanDoIntrospection default should be false, got %v", flag.Default)
			}
			break
		}
	}
	if !found {
		t.Error("FlagCanDoIntrospection should be in AvailableFlags")
	}
}

func TestAvailableFlags_UniqueNames(t *testing.T) {
	seen := make(map[string]bool)
	for _, flag := range AvailableFlags {
		if seen[flag.Name] {
			t.Errorf("Duplicate flag name: %s", flag.Name)
		}
		seen[flag.Name] = true
	}
}

func TestAvailableFlags_ValidDefinitions(t *testing.T) {
	for _, flag := range AvailableFlags {
		if flag.Name == "" {
			t.Error("Flag name cannot be empty")
		}
		if flag.Label == "" {
			t.Errorf("Flag %s should have a label", flag.Name)
		}
		if flag.Description == "" {
			t.Errorf("Flag %s should have a description", flag.Name)
		}
	}
}

func TestGetFlagDefault(t *testing.T) {
	tests := []struct {
		name     string
		flagName string
		want     bool
	}{
		{
			name:     "can_do_introspection returns false",
			flagName: FlagCanDoIntrospection,
			want:     false,
		},
		{
			name:     "unknown flag returns false",
			flagName: "unknown_flag",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetFlagDefault(tt.flagName)
			if got != tt.want {
				t.Errorf("GetFlagDefault(%s) = %v, want %v", tt.flagName, got, tt.want)
			}
		})
	}
}

func TestGetFlagValue(t *testing.T) {
	tests := []struct {
		name     string
		settings map[string]bool
		flagName string
		want     bool
	}{
		{
			name:     "nil settings returns default",
			settings: nil,
			flagName: FlagCanDoIntrospection,
			want:     false, // default for can_do_introspection
		},
		{
			name:     "empty settings returns default",
			settings: map[string]bool{},
			flagName: FlagCanDoIntrospection,
			want:     false,
		},
		{
			name: "flag explicitly set to true",
			settings: map[string]bool{
				FlagCanDoIntrospection: true,
			},
			flagName: FlagCanDoIntrospection,
			want:     true,
		},
		{
			name: "flag explicitly set to false",
			settings: map[string]bool{
				FlagCanDoIntrospection: false,
			},
			flagName: FlagCanDoIntrospection,
			want:     false,
		},
		{
			name: "unknown flag returns false",
			settings: map[string]bool{
				"some_flag": true,
			},
			flagName: "unknown_flag",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetFlagValue(tt.settings, tt.flagName)
			if got != tt.want {
				t.Errorf("GetFlagValue() = %v, want %v", got, tt.want)
			}
		})
	}
}
