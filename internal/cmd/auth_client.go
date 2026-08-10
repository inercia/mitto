package cmd

import (
	"os"

	"github.com/inercia/mitto/internal/instancefile"
)

// tokenSource reports where the token resolveTarget(f) would return came
// from, independent of the other target fields (mirrors resolveTarget's own
// per-field precedence: flag > MITTO_TOKEN env > instance.json > none). Pure
// diagnostic helper for `mitto auth status`'s token_source field — never
// prints the token value itself.
func tokenSource(f *serverFlags) string {
	if f.Token != "" {
		return "flag"
	}
	if os.Getenv("MITTO_TOKEN") != "" {
		return "env"
	}
	if inst, err := instancefile.Read(); err == nil && inst.Token != "" {
		return "instance.json"
	}
	return "none"
}
