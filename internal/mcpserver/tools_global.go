// tools_global.go: MCP tool handlers for workspace, config, runtime, and cold-start info.
// Extracted from server.go for maintainability (mitto-90f.2).
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/beads"
	"github.com/inercia/mitto/internal/coldstart"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// ListConversationsOutput wraps the list of conversations for MCP output schema compliance.
type ListConversationsOutput struct {
	Conversations []ConversationInfo `json:"conversations"`
}

// WorkspaceInfo contains information about a single workspace for MCP tool output.
type WorkspaceInfo struct {
	UUID       string                    `json:"uuid"`
	Name       string                    `json:"name,omitempty"`
	WorkingDir string                    `json:"working_dir"`
	ACPServer  string                    `json:"acp_server"`
	IsDefault  bool                      `json:"is_default,omitempty"` // True if this is the default workspace for its folder
	Metadata   *config.WorkspaceMetadata `json:"metadata,omitempty"`
}

// WorkspaceListInput is the input for the mitto_workspace_list tool.
type WorkspaceListInput struct {
	Filter string `json:"filter,omitempty"` // Optional: "active", "archived", or empty for all
}

// WorkspaceListOutput is the output for the mitto_workspace_list tool.
type WorkspaceListOutput struct {
	Workspaces []WorkspaceInfo `json:"workspaces"`
}

// WorkspaceUserDataField is a single field definition for the user_data schema.
type WorkspaceUserDataField struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"` // "string" (default), "url", or "filename"
}

// WorkspaceUpdateInput is the input for the mitto_workspace_update tool.
// UserDataSchema uses a plain slice: nil means absent (leave schema untouched);
// a non-nil empty slice (JSON []) means "provided but empty" (clears schema when merge=false).
type WorkspaceUpdateInput struct {
	SelfID              string                   `json:"self_id"`
	Workspace           string                   `json:"workspace,omitempty"`
	Description         *string                  `json:"description,omitempty"`
	URL                 *string                  `json:"url,omitempty"`
	Group               *string                  `json:"group,omitempty"`
	UserDataSchema      []WorkspaceUserDataField `json:"user_data_schema,omitempty"`
	UserDataSchemaMerge *bool                    `json:"user_data_schema_merge,omitempty"`
}

// WorkspaceUpdateOutput is the output for the mitto_workspace_update tool.
type WorkspaceUpdateOutput struct {
	Success       bool                      `json:"success"`
	Error         string                    `json:"error,omitempty"`
	WorkspaceUUID string                    `json:"workspace_uuid,omitempty"`
	WorkingDir    string                    `json:"working_dir,omitempty"`
	Updated       []string                  `json:"updated,omitempty"`
	Metadata      *config.WorkspaceMetadata `json:"metadata,omitempty"`
}

// createListWorkspacesHandler creates the handler for mitto_workspace_list tool.
func (s *Server) createListWorkspacesHandler() mcp.ToolHandlerFor[WorkspaceListInput, WorkspaceListOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input WorkspaceListInput) (*mcp.CallToolResult, WorkspaceListOutput, error) {
		s.mu.RLock()
		sm := s.sessionManager
		s.mu.RUnlock()

		if sm == nil {
			return nil, WorkspaceListOutput{}, fmt.Errorf("session manager not available")
		}

		// Build workspace activity map if filtering is requested
		var wsHasNonArchived map[string]bool // workingDir → has at least one non-archived session
		var wsHasAnySessions map[string]bool // workingDir → has at least one session (any state)
		if input.Filter == "active" || input.Filter == "archived" {
			s.mu.RLock()
			store := s.store
			s.mu.RUnlock()

			if store == nil {
				return nil, WorkspaceListOutput{}, fmt.Errorf("session store not available (required for filtering)")
			}

			sessions, err := store.List()
			if err != nil {
				return nil, WorkspaceListOutput{}, fmt.Errorf("failed to list sessions for filtering: %w", err)
			}

			wsHasNonArchived = make(map[string]bool)
			wsHasAnySessions = make(map[string]bool)
			for _, meta := range sessions {
				if meta.WorkingDir == "" {
					continue
				}
				wsHasAnySessions[meta.WorkingDir] = true
				if !meta.Archived {
					wsHasNonArchived[meta.WorkingDir] = true
				}
			}
		} else if input.Filter != "" {
			return nil, WorkspaceListOutput{}, fmt.Errorf("invalid filter value %q: must be \"active\", \"archived\", or omitted", input.Filter)
		}

		workspaces := sm.GetWorkspaces()
		infos := make([]WorkspaceInfo, 0, len(workspaces))

		for _, ws := range workspaces {
			// Apply filter
			if input.Filter == "active" {
				if !wsHasNonArchived[ws.WorkingDir] {
					continue // skip: no non-archived sessions
				}
			} else if input.Filter == "archived" {
				if !wsHasAnySessions[ws.WorkingDir] || wsHasNonArchived[ws.WorkingDir] {
					continue // skip: no sessions at all OR has non-archived sessions
				}
			}

			info := WorkspaceInfo{
				UUID:       ws.UUID,
				Name:       ws.Name,
				WorkingDir: ws.WorkingDir,
				ACPServer:  ws.ACPServer,
				IsDefault:  ws.IsDefault,
			}

			// Load .mittorc metadata if workspace has a working directory
			if ws.WorkingDir != "" {
				rc, err := config.LoadWorkspaceRC(ws.WorkingDir)
				if err != nil {
					s.logger.Warn("Failed to load workspace .mittorc",
						"working_dir", ws.WorkingDir,
						"error", err)
				}
				if rc != nil && rc.Metadata != nil {
					info.Metadata = rc.Metadata
				}
			}

			infos = append(infos, info)
		}

		return nil, WorkspaceListOutput{Workspaces: infos}, nil
	}
}

