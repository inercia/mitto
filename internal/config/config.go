// Package config handles configuration loading and management for Mitto.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ACPServer represents a single ACP server configuration.
type ACPServer struct {
	// Name is the identifier for this ACP server
	Name string
	// Command is the shell command to start the ACP server
	Command string
	// Cwd is the working directory for the ACP server process.
	// If empty, the process inherits the current working directory.
	// This eliminates the need for shell tricks like 'sh -c "cd /some/dir && command"'.
	Cwd string
	// Type is an optional type identifier for prompt matching.
	// Servers with the same type share prompts. If empty, Name is used as the type.
	Type string
	// Prompts is an optional list of predefined prompts specific to this ACP server
	Prompts []WebPrompt
	// RestrictedRunners contains per-runner-type configuration for this agent.
	RestrictedRunners map[string]*WorkspaceRunnerConfig
	// Source indicates where this server configuration originated from.
	// Used for config layering: servers from RC file are read-only in the UI.
	Source ConfigItemSource
	// AutoApprove enables automatic approval of permission requests for this ACP server.
	// This is a per-server override; the global AutoApprove flag takes precedence if set.
	AutoApprove bool
}

// GetType returns the type identifier for prompt matching.
// If Type is not set, returns the Name as the type.
func (s *ACPServer) GetType() string {
	if s.Type != "" {
		return s.Type
	}
	return s.Name
}

// PromptSource indicates where a prompt originated from.
type PromptSource string

const (
	// PromptSourceFile indicates the prompt was loaded from a .md file in MITTO_DIR/prompts/
	PromptSourceFile PromptSource = "file"
	// PromptSourceSettings indicates the prompt was defined in settings.json
	PromptSourceSettings PromptSource = "settings"
	// PromptSourceWorkspace indicates the prompt was defined in a workspace .mittorc file
	PromptSourceWorkspace PromptSource = "workspace"
)

// WebPrompt represents a predefined prompt for the web interface.
type WebPrompt struct {
	// Name is the display name for the prompt button
	Name string `json:"name"`
	// Prompt is the actual prompt text to send
	Prompt string `json:"prompt"`
	// BackgroundColor is an optional hex color string for the prompt button (e.g., "#E8F5E9")
	BackgroundColor string `json:"backgroundColor,omitempty"`
	// Description is an optional description shown as tooltip in the UI
	Description string `json:"description,omitempty"`
	// Source indicates where this prompt originated from (file, settings, workspace).
	// This is used by the frontend to determine which prompts should be saved back to settings.
	// Only prompts with Source="settings" or empty Source should be saved.
	Source PromptSource `json:"source,omitempty"`
	// ACPs is an optional comma-separated list of ACP server names this prompt applies to.
	// If empty, the prompt works with all ACP servers.
	// Example: "auggie, claude-code" means only show this prompt for those ACP servers.
	// This is included so the frontend can filter prompts client-side.
	ACPs string `json:"acps,omitempty"`
}

// WebHook represents a shell command hook configuration.
type WebHook struct {
	// Command is the shell command to execute.
	// Supports ${PORT} placeholder which is replaced with the actual port number.
	Command string `json:"command,omitempty"`
	// Name is an optional display name for the hook (shown in output)
	Name string `json:"name,omitempty"`
}

// WebHooks contains lifecycle hooks for the web server.
type WebHooks struct {
	// Up is executed after the web server starts listening
	Up WebHook `json:"up,omitempty"`
	// Down is executed right before the web server shuts down
	Down WebHook `json:"down,omitempty"`
}

// SimpleAuth represents simple username/password authentication.
type SimpleAuth struct {
	// Username is the required username for authentication
	Username string `json:"username"`
	// Password is the required password for authentication (stored as bcrypt hash in config recommended)
	Password string `json:"password"`
}

// AuthAllow represents IP-based authentication bypass configuration.
type AuthAllow struct {
	// IPs is a list of IP addresses or CIDR ranges that bypass authentication.
	// Examples: "127.0.0.1", "192.168.0.0/24", "::1"
	IPs []string `json:"ips,omitempty"`
}

// WebAuth represents authentication configuration for the web interface.
type WebAuth struct {
	// Simple enables simple username/password authentication when set
	Simple *SimpleAuth `json:"simple,omitempty"`
	// Allow contains IP addresses/CIDR ranges that bypass authentication
	Allow *AuthAllow `json:"allow,omitempty"`
}

// WebSecurity represents security configuration for the web interface.
type WebSecurity struct {
	// TrustedProxies is a list of IP addresses or CIDR ranges of trusted reverse proxies.
	// Only requests from these IPs will have X-Forwarded-For and X-Real-IP headers trusted.
	// If empty, these headers are never trusted (direct connections only).
	// Examples: "127.0.0.1", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"
	TrustedProxies []string `json:"trusted_proxies,omitempty"`

	// AllowedOrigins is a list of allowed origins for WebSocket connections.
	// If empty, only same-origin requests are allowed.
	// Use "*" to allow all origins (not recommended for production).
	AllowedOrigins []string `json:"allowed_origins,omitempty"`

	// RateLimitRPS is the rate limit for API requests per second per IP.
	// Default: 10
	RateLimitRPS float64 `json:"rate_limit_rps,omitempty"`

	// RateLimitBurst is the maximum burst size for rate limiting.
	// Default: 20
	RateLimitBurst int `json:"rate_limit_burst,omitempty"`

	// MaxWSConnectionsPerIP is the maximum number of concurrent WebSocket connections per IP.
	// Default: 10
	MaxWSConnectionsPerIP int `json:"max_ws_connections_per_ip,omitempty"`

	// MaxWSMessageSize is the maximum size of a WebSocket message in bytes.
	// Default: 65536 (64KB)
	MaxWSMessageSize int64 `json:"max_ws_message_size,omitempty"`

	// ScannerDefense contains configuration for blocking malicious IPs at the TCP level.
	// When external access is enabled (ExternalPort >= 0), scanner defense is enabled by default.
	ScannerDefense *ScannerDefenseConfig `json:"scanner_defense,omitempty"`
}

// HotkeyConfig represents a keyboard shortcut configuration.
// Format: "modifier+modifier+key" (e.g., "cmd+ctrl+m", "ctrl+alt+space")
// Supported modifiers: cmd, ctrl, alt, shift
// Supported keys: a-z, 0-9, space, tab, return, escape, delete, f1-f12
type HotkeyConfig struct {
	// Enabled controls whether this hotkey is active (default: true)
	Enabled *bool `json:"enabled,omitempty"`
	// Key is the hotkey combination (e.g., "cmd+ctrl+m")
	Key string `json:"key,omitempty"`
}

// MacHotkeys represents macOS-specific hotkey configuration.
type MacHotkeys struct {
	// ShowHide is the hotkey to toggle app visibility (default: "cmd+ctrl+m")
	ShowHide *HotkeyConfig `json:"show_hide,omitempty"`
}

