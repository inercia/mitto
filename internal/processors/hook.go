package processors

import (
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// IsEnabled returns true if the processor is enabled.
func (h *Processor) IsEnabled() bool {
	if h.Enabled == nil {
		return true // Default to enabled
	}
	return *h.Enabled
}

// GetTimeout returns the processor's timeout, using the default if not set.
// Prompt-mode processors use a higher default of 120s to allow time for dispatch.
func (h *Processor) GetTimeout() Duration {
	if h.Timeout == 0 {
		if h.IsPromptMode() {
			return Duration(120 * time.Second)
		}
		return Duration(DefaultTimeout)
	}
	return h.Timeout
}

// GetPriority returns the processor's priority, using the default if not set.
// For text-mode processors (Command == "" && Text != ""), an unset priority (0)
// returns 0 so that they sort before command-mode processors (default priority 100).
// For command-mode processors, an unset priority (0) returns DefaultPriority (100).
func (h *Processor) GetPriority() int {
	if h.Priority != 0 {
		return h.Priority
	}
	if h.IsTextMode() {
		return 0
	}
	return DefaultPriority
}

// GetInput returns the processor's input type, using the default if not set.
func (h *Processor) GetInput() InputType {
	if h.Input == "" {
		return DefaultInput
	}
	return h.Input
}

// GetOutput returns the processor's output type, using the default if not set.
func (h *Processor) GetOutput() OutputType {
	if h.Output == "" {
		return DefaultOutput
	}
	return h.Output
}

// GetWorkingDir returns the processor's working directory type, using the default if not set.
func (h *Processor) GetWorkingDir() WorkingDirType {
	if h.WorkingDir == "" {
		return DefaultWorkingDir
	}
	return h.WorkingDir
}

// GetOnError returns the processor's error handling, using the default if not set.
func (h *Processor) GetOnError() ErrorHandling {
	if h.OnError == "" {
		return DefaultErrorHandle
	}
	return h.OnError
}

// GetPosition returns the processor's position, using prepend as default.
func (h *Processor) GetPosition() config.ProcessorPosition {
	if h.Position == "" {
		return config.ProcessorPositionPrepend
	}
	return h.Position
}

// SkipReason describes why a processor was not applied.
type SkipReason string

const (
	SkipReasonNone            SkipReason = ""
	SkipReasonDisabled        SkipReason = "disabled"
	SkipReasonEnabledWhen     SkipReason = "enabledWhen_false"
	SkipReasonWhenFirst       SkipReason = "when=first_not_first_message"
	SkipReasonWhenExceptFirst SkipReason = "when=all-except-first_is_first_message"
	SkipReasonWhenUnknown     SkipReason = "unknown_when_condition"
	SkipReasonRerunNotDue     SkipReason = "rerun_not_due"
)

// ShouldApply returns true if the processor should apply given the message context.
// When it returns false, the SkipReason describes why.
func (h *Processor) ShouldApply(isFirstMessage bool, input *ProcessorInput) (bool, SkipReason) {
	if !h.IsEnabled() {
		return false, SkipReasonDisabled
	}

	// Check CEL expression (enabledWhen)
	if h.EnabledWhen != "" && input != nil {
		evaluator := config.GetCELEvaluator()
		if evaluator != nil {
			ctx := BuildCELContext(input)
			compiled, err := evaluator.Compile(h.EnabledWhen)
			if err != nil {
				// Invalid expression — fail-open (same as prompts)
				slog.Warn("Invalid enabledWhen expression in processor",
					"processor", h.Name,
					"expression", h.EnabledWhen,
					"error", err)
			} else {
				result, err := evaluator.Evaluate(compiled, ctx)
				if err != nil {
					slog.Warn("Failed to evaluate enabledWhen expression in processor",
						"processor", h.Name,
						"expression", h.EnabledWhen,
						"error", err)
				} else if !result {
					return false, SkipReasonEnabledWhen
				}
			}
		}
	}

	// Check when condition
	switch h.When {
	case config.ProcessorWhenFirst:
		if !isFirstMessage {
			return false, SkipReasonWhenFirst
		}
		return true, SkipReasonNone
	case config.ProcessorWhenAll:
		return true, SkipReasonNone
	case config.ProcessorWhenAllExceptFirst:
		if isFirstMessage {
			return false, SkipReasonWhenExceptFirst
		}
		return true, SkipReasonNone
	default:
		return false, SkipReasonWhenUnknown
	}
}

// BuildCELContext converts a ProcessorInput into a PromptEnabledContext
// suitable for CEL expression evaluation.
func BuildCELContext(input *ProcessorInput) *config.PromptEnabledContext {
	ctx := &config.PromptEnabledContext{}

	// Session context
	ctx.Session.ID = input.SessionID
	ctx.Session.Name = input.SessionName
	ctx.Session.IsChild = input.ParentSessionID != ""
	ctx.Session.ParentID = input.ParentSessionID
	ctx.Session.IsPeriodic = input.IsPeriodic

	// ACP context — get tags from the current server in AvailableACPServers
	ctx.ACP.Name = input.ACPServer
	for _, srv := range input.AvailableACPServers {
		if srv.Current {
			ctx.ACP.Type = srv.Type
			ctx.ACP.Tags = srv.Tags
			break
		}
	}
	if ctx.ACP.Type == "" {
		ctx.ACP.Type = input.ACPServer
	}

	// Workspace context
	ctx.Workspace.UUID = input.WorkspaceUUID
	ctx.Workspace.Folder = input.WorkingDir
	ctx.Workspace.HasUserDataSchema = input.HasUserDataSchema
	ctx.Workspace.HasMittoRC = input.HasMittoRC
	ctx.Workspace.HasMetadataDescription = input.HasMetadataDescription

	// Parent context
	if input.ParentSessionID != "" {
		ctx.Parent.Exists = true
		ctx.Parent.Name = input.ParentSessionName
		// ParentACPServer is not in ProcessorInput — leave empty
	}

	// Children context
	ctx.Children.Count = len(input.ChildSessions)
	ctx.Children.Exists = len(input.ChildSessions) > 0
	for _, child := range input.ChildSessions {
		ctx.Children.Names = append(ctx.Children.Names, child.Name)
		ctx.Children.ACPServers = append(ctx.Children.ACPServers, child.ACPServer)
		if child.ChildOrigin == "mcp" {
			ctx.Children.MCPCount++
		}
	}

	// Tools context
	if len(input.MCPToolNames) > 0 {
		ctx.Tools.Available = true
		ctx.Tools.Names = input.MCPToolNames
	}

	// Permissions context - resolve flags with defaults
	ctx.Permissions.CanDoIntrospection = session.GetFlagValue(input.AdvancedSettings, session.FlagCanDoIntrospection)
	ctx.Permissions.CanSendPrompt = session.GetFlagValue(input.AdvancedSettings, session.FlagCanSendPrompt)
	ctx.Permissions.CanPromptUser = session.GetFlagValue(input.AdvancedSettings, session.FlagCanPromptUser)
	ctx.Permissions.CanStartConversation = session.GetFlagValue(input.AdvancedSettings, session.FlagCanStartConversation)
	ctx.Permissions.CanInteractOtherWorkspaces = session.GetFlagValue(input.AdvancedSettings, session.FlagCanInteractOtherWorkspaces)
	ctx.Permissions.AutoApprovePermissions = session.GetFlagValue(input.AdvancedSettings, session.FlagAutoApprovePermissions)

	return ctx
}

// ResolveCommand resolves the command path.
// If the command starts with "./" or "../", it's resolved relative to the processor's directory.
// Otherwise, it's returned as-is (absolute path or PATH lookup).
func (h *Processor) ResolveCommand() string {
	if strings.HasPrefix(h.Command, "./") || strings.HasPrefix(h.Command, "../") {
		return filepath.Join(h.HookDir, h.Command)
	}
	return h.Command
}
