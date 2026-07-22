// Package config handles configuration loading and management for Mitto.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ModelProfile is a named model profile pairing a selection criteria with tags.
// Profiles let users tag models by capability (e.g. "Smart", "Cheap") independently
// of the raw model name, so other parts of Mitto can branch on capability tags rather
// than brittle model-name strings.
//
// **List order is priority** — the first profile carrying a given tag wins in
// ProfilesByTag / SelectPreferredModel resolution. Put your preferred variant first.
type ModelProfile struct {
	// Name is the display name for this profile (e.g. "Opus").
	Name string `json:"name"`
	// Criteria selects which model(s) this profile applies to, reusing the same
	// match-mode + pattern mechanism as ACPServer config-option constraints.
	Criteria *ACPServerConstraint `json:"criteria,omitempty"`
	// Tags is the list of capability tags carried by matching models (e.g. "Smart", "Cheap").
	Tags []string `json:"tags,omitempty"`
}

// DefaultModelProfiles returns the canonical, hardcoded set of model profiles.
// This is the single Go source of truth for well-known model-capability tags and
// mirrors the `models:` block in config/config.default.yaml (kept in sync by the
// `make check-model-tags` target).
//
// Unlike config.default.yaml — which only seeds settings.json on first run — these
// profiles are always available at runtime via EffectiveModelProfiles, so tag-based
// prompt preferredModels (e.g. `modelTag: Coding`) resolve even when the user's
// settings.json has an empty or partial `Models` list. A fresh copy is returned on
// each call so callers may mutate the result freely.
//
// List order = priority; put preferred variants first (the first profile carrying a
// tag wins in tag-based resolution).
func DefaultModelProfiles() []ModelProfile {
	contains := func(pattern string) *ACPServerConstraint {
		return &ACPServerConstraint{MatchMode: "contains", Pattern: pattern}
	}
	return []ModelProfile{
		{Name: "Claude", Criteria: contains("Claude"), Tags: []string{"Anthropic"}},
		{Name: "Claude Mythos", Criteria: contains("Mythos"), Tags: []string{"Smartest", "Reasoning", "Thinking", "Deep", "Slow", "Expensive"}},
		{Name: "Claude Opus", Criteria: contains("Opus"), Tags: []string{"Smartest", "Reasoning", "Thinking", "Deep", "Slow", "Expensive"}},
		{Name: "Claude Sonnet 5", Criteria: contains("Sonnet 5"), Tags: []string{"Smart", "Coding"}},
		{Name: "Claude Sonnet 4", Criteria: contains("Sonnet 4"), Tags: []string{"Smart", "Coding"}},
		{Name: "Claude Haiku", Criteria: contains("Haiku"), Tags: []string{"Fast", "Cheap"}},
		{Name: "GPT-5", Criteria: contains("GPT-5"), Tags: []string{"Smart", "Reasoning", "Thinking", "Deep", "Coding"}},
		{Name: "GPT-4", Criteria: contains("GPT-4"), Tags: []string{"Smart", "Coding"}},
		{Name: "OpenAI GPT", Criteria: contains("GPT"), Tags: []string{"OpenAI"}},
		{Name: "Gemini", Criteria: contains("Gemini"), Tags: []string{"Smart", "LongContext"}},
		{Name: "GLM", Criteria: contains("GLM"), Tags: []string{"Smart", "Coding", "OpenWeight", "SelfHostable"}},
		{Name: "DeepSeek", Criteria: contains("DeepSeek"), Tags: []string{"Smart", "Coding", "OpenWeight", "SelfHostable"}},
	}
}

// CanonicalModelTags returns the sorted, de-duplicated set of capability tags carried
// by DefaultModelProfiles. It is the authoritative list of tags that a prompt's
// preferredModels `modelTag:` may reference and is used by the `make check-model-tags`
// validator to reject unknown tags in builtin prompts.
func CanonicalModelTags() []string {
	seen := make(map[string]struct{})
	var tags []string
	for _, p := range DefaultModelProfiles() {
		for _, t := range p.Tags {
			if _, dup := seen[t]; dup {
				continue
			}
			seen[t] = struct{}{}
			tags = append(tags, t)
		}
	}
	sort.Strings(tags)
	return tags
}

// EffectiveModelProfiles returns the model profiles that should be used for tag/name
// resolution: the user-configured profiles (Config.Models) unioned with the canonical
// DefaultModelProfiles as a fallback. User profiles take precedence — a default profile
// is only appended when no user profile shares its name (case-insensitive). This
// guarantees well-known tags always resolve even when settings.json omits `models:`,
// without overriding any customisation the user has made. Safe to call on a nil Config.
//
// Ordering contract: user profiles come first, in user-supplied order; defaults are
// appended after, in DefaultModelProfiles order. Resolvers (ProfilesByTag,
// SelectPreferredModel) walk both halves top-to-bottom, so for equal-tag ties a user
// profile always trumps a default, and within each half list order = priority.
func (c *Config) EffectiveModelProfiles() []ModelProfile {
	var user []ModelProfile
	if c != nil {
		user = c.Models
	}
	out := make([]ModelProfile, len(user))
	copy(out, user)
	haveName := make(map[string]struct{}, len(user))
	for _, p := range user {
		haveName[strings.ToLower(p.Name)] = struct{}{}
	}
	for _, d := range DefaultModelProfiles() {
		if _, ok := haveName[strings.ToLower(d.Name)]; ok {
			continue
		}
		out = append(out, d)
	}
	return out
}

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
	// Env is a map of environment variables to set when starting the ACP server.
	// These are merged with the current environment (server-specific vars take precedence).
	Env map[string]string
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
	// Tags is an optional list of categorization tags for this ACP server.
	// Tags are single words or hyphenated-words (e.g., "coding", "fast-model").
	Tags []string
	// ModelProfile is the name of a Model profile (Config.Models) whose Criteria
	// should be used for session-start model auto-selection, replacing the
	// legacy free-text matchMode/pattern constraint under Constraints["model"].
	// Empty means no profile is selected; legacy Constraints["model"] (if any)
	// is used as a fallback. See FindModelProfile.
	ModelProfile string
	// ModelTag is a capability tag (e.g. "Smart", "Fast") used to resolve a
	// Model profile at session start when ModelProfile is empty. The first
	// profile in EffectiveModelProfiles carrying this tag supplies the model
	// Criteria. Mutually exclusive with ModelProfile in the UI, but if both are
	// set ModelProfile wins. Mirrors WorkspaceSettings.InitialModelTag.
	ModelTag string
	// InitialModelProfile is the name of a Model profile (Config.Models) applied
	// as the baseline model of every new conversation created against this ACP
	// server, right after the agent reports its available models. Empty means
	// keep the agent's default model. Mutually exclusive with InitialModelTag
	// in the UI; when both are set, InitialModelProfile wins. Serves as a
	// fallback when the workspace has no InitialModelProfile/InitialModelTag
	// set (see WorkspaceSettings.InitialModelProfile). Distinct from
	// ModelProfile above, which drives the legacy per-resume Constraints["model"]
	// auto-selection behavior.
	InitialModelProfile string
	// InitialModelTag selects the initial baseline model by capability tag
	// (e.g. "Coding"). Resolved to the first Model profile (Config.Models, in
	// definition order) carrying this tag whose Criteria matches an available
	// model. Empty means keep the agent's default model.
	InitialModelTag string
	// Constraints is an optional map of config option auto-selection rules.
	// The key is the config option category (e.g., "model", "mode").
	// When a session starts, matching constraints auto-select the appropriate option value.
	Constraints map[string]*ACPServerConstraint
	// ContextFlushCommand is an optional agent-native slash command (e.g. "/clear")
	// that flushes/clears the conversation context without restarting the agent.
	// Empty means the feature is disabled for this server.
	ContextFlushCommand string
}