// NotificationSoundsConfig represents notification sound settings.
type NotificationSoundsConfig struct {
	// AgentCompleted enables a sound when the agent finishes a response (default: false)
	AgentCompleted bool `json:"agent_completed,omitempty"`
}

// NotificationsConfig represents notification settings.
type NotificationsConfig struct {
	// Sounds contains notification sound settings
	Sounds *NotificationSoundsConfig `json:"sounds,omitempty"`
	// NativeEnabled enables native macOS notifications instead of in-app toasts.
	// When enabled, notifications appear in the macOS Notification Center.
	// Requires notification permission from the user. (default: false)
	NativeEnabled bool `json:"native_enabled,omitempty"`
}

// BadgeClickActionConfig configures the workspace badge click behavior in the conversation list.
// When enabled, clicking a workspace badge executes a shell command.
type BadgeClickActionConfig struct {
	// Enabled controls whether clicking the workspace badge executes a command.
	// Default: true (enabled by default)
	Enabled *bool `json:"enabled,omitempty"`
	// Command is the shell command to execute when the badge is clicked.
	// Supports ${WORKSPACE} placeholder which is replaced with the workspace directory path.
	// Default: "open ${WORKSPACE}" (opens the folder in Finder on macOS)
	Command string `json:"command,omitempty"`
}

// GetEnabled returns whether badge click action is enabled.
// Defaults to true if not explicitly set.
func (c *BadgeClickActionConfig) GetEnabled() bool {
	if c == nil || c.Enabled == nil {
		return true // Enabled by default
	}
	return *c.Enabled
}

// GetCommand returns the command to execute.
// Defaults to "open ${WORKSPACE}" if not set.
func (c *BadgeClickActionConfig) GetCommand() string {
	if c == nil || c.Command == "" {
		return "open ${WORKSPACE}"
	}
	return c.Command
}

// MacUIConfig represents macOS-specific UI configuration.
type MacUIConfig struct {
	// Hotkeys contains hotkey configuration for macOS
	Hotkeys *MacHotkeys `json:"hotkeys,omitempty"`
	// Notifications contains notification settings for macOS
	Notifications *NotificationsConfig `json:"notifications,omitempty"`
	// ShowInAllSpaces makes the window appear in all macOS Spaces (virtual desktops)
	// When enabled, the Mitto window will be visible across all Spaces.
	// Requires app restart to take effect. (default: false)
	ShowInAllSpaces bool `json:"show_in_all_spaces,omitempty"`
	// StartAtLogin enables launching Mitto automatically when the user logs in.
	// This uses macOS SMAppService API (requires macOS 13+).
	// (default: false)
	StartAtLogin bool `json:"start_at_login,omitempty"`
	// BadgeClickAction configures the workspace badge click behavior.
	// When enabled, clicking a workspace badge in the conversation list
	// executes a shell command (e.g., opening the folder in Finder).
	BadgeClickAction *BadgeClickActionConfig `json:"badge_click_action,omitempty"`
}

// ConfirmationsConfig represents confirmation dialog settings.
type ConfirmationsConfig struct {
	// DeleteSession controls whether to show confirmation when deleting a session (default: true)
	DeleteSession *bool `json:"delete_session,omitempty"`
	// QuitWithRunningSessions controls whether to show confirmation when quitting with running sessions (default: true)
	// This only applies to the macOS desktop app.
	QuitWithRunningSessions *bool `json:"quit_with_running_sessions,omitempty"`
}

// Conversation cycling mode constants.
// These determine which conversations are included when cycling with keyboard shortcuts or gestures.
const (
	// CyclingModeAll cycles through all non-archived conversations (default).
	CyclingModeAll = "all"
	// CyclingModeVisibleGroups cycles only through conversations in expanded/open groups.
	CyclingModeVisibleGroups = "visible_groups"
)

// WebUIConfig represents web-specific UI configuration.
type WebUIConfig struct {
	// InputFontFamily is the font family for the compose/input box.
	// Options: "system" (default), "monospace", "sans-serif", "serif",
	// or specific fonts: "menlo", "monaco", "consolas", "courier-new",
	// "jetbrains-mono", "sf-mono", "cascadia-code"
	InputFontFamily string `json:"input_font_family,omitempty"`

	// ConversationCyclingMode controls which conversations are included when cycling
	// with keyboard shortcuts (Cmd+Ctrl+Up/Down) or mobile swipe gestures.
	// Options: "all" (default) - all non-archived conversations
	//          "visible_groups" - only conversations in expanded groups
	ConversationCyclingMode string `json:"conversation_cycling_mode,omitempty"`

	// SingleExpandedGroup enables accordion-style behavior for conversation groups.
	// When enabled, at most one conversation group can be expanded at a time.
	// Expanding a group will automatically collapse any other expanded group.
	// This only applies when conversation grouping is enabled.
	// Default: false
	SingleExpandedGroup bool `json:"single_expanded_group,omitempty"`
}

// UIConfig represents UI configuration for the desktop app.
type UIConfig struct {
	// Confirmations contains confirmation dialog settings
	Confirmations *ConfirmationsConfig `json:"confirmations,omitempty"`
	// Web contains web-specific UI configuration
	Web *WebUIConfig `json:"web,omitempty"`
	// Mac contains macOS-specific UI configuration
	Mac *MacUIConfig `json:"mac,omitempty"`
}

// WebConfig represents web interface configuration.
type WebConfig struct {
	// Host is the HTTP server host/IP address (default: 127.0.0.1)
	// Use "0.0.0.0" to listen on all interfaces
	Host string `json:"host,omitempty"`
	// Port is the HTTP server port for local access (default: 8080, or random if 0)
	// This is the primary port used by the Web UI and macOS native app.
	Port int `json:"port,omitempty"`
	// ExternalPort is the HTTP server port for external access.
	// This port is only used when external access is enabled (Auth is configured).
	// The external listener binds to 0.0.0.0 on this port.
	// Values:
	//   -1 = disabled (no external listener, default)
	//    0 = random port (OS chooses an available port)
	//   >0 = specific port number
	// Note: omitempty is NOT used here because 0 is a valid value meaning "random port".
	ExternalPort int `json:"external_port"`
	// APIPrefix is a URL path prefix for all API endpoints and WebSocket routes.
	// This provides security through obscurity by making endpoints harder to discover.
	// The prefix is applied to /api/*, /ws, and session WebSocket endpoints.
	// Static assets (CSS, JS) and the root landing page (/) remain unprefixed.
	// Default: "/mitto"
	// Set to empty string "" to disable prefixing (use original paths).
	APIPrefix string `json:"api_prefix,omitempty"`
	// Theme is the UI theme/stylesheet to use.
	// Options: "default" (original Tailwind-based), "v2" (Clawdbot-inspired)
	// Default: "default"
	Theme string `json:"theme,omitempty"`
	// Hooks contains lifecycle hooks for the web server
	Hooks WebHooks `json:"hooks,omitempty"`
	// StaticDir is an optional directory to serve static files from instead of embedded assets.
	// When set, files are served from this directory, enabling hot-reloading during development.
	StaticDir string `json:"staticDir,omitempty"`
	// Auth contains authentication configuration
	Auth *WebAuth `json:"auth,omitempty"`
	// Security contains security configuration (rate limiting, WebSocket security, etc.)
	Security *WebSecurity `json:"security,omitempty"`
	// AccessLog contains access log configuration
	AccessLog *AccessLogConfig `json:"access_log,omitempty"`
}

