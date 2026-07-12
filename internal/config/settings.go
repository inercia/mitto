package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/fileutil"
	"github.com/inercia/mitto/internal/secrets"

	defaultConfig "github.com/inercia/mitto/config"
)

// ConfigSource indicates where the configuration was loaded from.
type ConfigSource int

const (
	// ConfigSourceNone indicates no configuration was loaded.
	ConfigSourceNone ConfigSource = iota
	// ConfigSourceRCFile indicates configuration was loaded from ~/.mittorc or equivalent.
	ConfigSourceRCFile
	// ConfigSourceSettingsJSON indicates configuration was loaded from settings.json.
	ConfigSourceSettingsJSON
	// ConfigSourceEmbeddedDefaults indicates configuration was loaded from embedded defaults.
	ConfigSourceEmbeddedDefaults
	// ConfigSourceCustomFile indicates configuration was loaded from a custom file (--config flag).
	ConfigSourceCustomFile
)

// LoadResult contains the loaded configuration and metadata about its source.
type LoadResult struct {
	// Config is the loaded configuration.
	Config *Config
	// Source indicates where the configuration was loaded from.
	Source ConfigSource
	// SourcePath is the path to the configuration file (empty for embedded defaults).
	SourcePath string
	// RCFilePath is the path to the RC file if one was used in the merge.
	// This is set even when Source is ConfigSourceSettingsJSON if an RC file was merged.
	RCFilePath string
	// HasRCFileServers indicates whether any ACP servers came from the RC file.
	// When true, those servers should be treated as read-only in the UI.
	HasRCFileServers bool
}

// Settings represents the persisted Mitto settings in JSON format.
// This struct mirrors the Config struct but uses JSON serialization
// and is stored in the Mitto data directory as settings.json.
type Settings struct {
	// ACPServers is the list of configured ACP servers (order matters - first is default)
	ACPServers []ACPServerSettings `json:"acp_servers"`
	// Prompts is a list of predefined prompts for the dropup menu (global prompts)
	Prompts []WebPrompt `json:"prompts,omitempty"`
	// PromptsDirs is a list of additional directories to search for prompt files.
	// These are searched in addition to the default MITTO_DIR/prompts/ directory.
	PromptsDirs []string `json:"prompts_dirs,omitempty"`
	// Web contains web interface configuration
	Web WebConfig `json:"web"`
	// UI contains desktop app UI configuration
	UI UIConfig `json:"ui,omitempty"`
	// Session contains session storage limits configuration
	Session *SessionConfig `json:"session,omitempty"`
	// Prewarm contains adaptive ACP/MCP pre-warming thresholds (mitto-mw0)
	Prewarm *PrewarmConfig `json:"prewarm,omitempty"`
	// Conversations contains global conversation processing configuration
	Conversations *ConversationsConfig `json:"conversations,omitempty"`
	// Permissions contains global permission handling configuration
	Permissions *PermissionsConfig `json:"permissions,omitempty"`
	// RestrictedRunners contains per-runner-type global configuration
	RestrictedRunners map[string]*WorkspaceRunnerConfig `json:"restricted_runners,omitempty"`
	// MCP contains MCP (Model Context Protocol) server configuration
	MCP *MCPConfig `json:"mcp,omitempty"`
	// Models is the list of named model profiles (criteria + tags)
	Models []ModelProfile `json:"models,omitempty"`
	// Shortcuts holds global per-section configurable shortcut buttons, keyed by
	// section ID (e.g. "conversations"). Merged with folder-level shortcuts at
	// render time (global entries first).
	Shortcuts map[string][]ShortcutButton `json:"shortcuts,omitempty"`
}

// DefaultStartupStaggerMs is the default stagger delay in milliseconds between
// session resumes on startup for sessions sharing the same ACP process.
const DefaultStartupStaggerMs = 300

// DefaultStartupLoopDelay is the default delay before the loop runner
// starts its first poll on startup. This gives interactive sessions time to
// resume first via WebSocket connections.
const DefaultStartupLoopDelay = 15 * time.Second

// DefaultStartupResumeConcurrency is the default maximum number of concurrent
// interactive ResumeSession calls issued from the cold-start WebSocket fan-out.
// Bounding this prevents the Mitto process from saturating itself (which can
// starve the agent's inbound MCP handshake on :5757/mcp) when many sessions
// reconnect at once (mitto-54k.1).
const DefaultStartupResumeConcurrency = 3

// DefaultLoopWorkspaceConcurrency caps how many scheduled loop prompts may be
// in flight simultaneously per WorkingDir + ACPServer pair. Because shared
// ACP processes (e.g. Auggie 0.32.x) do not parallelize prompts across
// sessions, dispatching multiple loop prompts to the same process at the same
// instant wedges all of them behind the shared inbox until the 10-minute
// watchdog. Capping at 1 stagger loops naturally without adding sleeps to the
// poll goroutine (mitto-61z). 0 disables the cap.
const DefaultLoopWorkspaceConcurrency = 1

