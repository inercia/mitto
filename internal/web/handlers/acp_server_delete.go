// Guided ACP-server deletion (bead mitto-pgt): replaces the frontend-only hard
// block on delete with a two-step backend flow. prepare-delete returns the
// impact plan (active conversations that block deletion, per-folder plans with
// reassign candidates, workspaces to delete/reassign); reassign-and-delete
// executes it (per-folder archive+metadata rewrite or delete, workspace config
// updates, and finally deletes the server from settings.json).
package handlers

import (
	"net/http"
	"sort"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/fileutil"
	"github.com/inercia/mitto/internal/session"
)

// acpServerDeleteArchiveTimeout bounds how long we wait for an in-flight
// response to complete before force-closing during reassign-and-delete.
// Same rationale/order-of-magnitude as archiveWaitTimeout in session_update.go.
const acpServerDeleteArchiveTimeout = 5 * time.Minute

// ACPServerPrepareDeleteResponse describes what would happen if the named
// server were deleted. The frontend uses it to drive the wizard: if
// HasActive is true (ActiveConversations non-empty) deletion is blocked;
// otherwise it walks Folders one by one collecting the user's reassign choices.
type ACPServerPrepareDeleteResponse struct {
	Server              string                  `json:"server"`
	HasActive           bool                    `json:"has_active"`
	ActiveConversations []ACPActiveConversation `json:"active_conversations"`
	Folders             []ACPServerFolderPlan   `json:"folders"`
}

// ACPActiveConversation identifies a live conversation that pins the server.
type ACPActiveConversation struct {
	SessionID           string `json:"session_id"`
	Name                string `json:"name,omitempty"`
	WorkingDir          string `json:"working_dir,omitempty"`
	IsPrompting         bool   `json:"is_prompting,omitempty"`
	HasConnectedClients bool   `json:"has_connected_clients,omitempty"`
}

// ACPServerFolderPlan describes a single working_dir where the server is
// configured (either via workspace config or via existing conversations),
// listing the reassign candidates and conversation counts.
type ACPServerFolderPlan struct {
	WorkingDir               string   `json:"working_dir"`
	WorkspaceName            string   `json:"workspace_name,omitempty"`
	WorkspaceUUIDs           []string `json:"workspace_uuids,omitempty"`
	ArchivedConversations    int      `json:"archived_conversations"`
	NonArchivedConversations int      `json:"non_archived_conversations"`
	// ReplacementCandidates are OTHER ACP servers already configured for this
	// folder (workspace-registered), sorted by name for stable ordering.
	ReplacementCandidates []string `json:"replacement_candidates"`
}

// ACPServerReassignAndDeleteRequest carries the user's per-folder choices.
// Folders map key is the working_dir; Value is either an ACP server name to
// reassign to, or the sentinel "" / "delete" to mean "delete conversations in
// this folder and remove the workspace config".
type ACPServerReassignAndDeleteRequest struct {
	// Folders maps working_dir → chosen replacement server name, or ""/"delete"
	// to delete conversations+workspaces in that folder. Any folder returned by
	// prepare-delete must be present here (missing entries are treated as
	// "delete").
	Folders map[string]string `json:"folders"`
}

// ACPServerReassignAndDeleteResponse summarises what the execute step did.
// Counts mirror the array lengths for easy consumption by the wizard's success
// step (which shows totals rather than IDs).
type ACPServerReassignAndDeleteResponse struct {
	Server                      string   `json:"server"`
	ReassignedConversations     []string `json:"reassigned_conversations,omitempty"`
	DeletedConversations        []string `json:"deleted_conversations,omitempty"`
	ReassignedWorkspaces        []string `json:"reassigned_workspaces,omitempty"`
	DeletedWorkspaces           []string `json:"deleted_workspaces,omitempty"`
	ReassignedConversationCount int      `json:"reassigned_conversation_count"`
	DeletedConversationCount    int      `json:"deleted_conversation_count"`
	ReassignedWorkspaceCount    int      `json:"reassigned_workspace_count"`
	DeletedWorkspaceCount       int      `json:"deleted_workspace_count"`
}