func (s *Server) handleWorkspaceUpdate(ctx context.Context, req *mcp.CallToolRequest, input WorkspaceUpdateInput) (*mcp.CallToolResult, WorkspaceUpdateOutput, error) {
	if input.SelfID == "" {
		return nil, WorkspaceUpdateOutput{Success: false, Error: "self_id is required"}, nil
	}

	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, WorkspaceUpdateOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found: the self_id '%s' could not be resolved", input.SelfID),
		}, nil
	}

	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, WorkspaceUpdateOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found or not running: %s", realSessionID),
		}, nil
	}

	s.mu.RLock()
	store := s.store
	sm := s.sessionManager
	s.mu.RUnlock()

	if store == nil {
		return nil, WorkspaceUpdateOutput{Success: false, Error: "session store not available"}, nil
	}
	if sm == nil {
		return nil, WorkspaceUpdateOutput{Success: false, Error: "session manager not available"}, nil
	}

	callerMeta, err := store.GetMetadata(realSessionID)
	if err != nil {
		return nil, WorkspaceUpdateOutput{
			Success: false,
			Error:   fmt.Sprintf("failed to get caller session metadata: %v", err),
		}, nil
	}

	// Resolve target workspace directory and UUID.
	var targetDir, targetUUID string
	if input.Workspace != "" {
		targetWS := sm.GetWorkspaceByUUID(input.Workspace)
		if targetWS == nil {
			return nil, WorkspaceUpdateOutput{
				Success: false,
				Error:   fmt.Sprintf("workspace not found: %s", input.Workspace),
			}, nil
		}
		targetDir = targetWS.WorkingDir
		targetUUID = targetWS.UUID
		if targetWS.WorkingDir != callerMeta.WorkingDir {
			if !s.checkSessionFlag(realSessionID, session.FlagCanInteractOtherWorkspaces) {
				return nil, WorkspaceUpdateOutput{
					Success: false,
					Error: fmt.Sprintf("cross-workspace operations require the 'Can interact with other workspaces' (%s) flag to be enabled in Advanced Settings",
						session.FlagCanInteractOtherWorkspaces),
				}, nil
			}
			if err := s.confirmCrossWorkspaceOperation(ctx, realSessionID, "modify workspace configuration", targetWS); err != nil {
				return nil, WorkspaceUpdateOutput{Success: false, Error: err.Error()}, nil
			}
		}
	} else {
		targetDir = callerMeta.WorkingDir
		for _, ws := range sm.GetWorkspaces() {
			if ws.WorkingDir == targetDir {
				targetUUID = ws.UUID
				break
			}
		}
	}

	if targetDir == "" {
		return nil, WorkspaceUpdateOutput{Success: false, Error: "target workspace has no working directory"}, nil
	}

	// Load current state.
	curRC, _ := config.LoadWorkspaceRC(targetDir)
	var curDesc, curURL, curGroup string
	var curFields []config.UserDataSchemaField
	if curRC != nil && curRC.Metadata != nil {
		curDesc = curRC.Metadata.Description
		curURL = curRC.Metadata.URL
		curGroup = curRC.Metadata.Group
		if curRC.Metadata.UserDataSchema != nil {
			curFields = curRC.Metadata.UserDataSchema.Fields
		}
	}

	var updated []string

	// Update metadata fields if any pointer is non-nil.
	if input.Description != nil || input.URL != nil || input.Group != nil {
		desc := curDesc
		if input.Description != nil {
			desc = *input.Description
		}
		u := curURL
		if input.URL != nil {
			u = *input.URL
		}
		grp := curGroup
		if input.Group != nil {
			grp = *input.Group
		}
		if err := config.SaveWorkspaceMetadata(targetDir, desc, u, grp); err != nil {
			return nil, WorkspaceUpdateOutput{Success: false, Error: fmt.Sprintf("failed to save metadata: %v", err)}, nil
		}
		if input.Description != nil {
			updated = append(updated, "description")
		}
		if input.URL != nil {
			updated = append(updated, "url")
		}
		if input.Group != nil {
			updated = append(updated, "group")
		}
	}

	// Update schema if user_data_schema was present in the input (nil = absent).
	if input.UserDataSchema != nil {
		merge := input.UserDataSchemaMerge == nil || *input.UserDataSchemaMerge

		// Convert and validate provided fields.
		newFields := make([]config.UserDataSchemaField, 0, len(input.UserDataSchema))
		for _, f := range input.UserDataSchema {
			t := config.UserDataAttributeType(f.Type).DefaultType()
			if !t.IsValid() {
				return nil, WorkspaceUpdateOutput{
					Success: false,
					Error:   fmt.Sprintf("invalid user_data field type %q for field %q (allowed: string, url, filename)", f.Type, f.Name),
				}, nil
			}
			newFields = append(newFields, config.UserDataSchemaField{
				Name:        f.Name,
				Description: f.Description,
				Type:        t,
			})
		}

		var finalFields []config.UserDataSchemaField
		if merge {
			// Start from existing fields; replace by name or append.
			finalFields = make([]config.UserDataSchemaField, len(curFields))
			copy(finalFields, curFields)
			for _, nf := range newFields {
				replaced := false
				for i, ef := range finalFields {
					if ef.Name == nf.Name {
						finalFields[i] = nf
						replaced = true
						break
					}
				}
				if !replaced {
					finalFields = append(finalFields, nf)
				}
			}
		} else {
			finalFields = newFields
		}

		if err := config.SaveWorkspaceUserDataSchema(targetDir, finalFields); err != nil {
			return nil, WorkspaceUpdateOutput{Success: false, Error: fmt.Sprintf("failed to save user_data schema: %v", err)}, nil
		}
		updated = append(updated, "user_data_schema")
	}

	if len(updated) == 0 {
		return nil, WorkspaceUpdateOutput{
			Success: false,
			Error:   "no properties to update: specify at least one of 'description', 'url', 'group', or 'user_data_schema'",
		}, nil
	}

	sm.InvalidateWorkspaceRC(targetDir)

	var outMeta *config.WorkspaceMetadata
	if newRC, _ := config.LoadWorkspaceRC(targetDir); newRC != nil {
		outMeta = newRC.Metadata
	}

	s.logger.Info("Workspace updated via MCP",
		"source_session", realSessionID,
		"target_dir", targetDir,
		"updated", updated)

	return nil, WorkspaceUpdateOutput{
		Success:       true,
		WorkspaceUUID: targetUUID,
		WorkingDir:    targetDir,
		Updated:       updated,
		Metadata:      outMeta,
	}, nil
}