// AccessLogConfig represents access log configuration.
type AccessLogConfig struct {
	// Enabled controls whether access logging is enabled.
	// Default: true (enabled when running as macOS app or via mitto-web)
	Enabled *bool `json:"enabled,omitempty"`
	// Path is the file path for the access log.
	// If empty, defaults to platform-specific logs directory:
	//   - macOS: ~/Library/Logs/Mitto/access.log
	//   - Linux: $XDG_STATE_HOME/mitto/access.log or ~/.local/state/mitto/access.log
	//   - Windows: %LOCALAPPDATA%\Mitto\Logs\access.log
	Path string `json:"path,omitempty"`
	// MaxSizeMB is the maximum size of the log file in megabytes before rotation.
	// Default: 10MB
	MaxSizeMB int `json:"max_size_mb,omitempty"`
	// MaxBackups is the maximum number of old log files to retain.
	// Default: 1
	MaxBackups int `json:"max_backups,omitempty"`
}

// DefaultAPIPrefix is the default URL prefix for API endpoints.
const DefaultAPIPrefix = "/mitto"

// GetAPIPrefix returns the API prefix, using the default if not set.
func (c *WebConfig) GetAPIPrefix() string {
	if c.APIPrefix == "" {
		return DefaultAPIPrefix
	}
	return c.APIPrefix
}

// ============================================================================
// Message Processor Types
//
// Message processors transform user messages before they are sent to the ACP
// server. Processors are defined in configuration and applied in order.
//
// Example YAML configuration:
//
//	conversations:
//	  processing:
//	    processors:
//	      - when: first
//	        position: prepend
//	        text: "You are a helpful assistant.\n\n"
//	      - when: all
//	        position: append
//	        text: "\n\n[Be concise]"
//
// Example usage in code:
//
//	processors := config.MergeProcessors(globalConfig.Conversations, workspaceConfig.Conversations)
//	processedMsg := config.ApplyProcessors(userMessage, processors, isFirst)
// ============================================================================

// ProcessorWhen defines when a message processor should be applied.
// Valid values: "first", "all", "all-except-first"
type ProcessorWhen string

const (
	// ProcessorWhenFirst applies only to the first message in a conversation.
	// Use this for initial context or system prompts.
	ProcessorWhenFirst ProcessorWhen = "first"
	// ProcessorWhenAll applies to all messages in the conversation.
	// Use this for reminders or constraints that should always be present.
	ProcessorWhenAll ProcessorWhen = "all"
	// ProcessorWhenAllExceptFirst applies to all messages except the first.
	// Use this for continuation markers or follow-up context.
	ProcessorWhenAllExceptFirst ProcessorWhen = "all-except-first"
)

// ProcessorPosition defines where the processor text is inserted relative to the message.
// Valid values: "prepend", "append"
type ProcessorPosition string

const (
	// ProcessorPositionPrepend inserts text before the user's message.
	ProcessorPositionPrepend ProcessorPosition = "prepend"
	// ProcessorPositionAppend inserts text after the user's message.
	ProcessorPositionAppend ProcessorPosition = "append"
)

// MessageProcessor defines a single message transformation rule.
// Processors are applied in order to transform user messages before sending to the ACP server.
// Each processor specifies when it applies, where to insert text, and what text to insert.
type MessageProcessor struct {
	// When specifies when this processor applies: "first", "all", or "all-except-first"
	When ProcessorWhen `json:"when" yaml:"when"`
	// Position specifies where to insert the text: "prepend" (before) or "append" (after)
	Position ProcessorPosition `json:"position" yaml:"position"`
	// Text is the content to insert at the specified position
	Text string `json:"text" yaml:"text"`
}

// ShouldApply determines if this processor should be applied to a message.
//
// Parameters:
//   - isFirstMessage: true if this is the first message in the conversation
//
// Returns true if the processor's When condition matches the message context.
// Returns false for unknown When values (fail-safe behavior).
func (p *MessageProcessor) ShouldApply(isFirstMessage bool) bool {
	switch p.When {
	case ProcessorWhenFirst:
		return isFirstMessage
	case ProcessorWhenAll:
		return true
	case ProcessorWhenAllExceptFirst:
		return !isFirstMessage
	default:
		// Unknown When value - don't apply (fail-safe)
		return false
	}
}

// Apply transforms the message by inserting the processor's text at the configured position.
//
// Parameters:
//   - message: the original user message
//
// Returns the transformed message with text prepended or appended.
// Returns the original message unchanged for unknown Position values.
func (p *MessageProcessor) Apply(message string) string {
	switch p.Position {
	case ProcessorPositionPrepend:
		return p.Text + message
	case ProcessorPositionAppend:
		return message + p.Text
	default:
		// Unknown Position value - return unchanged (fail-safe)
		return message
	}
}

// ConversationProcessing contains configuration for message processing.
// This is the inner structure that holds the actual processor list and merge behavior.
type ConversationProcessing struct {
	// Override controls merge behavior with parent (global) configuration.
	// If true, these processors completely replace parent processors.
	// If false (default), these processors are appended after parent processors.
	Override bool `json:"override,omitempty" yaml:"override,omitempty"`
	// Processors is the ordered list of message transformations.
	// Processors are applied sequentially in the order defined.
	Processors []MessageProcessor `json:"processors,omitempty" yaml:"processors,omitempty"`
}

// ConversationsConfig is the top-level configuration for conversation handling.
// It contains both message processing rules and queue behavior settings.
type ConversationsConfig struct {
	// Processing contains message transformation processors.
	// May be nil if no processors are configured.
	Processing *ConversationProcessing `json:"processing,omitempty" yaml:"processing,omitempty"`
	// Queue contains message queue configuration for handling messages while agent is busy.
	// May be nil to use default queue behavior.
	Queue *QueueConfig `json:"queue,omitempty" yaml:"queue,omitempty"`
	// ActionButtons contains configuration for suggested response buttons.
	// May be nil to use default behavior (enabled).
	ActionButtons *ActionButtonsConfig `json:"action_buttons,omitempty" yaml:"action_buttons,omitempty"`
	// FileLinks contains configuration for file path recognition and linking.
	// May be nil to use default behavior (enabled).
	FileLinks *FileLinksConfig `json:"file_links,omitempty" yaml:"file_links,omitempty"`
	// ExternalImages contains configuration for loading external images.
	// May be nil to use default behavior (disabled for security).
	ExternalImages *ExternalImagesConfig `json:"external_images,omitempty" yaml:"external_images,omitempty"`
}

