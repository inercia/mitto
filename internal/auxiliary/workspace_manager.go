package auxiliary

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/agents"
	"github.com/inercia/mitto/internal/fileutil"
	"github.com/inercia/mitto/internal/mcpdiscovery"
)

// defaultMCPToolsTTL bounds how long a persisted real-MCP tools snapshot is
// reused after a restart before a fresh probe is required (mitto-sys.8).
const defaultMCPToolsTTL = 15 * time.Minute

// Purpose constants for auxiliary sessions
const (
	PurposeTitleGen      = "title-gen"
	PurposeFollowUp      = "follow-up"
	PurposeImprovePrompt = "improve-prompt"
	PurposeQueueTitle    = "queue-title"
	PurposeMCPCheck      = "mcp-check"
	PurposeMCPTools      = "mcp-tools"

	// PurposeKeepAlive is a warm keepalive auxiliary session held by the
	// adaptive pre-warming controller (mitto-mw0) for slow/broken workspaces.
	// It carries no traffic; its sole job is to hold MCP-connection warmth so
	// the first real prompt hits an already-warm agent. Exempt from
	// AuxIdleTimeout while the workspace is pinned.
	PurposeKeepAlive = "keepalive"

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

	// mcpToolsNegatives tracks, per workspace and tool name, the number of
	// consecutive successful-but-negative fetches (the tool was previously
	// known but absent from the latest fetch). Guarded by mcpToolsCacheMu
	// (same lock as mcpToolsCache, since applyMCPToolsCachePolicy reads and
	// writes both together). See docs/devel/mcp-tool-discovery.md (Q3.1):
	// a tool is only downgraded to absent after 2 consecutive negatives.
	mcpToolsNegatives map[string]map[string]int

	// StdioToolsDiscoverer, when set, deterministically discovers a workspace's
	// MCP tools via direct tools/list (per configured stdio server). Injected
	// by the web layer. When nil (tests/CLI), FetchMCPTools uses the LLM path
	// only (legacy).
	StdioToolsDiscoverer func(ctx context.Context, workspaceUUID string) ([]mcpdiscovery.ServerToolsResult, error)

	// mcpBackoffActive dedups the per-workspace backoff re-probe goroutine
	// (at most one per workspace). Guarded by mcpBackoffMu.
	mcpBackoffActive map[string]bool
	mcpBackoffMu     sync.Mutex
	// mcpBackoffPolicy is the schedule used by EnsureMCPBackoffRetry; defaults
	// to mcpdiscovery.DefaultBackoffPolicy(), overridable in tests for speed.
	mcpBackoffPolicy mcpdiscovery.BackoffPolicy

	// MCPServerLister, when set, enumerates a workspace's configured MCP
	// servers (Command/Args/URL/Env) so EnsureMCPWatchers can open one
	// persistent notifications/tools/list_changed watcher per server.
	// Injected by the web layer (mitto-sys.4 increment 3/N). When nil
	// (tests/CLI), EnsureMCPWatchers is a no-op.
	MCPServerLister func(ctx context.Context, workspaceUUID string) ([]agents.MCPServer, error)

	// MCPWatchTransportFactory builds the transport used by EnsureMCPWatchers'
	// mcpdiscovery.WatchServer calls; nil defaults to
	// mcpdiscovery.DefaultTransportFactory. Overridable in tests to inject
	// in-memory transports.
	MCPWatchTransportFactory mcpdiscovery.TransportFactory

	// mcpWatchers holds, per workspace, the live persistent watchers opened
	// by EnsureMCPWatchers (one per configured server). Key presence (not
	// list length) marks a pool as active/starting — see EnsureMCPWatchers'
	// dedup and StopMCPWatchers' teardown. Guarded by mcpWatchersMu.
	mcpWatchers   map[string][]*mcpdiscovery.ToolListWatcher
	mcpWatchersMu sync.Mutex

	// MCPToolsPersistDir, when non-empty, enables on-disk persistence of the
	// real-MCP-derived (deterministic) tools list per workspace, so a restart
	// reuses the last snapshot within the TTL instead of re-probing every
	// server. The web layer wires it to an appdir-based directory; empty
	// (tests/CLI) disables persistence. Only deterministic results are ever
	// written — never the LLM fallback (docs/devel/mcp-tool-discovery.md, Q3.3).
	MCPToolsPersistDir string
	// mcpToolsTTL bounds reuse of a persisted snapshot; defaults to
	// defaultMCPToolsTTL. Overridable in tests for speed.
	mcpToolsTTL time.Duration

	// PrewarmPinReevaluator, when set, is called once per EnsureMCPBackoffRetry
	// round (after each MCP probe) so the adaptive pre-warming controller
	// (mitto-mw0) can re-run its health probe and apply hysteresis/expiry.
	// The web layer wires this to (*acpproc.ACPProcessManager).ReevaluatePrewarmPin
	// so pin/unpin decisions ride the same schedule as MCP reachability probes
	// without introducing an import cycle (auxiliary → acpproc is forbidden).
	// nil (tests/CLI) disables re-evaluation.
	PrewarmPinReevaluator func(workspaceUUID string)

	// MCPToolsRefreshedHook, when set, is invoked after an async re-verify
	// triggered by loadPersistedMCPTools completes and updates the in-memory
	// cache (mitto-dza, Fix 4). The web layer wires this to a
	// prompts_changed broadcast so `enabledWhen` tool-gates re-evaluate as
	// soon as LLM-only tools reappear after a restart. Invoked once per
	// successful async refresh, per workspace. nil (tests/CLI) disables.
	MCPToolsRefreshedHook func(workspaceUUID string)

	// mcpRefetchInFlight dedups the per-workspace async re-verify goroutine
	// triggered by loadPersistedMCPTools' suspect-snapshot path
	// (mitto-dza, Fix 4). Guarded by mcpRefetchMu.
	mcpRefetchInFlight map[string]bool
	mcpRefetchMu       sync.Mutex
}