// deleteChoice reports the user's chosen action for a folder.
type deleteChoice struct {
	replacement string // "" means delete
}

// reassignChoice parses the folder value. Empty / "delete" means delete.
func parseFolderChoice(v string) deleteChoice {
	if v == "" || v == "delete" {
		return deleteChoice{}
	}
	return deleteChoice{replacement: v}
}

// requireDeps verifies the shared dependencies the two handlers need. Returns
// true if the request was written to (rejected) and the handler should abort.
func (h *Handlers) acpDeleteDepsMissing(w http.ResponseWriter) bool {
	if h.deps.SessionManager == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Session manager not available")
		return true
	}
	if h.deps.Store == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Session store not available")
		return true
	}
	if h.deps.MittoConfig == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Configuration not available")
		return true
	}
	return false
}

// HandleACPServerPrepareDelete handles GET /api/acp-servers/{name}/prepare-delete.
// It returns the plan: active conversations that block deletion, per-folder
// impact (workspaces, conversation counts, reassign candidates).
func (h *Handlers) HandleACPServerPrepareDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	serverName := r.PathValue("name")
	if serverName == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "Server name is required")
		return
	}
	if h.acpDeleteDepsMissing(w) {
		return
	}

	srv, err := h.deps.MittoConfig.GetServer(serverName)
	if err != nil || srv == nil {
		writeErrorJSON(w, http.StatusNotFound, "", "ACP server not found: "+serverName)
		return
	}

	resp := ACPServerPrepareDeleteResponse{
		Server:              serverName,
		ActiveConversations: []ACPActiveConversation{},
		Folders:             []ACPServerFolderPlan{},
	}

	// Collect active-session snapshot from the running manager. Only sessions
	// that are actually running (in sm.sessions) show up here; archived/idle
	// persisted sessions do NOT.
	activeByID := make(map[string]SessionInfoSnapshot)
	for _, infos := range h.deps.SessionManager.GetSessionInfoByWorkspace() {
		for _, info := range infos {
			// Only sessions with observers OR actively prompting OR with
			// connected clients count as "active" for the block guard, matching
			// the bead's step-1 definition.
			if info.IsPrompting || info.HasObservers || info.HasConnectedClients {
				activeByID[info.SessionID] = SessionInfoSnapshot{
					IsPrompting:         info.IsPrompting,
					HasConnectedClients: info.HasConnectedClients,
				}
			}
		}
	}

	// Walk persisted sessions to attribute them to folders and to fill in
	// active-conversation names for the block dialog. This also captures
	// folders that no longer have a workspace config but still have sessions
	// bound to serverName (defense in depth).
	metas, err := h.deps.Store.List()
	if err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to list sessions during ACP server prepare-delete",
				"server", serverName, "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to list sessions")
		return
	}

	type folderAgg struct {
		archived      int
		nonArchived   int
		workspaceUUID map[string]struct{}
	}
	folders := make(map[string]*folderAgg)
	ensureFolder := func(dir string) *folderAgg {
		if fa, ok := folders[dir]; ok {
			return fa
		}
		fa := &folderAgg{workspaceUUID: make(map[string]struct{})}
		folders[dir] = fa
		return fa
	}

	for _, meta := range metas {
		if meta.ACPServer != serverName {
			continue
		}
		// Active guard: report and DO NOT include in folder plan (a folder
		// with an active session is a blocker until it stops).
		if snap, ok := activeByID[meta.SessionID]; ok {
			resp.ActiveConversations = append(resp.ActiveConversations, ACPActiveConversation{
				SessionID:           meta.SessionID,
				Name:                meta.Name,
				WorkingDir:          meta.WorkingDir,
				IsPrompting:         snap.IsPrompting,
				HasConnectedClients: snap.HasConnectedClients,
			})
			continue
		}
		fa := ensureFolder(meta.WorkingDir)
		if meta.Archived {
			fa.archived++
		} else {
			fa.nonArchived++
		}
	}

	// Attribute workspaces (whether or not they have conversations) to folders,
	// and compute per-folder replacement candidates.
	allWorkspaces := h.deps.SessionManager.GetWorkspaces()
	for _, ws := range allWorkspaces {
		if ws.ACPServer != serverName {
			continue
		}
		fa := ensureFolder(ws.WorkingDir)
		fa.workspaceUUID[ws.UUID] = struct{}{}
	}

	// Deterministic order for tests and stable UI.
	orderedDirs := make([]string, 0, len(folders))
	for dir := range folders {
		orderedDirs = append(orderedDirs, dir)
	}
	sort.Strings(orderedDirs)

	for _, dir := range orderedDirs {
		fa := folders[dir]
		plan := ACPServerFolderPlan{
			WorkingDir:               dir,
			ArchivedConversations:    fa.archived,
			NonArchivedConversations: fa.nonArchived,
			ReplacementCandidates:    []string{},
		}
		for uuid := range fa.workspaceUUID {
			plan.WorkspaceUUIDs = append(plan.WorkspaceUUIDs, uuid)
		}
		sort.Strings(plan.WorkspaceUUIDs)

		// Populate WorkspaceName from any workspace registered for this folder
		// that carries a friendly Name (folder-level field, shared across
		// workspaces for the same dir). Prefer the one bound to the server we
		// are deleting; fall back to any workspace for the folder.
		for _, ws := range h.deps.SessionManager.GetWorkspacesForFolder(dir) {
			if ws.Name == "" {
				continue
			}
			if plan.WorkspaceName == "" || ws.ACPServer == serverName {
				plan.WorkspaceName = ws.Name
				if ws.ACPServer == serverName {
					break
				}
			}
		}

		// Candidates = other servers already registered for this folder.
		seen := make(map[string]struct{})
		for _, ws := range h.deps.SessionManager.GetWorkspacesForFolder(dir) {
			if ws.ACPServer == "" || ws.ACPServer == serverName {
				continue
			}
			if _, dup := seen[ws.ACPServer]; dup {
				continue
			}
			seen[ws.ACPServer] = struct{}{}
			plan.ReplacementCandidates = append(plan.ReplacementCandidates, ws.ACPServer)
		}
		sort.Strings(plan.ReplacementCandidates)

		resp.Folders = append(resp.Folders, plan)
	}

	resp.HasActive = len(resp.ActiveConversations) > 0
	writeJSONOK(w, resp)
}

