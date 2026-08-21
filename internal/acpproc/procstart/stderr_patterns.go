package procstart

import (
	"log/slog"
	"regexp"
)

// StderrPatternsSpec is the pure-data (schema) form of per-agent stderr patterns.
// It intentionally mirrors internal/agents.StderrPatterns as plain string slices
// so the conversation package can compile them without importing internal/agents
// (internal/acpproc must not depend on internal/agents; mitto-k6h).
type StderrPatternsSpec struct {
	Crash    []string
	Ignore   []string
	Degraded []string
}

// CompiledStderrPatterns holds regex patterns compiled once from a
// StderrPatternsSpec. All three fields are separately populated so callers can
// wire each action class independently (mitto-k6h).
//
// Action-class semantics:
//   - Crash: OR'd with the hardcoded stderrCrashPatterns baseline; a match
//     triggers onCrashDetected (SDK-timeout bypass).
//   - Ignore: applied by StderrCollector.Write to suppress the debug-level
//     "agent stderr" log for matching writes. Buffer capture is unaffected.
//   - Degraded: fires onDegraded when a stderr chunk matches. onDegraded on the
//     shared process feeds a fail-side sample into the mitto-5eq rolling-window
//     saturation counter — frequent degraded output (alone or combined with real
//     RPC timeouts/bails) can promote the process to saturated and let GC
//     Tier 5/6 recycle it. NOT latched: a degraded line can recur and each
//     matching chunk fires once (mitto-k6h).
type CompiledStderrPatterns struct {
	Crash    []*regexp.Regexp
	Ignore   []*regexp.Regexp
	Degraded []*regexp.Regexp
}

// CompileStderrPatterns compiles the plain-string patterns in spec into a
// CompiledStderrPatterns. Invalid regexes are SKIPPED (logged as warnings via
// logger when non-nil) rather than causing a fatal error — a single malformed
// per-agent pattern must not prevent process start. Returns nil when spec is
// empty (no patterns of any class) so hot paths can cheaply short-circuit
// (mitto-k6h).
func CompileStderrPatterns(spec StderrPatternsSpec, logger *slog.Logger) *CompiledStderrPatterns {
	if len(spec.Crash) == 0 && len(spec.Ignore) == 0 && len(spec.Degraded) == 0 {
		return nil
	}
	out := &CompiledStderrPatterns{}
	out.Crash = compileRegexList(spec.Crash, "crash", logger)
	out.Ignore = compileRegexList(spec.Ignore, "ignore", logger)
	out.Degraded = compileRegexList(spec.Degraded, "degraded", logger)
	return out
}

// compileRegexList compiles each pattern; invalid ones are skipped with a warn
// log (never fatal). Empty input returns nil.
func compileRegexList(patterns []string, class string, logger *slog.Logger) []*regexp.Regexp {
	if len(patterns) == 0 {
		return nil
	}
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, raw := range patterns {
		if raw == "" {
			continue
		}
		re, err := regexp.Compile(raw)
		if err != nil {
			if logger != nil {
				logger.Warn("skipping invalid stderr pattern",
					"class", class,
					"pattern", raw,
					"error", err)
			}
			continue
		}
		compiled = append(compiled, re)
	}
	if len(compiled) == 0 {
		return nil
	}
	return compiled
}