// ActionButtonsConfig configures the follow-up suggestions feature.
// When enabled, agent messages are analyzed asynchronously to identify
// questions or follow-up prompts, and suggested response buttons are
// displayed to the user.
type ActionButtonsConfig struct {
	// Enabled controls whether follow-up suggestions are enabled.
	// When true, agent messages are analyzed to extract suggested responses.
	// Default: true (enabled by default)
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
}

// IsEnabled returns whether follow-up suggestions are enabled.
// Safe to call on nil receiver - returns true (the default) if not configured.
func (a *ActionButtonsConfig) IsEnabled() bool {
	if a == nil || a.Enabled == nil {
		return true // Default: enabled
	}
	return *a.Enabled
}

// FileLinksConfig configures file path recognition and linking in messages.
// When enabled, file paths in agent messages are detected and converted to
// clickable file:// links that open in the system default application.
type FileLinksConfig struct {
	// Enabled controls whether file path linking is enabled.
	// When true, file paths in messages are converted to clickable links.
	// Default: true (enabled by default)
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`

	// AllowOutsideWorkspace controls whether files outside the workspace can be linked.
	// When false, only files within the current workspace directory are linked.
	// Default: false (only workspace files)
	AllowOutsideWorkspace *bool `json:"allow_outside_workspace,omitempty" yaml:"allow_outside_workspace,omitempty"`
}

// IsEnabled returns whether file path linking is enabled.
// Safe to call on nil receiver - returns true (the default) if not configured.
func (f *FileLinksConfig) IsEnabled() bool {
	if f == nil || f.Enabled == nil {
		return true // Default: enabled
	}
	return *f.Enabled
}

// IsAllowOutsideWorkspace returns whether files outside the workspace can be linked.
// Safe to call on nil receiver - returns false (the default) if not configured.
func (f *FileLinksConfig) IsAllowOutsideWorkspace() bool {
	if f == nil || f.AllowOutsideWorkspace == nil {
		return false // Default: only workspace files
	}
	return *f.AllowOutsideWorkspace
}

// ExternalImagesConfig configures external image loading in messages.
// When enabled, the Content Security Policy (CSP) allows loading images
// from external HTTPS sources in rendered markdown content.
type ExternalImagesConfig struct {
	// Enabled controls whether external images are allowed.
	// When true, the CSP img-src directive includes 'https:'.
	// Default: false (only self, data:, and blob: are allowed)
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
}

// IsEnabled returns whether external images are allowed.
// Safe to call on nil receiver - returns false (the default) if not configured.
func (e *ExternalImagesConfig) IsEnabled() bool {
	if e == nil || e.Enabled == nil {
		return false // Default: disabled for security
	}
	return *e.Enabled
}

// DefaultQueueMaxSize is the default maximum number of messages allowed in a queue.
const DefaultQueueMaxSize = 10

// QueueConfig configures message queue behavior when the agent is busy.
// When a user sends a message while the agent is processing, the message
// is queued and automatically delivered when the agent becomes idle.
type QueueConfig struct {
	// Enabled controls whether queued messages are automatically sent to the agent.
	// When false, messages remain in the queue until manually sent or deleted.
	// Default: true (use pointer to distinguish "not set" from "false")
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`

	// DelaySeconds is the delay in seconds before sending the next queued message
	// after the agent finishes responding. Useful for rate-limiting.
	// Default: 0 (immediate)
	DelaySeconds int `json:"delay_seconds,omitempty" yaml:"delay_seconds,omitempty"`

	// MaxSize is the maximum number of messages allowed in the queue.
	// When the queue is full, new messages are rejected with an error.
	// Default: 10 (use pointer to distinguish "not set" from "0")
	MaxSize *int `json:"max_size,omitempty" yaml:"max_size,omitempty"`

	// AutoGenerateTitles controls whether short titles are automatically generated
	// for queued messages using the auxiliary conversation.
	// Default: true (use pointer to distinguish "not set" from "false")
	AutoGenerateTitles *bool `json:"auto_generate_titles,omitempty" yaml:"auto_generate_titles,omitempty"`
}

// IsEnabled returns whether queue processing is enabled.
// Safe to call on nil receiver - returns true (the default) if not configured.
func (q *QueueConfig) IsEnabled() bool {
	if q == nil || q.Enabled == nil {
		return true // Default: enabled
	}
	return *q.Enabled
}

// GetDelaySeconds returns the configured delay in seconds.
// Safe to call on nil receiver - returns 0 if not configured.
func (q *QueueConfig) GetDelaySeconds() int {
	if q == nil {
		return 0
	}
	return q.DelaySeconds
}

// GetMaxSize returns the maximum queue size.
// Safe to call on nil receiver - returns DefaultQueueMaxSize if not configured.
func (q *QueueConfig) GetMaxSize() int {
	if q == nil || q.MaxSize == nil {
		return DefaultQueueMaxSize
	}
	return *q.MaxSize
}

// ShouldAutoGenerateTitles returns whether titles should be auto-generated for queued messages.
// Safe to call on nil receiver - returns true (the default) if not configured.
func (q *QueueConfig) ShouldAutoGenerateTitles() bool {
	if q == nil || q.AutoGenerateTitles == nil {
		return true // Default: enabled
	}
	return *q.AutoGenerateTitles
}

// GetProcessors returns the list of message processors.
// Safe to call on nil receiver - returns nil if no processors are configured.
func (c *ConversationsConfig) GetProcessors() []MessageProcessor {
	if c == nil || c.Processing == nil {
		return nil
	}
	return c.Processing.Processors
}

// ShouldOverride returns whether workspace processors should replace global processors.
// Safe to call on nil receiver - returns false (merge behavior) if not configured.
func (c *ConversationsConfig) ShouldOverride() bool {
	if c == nil || c.Processing == nil {
		return false
	}
	return c.Processing.Override
}

// GetQueueConfig returns the queue configuration.
// Safe to call on nil receiver - returns nil if not configured.
func (c *ConversationsConfig) GetQueueConfig() *QueueConfig {
	if c == nil {
		return nil
	}
	return c.Queue
}

// GetActionButtonsConfig returns the action buttons configuration.
// Safe to call on nil receiver - returns nil if not configured.
func (c *ConversationsConfig) GetActionButtonsConfig() *ActionButtonsConfig {
	if c == nil {
		return nil
	}
	return c.ActionButtons
}

