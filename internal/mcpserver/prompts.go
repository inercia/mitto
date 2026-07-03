package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
)

// loadMergedPrompts loads and merges prompts from all sources for the given working directory.
// Returns the fully merged prompt list including disabled prompts.
// Priority order (lowest to highest):
//  1. Global file prompts (MITTO_DIR/prompts/*.prompt.yaml)
//  2. Settings prompts (config.Prompts)
//  3. ACP server-specific prompts
//  4. Workspace directory prompts (.mitto/prompts/*.prompt.yaml)
//  5. Workspace inline prompts (.mittorc)
func (s *Server) loadMergedPrompts(workingDir string) []config.WebPrompt {
	s.mu.RLock()
	cfg := s.config
	promptsCache := s.promptsCache
	sm := s.sessionManager
	s.mu.RUnlock()

	// 1. Global file prompts (MITTO_DIR/prompts/*.prompt.yaml)
	var globalFilePrompts []config.WebPrompt
	if promptsCache != nil {
		var err error
		globalFilePrompts, err = promptsCache.GetWebPrompts()
		if err != nil && s.logger != nil {
			s.logger.Warn("Failed to load global file prompts", "error", err)
		}
	}

	// 2. Settings prompts
	var settingsPrompts []config.WebPrompt
	if cfg != nil {
		settingsPrompts = cfg.Prompts
	}

	// 3. ACP server-specific prompts
	var acpServerName, acpServerType string
	if sm != nil {
		if ws := sm.GetWorkspace(workingDir); ws != nil {
			acpServerName = ws.ACPServer
		}
	}
	if acpServerName != "" && cfg != nil {
		acpServerType = cfg.GetServerType(acpServerName)
	}
	if acpServerType == "" {
		acpServerType = acpServerName
	}

	var serverPrompts []config.WebPrompt
	if acpServerType != "" && promptsCache != nil {
		sp, err := promptsCache.GetWebPromptsSpecificToACP(acpServerType)
		if err != nil && s.logger != nil {
			s.logger.Warn("Failed to load ACP-specific file prompts",
				"acp_server", acpServerName, "acp_type", acpServerType, "error", err)
		}
		serverPrompts = sp
	}
	// Also include inline per-server prompts from config
	if acpServerName != "" && cfg != nil {
		for _, srv := range cfg.ACPServers {
			if srv.Name == acpServerName {
				serverPrompts = append(serverPrompts, srv.Prompts...)
				break
			}
		}
	}

	// 4. Workspace directory prompts (.mitto/prompts/*.prompt.yaml and extra dirs)
	var dirPrompts []config.WebPrompt
	workspacePromptsDirs := []string{appdir.WorkspacePromptsDir(workingDir)}
	if sm != nil {
		workspacePromptsDirs = append(workspacePromptsDirs, sm.GetWorkspacePromptsDirs(workingDir)...)
	}
	for _, dir := range workspacePromptsDirs {
		absDir := dir
		if !filepath.IsAbs(dir) {
			absDir = filepath.Join(workingDir, dir)
		}
		prompts, err := config.LoadPromptsFromDir(absDir)
		if err != nil {
			continue
		}
		dirPrompts = config.MergePrompts(nil, dirPrompts, config.PromptsToWebPrompts(prompts))
	}

	// 5. Workspace inline prompts (.mittorc)
	var inlinePrompts []config.WebPrompt
	if sm != nil {
		inlinePrompts = sm.GetWorkspacePrompts(workingDir)
	}

	// Merge in two steps: first global+settings, then server+workspace on top
	globalMerged := config.MergePromptsKeepDisabled(globalFilePrompts, settingsPrompts, nil)
	allWorkspace := config.MergePromptsKeepDisabled(nil, dirPrompts, inlinePrompts)
	return config.MergePromptsKeepDisabled(globalMerged, serverPrompts, allWorkspace)
}

