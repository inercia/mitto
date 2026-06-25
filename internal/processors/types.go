// Package processors provides a unified message processor pipeline for Mitto.
// It supports three modes:
//   - Text-mode: simple prepend/append of static text (no external command).
//   - Command-mode: execute an external command to transform the message.
//   - Prompt-mode: send a prompt to an auxiliary ACP session as fire-and-forget.
//
// Text-mode processors are typically created from config.MessageProcessor entries
// via Manager.AddTextProcessors. Command-mode and prompt-mode processors are loaded
// from YAML files in the MITTO_DIR/processors/ directory.
package processors

import (
	"context"
	"time"

	"github.com/inercia/mitto/internal/config"
)

// InputType defines what data is sent to the processor's stdin.
type InputType string

const (
	// InputMessage sends the message with basic context.
	InputMessage InputType = "message"
	// InputConversation sends the full conversation history.
	InputConversation InputType = "conversation"
	// InputNone sends nothing to stdin.
	InputNone InputType = "none"
)

// OutputType defines how the processor's stdout is used.
type OutputType string

const (
	// OutputTransform replaces the message entirely with stdout.
	OutputTransform OutputType = "transform"
	// OutputPrepend prepends stdout to the message.
	OutputPrepend OutputType = "prepend"
	// OutputAppend appends stdout to the message.
	OutputAppend OutputType = "append"
	// OutputDiscard ignores stdout (side-effect only).
	OutputDiscard OutputType = "discard"

	// OutputNotify parses stdout as a UI notification (agentResponded phase only).
	// Accepts JSON {"title","message","style"} or plain text (first line = title).
	OutputNotify OutputType = "notify"
	// OutputActionButtons parses stdout as follow-up action buttons (agentResponded phase only).
	// Accepts JSON array [{label,prompt},...] or a single object.
	OutputActionButtons OutputType = "actionButtons"
	// OutputUserData parses stdout as user-data key→value patch (agentResponded phase only).
	// Accepts JSON object of string→string entries.
	OutputUserData OutputType = "userData"
)

// WorkingDirType defines the working directory for processor execution.
type WorkingDirType string

const (
	// WorkingDirSession uses the session's working directory.
	WorkingDirSession WorkingDirType = "session"
	// WorkingDirHook uses the processor file's directory.
	WorkingDirHook WorkingDirType = "hook"
)

// ProcessorSource indicates where a processor was loaded from.
type ProcessorSource string

const (
	// ProcessorSourceGlobal is a processor from MITTO_DIR/processors/.
	ProcessorSourceGlobal ProcessorSource = "global"
	// ProcessorSourceBuiltin is a processor from MITTO_DIR/processors/builtin/.
	ProcessorSourceBuiltin ProcessorSource = "builtin"
	// ProcessorSourceWorkspace is a processor from .mitto/processors/ (workspace-local).
	ProcessorSourceWorkspace ProcessorSource = "workspace"
	// ProcessorSourceConfig is a text-mode processor from settings/config.
	ProcessorSourceConfig ProcessorSource = "config"
)

// ErrorHandling defines how errors are handled.
type ErrorHandling string

const (
	// ErrorSkip continues without the processor on error.
	ErrorSkip ErrorHandling = "skip"
	// ErrorFail aborts the message on error.
	ErrorFail ErrorHandling = "fail"
)

// OutputFormat defines how command-mode stdout is interpreted.
type OutputFormat string

const (
	// OutputFormatJSON (default) parses stdout as JSON ProcessorOutput.
	OutputFormatJSON OutputFormat = "json"
	// OutputFormatRaw uses trimmed stdout directly as both Message and Text.
	// Useful when the command outputs plain text (e.g. markdown) rather than JSON.
	// Command-mode only.
	OutputFormatRaw OutputFormat = "raw"
)

// Default values for processor configuration.
const (
	DefaultTimeout      = 5 * time.Second
	DefaultPriority     = 100
	DefaultInput        = InputMessage
	DefaultOutput       = OutputTransform
	DefaultWorkingDir   = WorkingDirSession
	DefaultErrorHandle  = ErrorSkip
	DefaultOutputFormat = OutputFormatJSON
)

// PromptFunc is a callback for executing prompt-mode processors.
// Injected by the web layer to bridge the processor pipeline with auxiliary sessions.
// The function dispatches the prompt to an auxiliary session and returns immediately
// (fire-and-forget). Returns error only if the prompt couldn't be dispatched.
type PromptFunc func(ctx context.Context, workspaceUUID, processorName, prompt string) error

// Phase defines when in the conversation lifecycle a processor fires.
type Phase string

