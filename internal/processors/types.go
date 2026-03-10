// Package processors provides a unified message processor pipeline for Mitto.
// It supports two modes:
//   - Text-mode: simple prepend/append of static text (no external command).
//   - Command-mode: execute an external command to transform the message.
//
// Text-mode processors are typically created from config.MessageProcessor entries
// via Manager.AddTextProcessors. Command-mode processors are loaded from YAML files
// in the MITTO_DIR/processors/ directory.
package processors

import (
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

	// Workspaces limits the processor to specific workspace paths. Empty means all.
	Workspaces []string `yaml:"workspaces,omitempty" json:"workspaces,omitempty"`

	// FilePath is the path to the processor's YAML file (set internally).
	FilePath string `yaml:"-" json:"-"`
	// HookDir is the directory containing the processor file (set internally).
	HookDir string `yaml:"-" json:"-"`
}

// IsTextMode returns true if this processor operates in text-mode.
// Text-mode processors have no Command but have a non-empty Text field.
// They prepend or append the static Text string to the message without
// executing any external command.
func (h *Processor) IsTextMode() bool {
	return h.Command == "" && h.Text != ""
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

