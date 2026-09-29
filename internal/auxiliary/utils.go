package auxiliary

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FollowUpSuggestion represents a suggested follow-up action for the user.
type FollowUpSuggestion struct {
	Label string `json:"label"` // Short button text (1-4 words)
	Value string `json:"value"` // Full response to send when clicked
}

// trimQuotes removes surrounding quotes from a string.
func trimQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// stripPromptPreamble removes common LLM preamble patterns from improved prompt responses.
// This handles cases where the LLM adds introductory text despite being told not to.
func stripPromptPreamble(s string) string {
	s = strings.TrimSpace(s)

	// Common preamble patterns to strip (case-insensitive check)
	preambles := []string{
		"Here is the improved prompt:\n\n",
		"Here is the improved prompt:\n",
		"Here is the improved prompt:",
		"Here's the improved prompt:\n\n",
		"Here's the improved prompt:\n",
		"Here's the improved prompt:",
		"Here is your improved prompt:\n\n",
		"Here is your improved prompt:\n",
		"Here is your improved prompt:",
		"Improved prompt:\n\n",
		"Improved prompt:\n",
		"Improved prompt:",
		"Sure, here is the improved prompt:\n\n",
		"Sure, here is the improved prompt:\n",
		"Sure, here is the improved prompt:",
		"Sure! Here is the improved prompt:\n\n",
		"Sure! Here is the improved prompt:\n",
		"Sure! Here is the improved prompt:",
		"Sure, here's the improved prompt:\n\n",
		"Sure, here's the improved prompt:\n",
		"Sure, here's the improved prompt:",
		"Sure! Here's the improved prompt:\n\n",
		"Sure! Here's the improved prompt:\n",
		"Sure! Here's the improved prompt:",
	}

	lower := strings.ToLower(s)
	for _, p := range preambles {
		if strings.HasPrefix(lower, strings.ToLower(p)) {
			s = s[len(p):]
			break
		}
	}

	return strings.TrimSpace(s)
}

// truncateForLog truncates a string to maxLen characters for logging,
// appending "..." if truncated.
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen < 4 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// stripMarkdownFences removes markdown code fences from a string.
// Handles ```json ... ``` and ``` ... ``` patterns.
// Returns the original string unchanged if no valid fence pair is found.
// This is used to tolerate agents that wrap JSON responses in markdown code blocks.
func stripMarkdownFences(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Find the end of the opening fence line (may include a language hint like "json")
	newlineIdx := strings.Index(s, "\n")
	if newlineIdx < 0 {
		return s // No newline after opening fence — not a valid fenced block
	}
	rest := s[newlineIdx+1:]
	// Find the closing fence
	closeIdx := strings.LastIndex(rest, "```")
	if closeIdx < 0 {
		return s // No closing fence found
	}
	return strings.TrimSpace(rest[:closeIdx])
}

// parseFollowUpSuggestions parses the JSON response from the auxiliary conversation.
// It handles cases where the response might have extra text around the JSON.
// Returns an empty slice if parsing fails (this is not considered an error).
func parseFollowUpSuggestions(response string) []FollowUpSuggestion {
	response = strings.TrimSpace(response)
	response = stripMarkdownFences(response)

	// Try direct parsing first
	var suggestions []FollowUpSuggestion
	if err := json.Unmarshal([]byte(response), &suggestions); err == nil {
		return validateSuggestions(suggestions)
	}

	// Try to find JSON array in the response
	start := strings.Index(response, "[")
	end := strings.LastIndex(response, "]")
	if start >= 0 && end > start {
		jsonStr := response[start : end+1]
		if err := json.Unmarshal([]byte(jsonStr), &suggestions); err == nil {
			return validateSuggestions(suggestions)
		}
	}

	// Parsing failed - return empty slice
	return []FollowUpSuggestion{}
}

// validateSuggestions filters and validates the suggestions.
func validateSuggestions(suggestions []FollowUpSuggestion) []FollowUpSuggestion {
	var valid []FollowUpSuggestion
	for _, s := range suggestions {
		label := strings.TrimSpace(s.Label)
		value := strings.TrimSpace(s.Value)

		// Skip empty suggestions
		if label == "" || value == "" {
			continue
		}

		// Truncate if too long
		if len(label) > 50 {
			label = label[:47] + "..."
		}
		if len(value) > 1000 {
			value = value[:997] + "..."
		}

		valid = append(valid, FollowUpSuggestion{
			Label: label,
			Value: value,
		})

		// Limit to 5 suggestions max
		if len(valid) >= 5 {
			break
		}
	}
	return valid
}

// upstreamQuotaExceededMarker is a stable substring of the upstream
// usage-quota-exhaustion notice (mitto-rqi1). Some providers (e.g. GitHub
// Copilot) deliver this notice as ordinary successfully-streamed assistant
// text on an end_turn response rather than as an error, so it reaches the
// auxiliary response parsers looking like malformed JSON. Matching on this
// substring lets callers distinguish "upstream quota exhausted" from a
// genuine parse failure instead of reporting a misleading
// "invalid JSON response" diagnostic.
const upstreamQuotaExceededMarker = "exceeded your usage quota"

