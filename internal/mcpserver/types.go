package mcpserver

import (
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
)

// ConversationInfo contains information about a conversation/session.
type ConversationInfo struct {
	SessionID         string    `json:"session_id"`
	Title             string    `json:"title,omitempty"`
	Description       string    `json:"description,omitempty"`
	ACPServer         string    `json:"acp_server"`
	WorkingDir        string    `json:"working_dir"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	LastUserMessageAt time.Time `json:"last_user_message_at,omitempty"`
	MessageCount      int       `json:"message_count"`
	Status            string    `json:"status"`
	Archived          bool      `json:"archived"`
	SessionFolder     string    `json:"session_folder"`

	// Runtime status (only available when session is running)
	IsRunning      bool   `json:"is_running"`
	IsPrompting    bool   `json:"is_prompting"`
	IsLocked       bool   `json:"is_locked"`
	LockStatus     string `json:"lock_status,omitempty"`
	LockClientType string `json:"lock_client_type,omitempty"`
	LastSeq        int64  `json:"last_seq,omitempty"`
}

// ConfigInfo contains the Mitto configuration info.
// This is a sanitized version that doesn't expose sensitive data.
type ConfigInfo struct {
	ACPServers    []ACPServerInfo `json:"acp_servers"`
	WebConfig     WebConfigInfo   `json:"web"`
	HasPrompts    bool            `json:"has_prompts"`
	PromptsCount  int             `json:"prompts_count"`
	SessionConfig *SessionInfo    `json:"session,omitempty"`
}

// ACPServerInfo contains info about an ACP server.
type ACPServerInfo struct {
	Name         string `json:"name"`
	Command      string `json:"command"`
	PromptsCount int    `json:"prompts_count"`
}

// WebConfigInfo contains web configuration info.
type WebConfigInfo struct {
	Port             int    `json:"port"`
	ExternalPort     int    `json:"external_port"`
	APIPrefix        string `json:"api_prefix,omitempty"`
	Theme            string `json:"theme,omitempty"`
	HasAuth          bool   `json:"has_auth"`
	ExternalEnabled  bool   `json:"external_enabled"`
	HasHooksUp       bool   `json:"has_hooks_up"`
	HasHooksDown     bool   `json:"has_hooks_down"`
	RateLimitEnabled bool   `json:"rate_limit_enabled"`
}

// SessionInfo contains session storage configuration.
type SessionInfo struct {
	MaxMessagesPerSession  int    `json:"max_messages_per_session,omitempty"`
	MaxSessionSizeBytes    int64  `json:"max_session_size_bytes,omitempty"`
	ArchiveRetentionPeriod string `json:"archive_retention_period,omitempty"`
}

// RuntimeInfo contains runtime information about the Mitto instance.
type RuntimeInfo struct {
	// OS information
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	NumCPU   int    `json:"num_cpu"`
	Hostname string `json:"hostname,omitempty"`

	// Process information
	PID        int    `json:"pid"`
	Executable string `json:"executable,omitempty"`
	WorkingDir string `json:"working_dir,omitempty"`

	// Go runtime
	GoVersion    string `json:"go_version"`
	NumGoroutine int    `json:"num_goroutine"`

	// Mitto directories
	DataDir     string `json:"data_dir,omitempty"`
	SessionsDir string `json:"sessions_dir,omitempty"`
	LogsDir     string `json:"logs_dir,omitempty"`

	// Log files
	LogFiles LogFilesInfo `json:"log_files"`

	// Configuration files
	ConfigFiles ConfigFilesInfo `json:"config_files"`

	// Environment
	MittoDirEnv   string `json:"mitto_dir_env,omitempty"`
	MittoRCEnv    string `json:"mittorc_env,omitempty"`
	MittoLogLevel string `json:"mitto_log_level,omitempty"`
}

// LogFilesInfo contains paths to log files.
type LogFilesInfo struct {
	MainLog    string `json:"main_log,omitempty"`
	AccessLog  string `json:"access_log,omitempty"`
	WebViewLog string `json:"webview_log,omitempty"`
}

// ConfigFilesInfo contains paths to configuration files.
type ConfigFilesInfo struct {
	SettingsFile   string `json:"settings_file,omitempty"`
	WorkspacesFile string `json:"workspaces_file,omitempty"`
	RCFile         string `json:"rc_file,omitempty"`
}

// buildRuntimeInfo gathers runtime information about the Mitto instance.
func buildRuntimeInfo() *RuntimeInfo {
	info := &RuntimeInfo{
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		NumCPU:       runtime.NumCPU(),
		GoVersion:    runtime.Version(),
		NumGoroutine: runtime.NumGoroutine(),
		PID:          os.Getpid(),
	}

	// Hostname
	if hostname, err := os.Hostname(); err == nil {
		info.Hostname = hostname
	}

	// Executable path
	if exe, err := os.Executable(); err == nil {
		info.Executable = exe
	}

	// Working directory
	if wd, err := os.Getwd(); err == nil {
		info.WorkingDir = wd
	}

	// Mitto directories
	if dataDir, err := appdir.Dir(); err == nil {
		info.DataDir = dataDir
	}
	if sessionsDir, err := appdir.SessionsDir(); err == nil {
		info.SessionsDir = sessionsDir
	}
	if logsDir, err := appdir.LogsDir(); err == nil {
		info.LogsDir = logsDir

		// Log files (based on standard naming)
		info.LogFiles.MainLog = filepath.Join(logsDir, "mitto.log")
		info.LogFiles.AccessLog = filepath.Join(logsDir, "access.log")
		info.LogFiles.WebViewLog = filepath.Join(logsDir, "webview.log")
	}

	// Configuration files
	if settingsPath, err := appdir.SettingsPath(); err == nil {
		info.ConfigFiles.SettingsFile = settingsPath
	}
	if workspacesPath, err := appdir.WorkspacesPath(); err == nil {
		info.ConfigFiles.WorkspacesFile = workspacesPath
	}
	if rcPath, err := appdir.RCFilePath(); err == nil && rcPath != "" {
		info.ConfigFiles.RCFile = rcPath
	}

	// Environment variables
	info.MittoDirEnv = os.Getenv(appdir.MittoDirEnv)
	info.MittoRCEnv = os.Getenv(appdir.MittoRCEnv)
	info.MittoLogLevel = os.Getenv("MITTO_LOG_LEVEL")

	return info
}

// configToSafeOutput converts a config.Config to a sanitized ConfigInfo.
func configToSafeOutput(cfg *config.Config) *ConfigInfo {
	if cfg == nil {
		return nil
	}

	info := &ConfigInfo{
		ACPServers: make([]ACPServerInfo, len(cfg.ACPServers)),
	}

	// Copy ACP server info (without sensitive data)
	for i, srv := range cfg.ACPServers {
		info.ACPServers[i] = ACPServerInfo{
			Name:         srv.Name,
			Command:      srv.Command,
			PromptsCount: len(srv.Prompts),
		}
	}

	// Copy global prompts info
	info.HasPrompts = len(cfg.Prompts) > 0
	info.PromptsCount = len(cfg.Prompts)

	// Copy web config (without auth credentials)
	info.WebConfig = WebConfigInfo{
		Port:         cfg.Web.Port,
		ExternalPort: cfg.Web.ExternalPort,
		APIPrefix:    cfg.Web.APIPrefix,
		Theme:        cfg.Web.Theme,
		HasAuth:      cfg.Web.Auth != nil && cfg.Web.Auth.Simple != nil,
	}
	if cfg.Web.Auth != nil && cfg.Web.Auth.Simple != nil {
		info.WebConfig.ExternalEnabled = cfg.Web.Auth.Simple.Username != ""
	}
	info.WebConfig.HasHooksUp = cfg.Web.Hooks.Up.Command != ""
	info.WebConfig.HasHooksDown = cfg.Web.Hooks.Down.Command != ""
	if cfg.Web.Security != nil {
		info.WebConfig.RateLimitEnabled = cfg.Web.Security.RateLimitRPS > 0
	}

	// Copy session config
	if cfg.Session != nil {
		info.SessionConfig = &SessionInfo{
			MaxMessagesPerSession:  cfg.Session.MaxMessagesPerSession,
			MaxSessionSizeBytes:    cfg.Session.MaxSessionSizeBytes,
			ArchiveRetentionPeriod: cfg.Session.ArchiveRetentionPeriod,
		}
	}

	return info
}
