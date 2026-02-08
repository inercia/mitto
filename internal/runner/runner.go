// Package runner provides restricted execution for ACP agents.
//
// By default, agents run with no restrictions (exec runner).
// Users can opt-in to sandboxing by configuring restricted_runner settings.
//
// See docs/config/restricted.md for user documentation.
package runner

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/inercia/go-restricted-runner/pkg/common"
	grrunner "github.com/inercia/go-restricted-runner/pkg/runner"
	"github.com/inercia/mitto/internal/config"
)

// Runner wraps go-restricted-runner for ACP agent execution.
type Runner struct {
	runner grrunner.Runner
	config *ResolvedConfig
	logger *slog.Logger
	// FallbackInfo contains information about runner fallback (if it occurred)
	FallbackInfo *FallbackInfo
}

// FallbackInfo contains information about a runner fallback.
type FallbackInfo struct {
	// RequestedType is the runner type that was requested
	RequestedType string
	// FallbackType is the runner type that was used instead (usually "exec")
	FallbackType string
	// Reason is the error message explaining why fallback occurred
	Reason string
}

// ResolvedConfig contains the fully resolved runner configuration.
type ResolvedConfig struct {
	Type         string
	Restrictions *config.RunnerRestrictions
}

// NewRunner creates a new restricted runner.
//
// Configuration is resolved in this order (highest priority last):
//  1. Global per-runner-type config (globalRunnersByType)
//  2. Agent per-runner-type config (agentRunnersByType)
//  3. Workspace overrides for the resolved runner type (workspaceConfigByType)
//
// If all configs are nil, returns a runner with "exec" type (no restrictions).
//
// All parameters are maps of runner type -> config.
// The config for the resolved runner type is applied at each level.
func NewRunner(
	globalRunnersByType map[string]*config.WorkspaceRunnerConfig,
	agentRunnersByType map[string]*config.WorkspaceRunnerConfig,
	workspaceConfigByType map[string]*config.WorkspaceRunnerConfig,
	workspace string,
	logger *slog.Logger,
) (*Runner, error) {
	// Resolve configuration hierarchy
	resolved := resolveConfig(
		globalRunnersByType,
		agentRunnersByType,
		workspaceConfigByType,
	)

	// Create variable resolver
	varResolver, err := NewVariableResolver(workspace)
	if err != nil {
		return nil, fmt.Errorf("failed to create variable resolver: %w", err)
	}

	// Resolve variables in restrictions
	resolvedRestrictions := resolveVariables(resolved.Restrictions, varResolver)
	resolved.Restrictions = resolvedRestrictions

	// Convert to go-restricted-runner options
	options := toRunnerOptions(resolvedRestrictions)

	// Create logger for go-restricted-runner
	runnerLogger, err := common.NewLogger("", "", common.LogLevelInfo, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create runner logger: %w", err)
	}

	// Create the underlying runner
	runnerType := toRunnerType(resolved.Type)
	r, err := grrunner.New(runnerType, options, runnerLogger)

	// Check if runner is available on this platform
	var fallbackInfo *FallbackInfo

	// Handle creation errors (e.g., unsupported platform)
	if err != nil {
		requestedType := resolved.Type
		if logger != nil {
			logger.Warn("restricted runner creation failed, falling back to exec",
				"requested_type", requestedType,
				"error", err.Error())
		}

		// Store fallback information
		fallbackInfo = &FallbackInfo{
			RequestedType: requestedType,
			FallbackType:  "exec",
			Reason:        err.Error(),
		}

		// Fall back to exec runner
		r, err = grrunner.New(grrunner.TypeExec, grrunner.Options{}, runnerLogger)
		if err != nil {
			return nil, fmt.Errorf("failed to create fallback exec runner: %w", err)
		}
		resolved.Type = "exec"
	} else {
		// Runner created successfully, check implicit requirements
		if err := r.CheckImplicitRequirements(); err != nil {
			requestedType := resolved.Type
			if logger != nil {
				logger.Warn("restricted runner not available, falling back to exec",
					"requested_type", requestedType,
					"error", err.Error())
			}

			// Store fallback information
			fallbackInfo = &FallbackInfo{
				RequestedType: requestedType,
				FallbackType:  "exec",
				Reason:        err.Error(),
			}

			// Fall back to exec runner
			r, err = grrunner.New(grrunner.TypeExec, grrunner.Options{}, runnerLogger)
			if err != nil {
				return nil, fmt.Errorf("failed to create fallback exec runner: %w", err)
			}
			resolved.Type = "exec"
		}
	}

	if logger != nil {
		logger.Info("created restricted runner",
			"type", resolved.Type,
			"workspace", workspace,
			"fallback", fallbackInfo != nil)
	}

	return &Runner{
		runner:       r,
		config:       resolved,
		logger:       logger,
		FallbackInfo: fallbackInfo,
	}, nil
}

// RunWithPipes starts a command through the restricted runner with access to pipes.
//
// This method uses go-restricted-runner's RunWithPipes() for all runner types,
// which enables interactive communication with the process (required for ACP).
//
// Returns:
//   - stdin: WriteCloser for sending input to the process
//   - stdout: ReadCloser for reading process output
//   - stderr: ReadCloser for reading process errors
//   - wait: Function to wait for process completion and cleanup (must be called)
//   - err: Any error during process startup
//
// The caller must:
//   - Close stdin when done writing
//   - Call wait() to clean up resources
//   - Handle context cancellation (kills the process)
func (r *Runner) RunWithPipes(
	ctx context.Context,
	command string,
	args []string,
	env []string,
) (stdin WriteCloser, stdout ReadCloser, stderr ReadCloser, wait func() error, err error) {
	// Use go-restricted-runner's RunWithPipes method
	// This works for all runner types (exec, sandbox-exec, firejail, docker)
	return r.runner.RunWithPipes(ctx, command, args, env, nil)
}

// WriteCloser is an alias for io.WriteCloser for documentation clarity.
type WriteCloser = interface {
	Write(p []byte) (n int, err error)
	Close() error
}

// ReadCloser is an alias for io.ReadCloser for documentation clarity.
type ReadCloser = interface {
	Read(p []byte) (n int, err error)
	Close() error
}

// Type returns the runner type being used.
func (r *Runner) Type() string {
	return r.config.Type
}

// IsRestricted returns true if this runner applies restrictions (not exec).
func (r *Runner) IsRestricted() bool {
	return r.config.Type != "exec"
}