// SessionConfig represents session storage configuration.
type SessionConfig struct {
	// MaxMessagesPerSession is the maximum number of messages to retain per conversation.
	// When exceeded, oldest messages are automatically pruned after each new event.
	// Default: 2000 (applied at runtime by SessionManager when not explicitly set).
	// Set to a negative value to disable auto-pruning entirely.
	// Exposed in the Settings dialog under Conversations > Conversation History.
	MaxMessagesPerSession int `json:"max_messages_per_session,omitempty"`
	// MaxSessionSizeBytes is the maximum total size in bytes for a session's stored data.
	// When exceeded, oldest messages are pruned. Default: 0 (unlimited)
	// Not exposed in the Settings dialog.
	MaxSessionSizeBytes int64 `json:"max_session_size_bytes,omitempty"`
	// ArchiveRetentionPeriod specifies how long archived conversations are kept before auto-deletion.
	// Values: "never" (default - keep forever), "1d", "1w", "1m", "3m" (1 day, 1 week, 1 month, 3 months)
	ArchiveRetentionPeriod string `json:"archive_retention_period,omitempty"`
	// AutoArchiveInactiveAfter specifies how long a conversation must be inactive before being auto-archived.
	// Values: "" (default - disabled), "1d", "1w", "1m", "3m" (1 day, 1 week, 1 month, 3 months)
	AutoArchiveInactiveAfter string `json:"auto_archive_inactive_after,omitempty"`
	// StartupStaggerMs is the delay in milliseconds between consecutive session resumes on startup
	// for sessions sharing the same ACP process. This prevents overwhelming the ACP SDK's internal
	// notification channel when many sessions resume simultaneously.
	// Default: 0 (use DefaultStartupStaggerMs = 300 ms). Set to -1 to disable staggering entirely.
	StartupStaggerMs int `json:"startup_stagger_ms,omitempty"`
	// StartupLoopDelaySeconds is the delay in seconds before the loop runner
	// starts its first poll on startup. This gives interactive sessions time to resume
	// first via WebSocket connections, preventing thundering herd on ACP.
	// Default: 15 seconds. Set to 0 to disable (not recommended).
	StartupLoopDelaySeconds int `json:"startup_loop_delay_seconds,omitempty"`
	// StartupResumeConcurrency caps the number of concurrent interactive
	// ResumeSession calls issued from the cold-start WebSocket fan-out. When many
	// browsers reconnect at once (or a single browser holds many session tabs),
	// unbounded fan-out saturates the Mitto process and starves the agent's
	// inbound MCP handshake on :5757/mcp (see mitto-54k). The user-focused
	// ensure_resumed path is NOT throttled.
	// Default: 0 (use DefaultStartupResumeConcurrency = 3). Values <1 are clamped
	// to 1 (a semaphore of size 0 would deadlock every resume).
	StartupResumeConcurrency int `json:"startup_resume_concurrency,omitempty"`
	// LoopWorkspaceConcurrency caps how many scheduled loop prompts may be in
	// flight simultaneously per WorkingDir + ACPServer pair. When more than one
	// loop conversation in the same workspace becomes due at the same instant,
	// dispatching them concurrently to a shared ACP process wedges all of them
	// behind the shared inbox (mitto-61z). The scheduler skips over-capacity
	// sessions for the current poll cycle and retries on the next tick — no
	// schedule advance and no failure backoff for the skipped session. Manual
	// "Run Now" (forced) deliveries always bypass this cap.
	// Default: 0 (use DefaultLoopWorkspaceConcurrency = 1). Set to a large value
	// (or use the getter's semantics) to effectively disable the cap.
	LoopWorkspaceConcurrency int `json:"loop_workspace_concurrency,omitempty"`
	// LoopSuspendTimeout controls when idle loop conversations have their ACP
	// connection suspended to save memory. When a loop conversation's next prompt
	// is farther away than this timeout, its ACP session is closed even if the user has
	// it open in the sidebar. The conversation resumes transparently when focused or
	// when its loop prompt is due.
	// Values: "" (default - 30 minutes), "disabled", "15m", "30m", "1h", "2h"
	// Exposed in the Settings dialog under Conversations > Suspend Settings.
	LoopSuspendTimeout string `json:"loop_suspend_timeout,omitempty"`
	// MemoryRecycleThreshold controls when an idle shared ACP agent process is
	// recycled (stopped) to reclaim memory once its RSS (summed over the process
	// tree) exceeds this size. Recycling only affects fully-idle processes;
	// conversations resume transparently when focused. Values: "" (default,
	// disabled), "disabled", "3g", "4g", "6g", "8g".
	MemoryRecycleThreshold string `json:"memory_recycle_threshold,omitempty"`
	// AgentInactivityTimeout controls how long a prompt may go with zero streamed
	// agent activity (no tool call/UI prompt in flight) before the prompt inactivity
	// watchdog cancels it, clearing is_prompting and surfacing a recoverable error.
	// This breaks the GC deadlock where a wedged shared ACP process pins a session
	// as stuck forever. Values: "" (default, 10m), "disabled", "5m", "10m", "15m", "30m".
	AgentInactivityTimeout string `json:"agent_inactivity_timeout,omitempty"`
	// McpInitTimeout controls the extended per-attempt/total budget granted to the
	// very first session/new (and session/load) on a cold shared ACP process when
	// the request carries MCP servers. Rationale (mitto-8ul.1): agents (Auggie in
	// particular) block servicing session/new until MCP init completes, and their
	// internal MCP wait is ~225s — well past Mitto's normal 25s per-attempt budget.
	// A cold cold-start with MCP servers therefore fails as "context deadline
	// exceeded" even though the agent would eventually respond. This timeout
	// widens the budget for that first cold call only; once the process has
	// completed one successful session/new (or observed all-servers-ready) the
	// normal 25s budget is used again. Values: "" (default, 240s covering
	// Auggie's 225s + margin), "disabled" (use the normal budget), "120s"/"2m",
	// "240s"/"4m", "300s"/"5m".
	McpInitTimeout string `json:"mcp_init_timeout,omitempty"`
}

// ArchiveRetentionNever is the value for keeping archived conversations forever.
const ArchiveRetentionNever = "never"

// ValidArchiveRetentionPeriods contains all valid retention period values.
var ValidArchiveRetentionPeriods = []string{ArchiveRetentionNever, "1d", "1w", "1m", "3m"}

// GetArchiveRetentionPeriod returns the archive retention period, or "never" if not set.
func (c *SessionConfig) GetArchiveRetentionPeriod() string {
	if c == nil || c.ArchiveRetentionPeriod == "" {
		return ArchiveRetentionNever
	}
	return c.ArchiveRetentionPeriod
}

// GetAutoArchiveInactiveAfter returns the auto-archive inactive period string, or "" if not set.
func (c *SessionConfig) GetAutoArchiveInactiveAfter() string {
	if c == nil {
		return ""
	}
	return c.AutoArchiveInactiveAfter
}

// ValidLoopSuspendTimeouts contains all valid loop suspend timeout values.
var ValidLoopSuspendTimeouts = []string{"", "disabled", "15m", "30m", "1h", "2h"}

// GetLoopSuspendTimeout returns the loop suspend timeout string, or "" if not set.
func (c *SessionConfig) GetLoopSuspendTimeout() string {
	if c == nil {
		return ""
	}
	return c.LoopSuspendTimeout
}

// ParseLoopSuspendTimeout converts the loop suspend timeout string to a time.Duration.
// Returns the duration and true if the feature is enabled, or 0 and false if disabled.
// An empty string returns the default of 30 minutes.
func (c *SessionConfig) ParseLoopSuspendTimeout() (time.Duration, bool) {
	val := c.GetLoopSuspendTimeout()
	switch val {
	case "disabled":
		return 0, false
	case "", "30m":
		return 30 * time.Minute, true
	case "15m":
		return 15 * time.Minute, true
	case "1h":
		return time.Hour, true
	case "2h":
		return 2 * time.Hour, true
	default:
		// Unknown value — use default
		return 30 * time.Minute, true
	}
}

// ValidMemoryRecycleThresholds contains all valid memory recycle threshold values.
var ValidMemoryRecycleThresholds = []string{"", "disabled", "3g", "4g", "6g", "8g"}

// GetMemoryRecycleThreshold returns the memory recycle threshold string, or "" if not set.
func (c *SessionConfig) GetMemoryRecycleThreshold() string {
	if c == nil {
		return ""
	}
	return c.MemoryRecycleThreshold
}

// ParseMemoryRecycleThreshold returns the threshold in bytes and true when enabled,
// or (0, false) when disabled. Empty string = disabled (opt-in feature).
func (c *SessionConfig) ParseMemoryRecycleThreshold() (uint64, bool) {
	switch c.GetMemoryRecycleThreshold() {
	case "", "disabled":
		return 0, false
	case "3g":
		return uint64(3) * 1024 * 1024 * 1024, true
	case "4g":
		return uint64(4) * 1024 * 1024 * 1024, true
	case "6g":
		return uint64(6) * 1024 * 1024 * 1024, true
	case "8g":
		return uint64(8) * 1024 * 1024 * 1024, true
	default:
		// Unknown → disabled (safe)
		return 0, false
	}
}

