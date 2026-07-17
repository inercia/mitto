package procstart

import (
	"regexp"
	"strings"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/runner"
)

// mcpInitProgressPattern detects the "Waiting for N MCP server(s) to initialize"
// line the agent writes to stderr while it is blocking on MCP handshake. It is
// intentionally count-agnostic and case-insensitive so it survives minor phrasing
// variations across agent versions (mitto-8ul.1).
var mcpInitProgressPattern = regexp.MustCompile(`(?i)waiting for .* mcp server`)

// StartStderrMonitor starts a goroutine that reads from stderr and writes to the collector.
// If onCrashDetected is non-nil, it is called (at most once) when crash patterns are
// detected in the stderr output, enabling early process death signaling.
// If onFirstActivity is non-nil, it is called (at most once) the first time any bytes
// are observed on stderr — used by the startup watchdog to detect "live" processes.
// If onMCPInitProgress is non-nil, it is called on each chunk reporting the agent is
// waiting for MCP servers (re-fires per handshake episode; callers must dedup if
// needed — mitto-29q). If onMCPInitTimeout is non-nil, it is called (at most once)
// when the agent reports its MCP-init wait has timed out — callers use this to abort
// the pending session/new promptly with an actionable error (mitto-8ul.1). Neither
// MCP signal contributes to crash detection.
//
// If onDegraded is non-nil, it is called every time a stderr chunk matches a
// per-agent Degraded regex (mitto-k6h). Unlike onCrashDetected, onDegraded is
// NOT latched — a degraded line can recur and each matching chunk fires once.
// Callers on the shared process feed this into the mitto-5eq rolling-window
// saturation counter so stderr-observed degradation contributes to Tier 5/6
// recycle decisions alongside real RPC timeouts/bails.
//
// perAgent, when non-nil, contributes per-agent regex patterns on top of the
// hardcoded baseline (mitto-k6h):
//   - Crash regexes are OR'd with stderrCrashPatterns when matching for onCrashDetected.
//   - Ignore regexes are already applied by the collector (installed by the caller
//     via StderrCollector.SetIgnorePatterns before this monitor is started).
//   - Degraded regexes fire onDegraded (see above) — they feed the shared-process
//     saturation signal, not crash detection.
func StartStderrMonitor(
	stderr runner.ReadCloser,
	collector *StderrCollector,
	onCrashDetected func(),
	onFirstActivity func(),
	onMCPInitProgress func(),
	onMCPInitTimeout func(),
	onDegraded func(),
	perAgent *CompiledStderrPatterns,
) {
	go func() {
		crashSignaled := false
		activitySignaled := false
		mcpTimeoutSignaled := false
		buf := make([]byte, 4096)
		for {
			n, readErr := stderr.Read(buf)
			if n > 0 {
				collector.Write(buf[:n])

				if !activitySignaled && onFirstActivity != nil {
					activitySignaled = true
					onFirstActivity()
				}

				chunkStr := ""

				// Fix C: Check for crash patterns in stderr output.
				// This detects inner CLI subprocess death immediately from SDK
				// stderr messages, bypassing the 60s control request timeout.
				//
				// Per-agent crash regexes (mitto-k6h) are OR'd with the baseline —
				// either source firing counts as a crash.
				if !crashSignaled && onCrashDetected != nil {
					chunkStr = string(buf[:n])
					for _, pattern := range stderrCrashPatterns {
						if strings.Contains(chunkStr, pattern) {
							crashSignaled = true
							onCrashDetected()
							break
						}
					}
					if !crashSignaled && perAgent != nil && matchAnyRegex(perAgent.Crash, chunkStr) {
						crashSignaled = true
						onCrashDetected()
					}
				}

				// Degraded regexes: fire onDegraded on each matching chunk (mitto-k6h).
				// Unlike crash detection, there is NO one-shot latch — a degraded
				// line can recur and each match should contribute another sample
				// to the shared-process rolling-window saturation counter.
				if onDegraded != nil && perAgent != nil && len(perAgent.Degraded) > 0 {
					if chunkStr == "" {
						chunkStr = string(buf[:n])
					}
					if matchAnyRegex(perAgent.Degraded, chunkStr) {
						onDegraded()
					}
				}

				// MCP-init lifecycle signals (mitto-8ul.1): tolerant regex matches
				// so the exact phrasing/count in the agent's log line is not load-bearing.
				//
				// MCP-init progress fires on EVERY matching chunk (mitto-29q): agents
				// like Auggie re-run the MCP handshake on every session/new, so a
				// one-shot latch would only widen the budget for the first-ever
				// handshake. Duplicate logs/broadcasts are suppressed by the
				// CompareAndSwap edge-detection inside the onMCPInitProgress callback.
				// The hard-timeout signal remains one-shot.
				if onMCPInitProgress != nil || (onMCPInitTimeout != nil && !mcpTimeoutSignaled) {
					if chunkStr == "" {
						chunkStr = string(buf[:n])
					}
					if onMCPInitProgress != nil && mcpInitProgressPattern.MatchString(chunkStr) {
						onMCPInitProgress()
					}
					if !mcpTimeoutSignaled && onMCPInitTimeout != nil && mittoAcp.MCPInitTimeoutPattern.MatchString(chunkStr) {
						mcpTimeoutSignaled = true
						onMCPInitTimeout()
					}
				}
			}
			if readErr != nil {
				break
			}
		}
		collector.Close()
	}()
}