// NewWorkspaceAuxiliaryManager creates a new workspace-scoped auxiliary manager.
func NewWorkspaceAuxiliaryManager(provider ProcessProvider, logger *slog.Logger) *WorkspaceAuxiliaryManager {
	return &WorkspaceAuxiliaryManager{
		provider:           provider,
		logger:             logger,
		mcpCheckCache:      make(map[string]*MCPAvailabilityResult),
		mcpToolsCache:      make(map[string][]MCPToolInfo),
		mcpToolsNegatives:  make(map[string]map[string]int),
		mcpBackoffActive:   make(map[string]bool),
		mcpBackoffPolicy:   mcpdiscovery.DefaultBackoffPolicy(),
		mcpWatchers:        make(map[string][]*mcpdiscovery.ToolListWatcher),
		mcpToolsTTL:        defaultMCPToolsTTL,
		mcpRefetchInFlight: make(map[string]bool),
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

	// Real-MCP persistence (mitto-sys.8): within the TTL, reuse the on-disk
	// snapshot of the last deterministic tools/list result instead of
	// re-probing every server on restart. Only real-MCP results are ever
	// persisted, so this never resurrects an LLM-hallucinated list. A missing
	// or expired snapshot falls through to a fresh probe below.
	//
	// When the persisted snapshot is "suspect" (was written while at least one
	// MCP server was unreachable to the deterministic discoverer, or predates
	// the current schema), we still serve it instantly for zero-flicker gating
	// but kick off a background re-verify so LLM-only tools reappear in-memory
	// within seconds (mitto-dza, Fix 4).
	if persisted, ok, suspect := m.loadPersistedMCPTools(workspaceUUID); ok {
		m.mcpToolsCacheMu.Lock()
		m.mcpToolsCache[workspaceUUID] = persisted
		m.mcpToolsCacheMu.Unlock()
		if m.logger != nil {
			m.logger.Debug("mcp tools fetch: using persisted real-MCP snapshot",
				"workspace_uuid", workspaceUUID,
				"tool_count", len(persisted),
				"suspect", suspect)
		}
		if suspect {
			m.triggerAsyncMCPToolsRefetch(workspaceUUID)
		}
		return persisted, nil
	}

	return m.runMCPToolsFetch(ctx, workspaceUUID)
}

// runMCPToolsFetch performs the full deterministic-plus-LLM probe, updates the
// in-memory cache via the last-known-good policy, and persists the
// deterministic list to disk. Shared by the synchronous FetchMCPTools path and
// the async re-verify triggered by a suspect persisted snapshot
// (mitto-dza, Fix 4).
func (m *WorkspaceAuxiliaryManager) runMCPToolsFetch(ctx context.Context, workspaceUUID string) ([]MCPToolInfo, error) {
	// Try deterministic stdio discovery first (mitto-sys.2/mitto-sys.6): a
	// real tools/list per configured stdio server, no LLM involved. The LLM
	// fallback below only runs when discovery is unavailable, errors, or
	// reports at least one unreachable server (that server's tools may still
	// only be knowable via the LLM, or it may just be starting up).
	var deterministic []MCPToolInfo
	detNames := map[string]bool{}
	needLLM := true
	configuredServerCount := -1
	anyUnreachable := false

	if m.StdioToolsDiscoverer != nil {
		results, derr := m.StdioToolsDiscoverer(ctx, workspaceUUID)
		if derr != nil {
			if m.logger != nil {
				m.logger.Debug("mcp tools fetch: stdio discovery failed, falling back to LLM",
					"workspace_uuid", workspaceUUID,
					"error", derr.Error())
			}
		} else {
			configuredServerCount = len(results)
			for _, r := range results {
				if !r.Reachable {
					anyUnreachable = true
					continue
				}
				for _, name := range r.Tools {
					if detNames[name] {
						continue
					}
					detNames[name] = true
					deterministic = append(deterministic, MCPToolInfo{Name: name})
				}
			}
			needLLM = anyUnreachable || len(results) == 0
			if m.logger != nil {
				m.logger.Debug("mcp tools fetch: stdio discovery completed",
					"workspace_uuid", workspaceUUID,
					"server_count", len(results),
					"tool_count", len(deterministic),
					"any_unreachable", anyUnreachable,
					"need_llm", needLLM)
			}
		}
	}

	merged := deterministic

	if needLLM {
		llmTools, err := m.fetchMCPToolsViaLLM(ctx, workspaceUUID, configuredServerCount)
		if err != nil {
			if len(deterministic) == 0 {
				return nil, err
			}
			if m.logger != nil {
				m.logger.Warn("mcp tools fetch: LLM fallback failed, using deterministic tools only",
					"workspace_uuid", workspaceUUID,
					"error", err.Error())
			}
		} else {
			for _, tool := range llmTools {
				if detNames[tool.Name] {
					continue // deterministic entries win on name collision
				}
				merged = append(merged, tool)
			}
		}
	}

	// Sort tools alphabetically by name.
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Name < merged[j].Name
	})

	// Merge into the cache using the last-known-good policy: a previously
	// observed tool is never downgraded to absent on a single negative
	// fetch (see applyMCPToolsCachePolicy).
	result := m.applyMCPToolsCachePolicy(workspaceUUID, merged)

	// Persist ONLY the real-MCP-derived (deterministic) list to disk — never
	// the LLM fallback (mitto-sys.8, ADR Q3.3). A no-op when persistence is
	// disabled or discovery produced no tools. A subsequent restart reuses this
	// snapshot within the TTL instead of re-probing. Record anyUnreachable so
	// a future load knows to trigger an async re-verify (mitto-dza, Fix 4).
	m.savePersistedMCPTools(workspaceUUID, deterministic, anyUnreachable)

	if m.logger != nil {
		m.logger.Info("MCP tools fetch completed",
			"workspace_uuid", workspaceUUID,
			"tool_count", len(result),
			"deterministic_count", len(deterministic),
			"any_unreachable", anyUnreachable)
	}

	return result, nil
}