// ValidAgentInactivityTimeouts contains all valid agent inactivity timeout values.
var ValidAgentInactivityTimeouts = []string{"", "disabled", "5m", "10m", "15m", "30m"}

// GetAgentInactivityTimeout returns the agent inactivity timeout string, or "" if not set.
func (c *SessionConfig) GetAgentInactivityTimeout() string {
	if c == nil {
		return ""
	}
	return c.AgentInactivityTimeout
}

// ParseAgentInactivityTimeout converts the agent inactivity timeout string to a
// time.Duration. Returns the duration and true if the watchdog cancellation is
// enabled, or 0 and false if disabled. An empty string returns the default of 10
// minutes (enabled) — unlike MemoryRecycleThreshold, this feature defaults to on.
func (c *SessionConfig) ParseAgentInactivityTimeout() (time.Duration, bool) {
	switch c.GetAgentInactivityTimeout() {
	case "disabled":
		return 0, false
	case "", "10m":
		return 10 * time.Minute, true
	case "5m":
		return 5 * time.Minute, true
	case "15m":
		return 15 * time.Minute, true
	case "30m":
		return 30 * time.Minute, true
	default:
		// Unknown value — use default
		return 10 * time.Minute, true
	}
}

// ValidMcpInitTimeouts contains all valid MCP-init timeout values (mitto-8ul.1).
var ValidMcpInitTimeouts = []string{"", "disabled", "120s", "2m", "240s", "4m", "300s", "5m"}

// GetMcpInitTimeout returns the MCP-init timeout string, or "" if not set.
func (c *SessionConfig) GetMcpInitTimeout() string {
	if c == nil {
		return ""
	}
	return c.McpInitTimeout
}

// ParseMcpInitTimeout converts the MCP-init timeout string to a time.Duration.
// Returns (duration, true) when the extended cold-start budget is enabled and
// (0, false) when disabled. Empty string returns the default of 240s, which
// covers Auggie's internal 225s MCP-init wait + margin (mitto-8ul.1). Unknown
// values fall back to the default rather than silently disabling the feature.
func (c *SessionConfig) ParseMcpInitTimeout() (time.Duration, bool) {
	switch c.GetMcpInitTimeout() {
	case "disabled":
		return 0, false
	case "", "240s", "4m":
		return 240 * time.Second, true
	case "120s", "2m":
		return 120 * time.Second, true
	case "300s", "5m":
		return 300 * time.Second, true
	default:
		// Unknown value — use default
		return 240 * time.Second, true
	}
}

// GetStartupStaggerMs returns the stagger delay in milliseconds between consecutive session
// resumes on startup for sessions sharing the same ACP process.
// Returns DefaultStartupStaggerMs (300 ms) if not configured (0).
// Returns 0 (no stagger) if explicitly set to -1.
func (c *SessionConfig) GetStartupStaggerMs() int {
	if c == nil || c.StartupStaggerMs == 0 {
		return DefaultStartupStaggerMs
	}
	if c.StartupStaggerMs < 0 {
		return 0
	}
	return c.StartupStaggerMs
}

// GetStartupLoopDelay returns the startup delay for the loop runner.
// Returns DefaultStartupLoopDelay (15s) if not configured (0).
// Returns 0 to disable if explicitly set to a negative value.
func (c *SessionConfig) GetStartupLoopDelay() time.Duration {
	if c == nil || c.StartupLoopDelaySeconds == 0 {
		return DefaultStartupLoopDelay
	}
	if c.StartupLoopDelaySeconds < 0 {
		return 0
	}
	return time.Duration(c.StartupLoopDelaySeconds) * time.Second
}

// GetStartupResumeConcurrency returns the maximum number of concurrent
// interactive ResumeSession calls issued from the cold-start WebSocket fan-out.
// Returns DefaultStartupResumeConcurrency (3) if not configured (0). Values <1
// are clamped to 1 — a bound of 0 would deadlock every resume.
func (c *SessionConfig) GetStartupResumeConcurrency() int {
	if c == nil || c.StartupResumeConcurrency == 0 {
		return DefaultStartupResumeConcurrency
	}
	if c.StartupResumeConcurrency < 1 {
		return 1
	}
	return c.StartupResumeConcurrency
}

// GetLoopWorkspaceConcurrency returns the maximum number of scheduled loop
// prompts that may be in flight simultaneously per WorkingDir + ACPServer
// pair. Returns DefaultLoopWorkspaceConcurrency (1) if not configured (0).
// Negative values are treated as 0 (cap disabled). Manual "Run Now" (forced)
// deliveries always bypass the cap (mitto-61z).
func (c *SessionConfig) GetLoopWorkspaceConcurrency() int {
	if c == nil || c.LoopWorkspaceConcurrency == 0 {
		return DefaultLoopWorkspaceConcurrency
	}
	if c.LoopWorkspaceConcurrency < 0 {
		return 0
	}
	return c.LoopWorkspaceConcurrency
}

// PrewarmConfig represents adaptive ACP/MCP pre-warming thresholds (mitto-mw0).
// Pre-warming warms a workspace, probes its health (session/new latency + MCP
// readiness), and pins a warm keepalive session only for slow/broken workspaces.
type PrewarmConfig struct {
	// SessionNewFast is T_fast: session/new latency at/under which a workspace is
	// considered "fast" and does NOT need pinning. Aligned with the startup
	// watchdog WARN threshold at 10s.
	// Values: "" (default, 10s), "5s", "10s", "20s", "30s".
	SessionNewFast string `json:"session_new_fast,omitempty"`
	// McpReady is T_mcp: max time for all configured MCP servers to be reachable
	// before the workspace is flagged as slow.
	// Values: "" (default, 10s), "5s", "10s", "20s", "30s".
	McpReady string `json:"mcp_ready,omitempty"`
	// HealthyProbesToUnpin is the hysteresis N: consecutive healthy probes
	// required before unpinning a pinned workspace. Default: 3.
	HealthyProbesToUnpin int `json:"healthy_probes_to_unpin,omitempty"`
	// MaxPinDuration caps how long a pinned keepalive session is held before
	// giving up + alerting. "disabled" means no cap.
	// Values: "" (default, 30m), "disabled", "5m", "15m", "30m", "1h", "2h".
	MaxPinDuration string `json:"max_pin_duration,omitempty"`
	// MaxPinnedWorkspaces is the blast-radius cap on simultaneously-pinned
	// workspaces. Default: 5.
	MaxPinnedWorkspaces int `json:"max_pinned_workspaces,omitempty"`
	// AuxSchedule holds per-purpose staggered creation delays for the cold-start
	// auxiliary session prewarm (mitto-cgc). When nil, the per-purpose defaults
	// apply. Serialized creation guarantees <=1 concurrent session/new
	// regardless of the delay values.
	AuxSchedule *AuxScheduleConfig `json:"aux_schedule,omitempty"`
}

