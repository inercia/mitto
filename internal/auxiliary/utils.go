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

// parseFollowUpSuggestions parses the JSON response from the auxiliary conversation.
// It handles cases where the response might have extra text around the JSON.
// Returns an empty slice if parsing fails (this is not considered an error).
func parseFollowUpSuggestions(response string) []FollowUpSuggestion {
	response = strings.TrimSpace(response)

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

// mcpToolsResponse represents the JSON object returned by the MCP tools fetch prompt.
// The agent returns either {"tools": [...]} or {"error": "..."}.
type mcpToolsResponse struct {
	Tools []MCPToolInfo `json:"tools"`
	Error string        `json:"error"`
}

// parseMCPToolsList parses the JSON response from the MCP tools fetch.
// Accepts either a JSON object {"tools":[...], "error":"..."} or a bare JSON array [...].
// Returns the tools list and any error message reported by the agent.
func parseMCPToolsList(response string) ([]MCPToolInfo, string, error) {
	response = strings.TrimSpace(response)

	// Try parsing as a JSON object {"tools": [...], "error": "..."}
	var obj mcpToolsResponse
	if err := json.Unmarshal([]byte(response), &obj); err == nil && (len(obj.Tools) > 0 || obj.Error != "") {
		return obj.Tools, obj.Error, nil
	}

	// Try to find a JSON object in the response (may have surrounding text)
	start := strings.Index(response, "{")
	end := strings.LastIndex(response, "}")
	if start >= 0 && end > start {
		jsonStr := response[start : end+1]
		if err := json.Unmarshal([]byte(jsonStr), &obj); err == nil && (len(obj.Tools) > 0 || obj.Error != "") {
			return obj.Tools, obj.Error, nil
		}
	}

	// Fallback: try parsing as a bare JSON array (backward compatibility)
	var tools []MCPToolInfo
	if err := json.Unmarshal([]byte(response), &tools); err == nil {
		return tools, "", nil
	}

	// Try to find a JSON array in the response
	arrStart := strings.Index(response, "[")
	arrEnd := strings.LastIndex(response, "]")
	if arrStart >= 0 && arrEnd > arrStart {
		jsonStr := response[arrStart : arrEnd+1]
		if err := json.Unmarshal([]byte(jsonStr), &tools); err == nil {
			return tools, "", nil
		}
	}

	// Parsing failed
	return nil, "", fmt.Errorf("invalid JSON response: %s", truncateForLog(response, 100))
}

// parseMCPAvailabilityResult parses the JSON response from the MCP availability check.
// It handles cases where the response might have extra text around the JSON.
func parseMCPAvailabilityResult(response string) (*MCPAvailabilityResult, error) {
	response = strings.TrimSpace(response)

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
