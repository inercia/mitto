package mcpdiscovery

import (
	"context"
	"math"
	"time"
)

// BackoffPolicy configures a bounded exponential-backoff re-probe schedule
// for configured-but-unreachable MCP servers (docs/devel/mcp-tool-discovery.md,
// Q4, point 3). A server that starts up slowly (e.g. an npx-based stdio
// server, or a network server behind a warming-up proxy) is re-probed with
// increasing delays until it becomes reachable or MaxAttempts is exhausted,
// instead of being latched as permanently unreachable after a single probe.
type BackoffPolicy struct {
	// Base is the delay before the first retry (i.e. before the 2nd probe).
	Base time.Duration
	// Factor is the multiplier applied per attempt (e.g. 2.0 doubles the
	// delay each time).
	Factor float64
	// Max caps each individual delay. Max<=0 means uncapped growth.
	Max time.Duration
	// MaxAttempts is the total number of probe attempts, including the
	// first. MaxAttempts<=0 means unbounded (the caller's ctx is the only
	// bound).
	MaxAttempts int
}

// DefaultBackoffPolicy returns the recommended production policy: 1s base
// delay, doubling each attempt, capped at 30s, up to 10 total attempts
// (spanning roughly Base*(2^MaxAttempts-1) ≈ 8.5 minutes worst case before
// hitting the cap, then linear at Max thereafter).
func DefaultBackoffPolicy() BackoffPolicy {
	return BackoffPolicy{Base: time.Second, Factor: 2.0, Max: 30 * time.Second, MaxAttempts: 10}
}

// nextDelay returns the delay before the probe following the given 0-based
// attempt index: attempt 0 is the delay before the 2nd probe (== Base),
// attempt n is Base*Factor^n, capped at Max when Max>0. It is pure and
// deterministic (no jitter) so callers/tests can assert exact schedules;
// jitter can be layered on top by callers if needed. Zero/negative Base
// falls back to 1s; Factor<1 falls back to 1.0 (no growth, flat retries).
func (p BackoffPolicy) nextDelay(attempt int) time.Duration {
	base := p.Base
	if base <= 0 {
		base = time.Second
	}
	factor := p.Factor
	if factor < 1 {
		factor = 1.0
	}

	d := float64(base) * math.Pow(factor, float64(attempt))

	if p.Max <= 0 {
		if math.IsInf(d, 0) || math.IsNaN(d) || d > math.MaxInt64 {
			return time.Duration(math.MaxInt64)
		}
		return time.Duration(d)
	}

	if math.IsInf(d, 0) || math.IsNaN(d) || d > float64(p.Max) {
		return p.Max
	}
	return time.Duration(d)
}

// ProbeFunc probes a single MCP server and reports the outcome. It is the
// caller-supplied bridge to a concrete transport (e.g. a closure over
// DiscoverStdioServer/DiscoverNetworkServer bound to one agents.MCPServer).
type ProbeFunc func(ctx context.Context) ServerToolsResult

// RetryUntilReachable repeatedly calls probe, applying policy's backoff
// schedule between attempts, until probe reports Reachable=true, ctx is
// cancelled, or policy.MaxAttempts is exhausted. A timeout/connect/list
// failure (Reachable=false) is NEVER treated as a negative result — it only
// schedules another retry; callers must treat a discovered=false return as
// "keep the last-known-good state, do not downgrade." onReachable is invoked
// exactly once, only on a genuine reachable result, and is never invoked on
// exhaustion or ctx cancellation. Returns the last ServerToolsResult probed
// (which is the reachable one iff the bool is true) and whether the server
// was discovered reachable.
func RetryUntilReachable(ctx context.Context, policy BackoffPolicy, probe ProbeFunc, onReachable func(ServerToolsResult)) (ServerToolsResult, bool) {
	var res ServerToolsResult
	attempt := 0

	for {
		if ctx.Err() != nil {
			return res, false
		}

		res = probe(ctx)
		if res.Reachable {
			if onReachable != nil {
				onReachable(res)
			}
			return res, true
		}

		attempt++
		if policy.MaxAttempts > 0 && attempt >= policy.MaxAttempts {
			return res, false
		}

		d := policy.nextDelay(attempt - 1)
		timer := time.NewTimer(d)
		select {
		case <-ctx.Done():
			timer.Stop()
			return res, false
		case <-timer.C:
		}
	}
}

// ScheduleRetries launches a fire-and-forget RetryUntilReachable goroutine
// for every unreachable result in results, using probeFor(server.Name) to
// build each server's ProbeFunc. Reachable results are skipped. All
// goroutines are bound to ctx and exit promptly on cancellation.
func ScheduleRetries(ctx context.Context, results []ServerToolsResult, policy BackoffPolicy, probeFor func(server string) ProbeFunc, onReachable func(ServerToolsResult)) {
	for _, res := range results {
		if res.Reachable {
			continue
		}
		probe := probeFor(res.Server)
		go RetryUntilReachable(ctx, policy, probe, onReachable)
	}
}
