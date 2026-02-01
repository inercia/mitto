package auxiliary

import (
	"context"
	"fmt"
	"log/slog"
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
	prompt := fmt.Sprintf(
		`Consider this initial message in a conversation with an LLM: "%s"

What title would you use for this conversation? Keep it very short, just 2 or 3 words.
Reply with ONLY the title, nothing else.`,
		initialMessage,
	)

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

	prompt := fmt.Sprintf(
		`Summarize this message in 2-3 words for a queue display: "%s"

Reply with ONLY the short title, nothing else.`,
		truncatedMsg,
	)

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
	prompt := fmt.Sprintf(
		`The user wants to improve the following prompt. Please enhance it by making it clearer, more specific, and more effective, while preserving the user's intent. Consider the current project context. Return ONLY the improved prompt text without any explanations or preamble.

Original prompt:
%s`,
		userPrompt,
	)

	response, err := Prompt(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("failed to improve prompt: %w", err)
	}

	// Clean up the response - remove quotes, trim whitespace
	improved := trimQuotes(response)

	return improved, nil
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
