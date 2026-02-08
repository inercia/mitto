package runner

import (
	grrunner "github.com/inercia/go-restricted-runner/pkg/runner"
	"github.com/inercia/mitto/internal/config"
)

// resolveConfig merges configuration from all levels.
//
// Resolution order (highest priority last):
//  1. Global per-runner-type config (globalRunnersByType)
//  2. Agent per-runner-type config (agentRunnersByType)
//  3. Workspace overrides for the resolved runner type (workspaceByType)
//
// Returns a ResolvedConfig with type "exec" and nil restrictions if all inputs are nil.
//
// All parameters are maps of runner type -> config.
// The config for the resolved runner type is applied at each level.
func resolveConfig(
	globalRunnersByType map[string]*config.WorkspaceRunnerConfig,
	agentRunnersByType map[string]*config.WorkspaceRunnerConfig,
	workspaceByType map[string]*config.WorkspaceRunnerConfig,
) *ResolvedConfig {
	// Start with defaults: "exec" runner with no restrictions
	runnerType := "exec"
	var restrictions *config.RunnerRestrictions

	// Apply global per-runner-type config (if configured)
	if globalRunnersByType != nil {
		if globalCfg, ok := globalRunnersByType[runnerType]; ok && globalCfg != nil {
			if globalCfg.Type != "" {
				runnerType = globalCfg.Type
			}

			strategy := globalCfg.MergeStrategy
			if strategy == "" {
				strategy = "extend"
			}

			restrictions = MergeRestrictions(restrictions, globalCfg.Restrictions, strategy)
		}
	}

	// Apply agent per-runner-type config (if configured)
	if agentRunnersByType != nil {
		if agentCfg, ok := agentRunnersByType[runnerType]; ok && agentCfg != nil {
			if agentCfg.Type != "" {
				runnerType = agentCfg.Type
			}

			strategy := agentCfg.MergeStrategy
			if strategy == "" {
				strategy = "extend"
			}

			restrictions = MergeRestrictions(restrictions, agentCfg.Restrictions, strategy)
		}
	}

	// Apply workspace overrides for the resolved runner type (if configured)
	if workspaceByType != nil {
		if workspace, ok := workspaceByType[runnerType]; ok && workspace != nil {
			if workspace.Type != "" {
				runnerType = workspace.Type
			}

			strategy := workspace.MergeStrategy
			if strategy == "" {
				strategy = "extend"
			}

			restrictions = MergeRestrictions(restrictions, workspace.Restrictions, strategy)
		}
	}

	return &ResolvedConfig{
		Type:         runnerType,
		Restrictions: restrictions,
	}
}

// MergeRestrictions merges restrictions with the specified strategy.
//
// Strategy "replace": override completely replaces base (base is ignored).
// Strategy "extend" (default): override is merged with base, with override taking precedence.
func MergeRestrictions(base, override *config.RunnerRestrictions, strategy string) *config.RunnerRestrictions {
	if override == nil {
		return base
	}

	if strategy == "replace" {
		return override
	}

	// Default: "extend" strategy
	merged := &config.RunnerRestrictions{}

	if base != nil {
		// Copy base
		*merged = *base
	}

	// Override specific fields
	if override.AllowNetworking != nil {
		merged.AllowNetworking = override.AllowNetworking
	}

	// Merge folder lists (append unique entries)
	merged.AllowReadFolders = mergeFolderLists(
		getBaseFolders(base, func(r *config.RunnerRestrictions) []string { return r.AllowReadFolders }),
		override.AllowReadFolders,
	)
	merged.AllowWriteFolders = mergeFolderLists(
		getBaseFolders(base, func(r *config.RunnerRestrictions) []string { return r.AllowWriteFolders }),
		override.AllowWriteFolders,
	)
	merged.DenyFolders = mergeFolderLists(
		getBaseFolders(base, func(r *config.RunnerRestrictions) []string { return r.DenyFolders }),
		override.DenyFolders,
	)

	// Docker config: override completely if specified
	if override.Docker != nil {
		merged.Docker = override.Docker
	}

	return merged
}

// getBaseFolders safely extracts folder list from base restrictions.
func getBaseFolders(base *config.RunnerRestrictions, getter func(*config.RunnerRestrictions) []string) []string {
	if base == nil {
		return nil
	}
	return getter(base)
}

// mergeFolderLists merges two folder lists, removing duplicates.
func mergeFolderLists(base, override []string) []string {
	if len(override) == 0 {
		return base
	}

	seen := make(map[string]bool)
	result := make([]string, 0, len(base)+len(override))

	for _, path := range base {
		if !seen[path] {
			result = append(result, path)
			seen[path] = true
		}
	}

	for _, path := range override {
		if !seen[path] {
			result = append(result, path)
			seen[path] = true
		}
	}

	return result
}

// toRunnerOptions converts restrictions to go-restricted-runner options.
func toRunnerOptions(restrictions *config.RunnerRestrictions) grrunner.Options {
	options := grrunner.Options{}

	if restrictions == nil {
		return options
	}

	if restrictions.AllowNetworking != nil {
		options["allow_networking"] = *restrictions.AllowNetworking
	}

	if len(restrictions.AllowReadFolders) > 0 {
		options["allow_read_folders"] = restrictions.AllowReadFolders
	}

	if len(restrictions.AllowWriteFolders) > 0 {
		options["allow_write_folders"] = restrictions.AllowWriteFolders
	}

	if restrictions.Docker != nil {
		if restrictions.Docker.Image != "" {
			options["image"] = restrictions.Docker.Image
		}
		if restrictions.Docker.MemoryLimit != "" {
			options["memory_limit"] = restrictions.Docker.MemoryLimit
		}
		if restrictions.Docker.CPULimit != "" {
			options["cpu_limit"] = restrictions.Docker.CPULimit
		}
	}

	return options
}

// toRunnerType converts string to runner.Type.
func toRunnerType(typeStr string) grrunner.Type {
	switch typeStr {
	case "sandbox-exec":
		return grrunner.TypeSandboxExec
	case "firejail":
		return grrunner.TypeFirejail
	case "docker":
		return grrunner.TypeDocker
	default:
		return grrunner.TypeExec
	}
}
