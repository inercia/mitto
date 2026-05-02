package auxiliary

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// Purpose constants for auxiliary sessions
const (
	PurposeTitleGen      = "title-gen"
	PurposeFollowUp      = "follow-up"
	PurposeImprovePrompt = "improve-prompt"
	PurposeQueueTitle    = "queue-title"
	PurposeMCPCheck      = "mcp-check"
	PurposeMCPTools      = "mcp-tools"

	// PurposeProcessorPrefix is the prefix for processor-scoped auxiliary sessions.
	// Each prompt-mode processor gets its own session: "processor:<name>".
	PurposeProcessorPrefix = "processor:"
)

// MCPToolInfo represents information about a single MCP tool.
type MCPToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// MCPAvailabilityResult represents the result of checking MCP tool availability.
type MCPAvailabilityResult struct {
	Available             bool   `json:"available"`
	Message               string `json:"message,omitempty"`
	SuggestedRun          string `json:"suggested_run,omitempty"`
	SuggestedInstructions string `json:"suggested_instructions,omitempty"`
}

// WorkspaceAuxiliaryManager manages workspace-scoped auxiliary sessions.
// It provides high-level operations (title generation, prompt improvement, etc.)
// that delegate to the ProcessProvider for actual ACP session management.
type WorkspaceAuxiliaryManager struct {
	provider ProcessProvider
	logger   *slog.Logger

	// Cache for MCP availability checks per workspace
	mcpCheckCache   map[string]*MCPAvailabilityResult
	mcpCheckCacheMu sync.RWMutex

	// Cache for MCP tools list per workspace
	mcpToolsCache   map[string][]MCPToolInfo
	mcpToolsCacheMu sync.RWMutex
}

// NewWorkspaceAuxiliaryManager creates a new workspace-scoped auxiliary manager.
func NewWorkspaceAuxiliaryManager(provider ProcessProvider, logger *slog.Logger) *WorkspaceAuxiliaryManager {
	return &WorkspaceAuxiliaryManager{
		provider:      provider,
		logger:        logger,
		mcpCheckCache: make(map[string]*MCPAvailabilityResult),
		mcpToolsCache: make(map[string][]MCPToolInfo),
	}
}

// GenerateTitle generates a short title for a conversation based on the initial message.
func (m *WorkspaceAuxiliaryManager) GenerateTitle(ctx context.Context, workspaceUUID, initialMessage string) (string, error) {
	prompt := fmt.Sprintf(GenerateTitlePromptTemplate, initialMessage)

	response, err := m.provider.PromptAuxiliary(ctx, workspaceUUID, PurposeTitleGen, prompt)
	if err != nil {
		return "", fmt.Errorf("failed to generate title: %w", err)
	}

	// Clean up the response - remove quotes, trim whitespace
	title := trimQuotes(response)

	// Limit title length
	if len(title) > 50 {
		title = title[:47] + "..."
	}

	return title, nil
}