// AuxScheduleConfig holds per-purpose staggered creation delays for cold-start
// auxiliary session pre-warming (mitto-cgc). Each field is a Go duration string
// (e.g. "0s", "5s", "8s") parsed with time.ParseDuration. Empty or invalid
// values fall back to the per-purpose defaults. Serialized creation guarantees
// <=1 concurrent session/new regardless of these values.
type AuxScheduleConfig struct {
	McpCheck string `json:"mcp_check,omitempty"`
	McpTools string `json:"mcp_tools,omitempty"`
	TitleGen string `json:"title_gen,omitempty"`
	FollowUp string `json:"follow_up,omitempty"`
}

// AuxPrewarmEntry is one scheduled auxiliary prewarm creation (mitto-cgc).
// Purpose mirrors the string constants in internal/auxiliary (PurposeMCPCheck,
// PurposeMCPTools, PurposeTitleGen, PurposeFollowUp); Delay is measured from
// the prewarm anchor (moment prewarmAuxiliarySessions starts).
type AuxPrewarmEntry struct {
	Purpose string
	Delay   time.Duration
}

// Prewarm defaults (mitto-mw0).
const (
	DefaultPrewarmSessionNewFast       = 10 * time.Second
	DefaultPrewarmMcpReady             = 10 * time.Second
	DefaultPrewarmHealthyProbesToUnpin = 3
	DefaultPrewarmMaxPinDuration       = 30 * time.Minute
	DefaultPrewarmMaxPinnedWorkspaces  = 5
)

// Auxiliary prewarm per-purpose delay defaults (mitto-cgc, widened in
// mitto-7yj). Priority order is mcp-check/mcp-tools (tier 0) → title-gen
// (tier 1) → follow-up (tier 2).
//
// Two default sets exist:
//
//   - Multiplex agents (e.g. auggie): a single node process handles all ACP
//     sessions, so aux session/new is cheap. The defaults are aggressive but
//     no two aux purposes share the 0s slot — mcp-tools is nudged to 2s so
//     the tier-0 pair does not start simultaneously (mitto-7yj rush-friendly
//     stagger).
//
//   - Fork-per-session agents (e.g. Claude Code via @zed-industries/
//     claude-agent-acp): each ACP session/new forks a fresh `claude` OS
//     process which pins ~memory + CPU per aux session and creates a
//     synchronous cold-fork storm during prewarm. The defaults are widely
//     spread so real user demand can preempt (mitto-7yj rush-on-demand).
const (
	// Multiplex (auggie) defaults.
	DefaultAuxDelayMcpCheck = 0 * time.Second
	DefaultAuxDelayMcpTools = 2 * time.Second
	DefaultAuxDelayTitleGen = 8 * time.Second
	DefaultAuxDelayFollowUp = 12 * time.Second

	// Fork-per-session (Claude Code) defaults (mitto-7yj). Widely spread so
	// each cold `claude` fork does not pile onto the previous one, and so
	// getOrCreateAuxiliarySession callers can rush the schedule for any
	// purpose actually needed by user activity.
	DefaultAuxDelayForkMcpCheck = 0 * time.Second
	DefaultAuxDelayForkMcpTools = 8 * time.Second
	DefaultAuxDelayForkTitleGen = 20 * time.Second
	DefaultAuxDelayForkFollowUp = 35 * time.Second
)

// Purpose strings mirroring internal/auxiliary.Purpose* — hardcoded here to
// avoid an internal/config → internal/auxiliary dependency edge (mitto-cgc).
// The acpproc consumer references the auxiliary.Purpose* constants directly
// so any rename there is caught at compile time.
const (
	auxPurposeMcpCheck = "mcp-check"
	auxPurposeMcpTools = "mcp-tools"
	auxPurposeTitleGen = "title-gen"
	auxPurposeFollowUp = "follow-up"
)

// ValidSessionNewFast lists accepted values for PrewarmConfig.SessionNewFast.
var ValidSessionNewFast = []string{"", "5s", "10s", "20s", "30s"}

// GetSessionNewFast returns the SessionNewFast string, or "" if not set.
func (c *PrewarmConfig) GetSessionNewFast() string {
	if c == nil {
		return ""
	}
	return c.SessionNewFast
}

// ParseSessionNewFast converts the SessionNewFast string to a time.Duration.
// Returns (duration, true) — this threshold is always enabled. Empty/unknown
// values fall back to the 10s default.
func (c *PrewarmConfig) ParseSessionNewFast() (time.Duration, bool) {
	switch c.GetSessionNewFast() {
	case "", "10s":
		return DefaultPrewarmSessionNewFast, true
	case "5s":
		return 5 * time.Second, true
	case "20s":
		return 20 * time.Second, true
	case "30s":
		return 30 * time.Second, true
	default:
		return DefaultPrewarmSessionNewFast, true
	}
}

// ValidMcpReady lists accepted values for PrewarmConfig.McpReady.
var ValidMcpReady = []string{"", "5s", "10s", "20s", "30s"}

// GetMcpReady returns the McpReady string, or "" if not set.
func (c *PrewarmConfig) GetMcpReady() string {
	if c == nil {
		return ""
	}
	return c.McpReady
}

// ParseMcpReady converts the McpReady string to a time.Duration.
// Returns (duration, true) — this threshold is always enabled. Empty/unknown
// values fall back to the 10s default.
func (c *PrewarmConfig) ParseMcpReady() (time.Duration, bool) {
	switch c.GetMcpReady() {
	case "", "10s":
		return DefaultPrewarmMcpReady, true
	case "5s":
		return 5 * time.Second, true
	case "20s":
		return 20 * time.Second, true
	case "30s":
		return 30 * time.Second, true
	default:
		return DefaultPrewarmMcpReady, true
	}
}

// GetHealthyProbesToUnpin returns the hysteresis count, or the default (3)
// when unset or non-positive.
func (c *PrewarmConfig) GetHealthyProbesToUnpin() int {
	if c == nil || c.HealthyProbesToUnpin <= 0 {
		return DefaultPrewarmHealthyProbesToUnpin
	}
	return c.HealthyProbesToUnpin
}

// ValidMaxPinDurations lists accepted values for PrewarmConfig.MaxPinDuration.
var ValidMaxPinDurations = []string{"", "disabled", "5m", "15m", "30m", "1h", "2h"}

// GetMaxPinDuration returns the MaxPinDuration string, or "" if not set.
func (c *PrewarmConfig) GetMaxPinDuration() string {
	if c == nil {
		return ""
	}
	return c.MaxPinDuration
}