// triggerAsyncMCPToolsRefetch fires an async runMCPToolsFetch for the given
// workspace when a suspect persisted snapshot was just served
// (mitto-dza, Fix 4). At most one goroutine per workspace is in flight at any
// time; concurrent callers observing the same suspect state hit the in-flight
// guard and return immediately. On successful completion, MCPToolsRefreshedHook
// (when wired) is invoked so the web layer can broadcast prompts_changed and
// let `enabledWhen` tool-gates re-evaluate against the freshly loaded LLM-only
// tools.
func (m *WorkspaceAuxiliaryManager) triggerAsyncMCPToolsRefetch(workspaceUUID string) {
	m.mcpRefetchMu.Lock()
	if m.mcpRefetchInFlight[workspaceUUID] {
		m.mcpRefetchMu.Unlock()
		return
	}
	m.mcpRefetchInFlight[workspaceUUID] = true
	m.mcpRefetchMu.Unlock()

	go func() {
		defer func() {
			m.mcpRefetchMu.Lock()
			delete(m.mcpRefetchInFlight, workspaceUUID)
			m.mcpRefetchMu.Unlock()
		}()

		// The persisted snapshot has already been placed into mcpToolsCache by
		// the caller; drop that entry so runMCPToolsFetch actually re-probes
		// (it would otherwise be short-circuited by the cache check inside
		// FetchMCPTools — but note we call runMCPToolsFetch directly here,
		// which does the probe unconditionally). Keeping the cache populated
		// during the probe would risk serving stale data to concurrent
		// FetchMCPTools callers; overwriting it atomically via
		// applyMCPToolsCachePolicy at the end is the same behaviour as a
		// normal fetch, so we leave the pre-existing entry in place.
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		if m.logger != nil {
			m.logger.Debug("mcp tools async re-verify: starting",
				"workspace_uuid", workspaceUUID)
		}

		if _, err := m.runMCPToolsFetch(ctx, workspaceUUID); err != nil {
			if m.logger != nil {
				m.logger.Warn("mcp tools async re-verify: failed",
					"workspace_uuid", workspaceUUID,
					"error", err.Error())
			}
			return
		}

		if m.logger != nil {
			m.logger.Debug("mcp tools async re-verify: completed",
				"workspace_uuid", workspaceUUID)
		}
		if hook := m.MCPToolsRefreshedHook; hook != nil {
			hook(workspaceUUID)
		}
	}()
}

