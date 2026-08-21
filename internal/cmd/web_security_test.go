package cmd

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestWebRejectsUnauthenticatedNonLoopbackBind reproduces mitto-fekm. The web
// command must reject a remotely reachable primary bind before it starts serving.
func TestWebRejectsUnauthenticatedNonLoopbackBind(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}

	for _, tt := range []struct {
		name string
		host string
	}{
		{name: "IPv4", host: "192.0.2.10"},
		{name: "IPv6", host: "2001:db8::10"},
		{name: "IPv4 wildcard", host: "0.0.0.0"},
		{name: "IPv6 wildcard", host: "::"},
		{name: "hostname", host: "mitto.example"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestWebNonLoopbackBindHelper$")
			cmd.Env = append(os.Environ(),
				"MITTO_FEKM_HELPER=1",
				"MITTO_FEKM_HOST="+tt.host,
				"MITTO_DIR="+t.TempDir(),
			)
			output, err := cmd.CombinedOutput()
			if ctx.Err() == context.DeadlineExceeded {
				t.Fatalf("mitto web kept serving on unauthenticated --host %s; want startup rejection\n%s", tt.host, output)
			}
			if err != nil {
				t.Fatalf("helper failed without the expected bind-policy rejection: %v\n%s", err, output)
			}
		})
	}
}

func TestWebNonLoopbackBindHelper(t *testing.T) {
	if os.Getenv("MITTO_FEKM_HELPER") != "1" {
		return
	}

	rootCmd.SetArgs([]string{"web", "--host", os.Getenv("MITTO_FEKM_HOST"), "--port", "0", "--port-external", "-1"})
	err := Execute()
	if err == nil {
		t.Fatal("web command accepted unauthenticated non-loopback primary bind")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "non-loopback") ||
		!strings.Contains(strings.ToLower(err.Error()), "authentication") {
		t.Fatalf("unexpected startup error: %v", err)
	}
}