// ParseMaxPinDuration converts the MaxPinDuration string to a time.Duration.
// Returns (duration, true) when a cap applies, or (0, false) when "disabled"
// (no cap). Empty/unknown values fall back to the 30m default.
func (c *PrewarmConfig) ParseMaxPinDuration() (time.Duration, bool) {
	switch c.GetMaxPinDuration() {
	case "disabled":
		return 0, false
	case "", "30m":
		return DefaultPrewarmMaxPinDuration, true
	case "5m":
		return 5 * time.Minute, true
	case "15m":
		return 15 * time.Minute, true
	case "1h":
		return time.Hour, true
	case "2h":
		return 2 * time.Hour, true
	default:
		return DefaultPrewarmMaxPinDuration, true
	}
}

// GetMaxPinnedWorkspaces returns the blast-radius cap, or the default (5)
// when unset or non-positive.
func (c *PrewarmConfig) GetMaxPinnedWorkspaces() int {
	if c == nil || c.MaxPinnedWorkspaces <= 0 {
		return DefaultPrewarmMaxPinnedWorkspaces
	}
	return c.MaxPinnedWorkspaces
}

// parseAuxDelay parses a Go duration string. On empty or parse error it returns
// the supplied default. Negative durations are clamped to 0 (a negative offset
// would fire immediately, which is fine, but 0 is clearer to log).
func parseAuxDelay(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return def
	}
	if d < 0 {
		return 0
	}
	return d
}

// AuxPrewarmSchedule returns the priority-ordered, staggered auxiliary prewarm
// schedule with defaults applied (mitto-cgc). Nil-safe on the receiver. Entries
// are returned in nondecreasing Delay order so a single worker can sleep-until
// each offset. The ordering (mcp-check, mcp-tools, title-gen, follow-up) also
// encodes tier priority: tier-0 purposes ship first so tool-gating is ready
// before higher-tier auxiliaries compete for the shared ACP process.
//
// forkPerSession selects between the multiplex (auggie) and fork-per-session
// (Claude Code) default sets (mitto-7yj). An explicitly-set (non-empty)
// AuxScheduleConfig entry STILL overrides the chosen default, so callers can
// force any schedule regardless of agent kind.
func (c *PrewarmConfig) AuxPrewarmSchedule(forkPerSession bool) []AuxPrewarmEntry {
	var sched *AuxScheduleConfig
	if c != nil {
		sched = c.AuxSchedule
	}
	var mcpCheck, mcpTools, titleGen, followUp string
	if sched != nil {
		mcpCheck = sched.McpCheck
		mcpTools = sched.McpTools
		titleGen = sched.TitleGen
		followUp = sched.FollowUp
	}
	defMcpCheck := DefaultAuxDelayMcpCheck
	defMcpTools := DefaultAuxDelayMcpTools
	defTitleGen := DefaultAuxDelayTitleGen
	defFollowUp := DefaultAuxDelayFollowUp
	if forkPerSession {
		defMcpCheck = DefaultAuxDelayForkMcpCheck
		defMcpTools = DefaultAuxDelayForkMcpTools
		defTitleGen = DefaultAuxDelayForkTitleGen
		defFollowUp = DefaultAuxDelayForkFollowUp
	}
	entries := []AuxPrewarmEntry{
		{Purpose: auxPurposeMcpCheck, Delay: parseAuxDelay(mcpCheck, defMcpCheck)},
		{Purpose: auxPurposeMcpTools, Delay: parseAuxDelay(mcpTools, defMcpTools)},
		{Purpose: auxPurposeTitleGen, Delay: parseAuxDelay(titleGen, defTitleGen)},
		{Purpose: auxPurposeFollowUp, Delay: parseAuxDelay(followUp, defFollowUp)},
	}
	// Stable sort by Delay so the single-worker consumer can sleep-until each
	// offset. sortAuxPrewarmByDelay (a stable insertion sort) preserves the tier
	// ordering above when two purposes share the same delay (e.g. mcp-check and
	// mcp-tools at 0s).
	sortAuxPrewarmByDelay(entries)
	return entries
}

// sortAuxPrewarmByDelay stable-sorts entries in nondecreasing Delay order.
// Extracted so it can be unit-tested independently and to keep AuxPrewarmSchedule
// small.
func sortAuxPrewarmByDelay(entries []AuxPrewarmEntry) {
	// Insertion sort is fine for the tiny fixed length (4). Keeps the file
	// free of a sort import that already exists elsewhere.
	for i := 1; i < len(entries); i++ {
		j := i
		for j > 0 && entries[j-1].Delay > entries[j].Delay {
			entries[j-1], entries[j] = entries[j], entries[j-1]
			j--
		}
	}
}

// ScannerDefenseConfig holds configuration for the scanner defense system.
type ScannerDefenseConfig struct {
	// Enabled controls whether scanner defense is active.
	Enabled bool `json:"enabled"`
	// RateLimit is the maximum number of requests per RateWindowSeconds before blocking.
	RateLimit int `json:"rate_limit,omitempty"`
	// RateWindowSeconds is the rate limiting window in seconds.
	RateWindowSeconds int `json:"rate_window_seconds,omitempty"`
	// ErrorRateThreshold is the error rate (0.0-1.0) above which an IP is blocked.
	ErrorRateThreshold float64 `json:"error_rate_threshold,omitempty"`
	// MinRequestsForAnalysis is the minimum requests needed before analyzing error rates.
	MinRequestsForAnalysis int `json:"min_requests,omitempty"`
	// SuspiciousPathThreshold is the number of suspicious path hits that trigger a block.
	SuspiciousPathThreshold int `json:"suspicious_path_threshold,omitempty"`
	// BlockDurationSeconds is how long an IP remains blocked in seconds.
	BlockDurationSeconds int `json:"block_duration_seconds,omitempty"`
	// Whitelist contains CIDR notation ranges that should never be blocked.
	Whitelist []string `json:"whitelist,omitempty"`
	// IPBlockCommand is an optional external command to run when an IP is blocked.
	// The placeholder {ip} is replaced with the blocked IP address.
	// Example: "pfctl -t mitto_blocked -T add {ip}" or "iptables -A INPUT -s {ip} -j DROP"
	// If empty, no external command is executed (only in-memory blocklist is used).
	IPBlockCommand string `json:"ip_block_command,omitempty" yaml:"ip_block_command,omitempty"`
}

