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
