package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
)

// ListConversationsInput contains optional filter criteria for mitto_conversation_list.
// All fields are optional — when omitted, no filtering is applied for that field.
type ListConversationsInput struct {
	WorkingDir  *string `json:"working_dir,omitempty"`  // Filter by workspace folder (exact match)
	Archived    *bool   `json:"archived,omitempty"`     // Filter by archived status (true = only archived, false = only active)
	IsRunning   *bool   `json:"is_running,omitempty"`   // Filter by running status (true = only running, false = only stopped)
	ACPServer   *string `json:"acp_server,omitempty"`   // Filter by ACP server name (exact match)
	ExcludeSelf *string `json:"exclude_self,omitempty"` // Exclude this session ID from results (typically your own session)
}

// ConversationInfo contains information about a conversation/session.
// Used by mitto_conversation_list (returns time.Time for dates).
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

// ConversationDetails is the unified output structure for conversation-related tools.
// Used by mitto_conversation_get, mitto_conversation_get_current, and mitto_conversation_new.
// All dates are formatted as ISO 8601 strings for consistent JSON output.
type ConversationDetails struct {
	// Basic metadata
	SessionID         string `json:"session_id"`
	Title             string `json:"title,omitempty"`
	Description       string `json:"description,omitempty"`
	ACPServer         string `json:"acp_server,omitempty"`
	WorkingDir        string `json:"working_dir,omitempty"`
	CreatedAt         string `json:"created_at,omitempty"`           // ISO 8601 format
	UpdatedAt         string `json:"updated_at,omitempty"`           // ISO 8601 format
	LastUserMessageAt string `json:"last_user_message_at,omitempty"` // ISO 8601 format
	MessageCount      int    `json:"message_count"`
	Status            string `json:"status,omitempty"`
	Archived          bool   `json:"archived"`
	SessionFolder     string `json:"session_folder,omitempty"`

	// Runtime status (reflects current state if session is running)
	IsRunning      bool   `json:"is_running"`   // Whether the session is currently active
	IsPrompting    bool   `json:"is_prompting"` // Whether the agent is currently replying
	IsLocked       bool   `json:"is_locked"`    // Whether the session is locked by a client
	LockStatus     string `json:"lock_status,omitempty"`
	LockClientType string `json:"lock_client_type,omitempty"`
	LastSeq        int64  `json:"last_seq,omitempty"` // Last sequence number assigned

	// Parent/child relationship
	ParentSessionID string `json:"parent_session_id,omitempty"` // Parent session if this is a child conversation

	// Available ACP servers that can be used when creating new conversations from this session
	AvailableACPServers []AvailableACPServer `json:"available_acp_servers,omitempty"`
}