// maxMCPToolsAttempts bounds fetchMCPToolsViaLLM's retry loop (mitto-sys.7):
// one initial attempt plus one retry when the response fails strict parsing
// or is implausibly empty.
const maxMCPToolsAttempts = 2

// fetchMCPToolsViaLLM queries the agent's own LLM to introspect its MCP
// tools — the pre-mitto-sys.2 fallback path, used by FetchMCPTools when
// deterministic stdio discovery is unavailable, errors, or reports an
// unreachable server. Callers decide whether an error here is fatal
// (no deterministic tools to fall back on) or can be ignored.
//
// It retries once (mitto-sys.7, ADR Q2) when the response fails strict
// parsing, or when it plausibility-fails: zero tools returned while
// configuredServerCount indicates servers were actually configured
// (configuredServerCount <= 0 means "unknown/no discoverer", which skips the
// plausibility check and accepts a zero-tool response outright). An explicit
// agent-reported error is always definitive and never retried.
func (m *WorkspaceAuxiliaryManager) fetchMCPToolsViaLLM(ctx context.Context, workspaceUUID string, configuredServerCount int) ([]MCPToolInfo, error) {
	var lastErr error

	for attempt := 1; attempt <= maxMCPToolsAttempts; attempt++ {
		if m.logger != nil {
			m.logger.Debug("mcp tools fetch: starting",
				"workspace_uuid", workspaceUUID,
				"attempt", attempt)
		}

		prompt := FetchMCPToolsPromptTemplate
		if attempt > 1 {
			prompt += "\n\n" + mcpToolsRetryReminder
		}

		response, err := m.provider.PromptAuxiliary(ctx, workspaceUUID, PurposeMCPTools, prompt)
		if err != nil {
			if m.logger != nil {
				m.logger.Debug("mcp tools fetch: request failed",
					"workspace_uuid", workspaceUUID,
					"attempt", attempt,
					"error", err.Error())
			}
			lastErr = fmt.Errorf("failed to fetch MCP tools: %w", err)
			if ctx.Err() != nil {
				return nil, lastErr
			}
			continue
		}

		if m.logger != nil {
			m.logger.Debug("mcp tools fetch: received response",
				"workspace_uuid", workspaceUUID,
				"attempt", attempt,
				"response_length", len(response))
		}

		tools, agentError, perr := parseMCPToolsList(response)
		if perr != nil {
			if m.logger != nil {
				m.logger.Warn("mcp tools fetch: failed to parse response",
					"workspace_uuid", workspaceUUID,
					"attempt", attempt,
					"error", perr.Error(),
					"response", truncateForLog(response, 200))
			}
			lastErr = fmt.Errorf("failed to parse MCP tools response: %w", perr)
			continue
		}

		if agentError != "" {
			if m.logger != nil {
				m.logger.Warn("mcp tools fetch: agent reported error",
					"workspace_uuid", workspaceUUID,
					"attempt", attempt,
					"agent_error", agentError)
			}
			// If the agent reported an error but also returned some tools, use
			// them. If no tools were returned, propagate the error. Either way,
			// this is definitive — never retried.
			if len(tools) == 0 {
				return nil, fmt.Errorf("agent error: %s", agentError)
			}
			return tools, nil
		}

		if len(tools) == 0 && configuredServerCount > 0 && attempt < maxMCPToolsAttempts {
			if m.logger != nil {
				m.logger.Debug("mcp tools fetch: implausible empty response, retrying",
					"workspace_uuid", workspaceUUID,
					"attempt", attempt,
					"configured_server_count", configuredServerCount)
			}
			lastErr = fmt.Errorf("implausible empty tools: %d server(s) configured", configuredServerCount)
			continue
		}

		return tools, nil
	}

	return nil, lastErr
}