// SessionInfoSnapshot is a minimal, non-time-sensitive projection of
// SessionInfo used for active-conversation reporting.
type SessionInfoSnapshot struct {
	IsPrompting         bool
	HasConnectedClients bool
}

// HandleACPServerReassignAndDelete handles POST
// /api/acp-servers/{name}/reassign-and-delete. It applies the per-folder
// choices (reassign or delete), then removes the server from settings.json.
// Blocks when any active conversation still uses the server (mirrors the
// active-guard in prepare-delete).
func (h *Handlers) HandleACPServerReassignAndDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	serverName := r.PathValue("name")
	if serverName == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "Server name is required")
		return
	}
	if h.deps.ConfigReadOnly {
		writeErrorJSON(w, http.StatusForbidden, "", "Configuration is read-only (loaded from config file)")
		return
	}
	if h.acpDeleteDepsMissing(w) {
		return
	}

	srv, err := h.deps.MittoConfig.GetServer(serverName)
	if err != nil || srv == nil {
		writeErrorJSON(w, http.StatusNotFound, "", "ACP server not found: "+serverName)
		return
	}
	// RC-file-sourced servers cannot be edited via the API — the RC file is the
	// source of truth and settings.json will not shadow it here.
	if srv.Source == configPkg.SourceRCFile {
		writeErrorJSON(w, http.StatusForbidden, "", "ACP server is defined in RC file and must be removed there")
		return
	}

	var req ACPServerReassignAndDeleteRequest
	if !parseJSONBody(w, r, &req) {
		return
	}
	if req.Folders == nil {
		req.Folders = map[string]string{}
	}

	// Active-conversation guard (identical semantics to prepare-delete).
	activeByID := make(map[string]bool)
	for _, infos := range h.deps.SessionManager.GetSessionInfoByWorkspace() {
		for _, info := range infos {
			if info.IsPrompting || info.HasObservers || info.HasConnectedClients {
				activeByID[info.SessionID] = true
			}
		}
	}
	metas, err := h.deps.Store.List()
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to list sessions")
		return
	}
	var activeBlockers []string
	for _, meta := range metas {
		if meta.ACPServer == serverName && activeByID[meta.SessionID] {
			activeBlockers = append(activeBlockers, meta.SessionID)
		}
	}
	if len(activeBlockers) > 0 {
		writeJSON(w, http.StatusConflict, errorEnvelope{Error: errorBody{
			Code:    errCodeConflict,
			Message: "Cannot delete server: active conversations are using it",
			Details: map[string]any{"active_session_ids": activeBlockers},
		}})
		return
	}

	resp := ACPServerReassignAndDeleteResponse{Server: serverName}

	// Apply per-folder plans. Iterate deterministically for logging clarity.
	folderDirs := make([]string, 0, len(req.Folders))
	for dir := range req.Folders {
		folderDirs = append(folderDirs, dir)
	}
	sort.Strings(folderDirs)

	for _, dir := range folderDirs {
		choice := parseFolderChoice(req.Folders[dir])
		if choice.replacement != "" {
			// Validate the replacement is a known server.
			if _, err := h.deps.MittoConfig.GetServer(choice.replacement); err != nil {
				writeErrorJSON(w, http.StatusBadRequest, "", "Unknown replacement server for "+dir+": "+choice.replacement)
				return
			}
			if choice.replacement == serverName {
				writeErrorJSON(w, http.StatusBadRequest, "", "Replacement server cannot be the server being deleted")
				return
			}
			if err := h.reassignFolder(dir, serverName, choice.replacement, metas, &resp); err != nil {
				writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to reassign folder "+dir+": "+err.Error())
				return
			}
		} else {
			if err := h.deleteFolder(dir, serverName, metas, &resp); err != nil {
				writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to delete conversations for folder "+dir+": "+err.Error())
				return
			}
		}
	}

	// Persist workspace changes to disk via the server-provided callback.
	if h.deps.SyncConfigWorkspaces != nil {
		h.deps.SyncConfigWorkspaces()
	}

	// Finally, remove the server from settings.json.
	if err := h.removeServerFromSettings(serverName); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to remove server from settings: "+err.Error())
		return
	}
	// Apply to in-memory config so subsequent requests reflect the change.
	newServers := make([]configPkg.ACPServer, 0, len(h.deps.MittoConfig.ACPServers))
	for _, s := range h.deps.MittoConfig.ACPServers {
		if s.Name == serverName {
			continue
		}
		newServers = append(newServers, s)
	}
	h.deps.MittoConfig.ACPServers = newServers

	resp.ReassignedConversationCount = len(resp.ReassignedConversations)
	resp.DeletedConversationCount = len(resp.DeletedConversations)
	resp.ReassignedWorkspaceCount = len(resp.ReassignedWorkspaces)
	resp.DeletedWorkspaceCount = len(resp.DeletedWorkspaces)
	writeJSONOK(w, resp)
}