// AvailableACPServer describes an ACP server available for conversation creation.
type AvailableACPServer struct {
	Name    string   `json:"name"`              // Server name (used as identifier in mitto_conversation_new)
	Type    string   `json:"type,omitempty"`    // Server type for prompt matching
	Tags    []string `json:"tags,omitempty"`    // Optional categorization tags for this server
	Current bool     `json:"current,omitempty"` // True if this is the current session's ACP server
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

// =============================================================================
// Session-Scoped Tool Types
// =============================================================================

// CurrentSessionOutput is the output for get_current_session tool.
// It returns the same ConversationDetails as other conversation tools.
type CurrentSessionOutput = ConversationDetails

// SendPromptOutput is the output for send_prompt_to_conversation tool.
type SendPromptOutput struct {
	Success       bool   `json:"success"`
	MessageID     string `json:"message_id,omitempty"`
	QueuePosition int    `json:"queue_position,omitempty"`
	Error         string `json:"error,omitempty"`
}

// DeleteConversationInput is the input for mitto_conversation_delete tool.
type DeleteConversationInput struct {
	SelfID         string `json:"self_id"`         // YOUR session ID (the parent)
	ConversationID string `json:"conversation_id"` // Child conversation to delete
}

// DeleteConversationOutput is the output for mitto_conversation_delete tool.
type DeleteConversationOutput struct {
	Success        bool   `json:"success"`
	ConversationID string `json:"conversation_id,omitempty"`
	Error          string `json:"error,omitempty"`
}

// AskYesNoOutput is the output for the mitto_ui_ask_yes_no tool.
type AskYesNoOutput struct {
	Response string `json:"response"` // "yes" | "no" | "timeout"
	Label    string `json:"label,omitempty"`
}

// OptionsButtonsOutput is the output for the mitto_ui_options_buttons tool.
type OptionsButtonsOutput struct {
	Selected string `json:"selected,omitempty"`
	Index    int    `json:"index"`
	TimedOut bool   `json:"timed_out,omitempty"`
}

// OptionsComboOutput is the output for the mitto_ui_options_combo tool.
type OptionsComboOutput struct {
	Selected string `json:"selected,omitempty"`
	Index    int    `json:"index"`
	TimedOut bool   `json:"timed_out,omitempty"`
}

// =============================================================================
// Parent-Child Task Coordination Types
// =============================================================================

// childReportCollector collects reports from child conversations.
// It persists in-memory on the Server for the lifetime of the parent session.
//
// Reports are scoped by task_id. When the parent starts waiting for a new task,
// only reports from the previous (different) task are cleared. Reports for the
// same task are preserved across wait cycles, so children that already reported
// don't need to report again on retry.
type childReportCollector struct {
	parentSessionID string
	currentTaskID   string                  // task_id of the current/last wait cycle
	reports         map[string]*childReport // child_id -> report (nil = pending)
	mu              sync.Mutex

	// Wait signaling: non-nil only while a parent is actively waiting via mitto_children_tasks_wait.
	// Closing waitCh unblocks the waiting parent.
	waitCh     chan struct{}   // closed when all waitingFor children have reported
	waitingFor map[string]bool // child IDs the parent is blocking on

	// Track when we first started waiting for each child (per task).
	// Used to detect stuck children that have been waited on for too long.
	// Reset when task_id changes.
	firstWaitTime map[string]time.Time // child_id -> first wait start time
}

// addReport stores a child's report. Any child can report at any time.
// If the child provides a taskID, it is stored with the report for matching.
// If the parent is currently waiting and this report completes the wait set, signals the parent.
func (c *childReportCollector) addReport(childID string, taskID string, report json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()

	r := c.reports[childID]
	if r == nil {
		r = &childReport{}
		c.reports[childID] = r
	}
	r.Report = report
	r.Completed = true
	r.Timestamp = time.Now()
	r.TaskID = taskID

	c.checkAndSignalWait()
}

// checkAndSignalWait checks if all waited-on children have reported and signals if so.
// Must be called with c.mu held.
func (c *childReportCollector) checkAndSignalWait() {
	if c.waitCh == nil {
		return // No one waiting
	}
	for childID := range c.waitingFor {
		r := c.reports[childID]
		if r == nil || !r.Completed {
			return // Still waiting on this child
		}
	}
	// All waited-on children have reported
	select {
	case <-c.waitCh:
		// Already closed
	default:
		close(c.waitCh)
	}
}

// startWait sets up wait signaling for the given children.
//
// Task-scoped cleanup: if taskID differs from the previous wait's task, all
// reports are cleared (new task = clean slate). If taskID matches the previous
// wait (e.g. retry after timeout), existing reports for the same task are
// preserved so children that already reported don't need to report again.
//
// Returns (waitCh, alreadyDone). If alreadyDone is true, caller should not block.
func (c *childReportCollector) startWait(taskID string, childIDs []string) (chan struct{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Only clear reports when the task changes. Same task = preserve existing reports.
	if c.currentTaskID == "" {
		// First wait ever. Adopt the new task ID.
		// If a task_id is specified, filter out proactive reports that don't match
		// (child reported with a different task_id before parent started waiting).
		if taskID != "" {
			for id, r := range c.reports {
				if r != nil && r.Completed && r.TaskID != taskID {
					delete(c.reports, id)
				}
			}
		}
		c.currentTaskID = taskID
		if c.firstWaitTime == nil {
			c.firstWaitTime = make(map[string]time.Time)
		}
	} else if taskID != c.currentTaskID {
		// New task: clear all reports, but preserve any that were already
		// filed with the new taskID (child reported before parent started waiting).
		oldReports := c.reports
		c.reports = make(map[string]*childReport, len(childIDs))
		for _, id := range childIDs {
			if r := oldReports[id]; r != nil && r.Completed && r.TaskID == taskID {
				c.reports[id] = r // preserve matching report
			}
		}
		c.currentTaskID = taskID
		c.firstWaitTime = make(map[string]time.Time) // new task = reset stuck tracking
	}

	// Initialize firstWaitTime map if needed (first call ever).
	if c.firstWaitTime == nil {
		c.firstWaitTime = make(map[string]time.Time)
	}

	// Ensure all requested children have an entry (nil = pending if not already reported)
	for _, id := range childIDs {
		if _, exists := c.reports[id]; !exists {
			c.reports[id] = nil // pending
		}
	}

	// Build waitingFor set and record first wait time per child.
	now := time.Now()
	c.waitingFor = make(map[string]bool, len(childIDs))
	for _, id := range childIDs {
		c.waitingFor[id] = true
		if _, exists := c.firstWaitTime[id]; !exists {
			c.firstWaitTime[id] = now // record when we first started waiting for this child
		}
	}

	c.waitCh = make(chan struct{})

	// Check if all waited-on children have already reported (for this task)
	c.checkAndSignalWait()

	select {
	case <-c.waitCh:
		return c.waitCh, true // already done
	default:
		return c.waitCh, false
	}
}

// clearWait clears the wait signaling. Called when wait returns (completion, timeout, or cancel).
func (c *childReportCollector) clearWait() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waitCh = nil
	c.waitingFor = nil
}

// getPendingAndReported returns the lists of child IDs that are still pending
// and those that have already reported, from the current waitingFor set.
func (c *childReportCollector) getPendingAndReported() (pending []string, reported []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for childID := range c.waitingFor {
		r := c.reports[childID]
		if r != nil && r.Completed {
			reported = append(reported, childID)
		} else {
			pending = append(pending, childID)
		}
	}
	return
}

// isWaiting returns whether a parent is currently blocked waiting for reports.
func (c *childReportCollector) isWaiting() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.waitCh != nil
}

