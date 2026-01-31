// Package hooks provides external command-based hooks for message transformation.
// Hooks are loaded from YAML files in the MITTO_DIR/hooks/ directory and can
// execute arbitrary commands to dynamically transform messages before sending
// to the ACP server.
package msghooks

import (
	"time"

	"github.com/inercia/mitto/internal/config"
)

// InputType defines what data is sent to the hook's stdin.
type InputType string

const (
	// InputMessage sends the message with basic context.
	InputMessage InputType = "message"
	// InputConversation sends the full conversation history.
	InputConversation InputType = "conversation"
	// InputNone sends nothing to stdin.
	InputNone InputType = "none"
)

// OutputType defines how the hook's stdout is used.
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

// WorkingDirType defines the working directory for hook execution.
type WorkingDirType string

const (
	// WorkingDirSession uses the session's working directory.
	WorkingDirSession WorkingDirType = "session"
	// WorkingDirHook uses the hook file's directory.
	WorkingDirHook WorkingDirType = "hook"
)

// ErrorHandling defines how errors are handled.
type ErrorHandling string

const (
	// ErrorSkip continues without the hook on error.
	ErrorSkip ErrorHandling = "skip"
	// ErrorFail aborts the message on error.
	ErrorFail ErrorHandling = "fail"
)

// Default values for hook configuration.
const (
	DefaultTimeout     = 5 * time.Second
	DefaultPriority    = 100
	DefaultInput       = InputMessage
	DefaultOutput      = OutputTransform
	DefaultWorkingDir  = WorkingDirSession
	DefaultErrorHandle = ErrorSkip
)

// Hook represents a loaded hook definition.
type Hook struct {
	// Name is a human-readable identifier for the hook.
	Name string `yaml:"name" json:"name"`
	// Description provides additional context about what the hook does.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Enabled controls whether the hook is active. Default: true.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`

	// When specifies when the hook triggers: "first", "all", "all-except-first".
	When config.ProcessorWhen `yaml:"when" json:"when"`
	// Position specifies where in the pipeline: "prepend" or "append".
	Position config.ProcessorPosition `yaml:"position,omitempty" json:"position,omitempty"`
	// Priority determines execution order (lower = earlier). Default: 100.
	Priority int `yaml:"priority,omitempty" json:"priority,omitempty"`

	// Command is the executable to run. Can be absolute, relative to hook dir, or in PATH.
	Command string `yaml:"command" json:"command"`
	// Args are additional arguments passed to the command.
	Args []string `yaml:"args,omitempty" json:"args,omitempty"`

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

	// Workspaces limits the hook to specific workspace paths. Empty means all.
	Workspaces []string `yaml:"workspaces,omitempty" json:"workspaces,omitempty"`

	// FilePath is the path to the hook's YAML file (set internally).
	FilePath string `yaml:"-" json:"-"`
	// HookDir is the directory containing the hook file (set internally).
	HookDir string `yaml:"-" json:"-"`
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