// resolvePromptWorkingDir resolves the working directory for prompt operations.
// Uses the caller's session working dir by default, or the workspace's working dir if specified.
func (s *Server) resolvePromptWorkingDir(realSessionID string, workspaceUUID string) (string, error) {
	s.mu.RLock()
	store := s.store
	sm := s.sessionManager
	s.mu.RUnlock()

	var workingDir string
	if store != nil {
		meta, err := store.GetMetadata(realSessionID)
		if err != nil {
			return "", fmt.Errorf("failed to get session metadata: %w", err)
		}
		workingDir = meta.WorkingDir
	}

	if workspaceUUID != "" && sm != nil {
		ws := sm.GetWorkspaceByUUID(workspaceUUID)
		if ws == nil {
			return "", fmt.Errorf("workspace not found: %s", workspaceUUID)
		}
		workingDir = ws.WorkingDir
	}

	if workingDir == "" {
		return "", fmt.Errorf("could not determine working directory for session %s", realSessionID)
	}
	return workingDir, nil
}

// findPromptByName returns the merged prompt whose name matches (case-insensitive)
// the given name for the given working directory, and whether it was found.
// Resolution uses the same merged prompt list as mitto_prompt_get.
func (s *Server) findPromptByName(workingDir, name string) (config.WebPrompt, bool) {
	for _, p := range s.loadMergedPrompts(workingDir) {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return config.WebPrompt{}, false
}

// handlePromptList handles the mitto_prompt_list tool.
func (s *Server) handlePromptList(ctx context.Context, req *mcp.CallToolRequest, input PromptListInput) (*mcp.CallToolResult, PromptListOutput, error) {
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, PromptListOutput{Error: "could not resolve session: provide a valid self_id"}, nil
	}
	if s.getSession(realSessionID) == nil {
		return nil, PromptListOutput{Error: fmt.Sprintf("session not found or not running: %s", realSessionID)}, nil
	}

	workingDir, err := s.resolvePromptWorkingDir(realSessionID, input.Workspace)
	if err != nil {
		return nil, PromptListOutput{Error: err.Error()}, nil
	}

	merged := s.loadMergedPrompts(workingDir)
	prompts := make([]PromptInfo, 0, len(merged)) // Must be empty array, not nil — ACP validates this
	for _, p := range merged {
		prompts = append(prompts, PromptInfo{
			Name:            p.Name,
			Description:     p.Description,
			Group:           p.Group,
			BackgroundColor: p.BackgroundColor,
			Icon:            p.Icon,
			Source:          string(p.Source),
			Enabled:         p.Enabled,
			Periodic:        p.Periodic,
			Parameters:      p.Parameters,
		})
	}
	return nil, PromptListOutput{Success: true, Prompts: prompts, WorkingDir: workingDir}, nil
}

// handlePromptGet handles the mitto_prompt_get tool.
func (s *Server) handlePromptGet(ctx context.Context, req *mcp.CallToolRequest, input PromptGetInput) (*mcp.CallToolResult, PromptGetOutput, error) {
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, PromptGetOutput{Error: "could not resolve session: provide a valid self_id"}, nil
	}
	if s.getSession(realSessionID) == nil {
		return nil, PromptGetOutput{Error: fmt.Sprintf("session not found or not running: %s", realSessionID)}, nil
	}
	if input.Name == "" {
		return nil, PromptGetOutput{Error: "name is required"}, nil
	}

	workingDir, err := s.resolvePromptWorkingDir(realSessionID, input.Workspace)
	if err != nil {
		return nil, PromptGetOutput{Error: err.Error()}, nil
	}

	p, found := s.findPromptByName(workingDir, input.Name)
	if !found {
		return nil, PromptGetOutput{Error: "prompt not found: " + input.Name}, nil
	}
	return nil, PromptGetOutput{
		Success: true,
		Prompt: &PromptDetail{
			Name:            p.Name,
			Prompt:          p.Prompt,
			Description:     p.Description,
			Group:           p.Group,
			BackgroundColor: p.BackgroundColor,
			Icon:            p.Icon,
			Source:          string(p.Source),
			Enabled:         p.Enabled,
			Periodic:        p.Periodic,
			Parameters:      p.Parameters,
		},
	}, nil
}

