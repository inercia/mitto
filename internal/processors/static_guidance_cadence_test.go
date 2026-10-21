package processors

import (
	"context"
	"strings"
	"testing"
)

// Exercise the shipped definitions, not a synthetic cadence configuration.
func TestStaticGuidanceBuiltins_OncePerFreshContext(t *testing.T) {
	for _, name := range []string{"use-ui-tools", "beads-track-tasks"} {
		t.Run(name, func(t *testing.T) {
			loader := NewLoader("../../config/processors/builtin", nil)
			proc, err := loader.LoadFile("../../config/processors/builtin/" + name + ".yaml")
			if err != nil {
				t.Fatal(err)
			}
			// Availability gates are independent of cadence; do not require bd
			// on PATH or a connected UI to exercise scheduling.
			proc.EnabledWhen = ""
			m, runs := newRerunTestManager(proc)
			apply := func(first bool) *ProcessorResult {
				t.Helper()
				result, err := m.Apply(context.Background(), &ProcessorInput{
					Message: "hello", IsFirstMessage: first,
				})
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			if result := apply(true); !strings.Contains(result.Message, "# ") || len(okRuns(*runs)) != 1 {
				t.Fatalf("expected initial guidance, got %q; runs: %+v", result.Message, *runs)
			}
			*runs = nil
			for turn := 0; turn < 25; turn++ {
				m.AccumulateTokenUsage(100000)
				if result := apply(false); result.Message != "hello" {
					t.Fatalf("turn %d repeated guidance: %q", turn, result.Message)
				}
			}
			if applied := okRuns(*runs); len(applied) != 0 {
				t.Fatalf("retained context must not repeat guidance: %+v", applied)
			}
			*runs = nil
			if result := apply(true); !strings.Contains(result.Message, "# ") || len(okRuns(*runs)) != 1 {
				t.Fatalf("fresh context must receive guidance again, got %q; runs: %+v", result.Message, *runs)
			}
		})
	}
}
