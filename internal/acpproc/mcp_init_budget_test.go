package acpproc

// Tests for the MCP-init extended budget policy (mitto-8ul.1). The helper
// coldMCPBudget() decides whether NewSession/LoadSession should widen its
// per-attempt and total deadlines to give a cold agent time to finish its
// internal MCP-server handshake before Mitto times out.

import (
	"testing"
	"time"
)

func TestColdMCPBudget_DisabledByDefault(t *testing.T) {
	p := &SharedACPProcess{} // MCPInitTimeout unset = 0
	perAttempt, total, extended := p.coldMCPBudget(true /*hasMCPServers*/)
	if extended {
		t.Fatalf("expected extended=false when MCPInitTimeout=0, got extended=true")
	}
	if perAttempt != sessionCreateAttemptTimeout {
		t.Fatalf("perAttempt=%v, want %v", perAttempt, sessionCreateAttemptTimeout)
	}
	if total != sessionCreateTotalBudget {
		t.Fatalf("total=%v, want %v", total, sessionCreateTotalBudget)
	}
}

func TestColdMCPBudget_NoMCPServersStillExtends(t *testing.T) {
	// Under the current design (MCP attached globally, not per session/new),
	// hasMCPServers is not load-bearing: the extended budget applies to every
	// cold session/new so long as MCPInitTimeout > 0. mitto-8ul.1.
	p := &SharedACPProcess{}
	p.config.MCPInitTimeout = 240 * time.Second

	perAttempt, total, extended := p.coldMCPBudget(false /*hasMCPServers*/)
	if !extended {
		t.Fatal("expected extended=true even without hasMCPServers on cold start")
	}
	if perAttempt != 240*time.Second || total != 240*time.Second {
		t.Fatalf("budgets = (%v, %v), want (240s, 240s)", perAttempt, total)
	}
}

func TestColdMCPBudget_ColdWithMCPServersExtends(t *testing.T) {
	p := &SharedACPProcess{}
	p.config.MCPInitTimeout = 240 * time.Second

	perAttempt, total, extended := p.coldMCPBudget(true /*hasMCPServers*/)
	if !extended {
		t.Fatal("expected extended=true for cold session with MCP servers")
	}
	if perAttempt != 240*time.Second {
		t.Fatalf("perAttempt=%v, want 240s", perAttempt)
	}
	if total != 240*time.Second {
		t.Fatalf("total=%v, want 240s", total)
	}
}

func TestColdMCPBudget_WarmProcessRevertsToNormal(t *testing.T) {
	p := &SharedACPProcess{}
	p.config.MCPInitTimeout = 240 * time.Second
	// Simulate the process having completed one successful cold-start session RPC.
	p.mcpInitDone.Store(true)

	perAttempt, total, extended := p.coldMCPBudget(true /*hasMCPServers*/)
	if extended {
		t.Fatalf("expected extended=false once mcpInitDone=true, got extended=true")
	}
	if perAttempt != sessionCreateAttemptTimeout {
		t.Fatalf("perAttempt=%v, want %v", perAttempt, sessionCreateAttemptTimeout)
	}
	if total != sessionCreateTotalBudget {
		t.Fatalf("total=%v, want %v", total, sessionCreateTotalBudget)
	}
}

func TestRecommendedLoadTimeout(t *testing.T) {
	p := &SharedACPProcess{}
	p.config.MCPInitTimeout = 240 * time.Second

	// Cold: widen regardless of hasMCPServers hint (Mitto attaches MCP globally).
	if got := p.RecommendedLoadTimeout(true); got != 240*time.Second {
		t.Errorf("cold+mcp: got %v, want 240s", got)
	}
	if got := p.RecommendedLoadTimeout(false); got != 240*time.Second {
		t.Errorf("cold no-mcp-hint: got %v, want 240s", got)
	}
	// Warm: 0.
	p.mcpInitDone.Store(true)
	if got := p.RecommendedLoadTimeout(true); got != 0 {
		t.Errorf("warm: got %v, want 0", got)
	}
	// Disabled: 0.
	p2 := &SharedACPProcess{}
	p2.config.MCPInitTimeout = 0
	if got := p2.RecommendedLoadTimeout(true); got != 0 {
		t.Errorf("disabled: got %v, want 0", got)
	}
}

func TestBeginMCPInitWindow_ResetsPerCall(t *testing.T) {
	p := &SharedACPProcess{}
	p.mcpInitTimedOut.Store(true)

	ch := p.beginMCPInitWindow()
	if ch == nil {
		t.Fatal("expected non-nil channel from beginMCPInitWindow")
	}
	if p.mcpInitTimedOut.Load() {
		t.Fatal("expected mcpInitTimedOut to be reset by beginMCPInitWindow")
	}

	// A second call must return a fresh channel (the old one is orphaned so signals
	// from a previous RPC do not affect the new one).
	ch2 := p.beginMCPInitWindow()
	if ch == ch2 {
		t.Fatal("expected beginMCPInitWindow to return a fresh channel per call")
	}
}
