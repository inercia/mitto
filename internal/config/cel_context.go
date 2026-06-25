// Package config handles configuration loading and management for Mitto.
package config

// PromptEnabledContext holds all data available to CEL expressions
// for evaluating prompt enabled conditions.
// All fields have zero values that are safe to use in expressions.
type PromptEnabledContext struct {
	// ACP contains ACP server information
	ACP ACPContext
	// Workspace contains workspace information
	Workspace WorkspaceContext
	// Session contains current session information
	Session SessionContext
	// Parent contains parent session information (if this is a child session)
	Parent ParentContext
	// Children contains information about child sessions
	Children ChildrenContext
	// Tools contains MCP tools information (may be empty if not yet loaded)
	Tools ToolsContext
	// Permissions contains session permission flags (advanced settings)
	Permissions PermissionsContext
	// Item contains the per-row item context for list menus (e.g. a beads issue row).
	// All fields are empty strings when no item context is provided.
	Item ItemContext
	// Args holds the arguments supplied to the prompt (meta.Arguments) at send time.
	// It feeds template field interpolation ({{ .Args.NAME }}) in prompt bodies and,
	// once the CEL env declares the args variable (mitto-m7sb.5), the cond/when
	// template function. It is nil at menu time (enabledWhen evaluation), since no
	// prompt has been dispatched yet; nil is safe (a nil map indexes to "").
	Args map[string]string
}

// ACPServerInfo describes a single ACP server available in the workspace.
// Mirrors processors.AvailableACPServer but lives in the config package so
// that templatefuncs.go can format it without creating an import cycle.
type ACPServerInfo struct {
	// Name is the server identifier (e.g., "claude-code").
	Name string
	// Type is the server type for prompt matching. Defaults to Name if not set.
	Type string
	// Tags contains optional categorization labels (e.g., ["coding", "fast-model"]).
	Tags []string
	// Current is true if this is the active ACP server for the session.
	Current bool
}

// ACPContext holds ACP server context for CEL evaluation.
type ACPContext struct {
	// Name is the ACP server name (e.g., "auggie", "claude-code")
	Name string
	// Type is the ACP server type (defaults to Name if not set)
	Type string
	// Tags is the list of categorization tags for the ACP server
	Tags []string
	// AutoApprove indicates if permission requests are auto-approved
	AutoApprove bool
	// Available is the list of ACP servers that have workspaces configured for
	// the session's working directory. Used by the {{ .ACP.AvailableText }} template accessor.
	Available []ACPServerInfo
}

// AvailableText renders the available ACP servers as a human-readable
// comma-separated string (see FormatACPServers). Empty when none.
func (a ACPContext) AvailableText() string { return FormatACPServers(a.Available) }

// WorkspaceContext holds workspace context for CEL evaluation.
type WorkspaceContext struct {
	// UUID is the unique identifier of the workspace
	UUID string
	// Folder is the absolute path of the workspace directory
	Folder string
	// Name is the display name of the workspace
	Name string
	// HasUserDataSchema indicates whether the workspace has a user data schema defined in .mittorc
	HasUserDataSchema bool
	// HasMittoRC indicates whether a .mittorc file exists in the workspace directory
	HasMittoRC bool
	// HasMetadataDescription indicates whether the workspace has a metadata description in .mittorc
	HasMetadataDescription bool
	// UserDataSchemaJSON is the JSON representation of the workspace user data schema fields.
	// Empty when no schema is defined. Used by the {{ .Workspace.UserDataSchemaJSON }} template accessor.
	UserDataSchemaJSON string
}

// SessionContext holds current session context for CEL evaluation.
type SessionContext struct {
	// ID is the session identifier
	ID string
	// Name is the display name of the session
	Name string
	// IsChild indicates whether this session has a parent (is a child session)
	IsChild bool
	// IsAutoChild indicates whether this session was automatically created
	IsAutoChild bool
	// ParentID is the ID of the parent session (empty if not a child)
	ParentID string
	// IsPeriodic indicates whether the current prompt was triggered by the periodic runner
	IsPeriodic bool
	// IsPeriodicForced indicates whether a periodic prompt was triggered manually via
	// "run now" (as opposed to the normal scheduled delivery). Mirrors
	// ProcessorInput.IsPeriodicForced and the @mitto:periodic_forced placeholder.
	IsPeriodicForced bool
	// IsPeriodicConversation indicates whether the conversation is configured as a
	// periodic conversation (it has a periodic prompt configuration). Unlike
	// IsPeriodic, this reflects the conversation TYPE, not whether the current run
	// was triggered by the scheduler. Populated in the prompt-menu evaluation context.
	IsPeriodicConversation bool
	// HasBeadsIssue indicates whether the conversation has a beads issue associated
	// (the session metadata BeadsIssue field is non-empty).
	HasBeadsIssue bool
	// BeadsIssue is the linked beads issue ID (e.g. "bd-123"), empty if none.
	BeadsIssue string
	// UserDataJSON is the JSON representation of the current session's user data attributes.
	// Empty when no user data exists. Used by the {{ .Session.UserDataJSON }} template accessor.
	UserDataJSON string
}

