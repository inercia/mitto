package cel

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
	// UserData is the per-conversation user data (name→value). Feeds the UserData
	// template func ({{ UserData "NAME" }}), the .UserData map, and the CEL UserData
	// variable. nil at menu time is safe (nil map indexes to "").
	UserData map[string]string
	// Iteration holds loop-iteration info for the current run, enabling prompt
	// bodies to branch on which run they are in (e.g. {{ if .Iteration.IsFirst }}).
	// All-zero (Number=0, IsLoop=false) for non-loop prompts.
	Iteration IterationContext
	// Trigger carries per-fire trigger context. Populated only when the current
	// run was fired by a trigger that has structured data to expose to the
	// prompt body (currently: onTasks — see TriggerOnTasksContext). Nil for
	// scheduled, onCompletion, manual "Run Now", and non-loop dispatches, so
	// templates must guard both levels — nested `with` short-circuits on the
	// outer nil pointer:
	//     {{ with .Trigger }}{{ with .OnTasks }}...{{ end }}{{ end }}
	// Template-only: not declared on the CEL env (enabled-when evaluation runs
	// pre-dispatch when no trigger data exists yet).
	Trigger *TriggerContext
	// PromptTextResolver resolves a prompt NAME to its full body text within the
	// current workspace. Nil at menu/enabledWhen time — no resolver is available
	// there — in which case PromptText fails-closed (returns an error). Wired at
	// the dispatch/render path (see prompt_dispatcher.go). Template-only; NOT
	// exposed to CEL.
	PromptTextResolver func(name string) (string, error)
}

// IterationContext holds loop-iteration info for CEL/template evaluation.
// Number is the 0-based index of the current run (IterationCount at dispatch).
// Values are zero for non-loop prompts.
type IterationContext struct {
	// Number is the 0-based index of the current loop run.
	Number int
	// Max is the configured maximum number of runs (0 = unlimited).
	Max int
	// IsLoop indicates the current prompt was triggered by the loop runner.
	IsLoop bool
	// IsFirst is true when Number == 0.
	IsFirst bool
	// IsLast is true when Max > 0 && Number == Max-1.
	IsLast bool
	// IsUninterrupted is true ONLY on a scheduled (non-forced) loop run that
	// directly follows another such run of this same loop with nothing in between:
	// no user interjection, no forced "run now", no FreshContext, and within the same
	// process lifetime. Powered by a session-scoped in-memory marker that resets across
	// archive/unarchive, GC suspend/resume, process restart, ACP reinit, pause/re-enable,
	// and loop config changes. Prompt bodies branch on it to render a compact "continue"
	// form on uninterrupted continuation runs and the verbose form otherwise.
	IsUninterrupted bool
}

// TriggerContext holds trigger-source data for the current run. Only populated
// for triggers that expose structured data to the prompt body (currently only
// onTasks). Non-nil sub-fields mean the corresponding trigger fired; nil
// sub-fields mean it did not. Template-only — not exposed to CEL.
type TriggerContext struct {
	// OnTasks is populated only when the current fire was driven by a beads
	// change (onTasks trigger). Nil for scheduled/onCompletion/manual "Run Now"
	// dispatches. Templates should guard both levels — the enclosing .Trigger
	// pointer must be non-nil first:
	//     {{ with .Trigger }}{{ with .OnTasks }}...{{ end }}{{ end }}
	OnTasks *TriggerOnTasksContext
}

// TriggerOnTasksContext exposes the beads change delta already computed by the
// onTasks loop runner (see internal/web/loop_runner_tasks.go processTasksChange)
// to the loop prompt body, so prompts can act on which specific issues changed
// without re-scanning the world at agent-side startup.
type TriggerOnTasksContext struct {
	// Changes carries the diff between the previous baseline snapshot and the
	// current one. Shape mirrors what the CEL condition (Changes.*) already sees
	// so template and CEL views stay consistent — see internal/config/tasks_condition.go
	// TasksDelta and canonicalizeIssue for the per-issue canonical key set.
	Changes TasksChangesView
}

// TasksChangesView is the template-facing view of TasksDelta. Each slice holds
// per-issue map[string]any values with canonical keys id, type, status,
// priority, labels, title, assignee, updated_at (same as the CEL activation
// produced by canonicalizeIssue). All slices are non-nil (possibly empty) so
// {{ range }} always behaves.
type TasksChangesView struct {
	Added      []map[string]any
	Updated    []map[string]any
	Removed    []map[string]any
	Closed     []map[string]any
	Reopened   []map[string]any
	LabelAdded []map[string]any
	Touched    []map[string]any // = Added ∪ Updated
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
	// Peers holds non-archived conversations sharing this workspace (excluding self).
	// Used by the {{ .Workspace.Peers.* }} template namespace and Workspace.Peers.*
	// CEL variables for orchestrator prompts that need to reason about sibling
	// conversations. Empty when no peers exist or the workspace is unknown.
	Peers PeersContext
}