// ACPServerSettings is the JSON representation of an ACP server.
type ACPServerSettings struct {
	// Name is the identifier for this ACP server
	Name string `json:"name"`
	// Command is the shell command to start the ACP server
	Command string `json:"command"`
	// Cwd is the working directory for the ACP server process
	Cwd string `json:"cwd,omitempty"`
	// Type is an optional type identifier for prompt matching.
	// Servers with the same type share prompts. If empty, Name is used as the type.
	Type string `json:"type,omitempty"`
	// Env is a map of environment variables to set when starting the ACP server.
	// These are merged with the current environment (server-specific vars take precedence).
	Env map[string]string `json:"env,omitempty"`
	// Prompts is an optional list of predefined prompts specific to this ACP server
	Prompts []WebPrompt `json:"prompts,omitempty"`
	// RestrictedRunners contains per-runner-type configuration for this agent
	RestrictedRunners map[string]*WorkspaceRunnerConfig `json:"restricted_runners,omitempty"`
	// Source indicates where this server configuration originated from.
	// Used for config layering: servers from RC file are read-only in the UI.
	Source ConfigItemSource `json:"source,omitempty"`
	// AutoApprove enables automatic approval of permission requests for this ACP server.
	AutoApprove bool `json:"auto_approve,omitempty"`
	// Tags is an optional list of categorization tags for this ACP server.
	Tags []string `json:"tags,omitempty"`
	// ModelProfile is the name of a Model profile (Config.Models) used for
	// session-start model auto-selection; empty falls back to legacy Constraints.
	ModelProfile string `json:"model_profile,omitempty"`
	// ModelTag is a capability tag used to resolve a Model profile at session
	// start when ModelProfile is empty. Mirrors WorkspaceSettings.InitialModelTag.
	ModelTag string `json:"model_tag,omitempty"`
	// Constraints is an optional map of config option auto-selection rules.
	// The key is the config option category (e.g., "model", "mode").
	Constraints map[string]*ACPServerConstraint `json:"constraints,omitempty"`
	// ContextFlushCommand is an optional agent-native slash command (e.g. "/clear")
	// that flushes/clears the conversation context without restarting the agent.
	ContextFlushCommand string `json:"context_flush_command,omitempty"`
}

// ToConfig converts Settings to the internal Config struct.
func (s *Settings) ToConfig() *Config {
	cfg := &Config{
		ACPServers:        make([]ACPServer, len(s.ACPServers)),
		Prompts:           s.Prompts,
		PromptsDirs:       s.PromptsDirs,
		Web:               s.Web,
		UI:                s.UI,
		Session:           s.Session,
		Prewarm:           s.Prewarm,
		Conversations:     s.Conversations,
		Permissions:       s.Permissions,
		RestrictedRunners: s.RestrictedRunners,
		MCP:               s.MCP,
		Models:            s.Models,
		Shortcuts:         s.Shortcuts,
	}
	for i, srv := range s.ACPServers {
		cfg.ACPServers[i] = ACPServer(srv)
	}
	return cfg
}

// ConfigToSettings converts a Config to Settings for persistence.
func ConfigToSettings(cfg *Config) *Settings {
	s := &Settings{
		ACPServers:        make([]ACPServerSettings, len(cfg.ACPServers)),
		Prompts:           cfg.Prompts,
		PromptsDirs:       cfg.PromptsDirs,
		Web:               cfg.Web,
		UI:                cfg.UI,
		Session:           cfg.Session,
		Prewarm:           cfg.Prewarm,
		Conversations:     cfg.Conversations,
		Permissions:       cfg.Permissions,
		RestrictedRunners: cfg.RestrictedRunners,
		MCP:               cfg.MCP,
		Models:            cfg.Models,
		Shortcuts:         cfg.Shortcuts,
	}
	for i, srv := range cfg.ACPServers {
		s.ACPServers[i] = ACPServerSettings(srv)
	}
	return s
}

// loadRawSettings reads settings.json into a Settings struct WITHOUT the
// keychain/password migration performed by LoadSettings. It ensures the file
// exists (creating it from embedded defaults if missing) so callers always get
// a usable struct. Reading raw avoids materialising the keychain-stored auth
// password back into settings.json on rewrite.
func loadRawSettings() (*Settings, error) {
	if err := appdir.EnsureDir(); err != nil {
		return nil, fmt.Errorf("failed to create Mitto directory: %w", err)
	}
	settingsPath, err := appdir.SettingsPath()
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(settingsPath); os.IsNotExist(statErr) {
		if err := createDefaultSettings(); err != nil {
			return nil, fmt.Errorf("failed to create default settings: %w", err)
		}
	}
	var settings Settings
	if err := fileutil.ReadJSON(settingsPath, &settings); err != nil {
		return nil, fmt.Errorf("failed to read settings file %s: %w", settingsPath, err)
	}
	return &settings, nil
}

// GlobalShortcuts returns the global shortcut sections stored in settings.json,
// or nil if none are configured or settings cannot be read.
func GlobalShortcuts() map[string][]ShortcutButton {
	settings, err := loadRawSettings()
	if err != nil || settings == nil {
		return nil
	}
	return settings.Shortcuts
}

// SetGlobalShortcuts persists global shortcut sections to settings.json.
// Empty/absent sections are pruned. The existing settings file is read raw, its
// Shortcuts field replaced, and the whole file rewritten so no other settings
// (including the keychain-managed auth password) are lost or leaked.
func SetGlobalShortcuts(sections map[string][]ShortcutButton) error {
	settings, err := loadRawSettings()
	if err != nil {
		return err
	}
	// Prune sections with no buttons.
	cleaned := map[string][]ShortcutButton{}
	for k, v := range sections {
		if len(v) > 0 {
			cleaned[k] = v
		}
	}
	if len(cleaned) == 0 {
		settings.Shortcuts = nil
	} else {
		settings.Shortcuts = cleaned
	}
	return SaveSettings(settings)
}

// LoadSettings loads settings from the Mitto data directory.
// If settings.json doesn't exist, it creates it from the embedded default config.
// This function also ensures the Mitto directory exists.
//
// On platforms with secure credential storage (macOS Keychain):
//   - If a password exists in settings.json, it is migrated to the keychain
//     and removed from settings.json for security
//   - If no password is in settings.json, it is loaded from the keychain
func LoadSettings() (*Config, error) {
	// Ensure Mitto directory exists
	if err := appdir.EnsureDir(); err != nil {
		return nil, fmt.Errorf("failed to create Mitto directory: %w", err)
	}

	settingsPath, err := appdir.SettingsPath()
	if err != nil {
		return nil, err
	}

	// Check if settings.json exists
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		// Create settings.json from embedded default config
		if err := createDefaultSettings(); err != nil {
			return nil, fmt.Errorf("failed to create default settings: %w", err)
		}
	}

	// One-time, idempotent rewrite of legacy periodic_* keys to loop_* (mitto-8ir.12).
	// Runs on the raw JSON before unmarshalling so old data isn't silently dropped.
	if err := migrateSettingsFileIfNeeded(settingsPath); err != nil {
		return nil, fmt.Errorf("failed to migrate settings file %s: %w", settingsPath, err)
	}

	// Load settings from JSON file
	var settings Settings
	if err := fileutil.ReadJSON(settingsPath, &settings); err != nil {
		return nil, fmt.Errorf("failed to read settings file %s: %w", settingsPath, err)
	}

	// Deduplicate ACP server prompts to clean up any accumulation from previous bugs
	// This is a one-time fix that runs on every load, but is idempotent
	settingsModified := deduplicateACPServerPrompts(&settings)

	cfg := settings.ToConfig()

	// If we modified settings during deduplication, save the cleaned version
	if settingsModified {
		_ = SaveSettings(&settings) // Ignore error - deduplication is best-effort
	}

	// Handle external access password with secure storage
	if cfg.Web.Auth != nil && cfg.Web.Auth.Simple != nil {
		if secrets.IsSupported() {
			if cfg.Web.Auth.Simple.Password != "" {
				// Password found in settings.json - migrate it to keychain
				if err := migratePasswordToKeychain(&settings, cfg); err != nil {
					// Log warning but don't fail - password still works from settings
					// The migration will be attempted again on next load
					_ = err // Ignore migration error, password is still usable
				}
			} else {
				// No password in settings.json - try to load from keychain
				password, err := secrets.GetExternalAccessPassword()
				if err == nil && password != "" {
					cfg.Web.Auth.Simple.Password = password
				}
				// If password not found in Keychain, leave it empty
				// Validation should catch this case when external access is attempted
			}
		}
	}

	return cfg, nil
}