// ParentContext holds parent session context for CEL evaluation.
// All fields have zero values when there is no parent session.
type ParentContext struct {
	// Exists indicates whether a parent session exists
	Exists bool
	// Name is the display name of the parent session
	Name string
	// ACPServer is the ACP server name of the parent session
	ACPServer string
}

// ChildInfo describes a single child session for template rendering.
// Lives in config so templatefuncs.go can format it without an import cycle.
type ChildInfo struct {
	// ID is the child session identifier.
	ID string
	// Name is the child session title/name (may be empty if not yet set).
	Name string
	// ACPServer is the ACP server name used by the child session.
	ACPServer string
	// Origin is the child origin string: "auto", "mcp", or "human".
	Origin string
	// IsPrompting indicates the child agent is currently responding.
	IsPrompting bool
}

// ChildrenContext holds children sessions context for CEL evaluation.
type ChildrenContext struct {
	// Count is the number of child sessions
	Count int
	// Exists indicates whether there are any child sessions (Count > 0)
	Exists bool
	// MCPCount is the number of child sessions created via the MCP tool
	MCPCount int
	// Names contains the display names of child sessions
	Names []string
	// ACPServers contains the ACP server names of child sessions
	ACPServers []string
	// PromptingCount is the number of child sessions where the agent is currently responding
	PromptingCount int
	// IdleCount is the number of child sessions NOT currently prompting (Count - PromptingCount)
	IdleCount int
	// All contains structured info for all child sessions.
	// Used by the {{ .Children.AllText }} template accessor (FormatChildren).
	All []ChildInfo
	// MCP contains structured info for MCP-origin child sessions only.
	// Used by the {{ .Children.MCPText }} template accessor (FormatChildren on the MCP slice).
	MCP []ChildInfo
}

// AllText renders all child sessions as a human-readable comma-separated
// string (see FormatChildren). Empty when none.
func (c ChildrenContext) AllText() string { return FormatChildren(c.All) }

// MCPText renders MCP-origin child sessions only, comma-separated. Empty when none.
func (c ChildrenContext) MCPText() string { return FormatChildren(c.MCP) }

// ToolsContext holds MCP tools context for CEL evaluation.
type ToolsContext struct {
	// Available indicates whether the tool list is known (a definitive, non-empty
	// result has been fetched). When false, the tool list is unknown / not yet
	// fetched, and the tool-pattern functions (tools.hasPattern/hasAllPatterns/
	// hasAnyPattern) fail open (return true) so tool-gated prompts are not hidden
	// during the MCP-tools cache warm-up window.
	Available bool
	// Names contains the names of available tools
	Names []string
}

// ItemContext holds the generic per-row item context for CEL evaluation of list menus.
// Populated when a menu is opened for a specific row (e.g. a beads issue); empty otherwise.
// All fields are always present (empty string when unset) so expressions like item.status
// always resolve without a missing-key error.
type ItemContext struct {
	// Id is the unique identifier of the item (e.g. a beads issue ID like "mitto-abc")
	Id string
	// Status is the current status of the item (e.g. "open", "closed", "in_progress")
	Status string
	// Type is the type of the item (e.g. "task", "feature", "bug", "epic")
	Type string
	// Priority is the priority of the item as a string (e.g. "0", "1", "2", "3")
	Priority string
	// Kind distinguishes the source of the item (e.g. "beadsIssue")
	Kind string
}

// PermissionsContext holds session permission flags for CEL evaluation.
// Values are resolved using session.GetFlagValue() which applies defaults.
type PermissionsContext struct {
	// CanDoIntrospection maps to the "can_do_introspection" flag
	CanDoIntrospection bool
	// CanSendPrompt maps to the "can_send_prompt" flag
	CanSendPrompt bool
	// CanPromptUser maps to the "can_prompt_user" flag
	CanPromptUser bool
	// CanStartConversation maps to the "can_start_conversation" flag
	CanStartConversation bool
	// CanInteractOtherWorkspaces maps to the "can_interact_other_workspaces" flag
	CanInteractOtherWorkspaces bool
	// AutoApprovePermissions maps to the "auto_approve_permissions" flag
	AutoApprovePermissions bool
}
