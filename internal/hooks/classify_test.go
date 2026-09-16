package hooks

import "testing"

func TestClassifyHookOutput_Empty(t *testing.T) {
	transient, reason := ClassifyHookOutput("")
	if transient {
		t.Errorf("empty output should not be classified transient, got transient=true reason=%q", reason)
	}
	if reason != "" {
		t.Errorf("empty output should return empty reason, got %q", reason)
	}
}

func TestClassifyHookOutput_LoopbackDNSReadUDP(t *testing.T) {
	// Real cloudflared bootstrap failure line seen on production hosts.
	output := `2026-08-01T10:15:00Z ERR Failed to fetch features error="Get \"https://cfd-features.cloudflare.com/features?...\": dial tcp: lookup cfd-features.cloudflare.com on 127.0.0.53:53: read udp 127.0.0.1:44567->127.0.0.53:53: connection refused"`
	transient, reason := ClassifyHookOutput(output)
	if !transient {
		t.Fatalf("cloudflared loopback DNS read-udp refusal should be transient, got transient=false")
	}
	if reason == "" {
		t.Error("expected non-empty reason for transient classification")
	}
}

func TestClassifyHookOutput_LoopbackDNSReadTCP(t *testing.T) {
	output := `dial tcp: lookup example.com on 127.0.0.53:53: read tcp 127.0.0.1:44567->127.0.0.53:53: connection refused`
	transient, _ := ClassifyHookOutput(output)
	if !transient {
		t.Errorf("read tcp variant should be classified transient")
	}
}

func TestClassifyHookOutput_LoopbackDNSDialRefused(t *testing.T) {
	output := `lookup foo.bar on 127.0.0.53:53: dial udp 127.0.0.53:53: connection refused`
	transient, _ := ClassifyHookOutput(output)
	if !transient {
		t.Errorf("dial ... connection refused variant should be classified transient")
	}
}

func TestClassifyHookOutput_DNSQueryFailedPattern(t *testing.T) {
	// Second cloudflared pattern
	output := `2026-08-01T10:15:01Z ERR the DNS query failed error=lookup features.argotunnel.com on 127.0.0.53:53: server misbehaving`
	transient, reason := ClassifyHookOutput(output)
	if !transient {
		t.Fatalf("cloudflared DNS-query-failed pattern should be transient")
	}
	if reason == "" {
		t.Error("expected non-empty reason for transient classification")
	}
}

func TestClassifyHookOutput_UnrelatedError(t *testing.T) {
	// A real fatal error that should NOT be classified as transient (fail-closed).
	cases := []string{
		"panic: cloudflared: fatal — tunnel token is invalid",
		"error: bind: address already in use",
		"tunnel connection failed: authentication rejected",
		"tunnel-abc123.cfargotunnel.com resolved but connection refused",
		"lookup foo on 8.8.8.8:53: connection refused", // non-loopback port-53 lookup — deliberately not classified transient (only :53 on any IP would over-match)
	}
	for _, out := range cases {
		if transient, reason := ClassifyHookOutput(out); transient {
			t.Errorf("unrelated error %q classified transient with reason %q (fail-closed violated)", out, reason)
		}
	}
}

func TestClassifyHookOutput_FirstMatchWins(t *testing.T) {
	// Output containing BOTH patterns — first should win, both indicate transient.
	output := `lookup a.b on 127.0.0.53:53: read udp 127.0.0.1:1->127.0.0.53:53: connection refused
the DNS query failed error=lookup c.d on 127.0.0.53:53: some other reason`
	transient, reason := ClassifyHookOutput(output)
	if !transient {
		t.Fatal("output matching both patterns should still classify transient")
	}
	if reason == "" {
		t.Error("expected non-empty reason")
	}
}

// TestClassifyHookOutput_CorporateResolverIOTimeout verifies the mitto-07h fix:
// DNS "i/o timeout" against a non-loopback (corporate) resolver is classified
// transient, using the exact up-hook log line from the bead's evidence.
func TestClassifyHookOutput_CorporateResolverIOTimeout(t *testing.T) {
	output := `20:18:31 ERR Failed to fetch features, default to disable error="lookup cfd-features.argotunnel.com on 10.102.2.247:53: dial udp 10.102.2.247:53: i/o timeout"`
	transient, reason := ClassifyHookOutput(output)
	if !transient {
		t.Fatalf("corporate-resolver i/o timeout should be classified transient, got transient=false")
	}
	if reason == "" {
		t.Error("expected non-empty reason for transient classification")
	}
}

// TestClassifyHookOutput_EdgeDiscoveryIOTimeout verifies the second bead-evidence
// pattern: the "edge discovery" DNS-query-failed wrapper around an i/o timeout.
func TestClassifyHookOutput_EdgeDiscoveryIOTimeout(t *testing.T) {
	output := `20:20:06 ERR edge discovery: error looking up Cloudflare edge IPs: the DNS query failed error="lookup _v2-origintunneld._tcp.argotunnel.com on 10.102.2.247:53: dial udp 10.102.2.247:53: i/o timeout"`
	transient, reason := ClassifyHookOutput(output)
	if !transient {
		t.Fatalf("edge discovery i/o timeout should be classified transient, got transient=false")
	}
	if reason == "" {
		t.Error("expected non-empty reason for transient classification")
	}
}

// TestClassifyHookOutput_LoopbackIOTimeout verifies the widened regex also
// matches an i/o timeout against the loopback resolver (not just corporate
// IPs), confirming the fix is resolver-agnostic rather than swapping one
// hardcoded IP for another.
func TestClassifyHookOutput_LoopbackIOTimeout(t *testing.T) {
	output := `lookup foo.bar on 127.0.0.53:53: dial udp 127.0.0.53:53: i/o timeout`
	transient, _ := ClassifyHookOutput(output)
	if !transient {
		t.Errorf("loopback i/o timeout variant should be classified transient")
	}
}

func TestClassifyBenignExit_PkillNoMatch(t *testing.T) {
	benign, reason := ClassifyBenignExit("pkill -f 'cloudflared tunnel --protocol http2 run mitto'", 1, "")
	if !benign {
		t.Fatalf("simple pkill exit 1 with empty output should be classified benign")
	}
	if reason == "" {
		t.Error("expected non-empty reason for benign classification")
	}
}

func TestClassifyBenignExit_PkillNoArgs(t *testing.T) {
	benign, _ := ClassifyBenignExit("pkill mitto-tunnel", 1, "")
	if !benign {
		t.Errorf("bare pkill <name> exit 1 empty output should be classified benign")
	}
}

func TestClassifyBenignExit_FailClosed(t *testing.T) {
	cases := []struct {
		name    string
		command string
		exit    int
		output  string
	}{
		{"compound with semicolon", "pkill -f foo; rm -rf /tmp/x", 1, ""},
		{"compound with pipe", "pkill -f foo | tee /tmp/log", 1, ""},
		{"compound with ampersand", "pkill -f foo && echo done", 1, ""},
		{"command substitution", "pkill -f $(cat /tmp/pattern)", 1, ""},
		{"non-empty output", "pkill -f foo", 1, "some output"},
		{"exit code 2", "pkill -f foo", 2, ""},
		{"exit code 0", "pkill -f foo", 0, ""},
		{"unrelated command", "kill -9 1234", 1, ""},
		{"empty command", "", 1, ""},
	}
	for _, c := range cases {
		if benign, reason := ClassifyBenignExit(c.command, c.exit, c.output); benign {
			t.Errorf("%s: command=%q exit=%d output=%q classified benign with reason %q (fail-closed violated)",
				c.name, c.command, c.exit, c.output, reason)
		}
	}
}