// createListConversationsHandler creates the handler for list_conversations tool.
func (s *Server) createListConversationsHandler(sm SessionManager) mcp.ToolHandlerFor[ListConversationsInput, ListConversationsOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input ListConversationsInput) (*mcp.CallToolResult, ListConversationsOutput, error) {
		s.mu.RLock()
		store := s.store
		s.mu.RUnlock()

		if store == nil {
			return nil, ListConversationsOutput{}, fmt.Errorf("session store not available")
		}

		// A) Build workspace lookup maps for enriching results with workspace identity.
		// Composite key (workingDir+"|"+acpServer) → WorkspaceSettings for exact matching;
		// fallback key workingDir → WorkspaceSettings (first match) for partial matching.
		var wsCompositeMap map[string]config.WorkspaceSettings
		var wsFallbackMap map[string]config.WorkspaceSettings
		if sm != nil {
			workspaces := sm.GetWorkspaces()
			wsCompositeMap = make(map[string]config.WorkspaceSettings, len(workspaces))
			wsFallbackMap = make(map[string]config.WorkspaceSettings, len(workspaces))
			for _, ws := range workspaces {
				compositeKey := ws.WorkingDir + "|" + ws.ACPServer
				wsCompositeMap[compositeKey] = ws
				if _, exists := wsFallbackMap[ws.WorkingDir]; !exists {
					wsFallbackMap[ws.WorkingDir] = ws
				}
			}
		}

		// B) Permission-gated workspace filtering.
		// workingDirFilter, if non-empty, restricts results to a specific working directory
		// and takes precedence over input.WorkingDir.
		var workingDirFilter string

		if input.SelfID != "" {
			// Resolve caller's session ID.
			realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
			if realSessionID == "" {
				return nil, ListConversationsOutput{}, fmt.Errorf(
					"session not found: the self_id '%s' could not be resolved", input.SelfID)
			}

			// Get caller's metadata to determine their workspace.
			callerMeta, err := store.GetMetadata(realSessionID)
			if err != nil {
				return nil, ListConversationsOutput{}, fmt.Errorf(
					"failed to get caller metadata: %w", err)
			}

			hasXWPermissions := s.checkSessionFlag(realSessionID, session.FlagCanInteractOtherWorkspaces)

			if input.Workspace != nil {
				// Explicit workspace requested — resolve and check permissions.
				if sm == nil {
					return nil, ListConversationsOutput{}, fmt.Errorf("session manager not available")
				}
				targetWS := sm.GetWorkspaceByUUID(*input.Workspace)
				if targetWS == nil {
					return nil, ListConversationsOutput{}, fmt.Errorf("workspace not found: %s", *input.Workspace)
				}
				if targetWS.WorkingDir != callerMeta.WorkingDir && !hasXWPermissions {
					return nil, ListConversationsOutput{}, fmt.Errorf(
						"cross-workspace operations require the 'Can interact with other workspaces' (%s) flag to be enabled in Advanced Settings",
						session.FlagCanInteractOtherWorkspaces)
				}
				workingDirFilter = targetWS.WorkingDir
			} else {
				// No explicit workspace: scope to caller's own workspace unless they have cross-workspace permissions.
				if !hasXWPermissions {
					workingDirFilter = callerMeta.WorkingDir
				}
				// If caller has cross-workspace permissions and no workspace filter, list all.
			}
		} else if input.Workspace != nil {
			// No self_id but workspace UUID provided — resolve without permission checks (backward compat).
			if sm != nil {
				targetWS := sm.GetWorkspaceByUUID(*input.Workspace)
				if targetWS == nil {
					return nil, ListConversationsOutput{}, fmt.Errorf("workspace not found: %s", *input.Workspace)
				}
				workingDirFilter = targetWS.WorkingDir
			}
		}

		sessions, err := store.List()
		if err != nil {
			return nil, ListConversationsOutput{}, fmt.Errorf("failed to list sessions: %w", err)
		}

		conversations := make([]ConversationInfo, 0, len(sessions))
		for _, meta := range sessions {
			// C) Apply filters.
			// workspace-derived filter takes precedence over explicit input.WorkingDir.
			if workingDirFilter != "" {
				if meta.WorkingDir != workingDirFilter {
					continue
				}
			} else if input.WorkingDir != nil && meta.WorkingDir != *input.WorkingDir {
				continue
			}
			if input.Archived != nil && meta.Archived != *input.Archived {
				continue
			}
			if input.ACPServer != nil && meta.ACPServer != *input.ACPServer {
				continue
			}
			if input.ExcludeSelf != nil && meta.SessionID == *input.ExcludeSelf {
				continue
			}

			info := ConversationInfo{
				SessionID:         meta.SessionID,
				Title:             meta.Name,
				Description:       meta.Description,
				BeadsIssue:        meta.BeadsIssue,
				ACPServer:         meta.ACPServer,
				WorkingDir:        meta.WorkingDir,
				CreatedAt:         meta.CreatedAt,
				UpdatedAt:         meta.UpdatedAt,
				LastUserMessageAt: meta.LastUserMessageAt,
				MessageCount:      meta.EventCount,
				Status:            string(meta.Status),
				Archived:          meta.Archived,
				ArchiveReason:     string(meta.ArchiveReason),
				SessionFolder:     store.SessionDir(meta.SessionID),
				ChildOrigin:       string(meta.ChildOrigin),
			}

			// D) Enrich with workspace identity using composite key, falling back to working-dir-only lookup.
			if wsCompositeMap != nil {
				compositeKey := meta.WorkingDir + "|" + meta.ACPServer
				if ws, ok := wsCompositeMap[compositeKey]; ok {
					info.WorkspaceUUID = ws.UUID
					info.WorkspaceName = ws.Name
				} else if ws, ok := wsFallbackMap[meta.WorkingDir]; ok {
					info.WorkspaceUUID = ws.UUID
					info.WorkspaceName = ws.Name
				}
			}

			// Check lock status.
			lockInfo, err := store.GetLockInfo(meta.SessionID)
			if err == nil && lockInfo != nil {
				info.IsLocked = true
				info.LockStatus = string(lockInfo.Status)
				info.LockClientType = lockInfo.ClientType
				info.IsPrompting = lockInfo.Status == session.LockStatusProcessing
			}

			// Get running session info if available.
			if sm != nil {
				if bs := sm.GetSession(meta.SessionID); bs != nil {
					info.IsRunning = true
					info.IsPrompting = bs.IsPrompting()
					info.LastSeq = bs.GetMaxAssignedSeq()
				}
			}

			// Check if conversation has an active loop prompt.
			if p, err := store.Loop(meta.SessionID).Get(); err == nil && p != nil {
				info.IsLoop = p.Enabled
			}

			// Apply is_running filter after runtime status is resolved.
			if input.IsRunning != nil && info.IsRunning != *input.IsRunning {
				continue
			}

			conversations = append(conversations, info)
		}

		return nil, ListConversationsOutput{Conversations: conversations}, nil
	}
}

