package cel

import "testing"

// TestPromptsContext_Enabled_Exists covers the case-insensitive membership
// semantics of PromptsContext.Enabled / PromptsContext.Exists, plus fail-closed
// behaviour on empty/zero-value contexts (mitto-s1w).
func TestPromptsContext_Enabled_Exists(t *testing.T) {
	pc := PromptsContext{
		Names:        []string{"Loop fixing bug", "Loop implementing feature", "Disabled prompt"},
		EnabledNames: []string{"Loop fixing bug", "Loop implementing feature"},
	}

	cases := []struct {
		name       string
		query      string
		wantExists bool
		wantEnable bool
	}{
		{"exact case enabled", "Loop fixing bug", true, true},
		{"mixed case enabled", "loop FIXING BUG", true, true},
		{"lower case enabled", "loop implementing feature", true, true},
		{"registered but disabled", "Disabled prompt", true, false},
		{"registered but disabled mixed", "DISABLED PROMPT", true, false},
		{"unknown name", "Nonexistent prompt", false, false},
		{"empty name", "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pc.Exists(c.query); got != c.wantExists {
				t.Errorf("Exists(%q) = %v; want %v", c.query, got, c.wantExists)
			}
			if got := pc.Enabled(c.query); got != c.wantEnable {
				t.Errorf("Enabled(%q) = %v; want %v", c.query, got, c.wantEnable)
			}
		})
	}

	// Zero-value context — every query fails-closed (cold-start / no cache).
	var zero PromptsContext
	if zero.Exists("Loop fixing bug") {
		t.Errorf("zero PromptsContext.Exists should return false")
	}
	if zero.Enabled("Loop fixing bug") {
		t.Errorf("zero PromptsContext.Enabled should return false")
	}
}