// PeerInfo describes a single peer conversation (a non-archived session in the
// same workspace as the current one, excluding self). Mirrors ChildInfo shape
// for orchestrator prompts that inspect sibling conversations.
type PeerInfo struct {
	// ID is the peer session identifier.
	ID string
	// Name is the peer session title/name (may be empty if not yet set).
	Name string
	// ACPServer is the ACP server name used by the peer session.
	ACPServer string
	// ParentID is the peer's parent session ID (empty if the peer is a top-level session).
	ParentID string
	// Origin is the peer's child origin string: "auto", "mcp", "human", or ""
	// when the peer is a top-level session.
	Origin string
	// IsPrompting indicates the peer agent is currently responding.
	IsPrompting bool
	// BeadsIssue is the linked beads issue ID for the peer session
	// (e.g. "mitto-123"), or empty when the peer has no linked bead.
	// Rendered as a trailing " {<id>}" suffix by FormatPeers when non-empty
	// so orchestrator prompts can inline-dedupe by bead ID without an extra
	// mitto_conversation_list round-trip.
	BeadsIssue string
}

// PeersContext holds workspace-peers context for CEL evaluation. Mirrors
// ChildrenContext for the peers namespace (sibling conversations in the same
// workspace, excluding self).
type PeersContext struct {
	// Count is the number of peer conversations.
	Count int
	// Exists indicates whether there are any peer conversations (Count > 0).
	Exists bool
	// PromptingCount is the number of peer conversations where the agent is currently responding.
	PromptingCount int
	// IdleCount is the number of peer conversations NOT currently prompting (Count - PromptingCount).
	IdleCount int
	// All contains structured info for all peer conversations.
	// Used by the {{ .Workspace.Peers.AllText }} template accessor (FormatPeers).
	All []PeerInfo
}

// AllText renders all peer conversations as a human-readable comma-separated
// string (see FormatPeers). Empty when none.
func (p PeersContext) AllText() string { return FormatPeers(p.All) }

// SessionContext holds current session context for CEL evaluation.
type SessionContext struct {
	// ID is the session identifier
	ID string
	// Name is the display name of the session
	Name string
	// HasMessages indicates whether the conversation has had at least one user
	// message (derived from meta.LastUserMessageAt being non-zero). Used to gate
	// "continue"-style prompts that make no sense in an empty conversation.
	HasMessages bool
	// IsChild indicates whether this session has a parent (is a child session)
	IsChild bool
	// IsAutoChild indicates whether this session was automatically created
	IsAutoChild bool
	// ParentID is the ID of the parent session (empty if not a child)
	ParentID string
	// IsLoop indicates whether the current prompt was triggered by the loop runner
	IsLoop bool
	// IsLoopForced indicates whether a loop prompt was triggered manually via
	// "run now" (as opposed to the normal scheduled delivery). Mirrors
	// ProcessorInput.IsLoopForced and the @mitto:loop_forced placeholder.
	IsLoopForced bool
	// IsLoopRunOnStart indicates whether a loop prompt was fired by the boot
	// pulse (mitto-ystk): the once-per-startup dispatch triggered by
	// LoopRunner.fireOnStartPulses shortly after Mitto boots. Mirrors
	// ProcessorInput.IsLoopRunOnStart and the @mitto:loop_run_on_start placeholder.
	IsLoopRunOnStart bool
	// IsLoopConversation indicates whether the conversation is configured as a
	// loop conversation (it has a loop prompt configuration). Unlike
	// IsLoop, this reflects the conversation TYPE, not whether the current run
	// was triggered by the scheduler. Populated in the prompt-menu evaluation context.
	IsLoopConversation bool
	// HasBeadsIssue indicates whether the conversation has a beads issue associated
	// (the session metadata BeadsIssue field is non-empty).
	HasBeadsIssue bool
	// BeadsIssue is the linked beads issue ID (e.g. "bd-123"), empty if none.
	BeadsIssue string
	// UserDataJSON is the JSON representation of the current session's user data attributes.
	// Empty when no user data exists. Used by the {{ .Session.UserDataJSON }} template accessor.
	UserDataJSON string
	// ModelTags holds the capability tags resolved for the session's CURRENT model
	// (from the models: profiles in config). Empty when agentModels is unknown (cold start
	// / suspended session) or no profile matches. Feeds the Model(tag) template func and the
	// Session.HasModelTag CEL macro / "tag" in Session.ModelTags expression. A nil slice is safe.
	ModelTags []string
	// ModelName is the display name of the session's current model (convenience accessor for
	// {{ .Session.ModelName }} display). Empty when the model is unknown. Not the headline
	// surface — branch on ModelTags / HasModelTag rather than the brittle model-name string.
	ModelName string
}