// applyMCPToolsCachePolicy merges a fresh, successful MCP tools fetch into
// the per-workspace cache using a last-known-good policy
// (docs/devel/mcp-tool-discovery.md, Q3.1): a tool observed in a previous
// fetch is never downgraded to absent on a single negative fetch — removal
// requires two consecutive independent negative fetches. A tool that
// reappears in fresh resets its negative counter. Callers must only invoke
// this for a SUCCESSFUL fetch; errors/timeouts must never count as a negative.
//
// fresh is the tool list from the latest successful fetch (may be empty).
// The merged result is retained-known-tools (refreshed from fresh when
// present) plus any new fresh tools, sorted alphabetically by Name; it is
// stored into mcpToolsCache[workspaceUUID] and returned. When there is no
// prior cache entry and fresh is empty, no entry is created (returns nil,
// cache left untouched) — an empty first fetch establishes nothing.
func (m *WorkspaceAuxiliaryManager) applyMCPToolsCachePolicy(workspaceUUID string, fresh []MCPToolInfo) []MCPToolInfo {
	m.mcpToolsCacheMu.Lock()
	defer m.mcpToolsCacheMu.Unlock()

	cached, hadCache := m.mcpToolsCache[workspaceUUID]
	if !hadCache && len(fresh) == 0 {
		return nil
	}

	freshByName := make(map[string]MCPToolInfo, len(fresh))
	for _, tool := range fresh {
		freshByName[tool.Name] = tool
	}

	negatives := m.mcpToolsNegatives[workspaceUUID]
	if negatives == nil {
		negatives = make(map[string]int)
	}

	merged := make([]MCPToolInfo, 0, len(cached)+len(fresh))
	kept := make(map[string]bool, len(cached))

	for _, tool := range cached {
		if freshTool, ok := freshByName[tool.Name]; ok {
			delete(negatives, tool.Name)
			merged = append(merged, freshTool)
			kept[tool.Name] = true
			continue
		}
		negatives[tool.Name]++
		if negatives[tool.Name] >= 2 {
			delete(negatives, tool.Name) // two consecutive negatives: remove
			continue
		}
		merged = append(merged, tool) // last-known-good: retain as-is
		kept[tool.Name] = true
	}

	for _, tool := range fresh {
		if kept[tool.Name] {
			continue
		}
		merged = append(merged, tool)
		delete(negatives, tool.Name)
	}

	sort.Slice(merged, func(i, j int) bool { return merged[i].Name < merged[j].Name })

	if len(negatives) > 0 {
		m.mcpToolsNegatives[workspaceUUID] = negatives
	} else {
		delete(m.mcpToolsNegatives, workspaceUUID)
	}
	m.mcpToolsCache[workspaceUUID] = merged

	return merged
}

// mergeMCPToolsAdditive merges freshly-discovered tools into the workspace
// cache WITHOUT the two-negatives downgrade: tools absent from `fresh` are
// retained unconditionally, so a partial backoff re-probe (which only sees
// currently-reachable servers) never evicts tools contributed by other
// servers or the LLM fallback. Refreshes descriptions of tools present in
// fresh. Returns the merged, name-sorted list and whether it changed vs the
// prior cache. Takes mcpToolsCacheMu.
func (m *WorkspaceAuxiliaryManager) mergeMCPToolsAdditive(workspaceUUID string, fresh []MCPToolInfo) ([]MCPToolInfo, bool) {
	m.mcpToolsCacheMu.Lock()
	defer m.mcpToolsCacheMu.Unlock()

	cached := m.mcpToolsCache[workspaceUUID]

	byName := make(map[string]MCPToolInfo, len(cached)+len(fresh))
	order := make([]string, 0, len(cached)+len(fresh))
	for _, tool := range cached {
		byName[tool.Name] = tool
		order = append(order, tool.Name)
	}

	changed := false
	for _, tool := range fresh {
		if existing, ok := byName[tool.Name]; ok {
			if existing != tool {
				byName[tool.Name] = tool
				changed = true
			}
			continue
		}
		byName[tool.Name] = tool
		order = append(order, tool.Name)
		changed = true
	}

	if !changed {
		return cached, false
	}

	merged := make([]MCPToolInfo, 0, len(order))
	for _, name := range order {
		merged = append(merged, byName[name])
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Name < merged[j].Name })
	m.mcpToolsCache[workspaceUUID] = merged

	if negatives := m.mcpToolsNegatives[workspaceUUID]; negatives != nil {
		for _, tool := range fresh {
			delete(negatives, tool.Name)
		}
		if len(negatives) == 0 {
			delete(m.mcpToolsNegatives, workspaceUUID)
		}
	}

	return merged, true
}