const (
	// PhaseUserPrompt fires processors before the user's message is sent to the ACP server.
	PhaseUserPrompt Phase = "userPrompt"
	// PhaseAgentResponded fires processors after the agent has finished responding.
	// Only command-mode and prompt-mode processors are allowed for this phase.
	PhaseAgentResponded Phase = "agentResponded"
	// PhaseAgentIdle fires processors after the agent has finished responding AND the
	// message queue has been drained (the session goes idle). Within a burst of queued
	// messages it fires once, at the idle breakpoint, so the processor sees the full
	// exchange instead of a partial mid-burst turn. Same execution rules as
	// agentResponded (only command-mode and prompt-mode processors are allowed).
	PhaseAgentIdle Phase = "agentIdle"
)

// Match defines which messages in the sequence a processor applies to.
type Match string

const (
	// MatchFirst applies only to the first-ever message in the conversation.
	MatchFirst Match = "first"
	// MatchAll applies to every message.
	MatchAll Match = "all"
	// MatchAllExceptFirst applies to all messages except the first.
	MatchAllExceptFirst Match = "allExceptFirst"
)

// CadenceConfig throttles how often an agentResponded processor fires.
// All specified thresholds must be met simultaneously (AND logic).
// Only valid for when.on: agentResponded and when.match: all or allExceptFirst.
//
// Example:
//
//	when:
//	  on:    agentResponded
//	  match: all
//	  cadence:
//	    everyNTurns:  5      # fire every 5 agent responses
//	    everyNTokens: 10000  # AND only after 10k cumulative tokens
//	    afterInterval: 30m   # AND only if 30 minutes have passed
type CadenceConfig struct {
	// EveryNTurns fires the processor every N agent responses (1 = every turn).
	// Must be ≥ 1 when specified.
	EveryNTurns int `yaml:"everyNTurns,omitempty" json:"every_n_turns,omitempty"`
	// EveryNTokens fires once N cumulative tokens have been used since the last firing.
	// Must be ≥ 1 when specified.
	EveryNTokens int64 `yaml:"everyNTokens,omitempty" json:"every_n_tokens,omitempty"`
	// AfterInterval is the minimum wall-clock gap between firings (e.g. "5m", "1h").
	// Uses Go duration syntax. Must be > 0 when specified.
	AfterInterval string `yaml:"afterInterval,omitempty" json:"after_interval,omitempty"`
}

// GetAfterIntervalDuration parses the AfterInterval string into a time.Duration.
// Returns 0 if the field is unset or unparseable.
func (c *CadenceConfig) GetAfterIntervalDuration() time.Duration {
	if c == nil || c.AfterInterval == "" {
		return 0
	}
	d, err := time.ParseDuration(c.AfterInterval)
	if err != nil {
		return 0
	}
	return d
}

// WhenConfig specifies when a processor triggers and optional rerun configuration.
// Only the block form is accepted:
//
//	when:
//	  on:    userPrompt | agentResponded   # required
//	  match: first | all | allExceptFirst  # required
//	  rerun:                               # optional; only valid with on:userPrompt + match:first
//	    afterSentMsgs: 15
//	  cadence:                             # optional; only valid with on:agentResponded + match:all or allExceptFirst
//	    everyNTurns: 5
type WhenConfig struct {
	On             Phase          `yaml:"on" json:"on"`
	Match          Match          `yaml:"match" json:"match"`
	Rerun          *RerunConfig   `yaml:"rerun,omitempty" json:"rerun,omitempty"`
	Cadence        *CadenceConfig `yaml:"cadence,omitempty" json:"cadence,omitempty"`
	StopReasons    []string       `yaml:"stopReasons,omitempty" json:"stop_reasons,omitempty"`
	ExcludeOrigins []string       `yaml:"excludeOrigins,omitempty" json:"exclude_origins,omitempty"`
}