// IsUpstreamQuotaExceeded reports whether response text is an upstream
// usage-quota-exhaustion notice rather than a normal (JSON) agent response.
// See mitto-rqi1: this predicate is the fix seam that lets
// parseMCPToolsList, parseMCPAvailabilityResult, and parseToolPatternsCheck
// (and, transitively, workspace_manager.go's callers) report an actionable
// "upstream quota exceeded" outcome instead of a misleading
// "invalid JSON response" one.
func IsUpstreamQuotaExceeded(response string) bool {
	return strings.Contains(strings.ToLower(response), upstreamQuotaExceededMarker)
}

// ErrUpstreamQuotaExceeded is returned by the auxiliary response parsers
// when the response text is an upstream usage-quota-exhaustion notice
// (see IsUpstreamQuotaExceeded) instead of the expected JSON payload.
var ErrUpstreamQuotaExceeded = fmt.Errorf("upstream usage quota exceeded")

// mcpToolsResponse represents the JSON object returned by the MCP tools fetch prompt.
// The agent returns either {"tools": [...]} or {"error": "..."}.
type mcpToolsResponse struct {
	Tools []MCPToolInfo `json:"tools"`
	Error string        `json:"error"`
}

// parseMCPToolsList parses the JSON response from the MCP tools fetch. It is
// STRICT (mitto-sys.7, ADR Q2): the whole (trimmed, unfenced) response must be
// a single JSON object with a "tools" and/or "error" key — no substring
// extraction, no bare-array fallback. {"tools":[]} is a valid, legitimate
// "zero tools" answer, distinct from a parse failure. Returns the tools list
// and any error message reported by the agent.
func parseMCPToolsList(response string) ([]MCPToolInfo, string, error) {
	response = strings.TrimSpace(response)
	response = stripMarkdownFences(response)
	response = strings.TrimSpace(response)

	if IsUpstreamQuotaExceeded(response) {
		return nil, "", ErrUpstreamQuotaExceeded
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(response), &raw); err != nil {
		return nil, "", fmt.Errorf("invalid JSON response: %s", truncateForLog(response, 100))
	}
	if _, hasTools := raw["tools"]; !hasTools {
		if _, hasError := raw["error"]; !hasError {
			return nil, "", fmt.Errorf("invalid JSON response: %s", truncateForLog(response, 100))
		}
	}

	var obj mcpToolsResponse
	if err := json.Unmarshal([]byte(response), &obj); err != nil {
		return nil, "", fmt.Errorf("invalid JSON response: %s", truncateForLog(response, 100))
	}
	return obj.Tools, obj.Error, nil
}

// parseMCPAvailabilityResult parses the JSON response from the MCP availability check.
// It handles cases where the response might have extra text around the JSON.
func parseMCPAvailabilityResult(response string) (*MCPAvailabilityResult, error) {
	response = strings.TrimSpace(response)
	response = stripMarkdownFences(response)

	if IsUpstreamQuotaExceeded(response) {
		return nil, ErrUpstreamQuotaExceeded
	}

	// Try direct parsing first
	var result MCPAvailabilityResult
	if err := json.Unmarshal([]byte(response), &result); err == nil {
		return &result, nil
	}

	// Try to find JSON object in the response
	start := strings.Index(response, "{")
	end := strings.LastIndex(response, "}")
	if start >= 0 && end > start {
		jsonStr := response[start : end+1]
		if err := json.Unmarshal([]byte(jsonStr), &result); err == nil {
			return &result, nil
		}
	}

	// Parsing failed
	return nil, fmt.Errorf("invalid JSON response: %s", truncateForLog(response, 100))
}

// requiredToolsCheckResponse represents the JSON response from the required tools check.
type requiredToolsCheckResponse struct {
	Patterns map[string]bool `json:"patterns"`
}

// parseToolPatternsCheck parses the JSON response from the required tools pattern check.
// It handles cases where the response might have extra text around the JSON.
func parseToolPatternsCheck(response string) (map[string]bool, error) {
	response = strings.TrimSpace(response)
	response = stripMarkdownFences(response)

	if IsUpstreamQuotaExceeded(response) {
		return nil, ErrUpstreamQuotaExceeded
	}

	// Try direct parsing first
	var result requiredToolsCheckResponse
	if err := json.Unmarshal([]byte(response), &result); err == nil && result.Patterns != nil {
		return result.Patterns, nil
	}

	// Try to find JSON object in the response
	start := strings.Index(response, "{")
	end := strings.LastIndex(response, "}")
	if start >= 0 && end > start {
		jsonStr := response[start : end+1]
		if err := json.Unmarshal([]byte(jsonStr), &result); err == nil && result.Patterns != nil {
			return result.Patterns, nil
		}
	}

	// Parsing failed
	return nil, fmt.Errorf("invalid JSON response: %s", truncateForLog(response, 100))
}