// EnsureMCPBackoffRetry starts, at most once per workspace, a bounded
// exponential-backoff goroutine that re-probes configured-but-unreachable
// MCP servers via StdioToolsDiscoverer until every server is reachable, ctx
// is cancelled, or the backoff attempts are exhausted (mitto-sys.5). Each
// round additively merges newly-reachable servers' tools into the cache
// (last-known-good; never downgrades) and, when the tool set changed,
// invokes onUpdate with the merged list so the caller can broadcast. No-op
// when StdioToolsDiscoverer is nil or a loop is already active for the ws.
func (m *WorkspaceAuxiliaryManager) EnsureMCPBackoffRetry(ctx context.Context, workspaceUUID string, onUpdate func([]MCPToolInfo)) {
	if m.StdioToolsDiscoverer == nil {
		return
	}

	m.mcpBackoffMu.Lock()
	if m.mcpBackoffActive[workspaceUUID] {
		m.mcpBackoffMu.Unlock()
		return
	}
	m.mcpBackoffActive[workspaceUUID] = true
	m.mcpBackoffMu.Unlock()

	go func() {
		defer func() {
			m.mcpBackoffMu.Lock()
			delete(m.mcpBackoffActive, workspaceUUID)
			m.mcpBackoffMu.Unlock()
		}()

		if m.logger != nil {
			m.logger.Debug("mcp backoff: starting", "workspace_uuid", workspaceUUID)
		}

		policy := m.mcpBackoffPolicy
		probe := func(pctx context.Context) mcpdiscovery.ServerToolsResult {
			results, err := m.StdioToolsDiscoverer(pctx, workspaceUUID)
			if err != nil {
				// Re-evaluate the adaptive pre-warming pin even on discovery
				// error — an unhealthy probe here should keep the pin held
				// (or refresh the hysteresis reset).
				if m.PrewarmPinReevaluator != nil {
					m.PrewarmPinReevaluator(workspaceUUID)
				}
				return mcpdiscovery.ServerToolsResult{Server: workspaceUUID, Reachable: false, Err: err}
			}

			var reachableTools []MCPToolInfo
			seen := map[string]bool{}
			unreachable := 0
			for _, r := range results {
				if !r.Reachable {
					unreachable++
					continue
				}
				for _, name := range r.Tools {
					if seen[name] {
						continue
					}
					seen[name] = true
					reachableTools = append(reachableTools, MCPToolInfo{Name: name})
				}
			}

			merged, changed := m.mergeMCPToolsAdditive(workspaceUUID, reachableTools)
			if changed && onUpdate != nil {
				onUpdate(merged)
			}

			// Adaptive pre-warming re-evaluation (mitto-mw0): once per probe
			// round, ask the pin controller to re-run its health verdict.
			// Piggybacking on the MCP backoff loop keeps the pin/unpin cadence
			// aligned with actual MCP reachability changes and needs no extra
			// timer. Errors and short-circuit exits are covered by the pre-
			// return re-evaluation above and the final ExpirePinsAndAlert.
			if m.PrewarmPinReevaluator != nil {
				m.PrewarmPinReevaluator(workspaceUUID)
			}

			// Stop only when every configured server responded (none unreachable).
			return mcpdiscovery.ServerToolsResult{Server: workspaceUUID, Reachable: unreachable == 0}
		}

		_, allReachable := mcpdiscovery.RetryUntilReachable(ctx, policy, probe, nil)

		if m.logger != nil {
			m.logger.Debug("mcp backoff: stopped",
				"workspace_uuid", workspaceUUID,
				"all_reachable", allReachable)
		}
	}()
}