// ParentContext holds parent session context for CEL evaluation.
// All fields have zero values when there is no parent session.
type ParentContext struct {
	// Exists indicates whether a parent session exists
	Exists bool
	// ID is the session identifier of the parent session (empty if no parent)
	ID string
	// Name is the display name of the parent session
	Name string
	// ACPServer is the ACP server name of the parent session
	ACPServer string
}

// Ref renders the parent reference as "id (name)", or just "id" when the name is
// empty, or "" when there is no parent. Mirrors the @mitto:parent formatter and
// backs the {{ .Parent.Ref }} template accessor.
func (p ParentContext) Ref() string {
	if p.ID == "" {
		return ""
	}
	if p.Name != "" {
		return p.ID + " (" + p.Name + ")"
	}
	return p.ID
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
	// BeadsIssue is the linked beads issue ID for the child session
	// (e.g. "mitto-123"), or empty when the child has no linked bead.
	// Rendered as a trailing " {<id>}" suffix by FormatChildren when non-empty
	// so orchestrator prompts can inline-dedupe by bead ID without an extra
	// mitto_conversation_list round-trip.
	BeadsIssue string
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

// ServerToolState represents a single MCP server's tool-list availability
// state, used to decide fail-open vs fail-closed matching in
// hasPattern/hasAllPatterns/hasAnyPattern. See docs/devel/mcp-tool-discovery.md
// (Q3.2, Q4.1): a single global latch cannot distinguish a late-starting or
// unreachable server (which should stay fail-open) from a reachable one
// (which is authoritative), so state is tracked per server instead.
type ServerToolState int

const (
	// ServerToolStateUnknown is the cold-start default: the server has not
	// yet been probed. Pattern matching against its namespace fails open.
	ServerToolStateUnknown ServerToolState = iota
	// ServerToolStateReachable indicates a successful probe/tool-list fetch.
	// Its Names are authoritative: pattern matching is name-based (fail-closed).
	ServerToolStateReachable
	// ServerToolStateUnreachable indicates the server is known (e.g. present
	// in ListMCPServers) but could not be reached. Matching against its
	// namespace fails open, same as Unknown, pending bounded backoff retries
	// (out of scope here; see mitto-sys.5).
	ServerToolStateUnreachable
)

// String returns a stable, human-readable name for the state. Also used as
// the wire representation in the CEL activation map (see buildActivation in
// cel_evaluator.go) and parsed back via parseServerToolState.
func (s ServerToolState) String() string {
	switch s {
	case ServerToolStateReachable:
		return "reachable"
	case ServerToolStateUnreachable:
		return "configured-but-unreachable"
	default:
		return "unknown"
	}
}

// parseServerToolState parses the String() representation back into a
// ServerToolState. Unrecognized values default to Unknown (fail-open).
func parseServerToolState(s string) ServerToolState {
	switch s {
	case "reachable":
		return ServerToolStateReachable
	case "configured-but-unreachable":
		return ServerToolStateUnreachable
	default:
		return ServerToolStateUnknown
	}
}

// ServerToolInfo holds one MCP server's tool-list availability state and,
// when State == ServerToolStateReachable, the tool names it exposes.
type ServerToolInfo struct {
	// State is the server's current tool-list availability state.
	State ServerToolState
	// Names contains the tool names this server exposes. Only meaningful
	// when State == ServerToolStateReachable.
	Names []string
}

// AllServersToolKey is a synthetic "catch-all" server key in
// ToolsContext.Servers, used by callers that don't have real per-server
// identity for their tool names (currently: message processors — see
// NewProcessorToolsContext and internal/processors/hook.go).
// hasPattern/hasAllPatterns/hasAnyPattern fall back to this entry when a
// pattern's specific owning server isn't found in Servers, so such callers
// get flat, always-authoritative matching (no per-server fail-open grace) —
// preserving pre-mitto-sys.1 processor behavior. General per-server callers
// (e.g. internal/web/session_api.go via NewReachableToolsContext) never set
// this key, so an unrecognized server still fails open for them as intended.
// "" can never be produced by resolveServerName for a non-empty pattern, so
// it can't collide with a real server name.
const AllServersToolKey = ""

// ToolsContext holds MCP tools context for CEL evaluation.
//
// Availability is tracked PER SERVER (docs/devel/mcp-tool-discovery.md,
// Q3.2/Q4.1) instead of one global latch: hasPattern/hasAllPatterns/
// hasAnyPattern (templatefuncs.go) resolve a pattern (e.g. "jira_*") to its
// owning server (the token before the first underscore — see
// resolveServerName) and match name-based (fail-closed) only when that
// server is Reachable; Unknown/Unreachable servers, or servers absent from
// Servers entirely, fail open.
type ToolsContext struct {
	// Servers maps server name (e.g. "jira", "github") to its state and tool
	// names. Nil/empty means no server has been probed at all yet (genuine
	// cold start): every pattern falls through to fail-open, since no server
	// name is ever found. See AllServersToolKey for the processor-only
	// catch-all fallback.
	Servers map[string]ServerToolInfo
	// Names is the flattened list of all known tool names across all
	// Reachable servers (kept for legacy readers/template display, e.g.
	// `"x" in Tools.Names`). Does not drive hasPattern/hasAllPatterns/
	// hasAnyPattern directly anymore — see Servers.
	Names []string
	// Available is a derived, legacy convenience signal: true when Servers is
	// non-empty (some server has been probed / is known). It does NOT drive
	// hasPattern/hasAllPatterns/hasAnyPattern — those resolve per-server
	// state — but is kept for callers/tests that only care whether ANY tool
	// discovery has happened yet (e.g. `Tools.Available` in CEL expressions).
	Available bool
}

// GroupToolNamesByServer groups tool names by their owning server: the token
// before the first underscore (e.g. "jira_create_issue" -> "jira"). A name
// with no underscore is grouped under itself.
func GroupToolNamesByServer(names []string) map[string][]string {
	groups := make(map[string][]string)
	for _, name := range names {
		server := resolveServerName(name)
		groups[server] = append(groups[server], name)
	}
	return groups
}

// NewReachableToolsContext builds a ToolsContext from a flat, already-trusted
// tool-name list (e.g. the MCP tools cache), grouping names by their inferred
// owning server (GroupToolNamesByServer) and marking every such server
// Reachable (authoritative, fail-closed). A server with no names in the list
// is never represented, so an unrelated/not-yet-discovered server's patterns
// still fail open (per-server grace) — see internal/web/session_api.go.
func NewReachableToolsContext(names []string) ToolsContext {
	servers := make(map[string]ServerToolInfo)
	for server, ns := range GroupToolNamesByServer(names) {
		servers[server] = ServerToolInfo{State: ServerToolStateReachable, Names: ns}
	}
	return ToolsContext{Servers: servers, Names: names, Available: len(servers) > 0}
}

// NewProcessorToolsContext builds a ToolsContext for processor (message hook)
// evaluation, where names is the full, authoritative tool list at message-
// processing time (the cache is warmed on connect) but no real per-server
// identity is available. It marks a single AllServersToolKey entry Reachable
// so hasPattern/hasAllPatterns/hasAnyPattern are always fail-closed —
// matching pre-mitto-sys.1 processor behavior (no warm-up grace period),
// regardless of whether names is empty. See internal/processors/hook.go.
func NewProcessorToolsContext(names []string) ToolsContext {
	servers := map[string]ServerToolInfo{
		AllServersToolKey: {State: ServerToolStateReachable, Names: names},
	}
	return ToolsContext{Servers: servers, Names: names, Available: true}
}

// ItemContext holds the generic per-row item context for CEL evaluation of list menus.
// Populated when a menu is opened for a specific row (e.g. a beads issue); empty otherwise.
// String fields are always present (empty string when unset) so expressions like item.status
// always resolve without a missing-key error. Labels is nil/empty when no labels are set.
type ItemContext struct {
	// Id is the unique identifier of the item (e.g. a beads issue ID like "mitto-abc")
	Id string
	// Status is the current status of the item (e.g. "open", "closed", "in_progress")
	Status string
	// Type is the type of the item (e.g. "task", "feature", "bug", "epic")
	Type string
	// Priority is the priority of the item as a string (e.g. "0", "1", "2", "3")
	Priority string
	// Labels are the item's labels (e.g. a beads issue's labels like ["blog"]). Nil when none.
	Labels []string
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