// AreActionButtonsEnabled returns whether action buttons are enabled.
// Safe to call on nil receiver - returns false if not configured.
func (c *ConversationsConfig) AreActionButtonsEnabled() bool {
	if c == nil {
		return false
	}
	return c.ActionButtons.IsEnabled()
}

// AreExternalImagesEnabled returns whether external images are allowed.
// Safe to call on nil receiver - returns false (the default) if not configured.
func (c *ConversationsConfig) AreExternalImagesEnabled() bool {
	if c == nil {
		return false
	}
	return c.ExternalImages.IsEnabled()
}

// MergeProcessors combines global and workspace processors according to precedence rules.
//
// Merge behavior:
//   - If workspace has override=true: only workspace processors are used
//   - Otherwise: global processors run first, then workspace processors
//
// Parameters:
//   - global: the global (default) configuration from ~/.config/mitto/config.yaml
//   - workspace: the workspace-specific configuration from <workspace>/.mittorc
//
// Returns a combined list of processors in execution order.
// Returns nil if both configs are nil or have no processors.
func MergeProcessors(global, workspace *ConversationsConfig) []MessageProcessor {
	// If workspace wants to override, use only workspace processors
	if workspace != nil && workspace.ShouldOverride() {
		return workspace.GetProcessors()
	}

	// Merge: global first, then workspace
	var result []MessageProcessor

	if global != nil {
		result = append(result, global.GetProcessors()...)
	}
	if workspace != nil {
		result = append(result, workspace.GetProcessors()...)
	}

	return result
}

// ApplyProcessors transforms a message by running it through a list of processors.
//
// Parameters:
//   - message: the original user message
//   - processors: the list of processors to apply (typically from MergeProcessors)
//   - isFirstMessage: true if this is the first message in the conversation
//
// Returns the transformed message after all applicable processors have run.
// Each processor's ShouldApply is checked before applying.
func ApplyProcessors(message string, processors []MessageProcessor, isFirstMessage bool) string {
	result := message
	for _, processor := range processors {
		if processor.ShouldApply(isFirstMessage) {
			result = processor.Apply(result)
		}
	}
	return result
}

// ============================================================================
// Prompt Merging
//
// Prompts can come from multiple sources with different priorities.
// MergePrompts combines them, with later sources overriding earlier ones by name.
//
// Priority order (lowest to highest):
//   1. Global file prompts (MITTO_DIR/prompts/*.md)
//   2. Settings file prompts (config.Prompts)
//   3. Workspace prompts (.mittorc)
// ============================================================================

// MergePrompts combines prompts from multiple sources with proper priority.
// Later sources override earlier ones when prompts have the same name.
// The order of prompts is preserved, with higher-priority prompts appearing first.
//
// Each prompt's Source field is set to indicate its origin:
//   - PromptSourceFile for globalFilePrompts (already set by ToWebPrompt)
//   - PromptSourceSettings for settingsPrompts
//   - PromptSourceWorkspace for workspacePrompts
//
// Parameters:
//   - globalFilePrompts: prompts from MITTO_DIR/prompts/*.md (lowest priority)
//   - settingsPrompts: prompts from settings file (medium priority)
//   - workspacePrompts: prompts from workspace .mittorc (highest priority)
//
// Returns a merged list with duplicates removed (by name).
func MergePrompts(globalFilePrompts, settingsPrompts, workspacePrompts []WebPrompt) []WebPrompt {
	seen := make(map[string]bool)
	var result []WebPrompt

	// Add workspace prompts first (highest priority)
	for _, p := range workspacePrompts {
		if p.Name != "" && !seen[p.Name] {
			p.Source = PromptSourceWorkspace
			result = append(result, p)
			seen[p.Name] = true
		}
	}

	// Add settings prompts (medium priority)
	for _, p := range settingsPrompts {
		if p.Name != "" && !seen[p.Name] {
			p.Source = PromptSourceSettings
			result = append(result, p)
			seen[p.Name] = true
		}
	}

	// Add global file prompts (lowest priority)
	// Note: Source is already set to PromptSourceFile by ToWebPrompt()
	for _, p := range globalFilePrompts {
		if p.Name != "" && !seen[p.Name] {
			result = append(result, p)
			seen[p.Name] = true
		}
	}

	return result
}

// ============================================================================
// Restricted Runner Types
//
// Restricted runners provide sandboxed execution for ACP agents.
// By default, agents run with no restrictions (exec runner).
// Users can opt-in to sandboxing by configuring restricted_runners settings.
//
// Configuration is per-runner-type using WorkspaceRunnerConfig.
// See docs/config/restricted.md for user documentation.
// ============================================================================

// RunnerRestrictions defines the restrictions for a runner.
type RunnerRestrictions struct {
	// AllowNetworking controls network access.
	// WARNING: Setting to false will break network-based MCP servers.
	AllowNetworking *bool `json:"allow_networking,omitempty" yaml:"allow_networking,omitempty"`

	// AllowReadFolders lists folders that can be read (supports variables like $WORKSPACE, $HOME).
	AllowReadFolders []string `json:"allow_read_folders,omitempty" yaml:"allow_read_folders,omitempty"`

	// AllowWriteFolders lists folders that can be written (supports variables).
	AllowWriteFolders []string `json:"allow_write_folders,omitempty" yaml:"allow_write_folders,omitempty"`

	// DenyFolders lists folders that are explicitly denied (supports variables).
	// These override allow lists.
	DenyFolders []string `json:"deny_folders,omitempty" yaml:"deny_folders,omitempty"`

	// MergeWithDefaults controls whether to merge with default restrictions.
	MergeWithDefaults *bool `json:"merge_with_defaults,omitempty" yaml:"merge_with_defaults,omitempty"`

	// Docker contains Docker-specific options.
	Docker *DockerRestrictions `json:"docker,omitempty" yaml:"docker,omitempty"`
}

// DockerRestrictions defines Docker-specific restrictions.
type DockerRestrictions struct {
	// Image is the Docker image to use (required for docker runner).
	// The image must contain the agent executable and any MCP servers.
	Image string `json:"image,omitempty" yaml:"image,omitempty"`

	// MemoryLimit is the maximum memory the container can use (e.g., "2g").
	MemoryLimit string `json:"memory_limit,omitempty" yaml:"memory_limit,omitempty"`

	// CPULimit is the maximum CPU cores the container can use (e.g., "2.0").
	CPULimit string `json:"cpu_limit,omitempty" yaml:"cpu_limit,omitempty"`
}

// WorkspaceRunnerConfig represents per-runner-type configuration for restricted runners.
// This type is used at all levels: global, per-agent, and per-workspace.
type WorkspaceRunnerConfig struct {
	// Type overrides the runner type for this workspace.
	Type string `json:"type,omitempty" yaml:"type,omitempty"`

	// Restrictions are workspace-specific restrictions.
	Restrictions *RunnerRestrictions `json:"restrictions,omitempty" yaml:"restrictions,omitempty"`

	// MergeStrategy controls how to merge with agent/global config.
	// Options: "extend" (default) - merge with parent config, "replace" - ignore parent config
	MergeStrategy string `json:"merge_strategy,omitempty" yaml:"merge_strategy,omitempty"`
}