// EnsureMCPWatchers starts, at most once per workspace, a pool of persistent
// notifications/tools/list_changed watchers — one per server returned by
// MCPServerLister (mitto-sys.4, event-driven tool refresh, complementary to
// EnsureMCPBackoffRetry's polling). Each watcher's initial tools/list result
// and every subsequent change notification are additively merged into the
// workspace's tools cache via mergeMCPToolsAdditive (never removes tools —
// removal-on-list_changed is a documented follow-up, kept out of this
// increment for parity with the backoff path); onUpdate is invoked with the
// merged list whenever a merge actually changes it. No-op when
// MCPServerLister is nil or a pool is already active/starting for the
// workspace.
func (m *WorkspaceAuxiliaryManager) EnsureMCPWatchers(ctx context.Context, workspaceUUID string, onUpdate func([]MCPToolInfo)) {
	if m.MCPServerLister == nil {
		return
	}

	m.mcpWatchersMu.Lock()
	if _, exists := m.mcpWatchers[workspaceUUID]; exists {
		m.mcpWatchersMu.Unlock()
		return
	}
	// Reserve the slot immediately (present-but-nil) so concurrent Ensure
	// calls dedup against this pool while servers are still being listed
	// and connected.
	m.mcpWatchers[workspaceUUID] = nil
	m.mcpWatchersMu.Unlock()

	go func() {
		if m.logger != nil {
			m.logger.Debug("mcp watchers: starting", "workspace_uuid", workspaceUUID)
		}

		servers, err := m.MCPServerLister(ctx, workspaceUUID)
		if err != nil {
			if m.logger != nil {
				m.logger.Debug("mcp watchers: server lister failed",
					"workspace_uuid", workspaceUUID, "error", err.Error())
			}
			// Release the reservation so a future EnsureMCPWatchers call for
			// this workspace can retry, instead of being permanently deduped
			// against a pool that never started. Only clear it if it's still
			// the empty reservation we made above (a concurrent pool may have
			// since been created/populated after StopMCPWatchers, though that
			// races with this failed attempt).
			m.mcpWatchersMu.Lock()
			if w, ok := m.mcpWatchers[workspaceUUID]; ok && len(w) == 0 {
				delete(m.mcpWatchers, workspaceUUID)
			}
			m.mcpWatchersMu.Unlock()
			return
		}

		started := 0
		for _, srv := range servers {
			onChange := func(res mcpdiscovery.ServerToolsResult) {
				if !res.Reachable {
					return
				}
				tools := make([]MCPToolInfo, 0, len(res.Tools))
				for _, name := range res.Tools {
					tools = append(tools, MCPToolInfo{Name: name})
				}
				merged, changed := m.mergeMCPToolsAdditive(workspaceUUID, tools)
				if changed && onUpdate != nil {
					onUpdate(merged)
				}
			}

			w, initial, werr := mcpdiscovery.WatchServer(ctx, srv, m.MCPWatchTransportFactory, onChange)
			if werr != nil {
				// Unreachable at watch-time: EnsureMCPBackoffRetry's polling
				// path is responsible for retrying this server.
				if m.logger != nil {
					m.logger.Debug("mcp watchers: server unreachable, skipping",
						"workspace_uuid", workspaceUUID, "server", srv.Name, "error", werr.Error())
				}
				continue
			}

			onChange(initial) // surface tools already present at startup

			m.mcpWatchersMu.Lock()
			if _, exists := m.mcpWatchers[workspaceUUID]; !exists {
				// StopMCPWatchers/CloseAllMCPWatchers ran while we were
				// connecting: this pool was torn down. Close the just-opened
				// watcher and stop starting any more.
				m.mcpWatchersMu.Unlock()
				w.Close()
				return
			}
			m.mcpWatchers[workspaceUUID] = append(m.mcpWatchers[workspaceUUID], w)
			m.mcpWatchersMu.Unlock()
			started++
		}

		if m.logger != nil {
			m.logger.Debug("mcp watchers: started",
				"workspace_uuid", workspaceUUID,
				"server_count", len(servers),
				"watcher_count", started)
		}
	}()
}

// StopMCPWatchers closes and removes all active watchers for a workspace.
// Safe to call when no pool is active (no-op).
func (m *WorkspaceAuxiliaryManager) StopMCPWatchers(workspaceUUID string) {
	m.mcpWatchersMu.Lock()
	watchers := m.mcpWatchers[workspaceUUID]
	delete(m.mcpWatchers, workspaceUUID)
	m.mcpWatchersMu.Unlock()

	for _, w := range watchers {
		w.Close()
	}
}

// CloseAllMCPWatchers tears down every active watcher across all workspaces.
// Intended for server shutdown. Idempotent/safe when there are none.
func (m *WorkspaceAuxiliaryManager) CloseAllMCPWatchers() {
	m.mcpWatchersMu.Lock()
	all := m.mcpWatchers
	m.mcpWatchers = make(map[string][]*mcpdiscovery.ToolListWatcher)
	m.mcpWatchersMu.Unlock()

	for _, watchers := range all {
		for _, w := range watchers {
			w.Close()
		}
	}
}

