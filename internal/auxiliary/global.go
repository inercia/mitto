package auxiliary

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
)

var (
	globalManager *Manager
	globalMu      sync.Mutex
	globalOnce    sync.Once
)

// Initialize sets up the global auxiliary manager with the given ACP command.
// This should be called once at startup. Subsequent calls are ignored.
func Initialize(command string, logger *slog.Logger) {
	globalOnce.Do(func() {
		globalMu.Lock()
		defer globalMu.Unlock()
		globalManager = NewManager(command, logger)
	})
}

// GetManager returns the global auxiliary manager.
// Returns nil if Initialize has not been called.
func GetManager() *Manager {
	globalMu.Lock()
	defer globalMu.Unlock()
	return globalManager
}

// Shutdown closes the global auxiliary manager.
func Shutdown() error {
	globalMu.Lock()
	defer globalMu.Unlock()
	if globalManager != nil {
		err := globalManager.Close()
		globalManager = nil
		return err
	}
	return nil
}

// Prompt sends a message to the global auxiliary session and returns the response.
// This is a convenience function that uses the global manager.
func Prompt(ctx context.Context, message string) (string, error) {
	manager := GetManager()
	if manager == nil {
		return "", fmt.Errorf("auxiliary manager not initialized")
	}
	return manager.Prompt(ctx, message)
}

// GenerateTitle generates a short title for a conversation based on the initial message.
func GenerateTitle(ctx context.Context, initialMessage string) (string, error) {
	prompt := fmt.Sprintf(GenerateTitlePromptTemplate, initialMessage)

	response, err := Prompt(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("failed to generate title: %w", err)
	}

	// Clean up the response - remove quotes, trim whitespace
	title := response
	title = trimQuotes(title)

	// Limit title length
	if len(title) > 50 {
		title = title[:47] + "..."
	}

	return title, nil
}

// GenerateQueuedMessageTitle generates a short title for a queued message.
// The title is meant to be a brief summary (2-3 words) to help identify the message in the queue.
func GenerateQueuedMessageTitle(ctx context.Context, message string) (string, error) {
	// Truncate very long messages to avoid overwhelming the prompt
	truncatedMsg := message
	if len(truncatedMsg) > 500 {
		truncatedMsg = truncatedMsg[:497] + "..."
	}

	prompt := fmt.Sprintf(GenerateQueuedMessageTitlePromptTemplate, truncatedMsg)

	response, err := Prompt(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("failed to generate queued message title: %w", err)
	}

	// Clean up the response - remove quotes, trim whitespace
	title := trimQuotes(response)

	// Limit title length
	if len(title) > 30 {
		title = title[:27] + "..."
	}

	return title, nil
}

// ImprovePrompt enhances a user's prompt to make it clearer, more specific, and more effective.
func ImprovePrompt(ctx context.Context, userPrompt string) (string, error) {
	prompt := fmt.Sprintf(ImprovePromptTemplate, userPrompt)

	response, err := Prompt(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("failed to improve prompt: %w", err)
	}

	// Clean up the response - remove quotes, trim whitespace
	improved := trimQuotes(response)

	return improved, nil
}

// FollowUpSuggestion represents a suggested follow-up response.
type FollowUpSuggestion struct {
	// Label is the short button text (2-4 words)
	Label string `json:"label"`
	// Value is the full response text to send when clicked
	Value string `json:"value"`
}

// AnalyzeFollowUpQuestions analyzes an agent message and extracts follow-up suggestions.
// It uses the auxiliary conversation to identify questions or prompts in the agent's response
// and returns suggested responses the user might want to send.
// Returns an empty slice if no follow-up questions are found.
func AnalyzeFollowUpQuestions(ctx context.Context, agentMessage string) ([]FollowUpSuggestion, error) {
	manager := GetManager()
	if manager == nil {
		return nil, fmt.Errorf("auxiliary manager not initialized")
	}

	// Truncate very long messages to avoid overwhelming the prompt
	truncatedMsg := agentMessage
	if len(truncatedMsg) > 2000 {
		truncatedMsg = truncatedMsg[:1997] + "..."
	}

	prompt := fmt.Sprintf(AnalyzeFollowUpQuestionsPromptTemplate, truncatedMsg)

	response, err := manager.Prompt(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze follow-up questions: %w", err)
	}

	// Parse JSON response - returns empty slice if parsing fails (not an error)
	suggestions := parseFollowUpSuggestions(response)

	return suggestions, nil
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

	// Try to extract JSON array from the response
	// Look for [...] pattern
	jsonPattern := regexp.MustCompile(`\[[\s\S]*\]`)
	match := jsonPattern.FindString(response)
	if match != "" {
		if err := json.Unmarshal([]byte(match), &suggestions); err == nil {
			return validateSuggestions(suggestions)
		}
	}

	// If we can't parse it, return empty slice (not an error - just no suggestions)
	return nil
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

// trimQuotes removes surrounding quotes from a string.
func trimQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
