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
}

// WorkspaceContext holds workspace context for CEL evaluation.
type WorkspaceContext struct {
	// UUID is the unique identifier of the workspace
	UUID string
	// Folder is the absolute path of the workspace directory
	Folder string
	// Name is the display name of the workspace
	Name string
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

// ChildrenContext holds children sessions context for CEL evaluation.
type ChildrenContext struct {
	// Count is the number of child sessions
	Count int
	// Exists indicates whether there are any child sessions (Count > 0)
	Exists bool
	// Names contains the display names of child sessions
	Names []string
	// ACPServers contains the ACP server names of child sessions
	ACPServers []string
}

// ToolsContext holds MCP tools context for CEL evaluation.
type ToolsContext struct {
	// Available indicates whether the tool list has been fetched
	Available bool
	// Names contains the names of available tools
	Names []string
}