// ClearMCPToolsCache clears the cached MCP tools list for a workspace.
// This can be used to force a re-fetch, for example after MCP server configuration changes.
func (m *WorkspaceAuxiliaryManager) ClearMCPToolsCache(workspaceUUID string) {
	m.mcpToolsCacheMu.Lock()
	delete(m.mcpToolsCache, workspaceUUID)
	m.mcpToolsCacheMu.Unlock()

	// Also drop the persisted snapshot so a manual refresh forces a fresh
	// probe rather than reusing stale disk state (mitto-sys.8).
	m.deletePersistedMCPTools(workspaceUUID)

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

// persistedMCPToolsSchemaVersion is the current on-disk schema version for
// persistedMCPTools. Bumped from 0 (implicit) to 1 by mitto-dza when the
// AnyUnreachable flag was added; snapshots with SchemaVersion == 0 are
// treated as suspect (one free async re-verify on load) so upgrades from
// pre-mitto-dza builds recover their LLM-only tools within seconds.
const persistedMCPToolsSchemaVersion = 1

// persistedMCPTools is the on-disk representation of a workspace's
// real-MCP-derived tools snapshot (mitto-sys.8). Only deterministic
// (direct tools/list) results are persisted; the LLM fallback is never
// written to disk (docs/devel/mcp-tool-discovery.md, Q3.3).
//
// AnyUnreachable records whether, at persist time, the deterministic
// discoverer flagged at least one configured MCP server as unreachable —
// meaning some of the workspace's tools were only known via LLM
// introspection and are therefore missing from the persisted list. On
// load, a true value triggers an async re-fetch so LLM-only tools reappear
// in-memory within seconds instead of waiting for the TTL to expire
// (mitto-dza, Fix 4).
type persistedMCPTools struct {
	Tools          []MCPToolInfo `json:"tools"`
	UpdatedAt      time.Time     `json:"updated_at"`
	SchemaVersion  int           `json:"schema_version,omitempty"`
	AnyUnreachable bool          `json:"any_unreachable,omitempty"`
}

// mcpToolsPersistPath returns the on-disk path for a workspace's persisted
// real-MCP tools snapshot, or "" when persistence is disabled or the workspace
// UUID is empty.
func (m *WorkspaceAuxiliaryManager) mcpToolsPersistPath(workspaceUUID string) string {
	if m.MCPToolsPersistDir == "" || workspaceUUID == "" {
		return ""
	}
	return filepath.Join(m.MCPToolsPersistDir, workspaceUUID+".json")
}

// loadPersistedMCPTools returns the persisted real-MCP tools for a workspace
// when a snapshot exists and is still within the TTL, along with a `suspect`
// flag that is true when the caller should trigger an async re-verify — either
// because the snapshot was written while at least one MCP server was
// unreachable to the deterministic discoverer (so LLM-only tools are missing
// from disk, mitto-dza Fix 4), or because it predates the current schema
// (SchemaVersion == 0) and its trust semantics are unknown. Returns
// (nil, false, false) when persistence is disabled, the file is missing/
// unreadable, the snapshot is empty, or it has expired.
func (m *WorkspaceAuxiliaryManager) loadPersistedMCPTools(workspaceUUID string) ([]MCPToolInfo, bool, bool) {
	path := m.mcpToolsPersistPath(workspaceUUID)
	if path == "" {
		return nil, false, false
	}

	var snapshot persistedMCPTools
	if err := fileutil.ReadJSON(path, &snapshot); err != nil {
		return nil, false, false
	}
	if len(snapshot.Tools) == 0 {
		return nil, false, false
	}

	ttl := m.mcpToolsTTL
	if ttl <= 0 {
		ttl = defaultMCPToolsTTL
	}
	if age := time.Since(snapshot.UpdatedAt); age > ttl {
		if m.logger != nil {
			m.logger.Debug("mcp tools persist: snapshot expired, forcing re-probe",
				"workspace_uuid", workspaceUUID,
				"age", age.String())
		}
		return nil, false, false
	}

	suspect := snapshot.AnyUnreachable || snapshot.SchemaVersion < persistedMCPToolsSchemaVersion
	return snapshot.Tools, true, suspect
}

// savePersistedMCPTools writes a workspace's real-MCP-derived tools snapshot to
// disk atomically. It is a no-op when persistence is disabled or tools is empty.
// Only deterministic results must be passed here — never the LLM fallback list.
// anyUnreachable records whether the deterministic discoverer flagged at least
// one MCP server as unreachable at persist time; a true value tells a later
// loadPersistedMCPTools to trigger an async re-verify so LLM-only tools can
// reappear after a restart (mitto-dza, Fix 4).
func (m *WorkspaceAuxiliaryManager) savePersistedMCPTools(workspaceUUID string, tools []MCPToolInfo, anyUnreachable bool) {
	path := m.mcpToolsPersistPath(workspaceUUID)
	if path == "" || len(tools) == 0 {
		return
	}

	snapshot := persistedMCPTools{
		Tools:          tools,
		UpdatedAt:      time.Now(),
		SchemaVersion:  persistedMCPToolsSchemaVersion,
		AnyUnreachable: anyUnreachable,
	}
	if err := fileutil.WriteJSONAtomic(path, &snapshot, 0o644); err != nil {
		if m.logger != nil {
			m.logger.Warn("mcp tools persist: failed to write snapshot",
				"workspace_uuid", workspaceUUID,
				"error", err.Error())
		}
	}
}

// deletePersistedMCPTools removes a workspace's persisted tools snapshot, if
// any. Used by ClearMCPToolsCache so a manual refresh forces a fresh probe.
func (m *WorkspaceAuxiliaryManager) deletePersistedMCPTools(workspaceUUID string) {
	path := m.mcpToolsPersistPath(workspaceUUID)
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		if m.logger != nil {
			m.logger.Warn("mcp tools persist: failed to remove snapshot",
				"workspace_uuid", workspaceUUID,
				"error", err.Error())
		}
	}
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

	prompt := fmt.Sprintf(CheckToolPatternsPromptTemplate, patternsStr)

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

	result, err := parseToolPatternsCheck(response)
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