// PermissionsConfig configures how permission requests from agents are handled.
// Permission requests occur when an agent wants to perform sensitive operations
// like running commands, accessing files outside the workspace, etc.
type PermissionsConfig struct {
	// AutoApprove enables automatic approval of permission requests.
	// When true, all permission requests are automatically approved without
	// showing a dialog to the user.
	// Default: true (until the permission UI is fully implemented)
	// TODO: Change default to false once permission dialog is implemented.
	AutoApprove *bool `json:"auto_approve,omitempty" yaml:"auto_approve,omitempty"`
}

// IsAutoApprove returns whether permission requests should be auto-approved.
// Safe to call on nil receiver - returns true (the current default) if not configured.
func (p *PermissionsConfig) IsAutoApprove() bool {
	if p == nil || p.AutoApprove == nil {
		return true // Default: auto-approve until UI is ready
	}
	return *p.AutoApprove
}

// MCPConfig contains configuration for the MCP (Model Context Protocol) server.
// The MCP server provides debugging tools and UI prompt functionality to AI agents.
type MCPConfig struct {
	// Enabled controls whether the MCP server is started. Default: true.
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	// Host is the address to bind the MCP server to. Default: "127.0.0.1".
	Host string `json:"host,omitempty" yaml:"host,omitempty"`
	// Port is the port to listen on. Default: 5757.
	// Use 0 to let the system pick a free port.
	Port *int `json:"port,omitempty" yaml:"port,omitempty"`
}

// IsEnabled returns whether the MCP server should be started.
func (c *MCPConfig) IsEnabled() bool {
	if c == nil || c.Enabled == nil {
		return true // Default: enabled
	}
	return *c.Enabled
}

// GetHost returns the host to bind the MCP server to.
func (c *MCPConfig) GetHost() string {
	if c == nil || c.Host == "" {
		return "127.0.0.1" // Default: localhost only
	}
	return c.Host
}

// GetPort returns the port for the MCP server.
// Returns -1 if not configured (use default), or the configured port.
func (c *MCPConfig) GetPort() int {
	if c == nil || c.Port == nil {
		return -1 // Signal to use default
	}
	return *c.Port
}

// Config represents the complete Mitto configuration.
type Config struct {
	// ACPServers is the list of configured ACP servers (order matters - first is default)
	ACPServers []ACPServer
	// Prompts is a list of predefined prompts for the dropup menu (global prompts)
	Prompts []WebPrompt
	// PromptsDirs is a list of additional directories to search for prompt files.
	// These are searched in addition to the default MITTO_DIR/prompts/ directory.
	// Paths can be absolute or relative (resolved against the config file's directory).
	PromptsDirs []string
	// Web contains web interface configuration
	Web WebConfig
	// UI contains desktop app UI configuration
	UI UIConfig
	// Session contains session storage limits configuration (not exposed in Settings dialog)
	Session *SessionConfig
	// Conversations contains global conversation processing configuration
	Conversations *ConversationsConfig
	// Permissions contains global permission handling configuration
	Permissions *PermissionsConfig
	// RestrictedRunners contains per-runner-type global configuration.
	// Key is the runner type (e.g., "exec", "sandbox-exec", "firejail", "docker").
	RestrictedRunners map[string]*WorkspaceRunnerConfig
	// MCP contains MCP (Model Context Protocol) server configuration
	MCP *MCPConfig
}

// rawACPServerConfig is used for YAML unmarshaling of ACP server entries.
type rawACPServerConfig struct {
	Command string `yaml:"command"`
	Cwd     string `yaml:"cwd"`
	Type    string `yaml:"type"` // Optional type for prompt matching; defaults to name
	Prompts []struct {
		Name            string `yaml:"name"`
		Prompt          string `yaml:"prompt"`
		BackgroundColor string `yaml:"backgroundColor"`
	} `yaml:"prompts"`
	RestrictedRunners map[string]*WorkspaceRunnerConfig `yaml:"restricted_runners"`
}

// rawConfig is used for YAML unmarshaling to handle the map-based format.
type rawConfig struct {
	ACP []map[string]rawACPServerConfig `yaml:"acp"`
	// Prompts is the top-level prompts section for global prompts
	Prompts []struct {
		Name            string `yaml:"name"`
		Prompt          string `yaml:"prompt"`
		BackgroundColor string `yaml:"backgroundColor"`
	} `yaml:"prompts"`
	// PromptsDirs is a list of additional directories to search for prompt files
	PromptsDirs []string `yaml:"prompts_dirs"`
	Web         struct {
		Host         string `yaml:"host"`
		Port         int    `yaml:"port"`
		ExternalPort int    `yaml:"external_port"`
		APIPrefix    string `yaml:"api_prefix"`
		Theme        string `yaml:"theme"`
		StaticDir    string `yaml:"static_dir"`
		Hooks        struct {
			Up struct {
				Command string `yaml:"command"`
				Name    string `yaml:"name"`
			} `yaml:"up"`
			Down struct {
				Command string `yaml:"command"`
				Name    string `yaml:"name"`
			} `yaml:"down"`
		} `yaml:"hooks"`
		Auth *struct {
			Simple *struct {
				Username string `yaml:"username"`
				Password string `yaml:"password"`
			} `yaml:"simple"`
			Allow *struct {
				IPs []string `yaml:"ips"`
			} `yaml:"allow"`
		} `yaml:"auth"`
		Security *struct {
			TrustedProxies        []string `yaml:"trusted_proxies"`
			AllowedOrigins        []string `yaml:"allowed_origins"`
			RateLimitRPS          float64  `yaml:"rate_limit_rps"`
			RateLimitBurst        int      `yaml:"rate_limit_burst"`
			MaxWSConnectionsPerIP int      `yaml:"max_ws_connections_per_ip"`
			MaxWSMessageSize      int64    `yaml:"max_ws_message_size"`
		} `yaml:"security"`
	} `yaml:"web"`
	UI *struct {
		Confirmations *struct {
			DeleteSession           *bool `yaml:"delete_session"`
			QuitWithRunningSessions *bool `yaml:"quit_with_running_sessions"`
		} `yaml:"confirmations"`
		Web *struct {
			InputFontFamily         string `yaml:"input_font_family"`
			ConversationCyclingMode string `yaml:"conversation_cycling_mode"`
			SingleExpandedGroup     bool   `yaml:"single_expanded_group"`
		} `yaml:"web"`
		Mac *struct {
			Hotkeys *struct {
				ShowHide *struct {
					Enabled *bool  `yaml:"enabled"`
					Key     string `yaml:"key"`
				} `yaml:"show_hide"`
			} `yaml:"hotkeys"`
			Notifications *struct {
				Sounds *struct {
					AgentCompleted bool `yaml:"agent_completed"`
				} `yaml:"sounds"`
				NativeEnabled bool `yaml:"native_enabled"`
			} `yaml:"notifications"`
			ShowInAllSpaces  bool `yaml:"show_in_all_spaces"`
			StartAtLogin     bool `yaml:"start_at_login"`
			BadgeClickAction *struct {
				Enabled *bool  `yaml:"enabled"`
				Command string `yaml:"command"`
			} `yaml:"badge_click_action"`
		} `yaml:"mac"`
	} `yaml:"ui"`
	Conversations *struct {
		Processing *struct {
			Override   bool `yaml:"override"`
			Processors []struct {
				When     string `yaml:"when"`
				Position string `yaml:"position"`
				Text     string `yaml:"text"`
			} `yaml:"processors"`
		} `yaml:"processing"`
		Queue *struct {
			Enabled            *bool `yaml:"enabled"`
			DelaySeconds       int   `yaml:"delay_seconds"`
			MaxSize            *int  `yaml:"max_size"`
			AutoGenerateTitles *bool `yaml:"auto_generate_titles"`
		} `yaml:"queue"`
		ActionButtons *struct {
			Enabled *bool `yaml:"enabled"`
		} `yaml:"action_buttons"`
		ExternalImages *struct {
			Enabled *bool `yaml:"enabled"`
		} `yaml:"external_images"`
	} `yaml:"conversations"`
	// RestrictedRunners is the top-level per-runner-type configuration
	RestrictedRunners map[string]*WorkspaceRunnerConfig `yaml:"restricted_runners"`
	// Permissions is the global permission handling configuration
	Permissions *struct {
		AutoApprove *bool `yaml:"auto_approve"`
	} `yaml:"permissions"`
	// MCP is the MCP server configuration
	MCP *struct {
		Enabled *bool  `yaml:"enabled"`
		Host    string `yaml:"host"`
		Port    *int   `yaml:"port"`
	} `yaml:"mcp"`
}