// Processor represents a loaded processor definition.
type Processor struct {
	// Name is a human-readable identifier for the processor.
	Name string `yaml:"name" json:"name"`
	// Description provides additional context about what the processor does.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Enabled controls whether the processor is active. Default: true.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`

	// When specifies when the processor triggers and optional rerun configuration.
	When WhenConfig `yaml:"when" json:"when"`
	// Mutate specifies where in the pipeline: "prepend" or "append".
	Mutate config.ProcessorMutate `yaml:"mutate,omitempty" json:"mutate,omitempty"`
	// Priority determines execution order (lower = earlier). Default: 100.
	Priority int `yaml:"priority,omitempty" json:"priority,omitempty"`

	// Command is the executable to run (command-mode only).
	// Can be absolute, relative to processor dir, or found via PATH.
	// If empty and Text is non-empty, the processor runs in text-mode.
	Command string `yaml:"command,omitempty" json:"command,omitempty"`
	// Args are additional arguments passed to the command (command-mode only).
	Args []string `yaml:"args,omitempty" json:"args,omitempty"`

	// Text is the static text to insert (text-mode only, used when Command is empty).
	Text string `yaml:"text,omitempty" json:"text,omitempty"`

	// Prompt is the prompt template to send to an auxiliary ACP session (prompt-mode only).
	// When set, Command and Text must be empty. The processor runs in fire-and-forget mode:
	// the prompt is dispatched to a workspace-scoped auxiliary session and the pipeline
	// continues immediately without waiting for the agent's response.
	// Supports @mitto:variable substitution.
	Prompt string `yaml:"prompt,omitempty" json:"prompt,omitempty"`

	// Input defines what to send to stdin: "message", "conversation", "none".
	Input InputType `yaml:"input,omitempty" json:"input,omitempty"`
	// Output defines how to use stdout: "transform", "prepend", "append", "discard".
	Output OutputType `yaml:"output,omitempty" json:"output,omitempty"`
	// OutputFormat defines how command-mode stdout is interpreted:
	//   - "json" (default): stdout is parsed as JSON ProcessorOutput.
	//   - "raw": trimmed stdout is used directly as both Message and Text,
	//     enabling plain-text (e.g. markdown) output without a JSON wrapper.
	// Command-mode only; ignored for text-mode and prompt-mode processors.
	OutputFormat OutputFormat `yaml:"outputFormat,omitempty" json:"output_format,omitempty"`

	// Timeout is the maximum execution time. Default: 5s.
	Timeout Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	// WorkingDir specifies the working directory: "session" or "hook".
	WorkingDir WorkingDirType `yaml:"working_dir,omitempty" json:"working_dir,omitempty"`
	// Environment contains additional environment variables for the command.
	Environment map[string]string `yaml:"environment,omitempty" json:"environment,omitempty"`

	// OnError defines error handling: "skip" or "fail". Default: "skip".
	OnError ErrorHandling `yaml:"on_error,omitempty" json:"on_error,omitempty"`

	// EnabledWhen is an optional CEL expression that determines whether this processor applies.
	// Uses the same CEL context as prompt enabledWhen expressions (ACP.*, Session.*, Parent.*,
	// Children.*, Workspace.*, Tools.*). If empty, the processor always applies (subject to
	// other filters). If the expression evaluates to false, the processor is skipped.
	// Example: 'ACP.Tags.exists(t, t == "reasoning")' — only apply for reasoning models.
	EnabledWhen string `yaml:"enabledWhen,omitempty" json:"enabled_when,omitempty"`

	// FilePath is the path to the processor's YAML file (set internally).
	FilePath string `yaml:"-" json:"-"`
	// HookDir is the directory containing the processor file (set internally).
	HookDir string `yaml:"-" json:"-"`
	// Source indicates where this processor was loaded from (set internally).
	Source ProcessorSource `yaml:"-" json:"source,omitempty"`
}

// RerunConfig configures automatic re-run for "when.match: first" processors.
type RerunConfig struct {
	// AfterTime is the duration after which the processor should re-run.
	// Supports Go duration strings: "10m", "1h", "30s", "2h30m".
	AfterTime string `yaml:"afterTime,omitempty" json:"after_time,omitempty"`
	// AfterSentMsgs is the number of user messages sent since the last run
	// after which the processor should re-run.
	AfterSentMsgs int `yaml:"afterSentMsgs,omitempty" json:"after_sent_msgs,omitempty"`
	// AfterTokens is the number of tokens consumed since the last run
	// after which the processor should re-run. Uses actual token usage from
	// the ACP server when available, falling back to character-based estimation.
	AfterTokens int `yaml:"afterTokens,omitempty" json:"after_tokens,omitempty"`

	// parsedDuration is the parsed duration from AfterTime (set during validation).
	parsedDuration time.Duration
}

// GetAfterDuration returns the parsed duration for AfterTime.
// Returns 0 if AfterTime is empty or not yet parsed.
func (r *RerunConfig) GetAfterDuration() time.Duration {
	if r == nil {
		return 0
	}
	return r.parsedDuration
}

// Validate parses and validates the RerunConfig fields.
func (r *RerunConfig) Validate() error {
	if r == nil {
		return nil
	}
	if r.AfterTime != "" {
		d, err := time.ParseDuration(r.AfterTime)
		if err != nil {
			return err
		}
		r.parsedDuration = d
	}
	return nil
}

// IsTextMode returns true if this processor operates in text-mode.
// Text-mode processors have no Command but have a non-empty Text field.
// They prepend or append the static Text string to the message without
// executing any external command.
func (h *Processor) IsTextMode() bool {
	return h.Command == "" && h.Text != ""
}