// GenerateQueuedMessageTitle generates a short title for a queued message.
// The title is meant to be a brief summary (2-3 words) to help identify the message in the queue.
func (m *WorkspaceAuxiliaryManager) GenerateQueuedMessageTitle(ctx context.Context, workspaceUUID, message string) (string, error) {
	// Truncate very long messages to avoid overwhelming the prompt
	truncatedMsg := message
	if len(truncatedMsg) > 500 {
		truncatedMsg = truncatedMsg[:497] + "..."
	}

	prompt := fmt.Sprintf(GenerateQueuedMessageTitlePromptTemplate, truncatedMsg)

	response, err := m.provider.PromptAuxiliary(ctx, workspaceUUID, PurposeQueueTitle, prompt)
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
func (m *WorkspaceAuxiliaryManager) ImprovePrompt(ctx context.Context, workspaceUUID, userPrompt string) (string, error) {
	prompt := fmt.Sprintf(ImprovePromptTemplate, userPrompt)

	response, err := m.provider.PromptAuxiliary(ctx, workspaceUUID, PurposeImprovePrompt, prompt)
	if err != nil {
		return "", fmt.Errorf("failed to improve prompt: %w", err)
	}

	// Clean up the response - remove quotes, trim whitespace, strip any preamble
	improved := trimQuotes(response)
	improved = stripPromptPreamble(improved)

	return improved, nil
}

// AnalyzeFollowUpQuestions analyzes an agent message and extracts follow-up suggestions.
// It uses the auxiliary conversation to identify questions or prompts in the agent's response
// and returns suggested responses the user might want to send.
// The userPrompt parameter provides context about what the user asked.
// Returns an empty slice if no follow-up questions are found.
func (m *WorkspaceAuxiliaryManager) AnalyzeFollowUpQuestions(ctx context.Context, workspaceUUID, userPrompt, agentMessage string) ([]FollowUpSuggestion, error) {
	// Truncate very long messages to avoid overwhelming the prompt
	const maxLen = 4000
	truncatedUserPrompt := userPrompt
	if len(truncatedUserPrompt) > maxLen {
		truncatedUserPrompt = truncatedUserPrompt[:maxLen-3] + "..."
	}
	truncatedAgentMsg := agentMessage
	if len(truncatedAgentMsg) > maxLen {
		truncatedAgentMsg = truncatedAgentMsg[:maxLen-3] + "..."
	}

	prompt := fmt.Sprintf(AnalyzeFollowUpQuestionsPromptTemplate, truncatedUserPrompt, truncatedAgentMsg)

	if m.logger != nil {
		m.logger.Debug("auxiliary follow-up analysis: sending request",
			"workspace_uuid", workspaceUUID,
			"user_prompt_length", len(truncatedUserPrompt),
			"agent_message_length", len(truncatedAgentMsg),
			"user_prompt_preview", truncateForLog(truncatedUserPrompt, 100),
			"agent_message_preview", truncateForLog(truncatedAgentMsg, 200),
		)
	}

	response, err := m.provider.PromptAuxiliary(ctx, workspaceUUID, PurposeFollowUp, prompt)
	if err != nil {
		if m.logger != nil {
			m.logger.Debug("auxiliary follow-up analysis: request failed",
				"workspace_uuid", workspaceUUID,
				"error", err.Error(),
			)
		}
		return nil, fmt.Errorf("failed to analyze follow-up questions: %w", err)
	}

	if m.logger != nil {
		m.logger.Debug("auxiliary follow-up analysis: received response",
			"workspace_uuid", workspaceUUID,
			"response_length", len(response),
			"response", truncateForLog(response, 500),
		)
	}

	// Parse JSON response - returns empty slice if parsing fails (not an error)
	suggestions := parseFollowUpSuggestions(response)

	if m.logger != nil {
		if len(suggestions) == 0 {
			m.logger.Debug("auxiliary follow-up analysis: no suggestions found",
				"workspace_uuid", workspaceUUID,
				"raw_response", truncateForLog(response, 300),
			)
		} else {
			labels := make([]string, len(suggestions))
			for i, s := range suggestions {
				labels[i] = s.Label
			}
			m.logger.Debug("auxiliary follow-up analysis: parsed suggestions",
				"workspace_uuid", workspaceUUID,
				"suggestion_count", len(suggestions),
				"labels", labels,
			)
		}
	}

	return suggestions, nil
}

// CheckMCPAvailability checks if Mitto MCP tools are available in the workspace's ACP server.
// Results are cached per workspace to avoid repeated checks.
// The mcpServerURL parameter should be the URL where the MCP server is expected to be running.
func (m *WorkspaceAuxiliaryManager) CheckMCPAvailability(ctx context.Context, workspaceUUID, mcpServerURL string) (*MCPAvailabilityResult, error) {
	// Check cache first
	m.mcpCheckCacheMu.RLock()
	if cached, ok := m.mcpCheckCache[workspaceUUID]; ok {
		m.mcpCheckCacheMu.RUnlock()
		if m.logger != nil {
			m.logger.Debug("mcp availability check: using cached result",
				"workspace_uuid", workspaceUUID,
				"available", cached.Available)
		}
		return cached, nil
	}
	m.mcpCheckCacheMu.RUnlock()

	// Perform the check
	if m.logger != nil {
		m.logger.Debug("mcp availability check: starting",
			"workspace_uuid", workspaceUUID,
			"mcp_server_url", mcpServerURL)
	}

	prompt := fmt.Sprintf(CheckMCPAvailabilityPromptTemplate, mcpServerURL, mcpServerURL)

	response, err := m.provider.PromptAuxiliary(ctx, workspaceUUID, PurposeMCPCheck, prompt)
	if err != nil {
		if m.logger != nil {
			m.logger.Debug("mcp availability check: request failed",
				"workspace_uuid", workspaceUUID,
				"error", err.Error())
		}
		return nil, fmt.Errorf("failed to check MCP availability: %w", err)
	}

	if m.logger != nil {
		m.logger.Debug("mcp availability check: received response",
			"workspace_uuid", workspaceUUID,
			"response_length", len(response),
			"response", truncateForLog(response, 500))
	}

	// Parse JSON response
	result, err := parseMCPAvailabilityResult(response)
	if err != nil {
		if m.logger != nil {
			m.logger.Warn("mcp availability check: failed to parse response",
				"workspace_uuid", workspaceUUID,
				"error", err.Error(),
				"response", truncateForLog(response, 200))
		}
		return nil, fmt.Errorf("failed to parse MCP availability response: %w", err)
	}

	// Cache the result
	m.mcpCheckCacheMu.Lock()
	m.mcpCheckCache[workspaceUUID] = result
	m.mcpCheckCacheMu.Unlock()

	if m.logger != nil {
		m.logger.Info("MCP availability check completed",
			"workspace_uuid", workspaceUUID,
			"available", result.Available,
			"message", result.Message)
	}

	return result, nil
}

// FetchMCPTools queries the agent for its list of available tools.
// Results are cached per workspace to avoid repeated queries.
func (m *WorkspaceAuxiliaryManager) FetchMCPTools(ctx context.Context, workspaceUUID string) ([]MCPToolInfo, error) {
	// Check cache first
	m.mcpToolsCacheMu.RLock()
	if cached, ok := m.mcpToolsCache[workspaceUUID]; ok {
		m.mcpToolsCacheMu.RUnlock()
		if m.logger != nil {
			m.logger.Debug("mcp tools fetch: using cached result",
				"workspace_uuid", workspaceUUID,
				"tool_count", len(cached))
		}
		return cached, nil
	}
	m.mcpToolsCacheMu.RUnlock()

	if m.logger != nil {
		m.logger.Debug("mcp tools fetch: starting",
			"workspace_uuid", workspaceUUID)
	}

	response, err := m.provider.PromptAuxiliary(ctx, workspaceUUID, PurposeMCPTools, FetchMCPToolsPromptTemplate)
	if err != nil {
		if m.logger != nil {
			m.logger.Debug("mcp tools fetch: request failed",
				"workspace_uuid", workspaceUUID,
				"error", err.Error())
		}
		return nil, fmt.Errorf("failed to fetch MCP tools: %w", err)
	}

	if m.logger != nil {
		m.logger.Debug("mcp tools fetch: received response",
			"workspace_uuid", workspaceUUID,
			"response_length", len(response))
	}

	tools, agentError, err := parseMCPToolsList(response)
	if err != nil {
		if m.logger != nil {
			m.logger.Warn("mcp tools fetch: failed to parse response",
				"workspace_uuid", workspaceUUID,
				"error", err.Error(),
				"response", truncateForLog(response, 200))
		}
		return nil, fmt.Errorf("failed to parse MCP tools response: %w", err)
	}

	if agentError != "" {
		if m.logger != nil {
			m.logger.Warn("mcp tools fetch: agent reported error",
				"workspace_uuid", workspaceUUID,
				"agent_error", agentError)
		}
		// If the agent reported an error but also returned some tools, use them.
		// If no tools were returned, propagate the error.
		if len(tools) == 0 {
			return nil, fmt.Errorf("agent error: %s", agentError)
		}
	}

	// Sort tools alphabetically by name.
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})

	// Only cache non-empty results.
	if len(tools) > 0 {
		m.mcpToolsCacheMu.Lock()
		m.mcpToolsCache[workspaceUUID] = tools
		m.mcpToolsCacheMu.Unlock()
	}

	if m.logger != nil {
		m.logger.Info("MCP tools fetch completed",
			"workspace_uuid", workspaceUUID,
			"tool_count", len(tools))
	}

	return tools, nil
}