// createGetConfigHandler creates the handler for get_config tool.
func (s *Server) createGetConfigHandler() mcp.ToolHandlerFor[struct{}, ConfigInfo] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, ConfigInfo, error) {
		s.mu.RLock()
		cfg := s.config
		s.mu.RUnlock()

		if cfg == nil {
			return nil, ConfigInfo{}, fmt.Errorf("configuration not available")
		}

		info := ConfigInfo{}

		// Marshal config to JSON for safe output
		data, err := json.Marshal(configToSafeOutput(cfg))
		if err != nil {
			return nil, ConfigInfo{}, fmt.Errorf("failed to marshal config: %w", err)
		}
		if err := json.Unmarshal(data, &info); err != nil {
			return nil, ConfigInfo{}, fmt.Errorf("failed to process config: %w", err)
		}

		return nil, info, nil
	}
}

// createGetRuntimeInfoHandler creates the handler for get_runtime_info tool.
func (s *Server) createGetRuntimeInfoHandler() mcp.ToolHandlerFor[struct{}, RuntimeInfo] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, RuntimeInfo, error) {
		info := buildRuntimeInfo()
		return nil, *info, nil
	}
}

// createColdStartRecentHandler creates the handler for the mitto_coldstart_recent tool.
// It returns the most recent cold-start summaries captured by the cold-start
// tracer (internal/coldstart), newest first. A Limit of 0 (or omitted) returns
// all summaries currently held in the ring buffer.
func (s *Server) createColdStartRecentHandler() mcp.ToolHandlerFor[ColdStartRecentInput, ColdStartRecent] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input ColdStartRecentInput) (*mcp.CallToolResult, ColdStartRecent, error) {
		sums := coldstart.RecentSummaries(input.Limit)
		out := ColdStartRecent{ColdStarts: sums}
		if input.ByWorkspace {
			out.WorkspaceStats = coldstart.AggregateByWorkspace(sums)
		}
		return nil, out, nil
	}
}