// IsPromptMode returns true if this processor operates in prompt-mode.
// Prompt-mode processors send a prompt to an auxiliary ACP session as fire-and-forget.
// They have a non-empty Prompt field and empty Command and Text fields.
func (h *Processor) IsPromptMode() bool {
	return h.Command == "" && h.Text == "" && h.Prompt != ""
}

// GetRerun returns the processor's rerun configuration (from When.Rerun).
func (p *Processor) GetRerun() *RerunConfig {
	return p.When.Rerun
}

// Duration is a wrapper for time.Duration that supports YAML unmarshaling.
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler for Duration.
func (d *Duration) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	if s == "" {
		*d = Duration(DefaultTimeout)
		return nil
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

// Duration returns the time.Duration value.
func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

// AfterProcessorInput captures the agent's completed turn for agentResponded processors.
// Fields are serialized to JSON (camelCase) for the processor's stdin payload.
// SessionDir and WorkspaceUUID are excluded from JSON serialization — they are used internally.
type AfterProcessorInput struct {
	// SessionID is the current session identifier.
	SessionID string `json:"sessionId"`
	// SessionDir is the on-disk directory for this session (used for processor state persistence).
	// This field is NOT serialized to JSON — it is for internal use only.
	SessionDir string `json:"-"`
	// WorkspaceUUID is the workspace identifier used to route prompt-mode processor dispatches.
	// This field is NOT serialized to JSON — it is for internal use only.
	WorkspaceUUID string `json:"-"`
	// WorkingDir is the session's working directory (used for WorkingDirSession processors).
	WorkingDir string `json:"workingDir,omitempty"`
	// Origin is the source of the prompt: "user", "queue", or "periodic-runner".
	Origin string `json:"origin"`
	// StopReason is the ACP stop reason string (e.g. "end_turn", "max_tokens").
	// These match the ACP SDK StopReason constants (snake_case).
	StopReason string `json:"stopReason"`
	// UserPrompt is the text of the user prompt that triggered this turn.
	UserPrompt string `json:"userPrompt"`
	// AgentMessages contains the concatenated text chunks from the agent's response.
	AgentMessages []string `json:"agentMessages"`
	// ToolCalls contains lightweight snapshots of tool calls made during the turn.
	ToolCalls []AfterToolCallSnapshot `json:"toolCalls"`
	// TokenUsage reports token consumption for the turn (may be nil if not reported).
	TokenUsage *AfterTokenUsage `json:"tokenUsage,omitempty"`
	// StartedAt is when the prompt was sent to the agent.
	StartedAt time.Time `json:"startedAt"`
	// EndedAt is when the agent's response was fully received.
	EndedAt time.Time `json:"endedAt"`
	// SessionIdle is true when no further queued message was dispatched after this turn,
	// i.e. the agent has drained its queue and gone idle. Used to gate agentIdle processors.
	// This field is NOT serialized to JSON — it is for internal gating only.
	SessionIdle bool `json:"-"`
}

// AfterToolCallSnapshot is a lightweight snapshot of one tool call from an agent turn.
type AfterToolCallSnapshot struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Title  string `json:"title"`
}

// AfterTokenUsage captures token consumption for the agent's turn.
type AfterTokenUsage struct {
	Input  int64 `json:"input"`
	Output int64 `json:"output"`
	Total  int64 `json:"total"`
}

// ApplyAfterResult contains the aggregated side-effects from all agentResponded processors.
// BackgroundSession (Task 4) consumes this to trigger UI notifications, enqueue action
// buttons, and patch workspace user_data.
type ApplyAfterResult struct {
	// Notifications collects entries from processors with output: notify.
	Notifications []AfterNotification `json:"notifications,omitempty"`
	// ActionButtons collects entries from processors with output: actionButtons.
	ActionButtons []AfterActionButton `json:"actionButtons,omitempty"`
	// UserDataPatch is the merged key→value patch from processors with output: userData.
	// Later processors override earlier ones on key collision.
	UserDataPatch map[string]string `json:"userDataPatch,omitempty"`
	// Errors holds non-fatal errors. A failing processor does not block later processors.
	Errors []ProcessorError `json:"errors,omitempty"`
}

// AfterNotification is a UI notification produced by an agentResponded processor.
type AfterNotification struct {
	Title   string `json:"title"`
	Message string `json:"message"`
	// Style is one of "info", "success", "warning", "error". Defaults to "info".
	Style string `json:"style,omitempty"`
}

// AfterActionButton is a suggested follow-up action produced by an agentResponded processor.
type AfterActionButton struct {
	Label  string `json:"label"`
	Prompt string `json:"prompt"`
}

// ProcessorError records a non-fatal error from a single processor in the after-phase pipeline.
type ProcessorError struct {
	ProcessorName string `json:"processorName"`
	Error         string `json:"error"`
}