// ClearMCPToolsCache clears the cached MCP tools list for a workspace.
// This can be used to force a re-fetch, for example after MCP server configuration changes.
func (m *WorkspaceAuxiliaryManager) ClearMCPToolsCache(workspaceUUID string) {
	m.mcpToolsCacheMu.Lock()
	delete(m.mcpToolsCache, workspaceUUID)
	m.mcpToolsCacheMu.Unlock()

	if m.logger != nil {
		m.logger.Debug("cleared MCP tools cache",
			"workspace_uuid", workspaceUUID)
	}
}

// GetCachedMCPTools returns the cached MCP tools list for a workspace without fetching.
// Returns the cached tools and true if found, or nil and false if not cached.
func (m *WorkspaceAuxiliaryManager) GetCachedMCPTools(workspaceUUID string) ([]MCPToolInfo, bool) {
	m.mcpToolsCacheMu.RLock()
	defer m.mcpToolsCacheMu.RUnlock()
	cached, ok := m.mcpToolsCache[workspaceUUID]
	return cached, ok
}

// ClearMCPCheckCache clears the cached MCP availability result for a workspace.
// This can be used to force a re-check, for example after installing the MCP server.
func (m *WorkspaceAuxiliaryManager) ClearMCPCheckCache(workspaceUUID string) {
	m.mcpCheckCacheMu.Lock()
	delete(m.mcpCheckCache, workspaceUUID)
	m.mcpCheckCacheMu.Unlock()

	if m.logger != nil {
		m.logger.Debug("cleared MCP check cache",
			"workspace_uuid", workspaceUUID)
	}
}

