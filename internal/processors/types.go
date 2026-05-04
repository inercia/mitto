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

// Default values for processor configuration.
const (
	DefaultTimeout     = 5 * time.Second
	DefaultPriority    = 100
	DefaultInput       = InputMessage
	DefaultOutput      = OutputTransform
	DefaultWorkingDir  = WorkingDirSession
	DefaultErrorHandle = ErrorSkip
)

// PromptFunc is a callback for executing prompt-mode processors.
// Injected by the web layer to bridge the processor pipeline with auxiliary sessions.
// The function dispatches the prompt to an auxiliary session and returns immediately
// (fire-and-forget). Returns error only if the prompt couldn't be dispatched.
type PromptFunc func(ctx context.Context, workspaceUUID, processorName, prompt string) error

// Processor represents a loaded processor definition.
type Processor struct {
	// Name is a human-readable identifier for the processor.
	Name string `yaml:"name" json:"name"`
	// Description provides additional context about what the processor does.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Enabled controls whether the processor is active. Default: true.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`

	// When specifies when the processor triggers: "first", "all", "all-except-first".
	When config.ProcessorWhen `yaml:"when" json:"when"`
	// Position specifies where in the pipeline: "prepend" or "append".
	Position config.ProcessorPosition `yaml:"position,omitempty" json:"position,omitempty"`
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

	// Timeout is the maximum execution time. Default: 5s.
	Timeout Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	// WorkingDir specifies the working directory: "session" or "hook".
	WorkingDir WorkingDirType `yaml:"working_dir,omitempty" json:"working_dir,omitempty"`
	// Environment contains additional environment variables for the command.
	Environment map[string]string `yaml:"environment,omitempty" json:"environment,omitempty"`

	// OnError defines error handling: "skip" or "fail". Default: "skip".
	OnError ErrorHandling `yaml:"on_error,omitempty" json:"on_error,omitempty"`

	// Rerun configures automatic re-run for "when: first" processors.
	// Allows a first-only processor to fire again after a time interval or message count,
	// refreshing context for the LLM. Only used with when: first.
	// If both AfterTime and AfterSentMsgs are set, whichever threshold is reached first
	// triggers the re-run.
	Rerun *RerunConfig `yaml:"rerun,omitempty" json:"rerun,omitempty"`

	// EnabledWhen is an optional CEL expression that determines whether this processor applies.
	// Uses the same CEL context as prompt enabledWhen expressions (acp.*, session.*, parent.*,
	// children.*, workspace.*, tools.*). If empty, the processor always applies (subject to
	// other filters). If the expression evaluates to false, the processor is skipped.
	// Example: 'acp.tags.exists(t, t == "reasoning")' — only apply for reasoning models.
	EnabledWhen string `yaml:"enabledWhen,omitempty" json:"enabled_when,omitempty"`

	// EnabledWhenMCP is an optional comma-separated list of tool name patterns required for
	// this processor. Patterns support * as wildcard (e.g., "jira_*,mitto_conversation_*").
	// If specified, the processor only applies when all required tool patterns are satisfied
	// (at least one matching tool exists for each pattern).
	// If empty, the processor always applies (no tool requirements).
	EnabledWhenMCP string `yaml:"enabledWhenMCP,omitempty" json:"enabled_when_mcp,omitempty"`

	// FilePath is the path to the processor's YAML file (set internally).
	FilePath string `yaml:"-" json:"-"`
	// HookDir is the directory containing the processor file (set internally).
	HookDir string `yaml:"-" json:"-"`
	// Source indicates where this processor was loaded from (set internally).
	Source ProcessorSource `yaml:"-" json:"source,omitempty"`
}

// RerunConfig configures automatic re-run for "when: first" processors.
type RerunConfig struct {
	// AfterTime is the duration after which the processor should re-run.
	// Supports Go duration strings: "10m", "1h", "30s", "2h30m".
	AfterTime string `yaml:"afterTime,omitempty" json:"after_time,omitempty"`
	// AfterSentMsgs is the number of user messages sent since the last run
	// after which the processor should re-run.
	AfterSentMsgs int `yaml:"afterSentMsgs,omitempty" json:"after_sent_msgs,omitempty"`

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