// maxChildWaitDuration is the maximum cumulative time a parent may wait for a single child
// across all retried wait calls. After this duration, the child is considered stuck and the
// server auto-reports it so the parent AI agent stops retrying indefinitely.
const maxChildWaitDuration = 30 * time.Minute

// getStuckChildren returns the IDs of children that have been waited on (and have not yet
// reported) for longer than maxChildWaitDuration in total across all retried wait calls.
func (c *childReportCollector) getStuckChildren() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	var stuck []string
	now := time.Now()
	for childID, firstWait := range c.firstWaitTime {
		r := c.reports[childID]
		if (r == nil || !r.Completed) && now.Sub(firstWait) > maxChildWaitDuration {
			stuck = append(stuck, childID)
		}
	}
	return stuck
}

// childReport stores the report from a single child conversation.
type childReport struct {
	Report    json.RawMessage `json:"report"`
	Completed bool            `json:"completed"`
	Timestamp time.Time       `json:"timestamp"`
	TaskID    string          `json:"task_id,omitempty"`
}

// ChildrenTasksWaitInput is the input for mitto_children_tasks_wait tool.
type ChildrenTasksWaitInput struct {
	SelfID         string   `json:"self_id"`                   // Parent session ID
	ChildrenList   []string `json:"children_list"`             // Child conversation IDs
	Prompt         string   `json:"prompt,omitempty"`          // Instruction to send to children
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"` // Optional timeout (default: 600s / 10 min)
	TaskID         string   `json:"task_id,omitempty"`         // Task identifier — reports are scoped by task
}

// ChildrenTasksWaitOutput is the output for mitto_children_tasks_wait tool.
type ChildrenTasksWaitOutput struct {
	Success  bool                       `json:"success"`
	Reports  map[string]ChildReportInfo `json:"reports,omitempty"`
	TimedOut bool                       `json:"timed_out,omitempty"`
	Warnings []string                   `json:"warnings,omitempty"` // Non-fatal issues (e.g., children not running)
	Error    string                     `json:"error,omitempty"`
}

// ChildReportData contains the typed report data from a child conversation.
// This is a typed struct (not json.RawMessage) so that the MCP SDK's jsonschema
// auto-generation produces a correct schema for output validation.
type ChildReportData struct {
	Status  string `json:"status,omitempty"`
	Summary string `json:"summary,omitempty"`
	Details string `json:"details,omitempty"`
}

// ChildReportInfo contains the report from a single child conversation.
type ChildReportInfo struct {
	Completed bool             `json:"completed"`
	Report    *ChildReportData `json:"report,omitempty"`
	Timestamp string           `json:"timestamp,omitempty"` // ISO 8601
	Status    string           `json:"status,omitempty"`    // "pending", "completed", "not_running"
	Reason    string           `json:"reason,omitempty"`    // Diagnostic hint when report is incomplete (e.g., "still_processing", "session_unregistered", "archived")
}

// ChildrenTasksReportInput is the input for mitto_children_tasks_report tool.
type ChildrenTasksReportInput struct {
	SelfID  string `json:"self_id"`           // Child session ID
	Status  string `json:"status"`            // e.g. "completed", "in_progress", "failed"
	Summary string `json:"summary"`           // Brief summary of findings/progress
	Details string `json:"details,omitempty"` // Optional detailed information
	TaskID  string `json:"task_id,omitempty"` // Task identifier matching the parent's wait
}

// ChildrenTasksReportOutput is the output for mitto_children_tasks_report tool.
type ChildrenTasksReportOutput struct {
	Success         bool   `json:"success"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	Error           string `json:"error,omitempty"`
}

// =============================================================================
// Conversation Wait Types
// =============================================================================

// ConversationWaitInput is the input for mitto_conversation_wait tool.
type ConversationWaitInput struct {
	SelfID         string `json:"self_id"`                   // YOUR session ID (the caller)
	ConversationID string `json:"conversation_id"`           // Target conversation to wait on
	What           string `json:"what"`                      // Condition to wait for: "agent_responded"
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"` // Optional timeout (default: 600s / 10 min)
}

// ConversationWaitOutput is the output for mitto_conversation_wait tool.
type ConversationWaitOutput struct {
	Success  bool   `json:"success"`
	What     string `json:"what"`                // The condition that was waited on
	TimedOut bool   `json:"timed_out,omitempty"` // True if the wait timed out before the condition was met
	Error    string `json:"error,omitempty"`
}