// reassignFolder archives + rewrites metadata for every non-archived
// conversation in dir bound to oldServer, clears ACPSessionID so unarchive
// resumes fresh on the new agent, and rewrites the workspace config's
// ACPServer field. Never deletes conversations.
func (h *Handlers) reassignFolder(dir, oldServer, newServer string, metas []session.Metadata, resp *ACPServerReassignAndDeleteResponse) error {
	sm := h.deps.SessionManager
	store := h.deps.Store

	for _, meta := range metas {
		if meta.WorkingDir != dir || meta.ACPServer != oldServer {
			continue
		}
		// Archive live sessions first (mirrors PATCH /api/sessions archived=true).
		if !meta.Archived && sm.GetSession(meta.SessionID) != nil {
			reason := "acp_server_reassigned"
			if !sm.CloseSessionGracefully(meta.SessionID, reason, acpServerDeleteArchiveTimeout) {
				sm.CloseSession(meta.SessionID, "acp_server_reassigned_timeout")
			}
			if h.deps.BroadcastACPStopped != nil {
				h.deps.BroadcastACPStopped(meta.SessionID, reason)
			}
		}
		// Rewrite metadata: new server + archived + clear the ACP-assigned id
		// (agent-specific, invalid under a different agent — same rationale as
		// hsClearPersistedACPSessionID / mitto-y1g). Also drop the persisted
		// current mode; it is agent-specific.
		if err := store.UpdateMetadata(meta.SessionID, func(m *session.Metadata) {
			m.ACPServer = newServer
			m.ACPSessionID = ""
			m.CurrentModeID = ""
			if !m.Archived {
				m.Archived = true
				m.ArchivedAt = time.Now()
				m.ArchiveReason = session.ArchiveReasonManual
			}
		}); err != nil {
			return err
		}
		resp.ReassignedConversations = append(resp.ReassignedConversations, meta.SessionID)
		// Broadcast archived state so clients update the sidebar even though
		// the archive was implicit here.
		if h.deps.BroadcastSessionArchived != nil {
			h.deps.BroadcastSessionArchived(meta.SessionID, true, session.ArchiveReasonManual)
		}
	}

	// Reassign every workspace config for (dir, oldServer) → newServer.
	for _, ws := range sm.GetWorkspaces() {
		if ws.WorkingDir != dir || ws.ACPServer != oldServer {
			continue
		}
		// Preserve UUID so external references (e.g. beads config keyed by
		// workspace UUID) survive the switch.
		updated := ws
		updated.ACPServer = newServer
		sm.RemoveWorkspace(ws.UUID)
		sm.AddWorkspace(updated)
		resp.ReassignedWorkspaces = append(resp.ReassignedWorkspaces, ws.UUID)
	}
	return nil
}