// handlePromptUpdate handles the mitto_prompt_update tool.
func (s *Server) handlePromptUpdate(ctx context.Context, req *mcp.CallToolRequest, input PromptUpdateInput) (*mcp.CallToolResult, PromptUpdateOutput, error) {
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, PromptUpdateOutput{Error: "could not resolve session: provide a valid self_id"}, nil
	}
	if s.getSession(realSessionID) == nil {
		return nil, PromptUpdateOutput{Error: fmt.Sprintf("session not found or not running: %s", realSessionID)}, nil
	}
	if input.Name == "" {
		return nil, PromptUpdateOutput{Error: "name is required"}, nil
	}

	workingDir, err := s.resolvePromptWorkingDir(realSessionID, input.Workspace)
	if err != nil {
		return nil, PromptUpdateOutput{Error: err.Error()}, nil
	}

	// Find existing prompt for default values
	merged := s.loadMergedPrompts(workingDir)
	var existing *config.WebPrompt
	for i := range merged {
		if strings.EqualFold(merged[i].Name, input.Name) {
			existing = &merged[i]
			break
		}
	}

	promptsDir := appdir.WorkspacePromptsDir(workingDir)
	slug := config.SlugifyPromptName(input.Name)
	if slug == "" {
		slug = "prompt"
	}
	filePath := filepath.Join(promptsDir, slug+".prompt.yaml")

	// Enable/disable only: no content fields set, only Enabled
	isEnableDisableOnly := input.Enabled != nil &&
		input.Prompt == "" &&
		input.Description == "" &&
		input.BackgroundColor == "" &&
		input.Group == ""

	if isEnableDisableOnly {
		if _, statErr := os.Stat(filePath); statErr == nil {
			if err := config.UpdatePromptFileEnabled(filePath, *input.Enabled); err != nil {
				return nil, PromptUpdateOutput{Error: "failed to update prompt file: " + err.Error()}, nil
			}
		} else {
			if err := config.SaveWorkspaceRCPromptEnabled(workingDir, input.Name, *input.Enabled); err != nil {
				return nil, PromptUpdateOutput{Error: "failed to update workspace config: " + err.Error()}, nil
			}
		}
		s.logger.Debug("Updated prompt enabled state",
			"name", input.Name, "enabled", *input.Enabled, "working_dir", workingDir)
		return nil, PromptUpdateOutput{Success: true, Path: filePath}, nil
	}

	// Content/metadata update — write full prompt file using existing values as defaults
	name := input.Name
	promptText := input.Prompt
	description := input.Description
	backgroundColor := input.BackgroundColor
	group := input.Group
	enabled := input.Enabled
	if existing != nil {
		if promptText == "" {
			promptText = existing.Prompt
		}
		if description == "" {
			description = existing.Description
		}
		if backgroundColor == "" {
			backgroundColor = existing.BackgroundColor
		}
		if group == "" {
			group = existing.Group
		}
		if enabled == nil {
			enabled = existing.Enabled
		}
	}

	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		return nil, PromptUpdateOutput{Error: "failed to create prompts directory: " + err.Error()}, nil
	}

	// Reject invalid Go-template syntax / cond CEL before persisting (mitto-m7sb.6).
	if err := config.PrecompileTemplateConds(name, promptText); err != nil {
		return nil, PromptUpdateOutput{Error: "invalid prompt template: " + err.Error()}, nil
	}
	// Warn (non-fatal) when body still uses deprecated @mitto: tokens (mitto-m7sb.9).
	config.WarnDeprecatedMittoVars(name, promptText)

	pf := &config.PromptFile{
		Name:            name,
		Description:     description,
		BackgroundColor: backgroundColor,
		Group:           group,
		Enabled:         enabled,
		Content:         promptText,
	}
	yamlBytes, err := yaml.Marshal(pf)
	if err != nil {
		return nil, PromptUpdateOutput{Error: "failed to marshal prompt file: " + err.Error()}, nil
	}
	if err := os.WriteFile(filePath, yamlBytes, 0o644); err != nil {
		return nil, PromptUpdateOutput{Error: "failed to write prompt file: " + err.Error()}, nil
	}

	s.logger.Debug("Updated workspace prompt file", "path", filePath, "name", name)
	return nil, PromptUpdateOutput{Success: true, Path: filePath}, nil
}