// createGoroutineGaugeRecentHandler creates the handler for the
// mitto_goroutine_gauge_recent tool (mitto-x3x). It returns the most recent
// periodic goroutine gauge samples, newest first — each sample already
// carries the per-category attribution (ACP processes, WS clients, open MCP
// SSE streams) alongside the raw goroutine total. A Limit of 0 (or omitted)
// returns all samples currently held in the ring buffer.
func (s *Server) createGoroutineGaugeRecentHandler() mcp.ToolHandlerFor[GoroutineGaugeRecentInput, GoroutineGaugeRecent] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GoroutineGaugeRecentInput) (*mcp.CallToolResult, GoroutineGaugeRecent, error) {
		return nil, GoroutineGaugeRecent{Samples: coldstart.RecentGaugeSamples(input.Limit)}, nil
	}
}

// BeadsCacheMetricsInput is the (empty) input for mitto_beads_cache_metrics.
type BeadsCacheMetricsInput struct{}

// createBeadsCacheMetricsHandler creates the handler for the
// mitto_beads_cache_metrics tool. The tool is registered only when the beads
// read cache is enabled (--beads-cache); the handler defensively guards
// against a nil callback and returns a zero-value snapshot if the cache was
// torn down between registration and invocation.
func (s *Server) createBeadsCacheMetricsHandler() mcp.ToolHandlerFor[BeadsCacheMetricsInput, beads.CacheMetrics] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input BeadsCacheMetricsInput) (*mcp.CallToolResult, beads.CacheMetrics, error) {
		fn := s.beadsCacheMetricsFn
		if fn == nil {
			return nil, beads.CacheMetrics{}, nil
		}
		return nil, fn(), nil
	}
}