// CheckRequiredToolPatterns checks if the agent has tools matching the given patterns.
// This sends a targeted query to the PurposeMCPTools auxiliary session (reusing it from FetchMCPTools).
// The patterns parameter should be a list of tool name patterns (e.g., ["jira_*", "slack_*"]).
func (m *WorkspaceAuxiliaryManager) CheckRequiredToolPatterns(ctx context.Context, workspaceUUID string, patterns []string) (map[string]bool, error) {
	if len(patterns) == 0 {
		return map[string]bool{}, nil
	}

	patternsStr := strings.Join(patterns, ", ")

	if m.logger != nil {
		m.logger.Debug("required tools check: starting",
			"workspace_uuid", workspaceUUID,
			"patterns", patternsStr)
	}

	prompt := fmt.Sprintf(CheckEnabledWhenMCPPromptTemplate, patternsStr)

	response, err := m.provider.PromptAuxiliary(ctx, workspaceUUID, PurposeMCPTools, prompt)
	if err != nil {
		if m.logger != nil {
			m.logger.Debug("required tools check: request failed",
				"workspace_uuid", workspaceUUID,
				"error", err.Error())
		}
		return nil, fmt.Errorf("failed to check required tools: %w", err)
	}

	if m.logger != nil {
		m.logger.Debug("required tools check: received response",
			"workspace_uuid", workspaceUUID,
			"response_length", len(response),
			"response", truncateForLog(response, 300))
	}

	result, err := parseEnabledWhenMCPCheck(response)
	if err != nil {
		if m.logger != nil {
			m.logger.Warn("required tools check: failed to parse response",
				"workspace_uuid", workspaceUUID,
				"error", err.Error(),
				"response", truncateForLog(response, 200))
		}
		return nil, fmt.Errorf("failed to parse required tools response: %w", err)
	}

	if m.logger != nil {
		m.logger.Info("Required tools check completed",
			"workspace_uuid", workspaceUUID,
			"patterns_checked", len(patterns),
			"result", result)
	}

	return result, nil
}

// PromptProcessorAsync sends a prompt to a processor-specific auxiliary session
// as fire-and-forget. The prompt is dispatched and the method returns immediately
// without waiting for the agent's response. Returns error only if the prompt
// couldn't be dispatched (no process, no session).
func (m *WorkspaceAuxiliaryManager) PromptProcessorAsync(ctx context.Context, workspaceUUID, processorName, prompt string) error {
	purpose := PurposeProcessorPrefix + processorName
	return m.provider.PromptAuxiliaryAsync(ctx, workspaceUUID, purpose, prompt)
}

// Close closes all auxiliary sessions managed by this manager.
func (m *WorkspaceAuxiliaryManager) Close() error {
	// The ProcessProvider handles cleanup when workspaces are closed
	// This method is here for future extensibility
	return nil
}