// Load reads and parses the configuration file from the given path.
// It supports both YAML and JSON formats, detected by file extension:
//   - .json: parsed as JSON (Settings format)
//   - .yaml, .yml, or any other extension: parsed as YAML
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	// Detect format by file extension
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".json" {
		return ParseJSON(data)
	}

	// Default to YAML for .yaml, .yml, or any other extension
	return Parse(data)
}

// ParseJSON parses JSON configuration data (Settings format) into a Config struct.
func ParseJSON(data []byte) (*Config, error) {
	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("failed to parse JSON config: %w", err)
	}

	cfg := settings.ToConfig()

	if len(cfg.ACPServers) == 0 {
		return nil, fmt.Errorf("no ACP servers configured")
	}

	return cfg, nil
}

// Parse parses YAML configuration data into a Config struct.
func Parse(data []byte) (*Config, error) {
	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	cfg := &Config{
		ACPServers: make([]ACPServer, 0, len(raw.ACP)),
	}

	for _, entry := range raw.ACP {
		for name, server := range entry {
			acpServer := ACPServer{
				Name:              name,
				Command:           server.Command,
				Cwd:               server.Cwd,
				Type:              server.Type, // Optional type for prompt matching
				RestrictedRunners: server.RestrictedRunners,
			}
			// Copy server-specific prompts
			for _, p := range server.Prompts {
				acpServer.Prompts = append(acpServer.Prompts, WebPrompt{
					Name:            p.Name,
					Prompt:          p.Prompt,
					BackgroundColor: p.BackgroundColor,
				})
			}
			cfg.ACPServers = append(cfg.ACPServers, acpServer)
		}
	}

	if len(cfg.ACPServers) == 0 {
		return nil, fmt.Errorf("no ACP servers configured")
	}

	// Populate global prompts (top-level)
	for _, p := range raw.Prompts {
		cfg.Prompts = append(cfg.Prompts, WebPrompt{
			Name:            p.Name,
			Prompt:          p.Prompt,
			BackgroundColor: p.BackgroundColor,
		})
	}

	// Populate prompts directories
	cfg.PromptsDirs = raw.PromptsDirs

	// Populate web config
	cfg.Web.Host = raw.Web.Host
	cfg.Web.Port = raw.Web.Port
	cfg.Web.ExternalPort = raw.Web.ExternalPort
	cfg.Web.APIPrefix = raw.Web.APIPrefix
	cfg.Web.Theme = raw.Web.Theme
	cfg.Web.StaticDir = raw.Web.StaticDir
	cfg.Web.Hooks.Up.Command = raw.Web.Hooks.Up.Command
	cfg.Web.Hooks.Up.Name = raw.Web.Hooks.Up.Name
	cfg.Web.Hooks.Down.Command = raw.Web.Hooks.Down.Command
	cfg.Web.Hooks.Down.Name = raw.Web.Hooks.Down.Name

	// Populate auth config
	if raw.Web.Auth != nil {
		cfg.Web.Auth = &WebAuth{}
		if raw.Web.Auth.Simple != nil {
			cfg.Web.Auth.Simple = &SimpleAuth{
				Username: raw.Web.Auth.Simple.Username,
				Password: raw.Web.Auth.Simple.Password,
			}
		}
		if raw.Web.Auth.Allow != nil && len(raw.Web.Auth.Allow.IPs) > 0 {
			cfg.Web.Auth.Allow = &AuthAllow{
				IPs: raw.Web.Auth.Allow.IPs,
			}
		}
	}

	// Populate security config
	if raw.Web.Security != nil {
		cfg.Web.Security = &WebSecurity{
			TrustedProxies:        raw.Web.Security.TrustedProxies,
			AllowedOrigins:        raw.Web.Security.AllowedOrigins,
			RateLimitRPS:          raw.Web.Security.RateLimitRPS,
			RateLimitBurst:        raw.Web.Security.RateLimitBurst,
			MaxWSConnectionsPerIP: raw.Web.Security.MaxWSConnectionsPerIP,
			MaxWSMessageSize:      raw.Web.Security.MaxWSMessageSize,
		}
	}

	// Populate UI config
	if raw.UI != nil {
		// Populate confirmations
		if raw.UI.Confirmations != nil {
			cfg.UI.Confirmations = &ConfirmationsConfig{
				DeleteSession:           raw.UI.Confirmations.DeleteSession,
				QuitWithRunningSessions: raw.UI.Confirmations.QuitWithRunningSessions,
			}
		}

		// Populate Web-specific config
		if raw.UI.Web != nil {
			cfg.UI.Web = &WebUIConfig{
				InputFontFamily:         raw.UI.Web.InputFontFamily,
				ConversationCyclingMode: raw.UI.Web.ConversationCyclingMode,
				SingleExpandedGroup:     raw.UI.Web.SingleExpandedGroup,
			}
		}

		// Populate Mac-specific config
		if raw.UI.Mac != nil {
			cfg.UI.Mac = &MacUIConfig{}

			// Populate hotkeys
			if raw.UI.Mac.Hotkeys != nil {
				cfg.UI.Mac.Hotkeys = &MacHotkeys{}
				if raw.UI.Mac.Hotkeys.ShowHide != nil {
					cfg.UI.Mac.Hotkeys.ShowHide = &HotkeyConfig{
						Enabled: raw.UI.Mac.Hotkeys.ShowHide.Enabled,
						Key:     raw.UI.Mac.Hotkeys.ShowHide.Key,
					}
				}
			}

			// Populate notifications
			if raw.UI.Mac.Notifications != nil {
				cfg.UI.Mac.Notifications = &NotificationsConfig{
					NativeEnabled: raw.UI.Mac.Notifications.NativeEnabled,
				}
				if raw.UI.Mac.Notifications.Sounds != nil {
					cfg.UI.Mac.Notifications.Sounds = &NotificationSoundsConfig{
						AgentCompleted: raw.UI.Mac.Notifications.Sounds.AgentCompleted,
					}
				}
			}

			// Populate show in all spaces setting
			cfg.UI.Mac.ShowInAllSpaces = raw.UI.Mac.ShowInAllSpaces

			// Populate start at login setting
			cfg.UI.Mac.StartAtLogin = raw.UI.Mac.StartAtLogin

			// Populate badge click action setting
			if raw.UI.Mac.BadgeClickAction != nil {
				cfg.UI.Mac.BadgeClickAction = &BadgeClickActionConfig{
					Enabled: raw.UI.Mac.BadgeClickAction.Enabled,
					Command: raw.UI.Mac.BadgeClickAction.Command,
				}
			}
		}
	}

	// Populate conversations config
	if raw.Conversations != nil {
		cfg.Conversations = &ConversationsConfig{}

		// Parse processing config
		if raw.Conversations.Processing != nil {
			processors := make([]MessageProcessor, 0, len(raw.Conversations.Processing.Processors))
			for _, p := range raw.Conversations.Processing.Processors {
				processors = append(processors, MessageProcessor{
					When:     ProcessorWhen(p.When),
					Position: ProcessorPosition(p.Position),
					Text:     p.Text,
				})
			}
			if len(processors) > 0 || raw.Conversations.Processing.Override {
				cfg.Conversations.Processing = &ConversationProcessing{
					Override:   raw.Conversations.Processing.Override,
					Processors: processors,
				}
			}
		}

		// Parse queue config
		if raw.Conversations.Queue != nil {
			cfg.Conversations.Queue = &QueueConfig{
				Enabled:            raw.Conversations.Queue.Enabled,
				DelaySeconds:       raw.Conversations.Queue.DelaySeconds,
				MaxSize:            raw.Conversations.Queue.MaxSize,
				AutoGenerateTitles: raw.Conversations.Queue.AutoGenerateTitles,
			}
		}

		// Parse action buttons config
		if raw.Conversations.ActionButtons != nil {
			cfg.Conversations.ActionButtons = &ActionButtonsConfig{
				Enabled: raw.Conversations.ActionButtons.Enabled,
			}
		}

		// Parse external images config
		if raw.Conversations.ExternalImages != nil {
			cfg.Conversations.ExternalImages = &ExternalImagesConfig{
				Enabled: raw.Conversations.ExternalImages.Enabled,
			}
		}

		// If no config was actually set, nil out the conversations config
		if cfg.Conversations.Processing == nil && cfg.Conversations.Queue == nil &&
			cfg.Conversations.ActionButtons == nil && cfg.Conversations.ExternalImages == nil {
			cfg.Conversations = nil
		}
	}

	// Copy restricted runners (top-level per-runner-type config)
	cfg.RestrictedRunners = raw.RestrictedRunners

	// Parse permissions config
	if raw.Permissions != nil {
		cfg.Permissions = &PermissionsConfig{
			AutoApprove: raw.Permissions.AutoApprove,
		}
	}

	// Parse MCP config
	if raw.MCP != nil {
		cfg.MCP = &MCPConfig{
			Enabled: raw.MCP.Enabled,
			Host:    raw.MCP.Host,
			Port:    raw.MCP.Port,
		}
	}

	return cfg, nil
}