// migrateSettingsFileIfNeeded performs a one-time, idempotent rewrite of legacy
// periodic_* keys in settings.json to their loop_* equivalents (mitto-8ir.12).
// It operates on the raw JSON (map[string]interface{}), not the typed Settings
// struct, so old data is fixed BEFORE the new (loop_*-only) struct tags read it.
//
// It is a no-op when settings.json doesn't exist yet, is empty, isn't a JSON
// object, or contains none of the legacy keys.
func migrateSettingsFileIfNeeded(settingsPath string) error {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		// Malformed or non-object JSON — let the normal load path surface the error.
		return nil
	}

	if !migrateSettingsPeriodicKeys(raw) {
		return nil
	}

	return fileutil.WriteJSONAtomic(settingsPath, raw, 0644)
}

// migrateSettingsPeriodicKeys renames legacy periodic_* settings keys to their
// loop_* equivalents in-place on the raw settings map. These settings live
// nested under the "session" and "conversations" objects (matching the
// SessionConfig and ConversationsConfig struct layout):
//
//	session.startup_periodic_delay_seconds            -> session.startup_loop_delay_seconds
//	session.periodic_suspend_timeout                  -> session.loop_suspend_timeout
//	conversations.max_periodic_iterations              -> conversations.max_loop_iterations
//	conversations.min_periodic_completion_delay_seconds -> conversations.min_loop_completion_delay_seconds
//
// For each mapping, the value is moved only when the old key is present and
// the new key is absent (old moved only when new absent); an already-migrated
// or partially-migrated file is left untouched for that key. Returns true if
// any key was renamed.
func migrateSettingsPeriodicKeys(raw map[string]interface{}) bool {
	changed := false

	renameKey := func(m map[string]interface{}, oldKey, newKey string) {
		if m == nil {
			return
		}
		oldVal, hasOld := m[oldKey]
		if !hasOld {
			return
		}
		if _, hasNew := m[newKey]; hasNew {
			return
		}
		m[newKey] = oldVal
		delete(m, oldKey)
		changed = true
	}

	if session, ok := raw["session"].(map[string]interface{}); ok {
		renameKey(session, "startup_periodic_delay_seconds", "startup_loop_delay_seconds")
		renameKey(session, "periodic_suspend_timeout", "loop_suspend_timeout")
	}
	if conversations, ok := raw["conversations"].(map[string]interface{}); ok {
		renameKey(conversations, "max_periodic_iterations", "max_loop_iterations")
		renameKey(conversations, "min_periodic_completion_delay_seconds", "min_loop_completion_delay_seconds")
	}

	return changed
}

// deduplicateACPServerPrompts removes duplicate prompts from ACP server configurations.
// This is a cleanup function to fix settings files that accumulated duplicates due to
// a bug where file-based prompts were being merged without deduplication.
// Returns true if any modifications were made.
func deduplicateACPServerPrompts(settings *Settings) bool {
	modified := false

	for i := range settings.ACPServers {
		prompts := settings.ACPServers[i].Prompts
		if len(prompts) <= 1 {
			continue
		}

		// Deduplicate by name, keeping the first occurrence
		seen := make(map[string]bool)
		var dedupedPrompts []WebPrompt

		for _, p := range prompts {
			if p.Name == "" {
				continue // Skip prompts without names
			}
			if !seen[p.Name] {
				seen[p.Name] = true
				dedupedPrompts = append(dedupedPrompts, p)
			}
		}

		// Check if we removed any duplicates
		if len(dedupedPrompts) < len(prompts) {
			settings.ACPServers[i].Prompts = dedupedPrompts
			modified = true
		}
	}

	return modified
}

// migratePasswordToKeychain moves the password from settings.json to the system keychain.
// This improves security by not storing passwords in plain text files.
func migratePasswordToKeychain(settings *Settings, cfg *Config) error {
	password := cfg.Web.Auth.Simple.Password

	// Save password to keychain
	if err := secrets.SetExternalAccessPassword(password); err != nil {
		return fmt.Errorf("failed to save password to keychain: %w", err)
	}

	// Clear password from settings and save
	settings.Web.Auth.Simple.Password = ""
	if err := SaveSettings(settings); err != nil {
		// Password is in keychain but settings.json still has the old password
		// This is not ideal but not critical - next load will try again
		return fmt.Errorf("failed to update settings after keychain migration: %w", err)
	}

	return nil
}

// createDefaultSettings parses the embedded YAML config and saves it as JSON.
func createDefaultSettings() error {
	// Parse the embedded default YAML config
	cfg, err := Parse(defaultConfig.DefaultConfigYAML)
	if err != nil {
		return fmt.Errorf("failed to parse embedded default config: %w", err)
	}

	// Convert to Settings and save
	settings := ConfigToSettings(cfg)
	if err := SaveSettings(settings); err != nil {
		return err
	}

	return nil
}

// SaveSettings saves settings to the Mitto data directory.
// Before writing, it creates a backup of the existing settings file (if it exists)
// at settings.json.bak. Only one backup is maintained at a time.
func SaveSettings(settings *Settings) error {
	settingsPath, err := appdir.SettingsPath()
	if err != nil {
		return err
	}

	// Create backup if settings.json already exists
	if _, err := os.Stat(settingsPath); err == nil {
		backupPath := settingsPath + ".bak"
		// Read existing settings and write to backup (overwrites any existing backup)
		data, err := os.ReadFile(settingsPath)
		if err != nil {
			return fmt.Errorf("failed to read settings for backup: %w", err)
		}
		if err := os.WriteFile(backupPath, data, 0644); err != nil {
			return fmt.Errorf("failed to create settings backup: %w", err)
		}
	}

	// Use atomic write for safety
	return fileutil.WriteJSONAtomic(settingsPath, settings, 0644)
}