// GetType returns the type identifier for prompt matching.
// If Type is not set, returns the Name as the type.
func (s *ACPServer) GetType() string {
	if s.Type != "" {
		return s.Type
	}
	return s.Name
}

// GetInitialModelPreference returns the ACP server's initial-model preference
// as an ordered list of PromptPreferredModel entries suitable for
// conversation.SelectPreferredModel. Returns nil when neither
// InitialModelProfile nor InitialModelTag is set. InitialModelProfile takes
// precedence over InitialModelTag when both are set. Serves as a fallback for
// fresh top-level conversations when the workspace has no initial-model
// preference set (see WorkspaceSettings.GetInitialModelPreference). Safe to
// call on a nil receiver.
func (s *ACPServer) GetInitialModelPreference() []PromptPreferredModel {
	if s == nil {
		return nil
	}
	if s.InitialModelProfile != "" {
		return []PromptPreferredModel{{ModelName: s.InitialModelProfile}}
	}
	if s.InitialModelTag != "" {
		return []PromptPreferredModel{{ModelTag: s.InitialModelTag}}
	}
	return nil
}

// FindModelProfile returns the named Model profile (case-insensitive match on
// ModelProfile.Name), or nil if name is empty or no profile matches.
func (c *Config) FindModelProfile(name string) *ModelProfile {
	if c == nil || name == "" {
		return nil
	}
	for i := range c.Models {
		if strings.EqualFold(c.Models[i].Name, name) {
			return &c.Models[i]
		}
	}
	return nil
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
	// ExternalAddress is an optional URL to health-check periodically.
	// When set, Mitto will restart the hooks (stop → wait → start) if the
	// address becomes unreachable.
	ExternalAddress string `json:"external_address,omitempty"`
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

// CloudflareAuth represents Cloudflare Access JWT authentication.
// When configured, requests with a valid Cloudflare Access JWT
// (in the Cf-Access-Jwt-Assertion header or CF_Authorization cookie)
// are authenticated without requiring username/password login.
type CloudflareAuth struct {
	TeamDomain string `json:"team_domain" yaml:"team_domain"`                       // e.g. "yourteam.cloudflareaccess.com"
	Audience   string `json:"audience"    yaml:"audience"`                          // Application AUD tag from Cloudflare Access
	CACertFile string `json:"ca_cert_file,omitempty" yaml:"ca_cert_file,omitempty"` // Optional: path to CA cert for JWKS endpoint (useful for testing or private CAs)
}

// Validate checks that the Cloudflare Access configuration is valid.
func (c *CloudflareAuth) Validate() error {
	if c.TeamDomain == "" {
		return fmt.Errorf("cloudflare auth: team_domain is required")
	}
	if strings.Contains(c.TeamDomain, "://") {
		return fmt.Errorf("cloudflare auth: team_domain should be a domain name, not a URL (e.g., 'yourteam.cloudflareaccess.com')")
	}
	if strings.Contains(c.TeamDomain, "/") {
		return fmt.Errorf("cloudflare auth: team_domain should not contain path components")
	}
	if c.Audience == "" {
		return fmt.Errorf("cloudflare auth: audience is required (Application AUD tag from Cloudflare Access)")
	}
	return nil
}

// WebAuth represents authentication configuration for the web interface.
type WebAuth struct {
	// Simple enables simple username/password authentication when set
	Simple *SimpleAuth `json:"simple,omitempty" yaml:"simple,omitempty"`
	// Cloudflare enables Cloudflare Access JWT authentication when set
	Cloudflare *CloudflareAuth `json:"cloudflare,omitempty" yaml:"cloudflare,omitempty"`
	// Allow contains IP addresses/CIDR ranges that bypass authentication
	Allow *AuthAllow `json:"allow,omitempty" yaml:"allow,omitempty"`
}

// HasCloudflareAuth returns true if Cloudflare Access authentication is configured and valid.
func (w *WebAuth) HasCloudflareAuth() bool {
	return w != nil && w.Cloudflare != nil && w.Cloudflare.Validate() == nil
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

// OpenTarget represents a single "Open In..." entry for a workspace folder.
// A target maps a stable ID to a shell command that opens the workspace directory
// in a specific application (Finder, Terminal, editor, etc.).
type OpenTarget struct {
	// ID is a stable identifier for the target (e.g., "finder", "vscode").
	// Builtin targets use well-known IDs; user targets use any non-empty string.
	ID string `json:"id"`
	// Label is the human-readable name shown in menus and settings.
	Label string `json:"label"`
	// Icon is an optional icon key used by the frontend to render an icon.
	Icon string `json:"icon,omitempty"`
	// Command is the shell command to execute.
	// Supports ${MITTO_WORKING_DIR} placeholder which is replaced with the workspace directory path.
	Command string `json:"command"`
	// Enabled controls whether the target appears in menus.
	// When nil, the effective value depends on Builtin: builtin targets default to true,
	// user-defined targets default to false. Explicit non-nil values always win.
	Enabled *bool `json:"enabled,omitempty"`
	// Builtin is true for platform-default entries seeded by Mitto.
	// This field is not user-settable; user-provided values are ignored on merge.
	Builtin bool `json:"builtin,omitempty"`
}

// GetEnabled returns the effective enabled state for the target.
// When Enabled is nil, builtin targets default to true and user-defined targets default to false.
func (t *OpenTarget) GetEnabled() bool {
	if t == nil {
		return false
	}
	if t.Enabled != nil {
		return *t.Enabled
	}
	return t.Builtin
}

// OpenInConfig represents the configurable list of "Open In..." targets for
// workspace folders (context menu on the workspace badge / folder buttons).
type OpenInConfig struct {
	// Targets is the ordered list of open-in entries.
	Targets []OpenTarget `json:"targets,omitempty"`
}

// DefaultOpenTargets returns the platform-specific default set of "Open In..."
// targets. All returned entries are marked Builtin: true; the default-enabled
// state is expressed via Enabled *bool so callers can distinguish "unset" from
// "explicit false".
func DefaultOpenTargets() []OpenTarget {
	bp := func(b bool) *bool { return &b }
	switch runtime.GOOS {
	case "darwin":
		return []OpenTarget{
			{ID: "finder", Label: "Finder", Icon: "finder", Command: "open ${MITTO_WORKING_DIR}", Enabled: bp(true), Builtin: true},
			{ID: "terminal", Label: "Terminal", Icon: "terminal", Command: "open -a Terminal ${MITTO_WORKING_DIR}", Enabled: bp(true), Builtin: true},
			{ID: "iterm", Label: "iTerm", Icon: "iterm", Command: "open -a iTerm ${MITTO_WORKING_DIR}", Enabled: bp(false), Builtin: true},
			{ID: "vscode", Label: "Visual Studio Code", Icon: "vscode", Command: `open -a "Visual Studio Code" ${MITTO_WORKING_DIR}`, Enabled: bp(false), Builtin: true},
			{ID: "cursor", Label: "Cursor", Icon: "cursor", Command: "open -a Cursor ${MITTO_WORKING_DIR}", Enabled: bp(false), Builtin: true},
			{ID: "xcode", Label: "Xcode", Icon: "xcode", Command: "open -a Xcode ${MITTO_WORKING_DIR}", Enabled: bp(false), Builtin: true},
			{ID: "goland", Label: "GoLand", Icon: "goland", Command: "open -a GoLand ${MITTO_WORKING_DIR}", Enabled: bp(false), Builtin: true},
		}
	case "linux":
		return []OpenTarget{
			{ID: "finder", Label: "File Manager", Icon: "finder", Command: "xdg-open ${MITTO_WORKING_DIR}", Enabled: bp(true), Builtin: true},
			{ID: "terminal", Label: "Terminal", Icon: "terminal", Command: "x-terminal-emulator --working-directory=${MITTO_WORKING_DIR}", Enabled: bp(true), Builtin: true},
		}
	case "windows":
		return []OpenTarget{
			{ID: "finder", Label: "Explorer", Icon: "finder", Command: "explorer %MITTO_WORKING_DIR%", Enabled: bp(true), Builtin: true},
			{ID: "terminal", Label: "Command Prompt", Icon: "terminal", Command: `cmd /c start "" cmd /K "cd /d %MITTO_WORKING_DIR%"`, Enabled: bp(true), Builtin: true},
		}
	default:
		return nil
	}
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
	// OpenIn configures the list of "Open In..." targets for the workspace folder.
	OpenIn *OpenInConfig `json:"open_in,omitempty"`
}

// EffectiveOpenTargets returns the effective ordered list of "Open In..." targets
// for this MacUIConfig. When no OpenIn config is set, it returns the platform
// defaults. Otherwise it starts from DefaultOpenTargets and merges user entries
// by ID.
func (c *MacUIConfig) EffectiveOpenTargets() []OpenTarget {
	if c == nil || c.OpenIn == nil || len(c.OpenIn.Targets) == 0 {
		return DefaultOpenTargets()
	}

	// Merge user entries into the platform defaults by ID.
	defaults := DefaultOpenTargets()
	userByID := make(map[string]OpenTarget, len(c.OpenIn.Targets))
	seen := make(map[string]bool, len(c.OpenIn.Targets))
	for _, u := range c.OpenIn.Targets {
		if u.ID == "" || seen[u.ID] {
			continue
		}
		seen[u.ID] = true
		userByID[u.ID] = u
	}

	result := make([]OpenTarget, 0, len(defaults)+len(c.OpenIn.Targets))
	usedIDs := make(map[string]bool, len(defaults))
	for _, d := range defaults {
		usedIDs[d.ID] = true
		if u, ok := userByID[d.ID]; ok {
			if u.Label != "" {
				d.Label = u.Label
			}
			if u.Icon != "" {
				d.Icon = u.Icon
			}
			if u.Command != "" {
				d.Command = u.Command
			}
			if u.Enabled != nil {
				d.Enabled = u.Enabled
			}
		}
		result = append(result, d)
	}
	for _, u := range c.OpenIn.Targets {
		if u.ID == "" || usedIDs[u.ID] {
			continue
		}
		usedIDs[u.ID] = true
		u.Builtin = false
		result = append(result, u)
	}
	return result
}

// DeleteConversation confirmation modes for ConfirmationsConfig.DeleteConversation.
const (
	// DeleteConversationAlways confirms on every conversation destruction (default).
	DeleteConversationAlways = "always"
	// DeleteConversationResponding confirms only when the agent is responding.
	DeleteConversationResponding = "responding"
	// DeleteConversationNever never confirms before destroying a conversation.
	DeleteConversationNever = "never"
)

// ConfirmationsConfig represents confirmation dialog settings.
type ConfirmationsConfig struct {
	// DeleteConversation controls when to show a confirmation dialog before
	// destroying a conversation (closing via Cmd+W, deleting from the sidebar,
	// or quitting the macOS app while an agent is responding). One of "always"
	// (default), "responding" (only when the agent is responding), or "never".
	DeleteConversation string `json:"delete_conversation,omitempty"`
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

	// InputFontSize is the font size for the compose/input box.
	// Options: "default" (14px), "small" (12px), "medium" (16px), "large" (18px), "xl" (20px)
	InputFontSize string `json:"input_font_size,omitempty"`

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
	// Beads contains configuration specific to the /api/issues (beads) endpoints
	Beads *WebBeadsConfig `json:"beads,omitempty" yaml:"beads,omitempty"`
}

// WebBeadsConfig gates optional behaviour on the beads (issues) endpoints.
// Kept as a nested pointer so unset config leaves every flag at its safe
// default (false) without ambiguity between "not configured" and "explicitly
// disabled".
type WebBeadsConfig struct {
	// AllowMigrateFromUI opts in to the POST /api/beads/migrate endpoint
	// that can trigger `bd migrate schema` + `bd dolt push` (or `bd bootstrap`)
	// from the Beads panel when a schema-skew error is detected. Defaults to
	// false because running migrate on the wrong clone of a remote-backed
	// database forks the schema — the safe default is to require the user
	// to opt in explicitly in settings.
	AllowMigrateFromUI bool `json:"allow_migrate_from_ui,omitempty" yaml:"allow_migrate_from_ui,omitempty"`
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
	// LogAll controls whether all HTTP requests are logged (like nginx/Apache access.log)
	// or only security-relevant events (login attempts, unauthorized access, rate limiting).
	// When nil, the default depends on the runtime: true for the macOS app, false for CLI.
	// Set to true for comprehensive HTTP access logging, false for security-events-only mode.
	LogAll *bool `json:"log_all,omitempty"`
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
//	      - when:
//	          on:    userPrompt
//	          match: first
//	        mutate: prepend
//	        text: "You are a helpful assistant.\n\n"
//	      - when:
//	          on:    userPrompt
//	          match: all
//	        mutate: append
//	        text: "\n\n[Be concise]"
//
// Example usage in code:
//
//	procs := config.MergeProcessors(globalConfig.Conversations, workspaceConfig.Conversations)
//	procMgr.AddTextProcessors(procs, 0) // priority 0 → runs before command-mode processors
// ============================================================================

// ProcessorPhase defines when in the conversation lifecycle a processor fires.
// Valid values: "userPrompt", "agentResponded", "agentIdle", "conversationClosed"
type ProcessorPhase string

const (
	// ProcessorPhaseUserPrompt fires processors before the user's message is sent to the agent.
	ProcessorPhaseUserPrompt ProcessorPhase = "userPrompt"
	// ProcessorPhaseAgentResponded fires processors after the agent has finished responding.
	ProcessorPhaseAgentResponded ProcessorPhase = "agentResponded"
	// ProcessorPhaseAgentIdle fires processors after the agent has finished responding
	// and the message queue has drained (single fire at the idle breakpoint).
	ProcessorPhaseAgentIdle ProcessorPhase = "agentIdle"
	// ProcessorPhaseConversationClosed fires processors once when the session is archived
	// (fire-and-forget). Only command-mode and prompt-mode with output:discard are allowed.
	ProcessorPhaseConversationClosed ProcessorPhase = "conversationClosed"
)

// ProcessorMatch defines which messages in the sequence a processor applies to.
// Valid values: "first", "all", "allExceptFirst"
type ProcessorMatch string

const (
	// ProcessorMatchFirst applies only to the first-ever message in a conversation.
	ProcessorMatchFirst ProcessorMatch = "first"
	// ProcessorMatchAll applies to every message.
	ProcessorMatchAll ProcessorMatch = "all"
	// ProcessorMatchAllExceptFirst applies to all messages except the first.
	ProcessorMatchAllExceptFirst ProcessorMatch = "allExceptFirst"
)

// ProcessorMutate defines where the processor text is inserted relative to the message.
// Valid values: "prepend", "append"
type ProcessorMutate string

const (
	// ProcessorMutatePrepend inserts text before the user's message.
	ProcessorMutatePrepend ProcessorMutate = "prepend"
	// ProcessorMutateAppend inserts text after the user's message.
	ProcessorMutateAppend ProcessorMutate = "append"
)

// ProcessorWhenBlock is the block form of the when condition used in inline .mittorc processors.
// Inline processors support on: and match: only — no rerun, stopReasons, or excludeOrigins.
//
//	when:
//	  on:    userPrompt | agentResponded
//	  match: first | all | allExceptFirst
type ProcessorWhenBlock struct {
	On    ProcessorPhase `yaml:"on" json:"on"`
	Match ProcessorMatch `yaml:"match" json:"match"`
}

// UnmarshalYAML enforces the block form for `when` in inline .mittorc processors.
// The scalar form (`when: first`) and the old `sent:` key are rejected.
func (w *ProcessorWhenBlock) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		return fmt.Errorf("processor 'when' must be a block (got scalar %q); use:\n  when:\n    on: userPrompt\n    match: %s", value.Value, value.Value)
	}
	// Check for legacy `sent:` key and provide a clear migration message.
	for i := 0; i+1 < len(value.Content); i += 2 {
		if value.Content[i].Value == "sent" {
			return fmt.Errorf("processor 'when.sent' is no longer supported; replace with 'on:' + 'match:' (e.g., on: userPrompt, match: %s)", value.Content[i+1].Value)
		}
	}
	type rawBlock ProcessorWhenBlock // avoid infinite recursion
	var raw rawBlock
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*w = ProcessorWhenBlock(raw)
	return nil
}

// MessageProcessor defines a single message transformation rule.
// Processors are applied in order to transform user messages before sending to the ACP server.
// Each processor specifies when it applies, where to insert text, and what text to insert.
type MessageProcessor struct {
	// When specifies when this processor applies.
	When ProcessorWhenBlock `json:"when" yaml:"when"`
	// Mutate specifies where to insert the text: "prepend" (before) or "append" (after)
	Mutate ProcessorMutate `json:"mutate" yaml:"mutate"`
	// Text is the content to insert at the specified position
	Text string `json:"text" yaml:"text"`
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
	// DefaultFlags contains default values for advanced settings flags that will be
	// applied to new conversations. Only flags explicitly set to true are stored.
	// If a flag is not present in this map, the compile-time default from
	// internal/session/flags.go is used instead.
	DefaultFlags map[string]bool `json:"default_flags,omitempty" yaml:"default_flags,omitempty"`
	// MaxChildConversations limits the number of child conversations a session can
	// spawn via the MCP mitto_conversation_new tool. Auto-children (created via
	// workspace auto_children config) are NOT counted toward this limit.
	// nil means use default (10). 0 means unlimited.
	MaxChildConversations *int `json:"max_child_conversations,omitempty" yaml:"max_child_conversations,omitempty"`
	// MaxLoopIterations caps the number of scheduled runs a loop conversation
	// performs before it auto-stops. nil = use default (DefaultMaxLoopIterations);
	// 0 = unlimited (still bounded by the hardcoded GlobalMaxLoopIterations backstop).
	MaxLoopIterations *int `json:"max_loop_iterations,omitempty" yaml:"max_loop_iterations,omitempty"`
	// MinLoopCompletionDelaySeconds is the global lower limit (floor) for the
	// on-completion loop trigger's delay. nil = use default (DefaultMinLoopCompletionDelaySeconds).
	MinLoopCompletionDelaySeconds *int `json:"min_loop_completion_delay_seconds,omitempty" yaml:"min_loop_completion_delay_seconds,omitempty"`
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

// DefaultMaxChildConversations is the default limit for child conversations
// spawned via MCP when no explicit limit is configured.
const DefaultMaxChildConversations = 10

// GetMaxChildConversations returns the configured max child conversations limit.
// Safe to call on nil receiver - returns DefaultMaxChildConversations if not configured.
// Returns 0 for unlimited.
func (c *ConversationsConfig) GetMaxChildConversations() int {
	if c == nil || c.MaxChildConversations == nil {
		return DefaultMaxChildConversations
	}
	return *c.MaxChildConversations
}

// DefaultMaxLoopIterations is the default user-facing cap on scheduled runs
// for a loop conversation when no explicit limit is configured.
const DefaultMaxLoopIterations = 100

// DefaultMinLoopCompletionDelaySeconds is the default floor (seconds) applied to the
// on-completion loop delay to prevent hot loops.
const DefaultMinLoopCompletionDelaySeconds = 5

// DefaultRunOnStartAntiFlapSeconds is the anti-flap window (seconds) applied to
// the loop boot pulse (mitto-ystk). When a loop with RunOnStart=true was last
// delivered within this window, the boot pulse is suppressed to prevent a
// Mitto restart from re-firing a loop that just ran.
const DefaultRunOnStartAntiFlapSeconds = 60

// GlobalMaxLoopIterations is the hardcoded absolute backstop on scheduled runs
// for any loop conversation. It can never be exceeded by config.
const GlobalMaxLoopIterations = 1000

// GetMaxLoopIterations returns the configured default max loop-iterations cap.
// Safe to call on nil receiver - returns DefaultMaxLoopIterations when unset.
// Returns 0 for unlimited. The returned value is clamped to GlobalMaxLoopIterations.
func (c *ConversationsConfig) GetMaxLoopIterations() int {
	if c == nil || c.MaxLoopIterations == nil {
		return DefaultMaxLoopIterations
	}
	v := *c.MaxLoopIterations
	if v > GlobalMaxLoopIterations {
		return GlobalMaxLoopIterations
	}
	return v
}

// GetMinLoopCompletionDelaySeconds returns the configured floor for the on-completion delay.
// Safe to call on nil receiver - returns DefaultMinLoopCompletionDelaySeconds when unset.
// A configured value < 0 is treated as 0.
func (c *ConversationsConfig) GetMinLoopCompletionDelaySeconds() int {
	if c == nil || c.MinLoopCompletionDelaySeconds == nil {
		return DefaultMinLoopCompletionDelaySeconds
	}
	v := *c.MinLoopCompletionDelaySeconds
	if v < 0 {
		return 0
	}
	return v
}

// EffectiveMaxLoopIterations returns the binding iteration cap for a loop
// conversation: the smallest positive of { promptMax, configMax, GlobalMaxLoopIterations }.
//
// All three inputs are literal caps, not sentinels for anything other than
// "unlimited":
//   - promptMax and configMax: 0 means "unlimited" (that cap is ignored).
//   - Any positive value is treated literally (e.g. configMax=1 means stop after 1).
//   - GlobalMaxLoopIterations is the hardcoded absolute backstop and always applies.
//
// Per-conversation caps are honored below the global safeguard: if promptMax > 0
// and smaller than configMax, the per-conversation cap wins. The result is always
// positive.
func EffectiveMaxLoopIterations(promptMax, configMax int) int {
	effective := GlobalMaxLoopIterations
	if promptMax > 0 && promptMax < effective {
		effective = promptMax
	}
	if configMax > 0 && configMax < effective {
		effective = configMax
	}
	return effective
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
// The server is always started; only its bind host/port are configurable.
type MCPConfig struct {
	// Host is the address to bind the MCP server to. Default: "127.0.0.1".
	Host string `json:"host,omitempty" yaml:"host,omitempty"`
	// Port is the port to listen on. Default: 5757.
	// Must be a fixed port (1-65535). 0 (auto-assigned / random) is NOT allowed:
	// the full MCP address must be known in advance so ACP servers can be
	// configured to connect to it.
	Port *int `json:"port,omitempty" yaml:"port,omitempty"`
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
	// Stats contains dashboard time-series stats configuration (mitto-a86b).
	Stats *StatsConfig
	// Prewarm contains adaptive ACP/MCP pre-warming thresholds (mitto-mw0)
	Prewarm *PrewarmConfig
	// Conversations contains global conversation processing configuration
	Conversations *ConversationsConfig
	// Permissions contains global permission handling configuration
	Permissions *PermissionsConfig
	// RestrictedRunners contains per-runner-type global configuration.
	// Key is the runner type (e.g., "exec", "sandbox-exec", "firejail", "docker").
	RestrictedRunners map[string]*WorkspaceRunnerConfig
	// MCP contains MCP (Model Context Protocol) server configuration
	MCP *MCPConfig
	// Models is the list of named model profiles (criteria + tags) for tag-based
	// model-capability lookups.
	Models []ModelProfile
	// Shortcuts holds global per-section configurable shortcut buttons, keyed by
	// section ID (e.g. "conversations", "tasksList", "beadsIssue"). These are
	// merged with folder-level shortcuts at render time (global entries first).
	Shortcuts map[string][]ShortcutButton
}

// rawModelCriteria is used for YAML unmarshaling of a model profile's criteria.
// It mirrors ACPServerConstraint but with explicit yaml tags (yaml.v3 lowercases
// field names by default, which would turn MatchMode into "matchmode").
type rawModelCriteria struct {
	MatchMode string `yaml:"matchMode"`
	Pattern   string `yaml:"pattern"`
}

// rawModelProfile is used for YAML unmarshaling of model profile entries.
type rawModelProfile struct {
	Name     string            `yaml:"name"`
	Criteria *rawModelCriteria `yaml:"criteria"`
	Tags     []string          `yaml:"tags"`
}

// rawACPServerConfig is used for YAML unmarshaling of ACP server entries.
type rawACPServerConfig struct {
	Command string            `yaml:"command"`
	Cwd     string            `yaml:"cwd"`
	Type    string            `yaml:"type"` // Optional type for prompt matching; defaults to name
	Env     map[string]string `yaml:"env"`  // Environment variables to set when starting the server
	Tags    []string          `yaml:"tags"` // Optional categorization tags
	Prompts []struct {
		Name            string            `yaml:"name"`
		Prompt          string            `yaml:"prompt"`
		BackgroundColor string            `yaml:"backgroundColor"`
		Icon            string            `yaml:"icon"`
		Description     string            `yaml:"description"`
		Group           string            `yaml:"group"`
		Menus           string            `yaml:"menus"`
		Enabled         *bool             `yaml:"enabled"`
		EnabledWhen     string            `yaml:"enabledWhen"`
		Loop            *PromptLoop       `yaml:"loop,omitempty"`
		Parameters      []PromptParameter `yaml:"parameters"`
		Tags            []string          `yaml:"tags"`
		Singleton       bool              `yaml:"singleton"`
		Target          *PromptTarget     `yaml:"target,omitempty"`
	} `yaml:"prompts"`
	RestrictedRunners   map[string]*WorkspaceRunnerConfig `yaml:"restricted_runners"`
	ContextFlushCommand string                            `yaml:"contextFlushCommand"`
}

// rawConfig is used for YAML unmarshaling to handle the map-based format.
type rawConfig struct {
	ACP []map[string]rawACPServerConfig `yaml:"acp"`
	// Models is the top-level named model profiles section
	Models []rawModelProfile `yaml:"models"`
	// Prompts is the top-level prompts section for global prompts
	Prompts []struct {
		Name            string            `yaml:"name"`
		Prompt          string            `yaml:"prompt"`
		BackgroundColor string            `yaml:"backgroundColor"`
		Icon            string            `yaml:"icon"`
		Description     string            `yaml:"description"`
		Group           string            `yaml:"group"`
		Menus           string            `yaml:"menus"`
		Enabled         *bool             `yaml:"enabled"`
		EnabledWhen     string            `yaml:"enabledWhen"`
		Loop            *PromptLoop       `yaml:"loop,omitempty"`
		Parameters      []PromptParameter `yaml:"parameters"`
		Tags            []string          `yaml:"tags"`
		Singleton       bool              `yaml:"singleton"`
		Target          *PromptTarget     `yaml:"target,omitempty"`
	} `yaml:"prompts"`
	// PromptsDirs is a list of additional directories to search for prompt files
	PromptsDirs []string `yaml:"prompts_dirs"`
	// Shortcuts is the top-level global shortcut buttons section, keyed by section ID.
	Shortcuts map[string][]ShortcutButton `yaml:"shortcuts"`
	Web       struct {
		Host         string `yaml:"host"`
		Port         int    `yaml:"port"`
		ExternalPort int    `yaml:"external_port"`
		APIPrefix    string `yaml:"api_prefix"`
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
			ExternalAddress string `yaml:"external_address"`
		} `yaml:"hooks"`
		Auth *struct {
			Simple *struct {
				Username string `yaml:"username"`
				Password string `yaml:"password"`
			} `yaml:"simple"`
			Cloudflare *struct {
				TeamDomain string `yaml:"team_domain"`
				Audience   string `yaml:"audience"`
				CACertFile string `yaml:"ca_cert_file"`
			} `yaml:"cloudflare"`
			Allow *struct {
				IPs []string `yaml:"ips"`
			} `yaml:"allow"`
		} `yaml:"auth"`
		Security *struct {
			TrustedProxies   []string `yaml:"trusted_proxies"`
			AllowedOrigins   []string `yaml:"allowed_origins"`
			RateLimitRPS     float64  `yaml:"rate_limit_rps"`
			RateLimitBurst   int      `yaml:"rate_limit_burst"`
			MaxWSMessageSize int64    `yaml:"max_ws_message_size"`
		} `yaml:"security"`
		Beads *struct {
			AllowMigrateFromUI bool `yaml:"allow_migrate_from_ui"`
		} `yaml:"beads"`
	} `yaml:"web"`
	UI *struct {
		Confirmations *struct {
			DeleteConversation string `yaml:"delete_conversation"`
		} `yaml:"confirmations"`
		Web *struct {
			InputFontFamily         string `yaml:"input_font_family"`
			InputFontSize           string `yaml:"input_font_size"`
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
			ShowInAllSpaces bool `yaml:"show_in_all_spaces"`
			StartAtLogin    bool `yaml:"start_at_login"`
			OpenIn          *struct {
				Targets []struct {
					ID      string `yaml:"id"`
					Label   string `yaml:"label"`
					Icon    string `yaml:"icon"`
					Command string `yaml:"command"`
					Enabled *bool  `yaml:"enabled"`
					Builtin bool   `yaml:"builtin"`
				} `yaml:"targets"`
			} `yaml:"open_in"`
		} `yaml:"mac"`
	} `yaml:"ui"`
	Conversations *struct {
		Processing *struct {
			Override   bool `yaml:"override"`
			Processors []struct {
				When   ProcessorWhenBlock `yaml:"when"`
				Mutate string             `yaml:"mutate"`
				Text   string             `yaml:"text"`
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
		DefaultFlags                  map[string]bool `yaml:"default_flags"`
		MaxChildConversations         *int            `yaml:"max_child_conversations"`
		MaxLoopIterations             *int            `yaml:"max_loop_iterations"`
		MinLoopCompletionDelaySeconds *int            `yaml:"min_loop_completion_delay_seconds"`
	} `yaml:"conversations"`
	// RestrictedRunners is the top-level per-runner-type configuration
	RestrictedRunners map[string]*WorkspaceRunnerConfig `yaml:"restricted_runners"`
	// Permissions is the global permission handling configuration
	Permissions *struct {
		AutoApprove *bool `yaml:"auto_approve"`
	} `yaml:"permissions"`
	// Session is the session storage/startup configuration
	Session *struct {
		MaxMessagesPerSession    int    `yaml:"max_messages_per_session"`
		MaxSessionSizeBytes      int64  `yaml:"max_session_size_bytes"`
		ArchiveRetentionPeriod   string `yaml:"archive_retention_period"`
		AutoArchiveInactiveAfter string `yaml:"auto_archive_inactive_after"`
		StartupStaggerMs         int    `yaml:"startup_stagger_ms"`
		StartupLoopDelaySeconds  int    `yaml:"startup_loop_delay_seconds"`
		StartupResumeConcurrency int    `yaml:"startup_resume_concurrency"`
		LoopWorkspaceConcurrency int    `yaml:"loop_workspace_concurrency"`
		LoopSuspendTimeout       string `yaml:"loop_suspend_timeout"`
		MemoryRecycleThreshold   string `yaml:"memory_recycle_threshold"`
		AgentInactivityTimeout   string `yaml:"agent_inactivity_timeout"`
		McpInitTimeout           string `yaml:"mcp_init_timeout"`
	} `yaml:"session"`
	// Stats is the dashboard time-series stats configuration (mitto-a86b.9)
	Stats *struct {
		RetentionHours *int `yaml:"retention_hours"`
	} `yaml:"stats"`
	// Prewarm is the adaptive pre-warming thresholds (mitto-mw0)
	Prewarm *struct {
		SessionNewFast       string `yaml:"session_new_fast"`
		McpReady             string `yaml:"mcp_ready"`
		HealthyProbesToUnpin int    `yaml:"healthy_probes_to_unpin"`
		MaxPinDuration       string `yaml:"max_pin_duration"`
		MaxPinnedWorkspaces  int    `yaml:"max_pinned_workspaces"`
		// AuxSchedule holds per-purpose staggered creation delays for the
		// cold-start auxiliary session prewarm (mitto-cgc).
		AuxSchedule *struct {
			McpCheck string `yaml:"mcp_check"`
			McpTools string `yaml:"mcp_tools"`
			TitleGen string `yaml:"title_gen"`
			FollowUp string `yaml:"follow_up"`
		} `yaml:"aux_schedule"`
	} `yaml:"prewarm"`
	// MCP is the MCP server configuration
	MCP *struct {
		Host string `yaml:"host"`
		Port *int   `yaml:"port"`
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
				Name:                name,
				Command:             server.Command,
				Cwd:                 server.Cwd,
				Type:                server.Type, // Optional type for prompt matching
				Env:                 server.Env,  // Environment variables
				RestrictedRunners:   server.RestrictedRunners,
				Tags:                server.Tags, // Optional categorization tags
				ContextFlushCommand: server.ContextFlushCommand,
			}
			// Copy server-specific prompts
			for _, p := range server.Prompts {
				// Skip prompts with empty name or prompt text
				if p.Name == "" || p.Prompt == "" {
					continue
				}
				// Skip disabled prompts
				if p.Enabled != nil && !*p.Enabled {
					continue
				}
				wp := WebPrompt{
					Name:            p.Name,
					Prompt:          p.Prompt,
					BackgroundColor: p.BackgroundColor,
					Icon:            p.Icon,
					Description:     p.Description,
					Group:           p.Group,
					Menus:           p.Menus,
					Singleton:       p.Singleton,
					Target:          p.Target,
					Tags:            p.Tags,
					EnabledWhen:     p.EnabledWhen,
					Loop:            p.Loop,
					Parameters:      p.Parameters,
				}
				acpServer.Prompts = append(acpServer.Prompts, wp)
			}
			cfg.ACPServers = append(cfg.ACPServers, acpServer)
		}
	}

	// Populate model profiles (top-level models:)
	for _, m := range raw.Models {
		// Skip profiles without a name
		if m.Name == "" {
			continue
		}
		mp := ModelProfile{
			Name: m.Name,
			Tags: m.Tags,
		}
		if m.Criteria != nil {
			mp.Criteria = &ACPServerConstraint{
				MatchMode: m.Criteria.MatchMode,
				Pattern:   m.Criteria.Pattern,
			}
		}
		cfg.Models = append(cfg.Models, mp)
	}

	// Populate global prompts (top-level)
	for _, p := range raw.Prompts {
		// Skip prompts with empty name
		if p.Name == "" {
			continue
		}
		// Allow empty prompt text only when disabled (used to suppress same-named prompts)
		isDisabled := p.Enabled != nil && !*p.Enabled
		if p.Prompt == "" && !isDisabled {
			continue
		}
		if err := ValidatePromptParameters(p.Menus, p.Parameters); err != nil {
			continue
		}
		wp := WebPrompt{
			Name:            p.Name,
			Prompt:          p.Prompt,
			BackgroundColor: p.BackgroundColor,
			Icon:            p.Icon,
			Description:     p.Description,
			Group:           p.Group,
			Menus:           p.Menus,
			Singleton:       p.Singleton,
			Target:          p.Target,
			Tags:            p.Tags,
			EnabledWhen:     p.EnabledWhen,
			Enabled:         p.Enabled,
			Loop:            p.Loop,
			Parameters:      p.Parameters,
		}
		cfg.Prompts = append(cfg.Prompts, wp)
	}

	// Populate prompts directories
	cfg.PromptsDirs = raw.PromptsDirs

	// Populate global shortcut buttons
	cfg.Shortcuts = raw.Shortcuts

	// Populate web config
	cfg.Web.Host = raw.Web.Host
	cfg.Web.Port = raw.Web.Port
	cfg.Web.ExternalPort = raw.Web.ExternalPort
	cfg.Web.APIPrefix = raw.Web.APIPrefix
	cfg.Web.StaticDir = raw.Web.StaticDir
	cfg.Web.Hooks.Up.Command = raw.Web.Hooks.Up.Command
	cfg.Web.Hooks.Up.Name = raw.Web.Hooks.Up.Name
	cfg.Web.Hooks.Down.Command = raw.Web.Hooks.Down.Command
	cfg.Web.Hooks.Down.Name = raw.Web.Hooks.Down.Name
	cfg.Web.Hooks.ExternalAddress = raw.Web.Hooks.ExternalAddress

	// Populate auth config
	if raw.Web.Auth != nil {
		cfg.Web.Auth = &WebAuth{}
		if raw.Web.Auth.Simple != nil {
			cfg.Web.Auth.Simple = &SimpleAuth{
				Username: raw.Web.Auth.Simple.Username,
				Password: raw.Web.Auth.Simple.Password,
			}
		}
		if raw.Web.Auth.Cloudflare != nil {
			cfg.Web.Auth.Cloudflare = &CloudflareAuth{
				TeamDomain: raw.Web.Auth.Cloudflare.TeamDomain,
				Audience:   raw.Web.Auth.Cloudflare.Audience,
				CACertFile: raw.Web.Auth.Cloudflare.CACertFile,
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
			TrustedProxies:   raw.Web.Security.TrustedProxies,
			AllowedOrigins:   raw.Web.Security.AllowedOrigins,
			RateLimitRPS:     raw.Web.Security.RateLimitRPS,
			RateLimitBurst:   raw.Web.Security.RateLimitBurst,
			MaxWSMessageSize: raw.Web.Security.MaxWSMessageSize,
		}
	}
	if raw.Web.Beads != nil {
		cfg.Web.Beads = &WebBeadsConfig{
			AllowMigrateFromUI: raw.Web.Beads.AllowMigrateFromUI,
		}
	}

	// Populate UI config
	if raw.UI != nil {
		// Populate confirmations
		if raw.UI.Confirmations != nil {
			cfg.UI.Confirmations = &ConfirmationsConfig{
				DeleteConversation: raw.UI.Confirmations.DeleteConversation,
			}
		}

		// Populate Web-specific config
		if raw.UI.Web != nil {
			cfg.UI.Web = &WebUIConfig{
				InputFontFamily:         raw.UI.Web.InputFontFamily,
				InputFontSize:           raw.UI.Web.InputFontSize,
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

			// Populate open-in targets
			if raw.UI.Mac.OpenIn != nil {
				cfg.UI.Mac.OpenIn = &OpenInConfig{}
				for _, t := range raw.UI.Mac.OpenIn.Targets {
					cfg.UI.Mac.OpenIn.Targets = append(cfg.UI.Mac.OpenIn.Targets, OpenTarget{
						ID:      t.ID,
						Label:   t.Label,
						Icon:    t.Icon,
						Command: t.Command,
						Enabled: t.Enabled,
						Builtin: t.Builtin,
					})
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
					When:   p.When,
					Mutate: ProcessorMutate(p.Mutate),
					Text:   p.Text,
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

		// Copy default flags
		if raw.Conversations.DefaultFlags != nil {
			cfg.Conversations.DefaultFlags = raw.Conversations.DefaultFlags
		}

		// Copy max child conversations
		if raw.Conversations.MaxChildConversations != nil {
			cfg.Conversations.MaxChildConversations = raw.Conversations.MaxChildConversations
		}

		// Copy max loop iterations
		if raw.Conversations.MaxLoopIterations != nil {
			cfg.Conversations.MaxLoopIterations = raw.Conversations.MaxLoopIterations
		}

		// Copy min loop completion delay
		if raw.Conversations.MinLoopCompletionDelaySeconds != nil {
			cfg.Conversations.MinLoopCompletionDelaySeconds = raw.Conversations.MinLoopCompletionDelaySeconds
		}

		// If no config was actually set, nil out the conversations config
		if cfg.Conversations.Processing == nil && cfg.Conversations.Queue == nil &&
			cfg.Conversations.ActionButtons == nil && cfg.Conversations.ExternalImages == nil &&
			cfg.Conversations.DefaultFlags == nil && cfg.Conversations.MaxChildConversations == nil &&
			cfg.Conversations.MaxLoopIterations == nil && cfg.Conversations.MinLoopCompletionDelaySeconds == nil {
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

	// Parse session config
	if raw.Session != nil {
		cfg.Session = &SessionConfig{
			MaxMessagesPerSession:    raw.Session.MaxMessagesPerSession,
			MaxSessionSizeBytes:      raw.Session.MaxSessionSizeBytes,
			ArchiveRetentionPeriod:   raw.Session.ArchiveRetentionPeriod,
			AutoArchiveInactiveAfter: raw.Session.AutoArchiveInactiveAfter,
			StartupStaggerMs:         raw.Session.StartupStaggerMs,
			StartupLoopDelaySeconds:  raw.Session.StartupLoopDelaySeconds,
			StartupResumeConcurrency: raw.Session.StartupResumeConcurrency,
			LoopWorkspaceConcurrency: raw.Session.LoopWorkspaceConcurrency,
			LoopSuspendTimeout:       raw.Session.LoopSuspendTimeout,
			MemoryRecycleThreshold:   raw.Session.MemoryRecycleThreshold,
			AgentInactivityTimeout:   raw.Session.AgentInactivityTimeout,
			McpInitTimeout:           raw.Session.McpInitTimeout,
		}
	}

	// Parse stats config (mitto-a86b.9)
	if raw.Stats != nil {
		cfg.Stats = &StatsConfig{
			RetentionHours: raw.Stats.RetentionHours,
		}
	}

	// Parse prewarm config (mitto-mw0)
	if raw.Prewarm != nil {
		cfg.Prewarm = &PrewarmConfig{
			SessionNewFast:       raw.Prewarm.SessionNewFast,
			McpReady:             raw.Prewarm.McpReady,
			HealthyProbesToUnpin: raw.Prewarm.HealthyProbesToUnpin,
			MaxPinDuration:       raw.Prewarm.MaxPinDuration,
			MaxPinnedWorkspaces:  raw.Prewarm.MaxPinnedWorkspaces,
		}
		// Aux prewarm schedule (mitto-cgc): only populate when present so
		// AuxPrewarmSchedule() returns the pure per-purpose defaults otherwise.
		if raw.Prewarm.AuxSchedule != nil {
			cfg.Prewarm.AuxSchedule = &AuxScheduleConfig{
				McpCheck: raw.Prewarm.AuxSchedule.McpCheck,
				McpTools: raw.Prewarm.AuxSchedule.McpTools,
				TitleGen: raw.Prewarm.AuxSchedule.TitleGen,
				FollowUp: raw.Prewarm.AuxSchedule.FollowUp,
			}
		}
	}

	// Parse MCP config
	if raw.MCP != nil {
		cfg.MCP = &MCPConfig{
			Host: raw.MCP.Host,
			Port: raw.MCP.Port,
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

// ProfileByName returns a pointer to the profile in profiles with the given name
// (case-insensitive), or nil when none matches. This is the pure, slice-based core
// shared by (*Config).ModelProfileByName and by conversation.SelectPreferredModel,
// which only has a []ModelProfile (not a *Config) available at call time.
func ProfileByName(profiles []ModelProfile, name string) *ModelProfile {
	for i := range profiles {
		if strings.EqualFold(profiles[i].Name, name) {
			return &profiles[i]
		}
	}
	return nil
}

// ProfilesByTag returns all profiles in profiles carrying the given tag
// (case-insensitive). Returns nil when none match. This is the pure, slice-based
// core shared by (*Config).ModelProfilesByTag and by conversation.SelectPreferredModel.
func ProfilesByTag(profiles []ModelProfile, tag string) []ModelProfile {
	var out []ModelProfile
	for _, p := range profiles {
		for _, t := range p.Tags {
			if strings.EqualFold(t, tag) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// ModelProfileByName returns the model profile with the given name (case-insensitive).
// The bool is false when no profile matches. Intended for consumers that need to look up
// a profile's tags or criteria by its display name. Resolution uses EffectiveModelProfiles
// so well-known profiles resolve even when settings.json omits `models:`.
func (c *Config) ModelProfileByName(name string) (*ModelProfile, bool) {
	profiles := c.EffectiveModelProfiles()
	p := ProfileByName(profiles, name)
	return p, p != nil
}

// ModelProfilesByTag returns all model profiles carrying the given tag (case-insensitive),
// mirroring how ACP server tags are compared elsewhere. Returns an empty slice when none match.
// Resolution uses EffectiveModelProfiles so well-known tags resolve even when settings.json
// omits `models:`.
func (c *Config) ModelProfilesByTag(tag string) []ModelProfile {
	return ProfilesByTag(c.EffectiveModelProfiles(), tag)
}

// ResolveModelTags returns the UNION of capability tags from every model profile whose
// Criteria matches modelName (using the shared ConstraintMatchesName engine). Tags are
// de-duplicated case-insensitively, preserving first-seen order. Resolution uses
// EffectiveModelProfiles so well-known tags resolve even when settings.json omits `models:`.
// Returns nil when modelName is empty or nothing matches (a nil slice is safe to range/index).
func (c *Config) ResolveModelTags(modelName string) []string {
	if modelName == "" {
		return nil
	}
	return resolveModelTags(c.EffectiveModelProfiles(), modelName)
}

// resolveModelTags is the pure, slice-based core shared by (*Config).ResolveModelTags.
// It returns the union (case-insensitive de-dup, first-seen order) of tags from every
// profile whose Criteria matches modelName. Kept separate so callers with a plain
// []ModelProfile (and tests) can resolve without the canonical-default merge.
func resolveModelTags(profiles []ModelProfile, modelName string) []string {
	var tags []string
	seen := make(map[string]struct{})
	for i := range profiles {
		p := &profiles[i]
		if !ConstraintMatchesName(p.Criteria, modelName) {
			continue
		}
		for _, t := range p.Tags {
			key := strings.ToLower(t)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			tags = append(tags, t)
		}
	}
	return tags
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

// DeleteConversationMode returns the configured confirmation mode for destroying
// a conversation. Defaults to DeleteConversationAlways when unset or invalid.
func (c *Config) DeleteConversationMode() string {
	if c.UI.Confirmations == nil {
		return DeleteConversationAlways
	}
	switch c.UI.Confirmations.DeleteConversation {
	case DeleteConversationResponding, DeleteConversationNever:
		return c.UI.Confirmations.DeleteConversation
	default:
		return DeleteConversationAlways
	}
}

// ShouldConfirmDeleteRespondingSession returns whether to show a confirmation dialog
// before destroying a conversation while its agent is actively responding (and, on the
// macOS app, before quitting with responding agents). True unless the mode is "never".
func (c *Config) ShouldConfirmDeleteRespondingSession() bool {
	return c.DeleteConversationMode() != DeleteConversationNever
}