// deleteFolder deletes every conversation in dir bound to oldServer and
// removes the matching workspace configs. Used when the user picked "delete"
// (or there is no reassign candidate) for a folder.
func (h *Handlers) deleteFolder(dir, oldServer string, metas []session.Metadata, resp *ACPServerReassignAndDeleteResponse) error {
	sm := h.deps.SessionManager
	store := h.deps.Store

	for _, meta := range metas {
		if meta.WorkingDir != dir || meta.ACPServer != oldServer {
			continue
		}
		// Close any live session first so ACP process shuts down cleanly.
		if sm.GetSession(meta.SessionID) != nil {
			sm.CloseSession(meta.SessionID, "acp_server_deleted")
		}
		// Cascade-delete children too (matches HandleDeleteSession behavior).
		childIDs, _ := store.FindAllChildrenRecursive(meta.SessionID)
		for _, cid := range childIDs {
			sm.CloseSession(cid, "parent_deleted")
		}
		if err := store.Delete(meta.SessionID); err != nil && err != session.ErrSessionNotFound {
			return err
		}
		resp.DeletedConversations = append(resp.DeletedConversations, meta.SessionID)
		if h.deps.BroadcastSessionDeleted != nil {
			h.deps.BroadcastSessionDeleted(meta.SessionID)
			for _, cid := range childIDs {
				h.deps.BroadcastSessionDeleted(cid)
			}
		}
	}

	for _, ws := range sm.GetWorkspaces() {
		if ws.WorkingDir != dir || ws.ACPServer != oldServer {
			continue
		}
		sm.RemoveWorkspace(ws.UUID)
		resp.DeletedWorkspaces = append(resp.DeletedWorkspaces, ws.UUID)
	}
	return nil
}

// removeServerFromSettings persists the server removal to settings.json. Uses
// the same read-modify-write pattern as HandleConfirmAgents (agent_discovery.go).
func (h *Handlers) removeServerFromSettings(serverName string) error {
	settingsPath, err := appdir.SettingsPath()
	if err != nil {
		return err
	}
	var settings configPkg.Settings
	if err := fileutil.ReadJSON(settingsPath, &settings); err != nil {
		return err
	}
	filtered := make([]configPkg.ACPServerSettings, 0, len(settings.ACPServers))
	for _, s := range settings.ACPServers {
		if s.Name == serverName {
			continue
		}
		filtered = append(filtered, s)
	}
	settings.ACPServers = filtered
	return configPkg.SaveSettings(&settings)
}