// LoadSettingsWithFallback loads configuration using a layered approach:
//  1. Load settings.json as the base (creates from defaults if needed)
//  2. If an RC file exists (~/.mittorc), merge its ACP servers into the config
//
// The RC file servers have higher priority and will override settings servers with the same name.
// RC file servers are marked with Source=SourceRCFile and are read-only in the UI.
// Settings servers are marked with Source=SourceSettings and can be edited via UI.
//
// Non-ACP settings (web, ui, etc.) come from settings.json when no RC file exists,
// or from the RC file when one exists (for backward compatibility).
func LoadSettingsWithFallback() (*LoadResult, error) {
	return loadSettingsWithFallback(true)
}

// LoadSettingsWithFallbackNoKeychain is identical to LoadSettingsWithFallback
// but never accesses secure storage (Keychain) to materialise the external
// access password. It is intended for non-web, non-interactive commands (e.g.
// `mitto prompt`) that only need the ACP server configuration and must never
// touch web authentication — both because they don't serve HTTP and because
// the Keychain unlock prompt hangs when the binary runs headless (no TTY).
func LoadSettingsWithFallbackNoKeychain() (*LoadResult, error) {
	return loadSettingsWithFallback(false)
}

// loadSettingsWithFallback implements LoadSettingsWithFallback. When
// withKeychain is false, the keychain password load is skipped entirely so no
// web-auth secret is ever read.
func loadSettingsWithFallback(withKeychain bool) (*LoadResult, error) {
	// Check for RC file
	rcPath, err := appdir.RCFilePath()
	if err != nil {
		return nil, fmt.Errorf("failed to check RC file: %w", err)
	}

	// Always ensure settings.json exists (needed for UI settings persistence)
	settingsPath, err := appdir.SettingsPath()
	if err != nil {
		return nil, err
	}

	// Ensure Mitto directory exists
	if err := appdir.EnsureDir(); err != nil {
		return nil, fmt.Errorf("failed to create Mitto directory: %w", err)
	}

	// Create settings.json from defaults if it doesn't exist
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		if err := createDefaultSettings(); err != nil {
			return nil, fmt.Errorf("failed to create default settings: %w", err)
		}
	}

	// One-time, idempotent rewrite of legacy periodic_* keys to loop_* (mitto-8ir.12).
	// Runs on the raw JSON before unmarshalling so old data isn't silently dropped.
	if err := migrateSettingsFileIfNeeded(settingsPath); err != nil {
		return nil, fmt.Errorf("failed to migrate settings file %s: %w", settingsPath, err)
	}

	// Load settings.json
	var settings Settings
	if err := fileutil.ReadJSON(settingsPath, &settings); err != nil {
		return nil, fmt.Errorf("failed to read settings file %s: %w", settingsPath, err)
	}

	// Mark all settings servers with their source
	for i := range settings.ACPServers {
		settings.ACPServers[i].Source = SourceSettings
	}

	// Convert settings to config
	settingsCfg := settings.ToConfig()

	// Handle keychain password loading
	if withKeychain {
		if err := loadKeychainPassword(settingsCfg); err != nil {
			// Non-fatal, just log and continue
			_ = err
		}
	}

	// If no RC file, return settings-only config
	if rcPath == "" {
		return &LoadResult{
			Config:           settingsCfg,
			Source:           ConfigSourceSettingsJSON,
			SourcePath:       settingsPath,
			HasRCFileServers: false,
		}, nil
	}

	// RC file exists - load and merge
	rcCfg, err := Load(rcPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load RC file %s: %w", rcPath, err)
	}

	// Mark all RC file servers with their source
	for i := range rcCfg.ACPServers {
		rcCfg.ACPServers[i].Source = SourceRCFile
	}

	// Merge ACP servers: RC file servers take priority
	mergeResult := MergeACPServers(rcCfg.ACPServers, settingsCfg.ACPServers)

	// Build the final merged config
	// Use RC file config as base (for non-ACP settings like web, prompts, etc.)
	// but override ACP servers with the merged list
	mergedCfg := rcCfg
	mergedCfg.ACPServers = mergeResult.Items

	// Merge Web settings from settings.json into the merged config
	// These settings are typically configured via the UI and saved to settings.json,
	// not in the RC file. We need to preserve them even when an RC file is used.
	//
	// Auth settings (username/password for external access)
	if settingsCfg.Web.Auth != nil {
		mergedCfg.Web.Auth = settingsCfg.Web.Auth
	}

	// ExternalPort (external access port configuration)
	if mergedCfg.Web.ExternalPort == 0 && settingsCfg.Web.ExternalPort != 0 {
		mergedCfg.Web.ExternalPort = settingsCfg.Web.ExternalPort
	}

	// Hooks (lifecycle hooks for tunneling etc.) - merge if not set in RC file
	if mergedCfg.Web.Hooks.Up.Command == "" && settingsCfg.Web.Hooks.Up.Command != "" {
		mergedCfg.Web.Hooks.Up = settingsCfg.Web.Hooks.Up
	}
	if mergedCfg.Web.Hooks.Down.Command == "" && settingsCfg.Web.Hooks.Down.Command != "" {
		mergedCfg.Web.Hooks.Down = settingsCfg.Web.Hooks.Down
	}
	if mergedCfg.Web.Hooks.ExternalAddress == "" && settingsCfg.Web.Hooks.ExternalAddress != "" {
		mergedCfg.Web.Hooks.ExternalAddress = settingsCfg.Web.Hooks.ExternalAddress
	}

	// Host setting (for external access - 0.0.0.0 vs 127.0.0.1)
	// If settings.json has 0.0.0.0 (external access enabled), use it
	if settingsCfg.Web.Host == "0.0.0.0" {
		mergedCfg.Web.Host = settingsCfg.Web.Host
	}

	// MCP settings (configured via UI, saved to settings.json) — apply when RC file doesn't set them
	if mergedCfg.MCP == nil && settingsCfg.MCP != nil {
		mergedCfg.MCP = settingsCfg.MCP
	}

	// Load keychain password for the merged config
	// This loads the password from keychain if Auth is configured but password is empty
	if withKeychain {
		if err := loadKeychainPassword(mergedCfg); err != nil {
			// Non-fatal, just log and continue
			_ = err
		}
	}

	return &LoadResult{
		Config:           mergedCfg,
		Source:           ConfigSourceRCFile, // Primary source is RC file
		SourcePath:       rcPath,
		RCFilePath:       rcPath,
		HasRCFileServers: mergeResult.HasRCFileItems,
	}, nil
}

// loadKeychainPassword loads the external access password from keychain if available.
func loadKeychainPassword(cfg *Config) error {
	if cfg.Web.Auth == nil || cfg.Web.Auth.Simple == nil {
		return nil
	}
	if !secrets.IsSupported() {
		return nil
	}
	if cfg.Web.Auth.Simple.Password != "" {
		// Password already set, no need to load from keychain
		return nil
	}
	password, err := secrets.GetExternalAccessPassword()
	if err != nil {
		return err
	}
	if password != "" {
		cfg.Web.Auth.Simple.Password = password
	}
	return nil
}