// DefaultServer returns the default ACP server (first in the list).
func (c *Config) DefaultServer() *ACPServer {
	if len(c.ACPServers) == 0 {
		return nil
	}
	return &c.ACPServers[0]
}

// GetServer returns the ACP server with the given name.
func (c *Config) GetServer(name string) (*ACPServer, error) {
	for i := range c.ACPServers {
		if c.ACPServers[i].Name == name {
			return &c.ACPServers[i], nil
		}
	}
	return nil, fmt.Errorf("ACP server %q not found in configuration", name)
}

// GetServerType returns the type identifier for an ACP server by name.
// If the server has a Type set, returns that; otherwise returns the server name.
// Returns empty string if the server is not found.
func (c *Config) GetServerType(name string) string {
	srv, err := c.GetServer(name)
	if err != nil {
		return ""
	}
	return srv.GetType()
}

// ServerNames returns a list of all configured server names.
func (c *Config) ServerNames() []string {
	names := make([]string, len(c.ACPServers))
	for i, srv := range c.ACPServers {
		names[i] = srv.Name
	}
	return names
}

// DefaultShowHideHotkey is the default hotkey for toggling app visibility.
const DefaultShowHideHotkey = "cmd+ctrl+m"

// GetShowHideHotkey returns the configured show/hide hotkey.
// Returns the hotkey string and whether it's enabled.
// If not configured, returns the default ("cmd+ctrl+m", true).
func (c *Config) GetShowHideHotkey() (key string, enabled bool) {
	// Default values
	key = DefaultShowHideHotkey
	enabled = true

	if c.UI.Mac == nil || c.UI.Mac.Hotkeys == nil || c.UI.Mac.Hotkeys.ShowHide == nil {
		return key, enabled
	}

	hk := c.UI.Mac.Hotkeys.ShowHide

	// Check if explicitly disabled
	if hk.Enabled != nil && !*hk.Enabled {
		return "", false
	}

	// Use custom key if provided
	if hk.Key != "" {
		key = hk.Key
	}

	return key, enabled
}

// ShouldConfirmQuitWithRunningSessions returns whether to show a confirmation dialog
// when quitting the app with running sessions. Defaults to true.
func (c *Config) ShouldConfirmQuitWithRunningSessions() bool {
	if c.UI.Confirmations == nil || c.UI.Confirmations.QuitWithRunningSessions == nil {
		return true // Default to true
	}
	return *c.UI.Confirmations.QuitWithRunningSessions
}
