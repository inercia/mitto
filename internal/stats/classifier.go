package stats

import (
	"regexp"
	"strings"

	"github.com/inercia/mitto/internal/session"
)

// This file holds the v1 token estimator and MCP-call classifier used by the
// Aggregator (aggregator.go) and by the stats.5 backfiller. Both are stable
// enough to expose so external adapters (the live SessionObserver in
// internal/web, the backfill replayer) can share the exact same heuristics,
// keeping ingest counts identical between the live and backfill paths.
//
// Bumping EstimatorVersion (stats.go) forces stats.5 to recompute historical
// rows; any behavioural change to these helpers must be paired with such a
// bump.

// ImageTokenCost is the fixed placeholder token cost applied per image
// attachment in a user_prompt. Matches the spec formula
// `tokens_est(image_ref) = 64`.
const ImageTokenCost int64 = 64

// EstimateTokensText returns the length-based token estimate for a text
// payload (`user_prompt.message`, `agent_message.text`, `agent_thought.text`)
// per the bead spec formula:
//
//	tokens_est(text) = max(1, (len_utf8(text) + 3) / 4)
//
// The length is measured in UTF-8 bytes (Go's built-in len for strings) rather
// than runes, matching the "~4 chars per token" heuristic the industry uses
// for byte-level BPE tokenisers. For an empty string the function returns 0
// (rather than the spec's floor of 1) so the aggregator does not credit
// tool-only turns or attachment-only prompts with phantom text tokens; the
// spec's `max(1, ...)` floor is a no-op for any non-empty input because
// (n+3)/4 >= 1 already holds for every n >= 1.
func EstimateTokensText(s string) int64 {
	n := len(s)
	if n == 0 {
		return 0
	}
	t := int64((n + 3) / 4)
	if t < 1 {
		t = 1
	}
	return t
}

// EstimateTokensImage returns the flat per-image token cost. Kept as a
// function (rather than exposing only the constant) so future model-aware
// image tokenisers can slot in without changing every call-site.
func EstimateTokensImage() int64 { return ImageTokenCost }

// EstimateTokensFile returns the token estimate for a text-file attachment of
// the given byte size, per the bead spec formula:
//
//	tokens_est(text_file, size) = (size + 3) / 4
//
// Negative sizes are clamped to zero. The caller is responsible for resolving
// a session.FileRef's ID into a byte size (typically via session.Store).
// When the caller cannot resolve a size, EstimateTokensFileRef provides a
// name-length proxy fallback.
func EstimateTokensFile(size int64) int64 {
	if size <= 0 {
		return 0
	}
	return (size + 3) / 4
}

// EstimateTokensFileRef returns a size-agnostic token estimate for a FileRef
// based on the reference's own metadata (name length + a small MIME-class
// overhead). Used by the aggregator when no FileSizeResolver is wired, so
// stats keep flowing before stats.4/stats.5 pass a resolver in.
func EstimateTokensFileRef(f session.FileRef) int64 {
	n := len(f.Name) + 16
	if n > 4096 {
		n = 4096
	}
	return int64((n + 3) / 4)
}

// MCPTitleRegexps is the extensible default list of tool-title patterns that
// classify a tool_call as an MCP call (bead spec rule 3). Callers may append
// project-specific patterns at process start without touching config; each
// regexp is anchored with ^ and matched case-sensitively against the raw
// ToolCallData.Title.
//
// The default set covers the community MCP servers common in this workspace:
//
//	^(github|slack|jira|linear|fj|splunk|puppeteer)[-_]
//
// The `mitto_` prefix used by our own MCP tools is handled by IsMCPCall as a
// dedicated fast-path rule (rule 2) so it does not need a regex entry.
var MCPTitleRegexps = []*regexp.Regexp{
	regexp.MustCompile(`^(github|slack|jira|linear|fj|splunk|puppeteer)[-_]`),
}

// IsMCPCall reports whether a ToolCallData represents an MCP-classified tool
// call, applying the bead's four rules in order and returning on the first
// match:
//
//  1. td.Kind == "mcp"           → true (future-proof; explicit protocol flag)
//  2. td.Title has "mitto_" pref  → true (our own MCP tools; fast path, no regex)
//  3. td.Title matches any regexp in MCPTitleRegexps → true
//  4. otherwise                   → false
func IsMCPCall(td session.ToolCallData) bool {
	if td.Kind == "mcp" {
		return true
	}
	if strings.HasPrefix(td.Title, "mitto_") {
		return true
	}
	for _, re := range MCPTitleRegexps {
		if re != nil && re.MatchString(td.Title) {
			return true
		}
	}
	return false
}
