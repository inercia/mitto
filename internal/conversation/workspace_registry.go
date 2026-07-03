package conversation

import (
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/processors"
)

// WorkspaceRegistry manages workspace configuration and per-workspace RC config.
// It is a leaf type: it never calls back into SessionManager.
// Its own mu provides a consistent lock order: sm.mu → wsRegistry.mu.
type WorkspaceRegistry struct {
	mu               sync.RWMutex
	workspaces       map[string]*config.WorkspaceSettings
	defaultWorkspace *config.WorkspaceSettings
	fromCLI          bool
	onWorkspaceSave  WorkspaceSaveFunc
	workspaceRCCache *config.WorkspaceRCCache
	mittoConfig      *config.Config
	logger           *slog.Logger
}

// newWorkspaceRegistry creates a WorkspaceRegistry with an initialised workspace map
// and a fresh RC cache with the standard 30-second TTL.
func newWorkspaceRegistry(logger *slog.Logger, fromCLI bool, onSave WorkspaceSaveFunc) *WorkspaceRegistry {
	return &WorkspaceRegistry{
		workspaces:       make(map[string]*config.WorkspaceSettings),
		fromCLI:          fromCLI,
		onWorkspaceSave:  onSave,
		workspaceRCCache: config.NewWorkspaceRCCache(30 * time.Second),
		logger:           logger,
	}
}

// setMittoConfig stores the Mitto configuration (used for ACP server resolution).
func (r *WorkspaceRegistry) setMittoConfig(cfg *config.Config) {
	r.mu.Lock()
	r.mittoConfig = cfg
	r.mu.Unlock()
}

// SetWorkspaces replaces the workspace map.
// Workspaces without UUIDs have UUIDs generated automatically.
func (r *WorkspaceRegistry) SetWorkspaces(workspaces []config.WorkspaceSettings) {
	r.mu.Lock()

	r.workspaces = make(map[string]*config.WorkspaceSettings)
	r.defaultWorkspace = nil

	for i := range workspaces {
		ws := &workspaces[i]
		ws.EnsureUUID()
		r.workspaces[ws.UUID] = ws
		if r.defaultWorkspace == nil {
			r.defaultWorkspace = ws
		}
	}

	shouldSave := !r.fromCLI && r.onWorkspaceSave != nil
	var workspacesToSave []config.WorkspaceSettings
	if shouldSave {
		workspacesToSave = r.getWorkspacesLocked()
	}
	r.mu.Unlock()

	if shouldSave {
		if err := r.onWorkspaceSave(workspacesToSave); err != nil && r.logger != nil {
			r.logger.Error("Failed to save workspaces", "error", err)
		}
	}
}

// GetWorkspaces returns all configured workspaces.
func (r *WorkspaceRegistry) GetWorkspaces() []config.WorkspaceSettings {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.workspaces) == 0 {
		if r.defaultWorkspace != nil && (r.defaultWorkspace.ACPServer != "" || r.defaultWorkspace.ACPCommandOverride != "") {
			return []config.WorkspaceSettings{*r.defaultWorkspace}
		}
		return []config.WorkspaceSettings{}
	}

	result := make([]config.WorkspaceSettings, 0, len(r.workspaces))
	for _, ws := range r.workspaces {
		result = append(result, *ws)
	}
	return result
}

// GetWorkspace returns a workspace matching the given directory.
// If multiple workspaces share the same directory (with different ACP servers),
// the one marked IsDefault is preferred; otherwise the first one found is returned.
func (r *WorkspaceRegistry) GetWorkspace(workingDir string) *config.WorkspaceSettings {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var first *config.WorkspaceSettings
	for _, ws := range r.workspaces {
		if ws.WorkingDir == workingDir {
			if ws.IsDefault {
				return ws
			}
			if first == nil {
				first = ws
			}
		}
	}
	return first
}

// GetWorkspaceByDirAndACP returns the workspace matching both directory and ACP server.
// If acpServer is empty, returns the first workspace matching the directory.
func (r *WorkspaceRegistry) GetWorkspaceByDirAndACP(workingDir, acpServer string) *config.WorkspaceSettings {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.getWorkspaceByDirAndACPLocked(workingDir, acpServer)
}

// getWorkspaceByDirAndACPLocked returns the workspace matching both directory and ACP server.
// Caller must hold r.mu.
func (r *WorkspaceRegistry) getWorkspaceByDirAndACPLocked(workingDir, acpServer string) *config.WorkspaceSettings {
	var first *config.WorkspaceSettings
	for _, ws := range r.workspaces {
		if ws.WorkingDir == workingDir {
			if acpServer == "" || ws.ACPServer == acpServer {
				if acpServer == "" && ws.IsDefault {
					return ws
				}
				if first == nil {
					first = ws
				}
			}
		}
	}
	return first
}

// resolveWorkspaceForACPLocked returns the workspace to use for a given directory/server pair.
// Caller must hold r.mu.
func (r *WorkspaceRegistry) resolveWorkspaceForACPLocked(workingDir, acpServer string) *config.WorkspaceSettings {
	if ws := r.getWorkspaceByDirAndACPLocked(workingDir, acpServer); ws != nil {
		return ws
	}
	if r.defaultWorkspace == nil {
		return nil
	}
	if acpServer != "" && r.defaultWorkspace.ACPServer != acpServer {
		return nil
	}
	if workingDir != "" && r.defaultWorkspace.WorkingDir != "" && r.defaultWorkspace.WorkingDir != workingDir {
		return nil
	}
	return r.defaultWorkspace
}

// resolveWorkspaceForACP is the self-locking variant of resolveWorkspaceForACPLocked.
func (r *WorkspaceRegistry) resolveWorkspaceForACP(workingDir, acpServer string) *config.WorkspaceSettings {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.resolveWorkspaceForACPLocked(workingDir, acpServer)
}

// GetWorkspaceByUUID returns the workspace with the given UUID.
// Returns nil if no workspace with that UUID exists.
func (r *WorkspaceRegistry) GetWorkspaceByUUID(uuid string) *config.WorkspaceSettings {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if ws, ok := r.workspaces[uuid]; ok {
		return ws
	}
	if r.defaultWorkspace != nil && r.defaultWorkspace.UUID == uuid {
		return r.defaultWorkspace
	}
	return nil
}

// GetWorkspacesForFolder returns all workspace configurations for the given folder.
func (r *WorkspaceRegistry) GetWorkspacesForFolder(folder string) []config.WorkspaceSettings {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []config.WorkspaceSettings
	seen := make(map[string]bool)

	for _, ws := range r.workspaces {
		if ws.WorkingDir == folder {
			result = append(result, *ws)
			seen[ws.UUID] = true
		}
	}

	if r.defaultWorkspace != nil && r.defaultWorkspace.WorkingDir == folder {
		if !seen[r.defaultWorkspace.UUID] {
			result = append(result, *r.defaultWorkspace)
		}
	}

	return result
}

// GetDefaultWorkspace returns the default workspace, or nil if none is configured.
func (r *WorkspaceRegistry) GetDefaultWorkspace() *config.WorkspaceSettings {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.defaultWorkspace
}

// AddWorkspace adds a new workspace to the registry.
func (r *WorkspaceRegistry) AddWorkspace(ws config.WorkspaceSettings) {
	r.mu.Lock()

	if r.workspaces == nil {
		r.workspaces = make(map[string]*config.WorkspaceSettings)
	}

	ws.EnsureUUID()
	r.workspaces[ws.UUID] = &ws

	if r.defaultWorkspace == nil || r.defaultWorkspace.WorkingDir == "" {
		r.defaultWorkspace = &ws
	}

	if r.logger != nil {
		r.logger.Info("Added workspace",
			"uuid", ws.UUID,
			"working_dir", ws.WorkingDir,
			"acp_server", ws.ACPServer,
			"total_workspaces", len(r.workspaces))
	}

	shouldSave := !r.fromCLI && r.onWorkspaceSave != nil
	var workspacesToSave []config.WorkspaceSettings
	if shouldSave {
		workspacesToSave = r.getWorkspacesLocked()
	}
	r.mu.Unlock()

	if shouldSave {
		if err := r.onWorkspaceSave(workspacesToSave); err != nil && r.logger != nil {
			r.logger.Error("Failed to save workspaces", "error", err)
		}
	}
}

// getWorkspacesLocked returns all workspaces as a slice. Caller must hold r.mu.
func (r *WorkspaceRegistry) getWorkspacesLocked() []config.WorkspaceSettings {
	result := make([]config.WorkspaceSettings, 0, len(r.workspaces))
	for _, ws := range r.workspaces {
		result = append(result, *ws)
	}
	return result
}

// RemoveWorkspace removes a workspace by UUID from the registry.
func (r *WorkspaceRegistry) RemoveWorkspace(uuid string) {
	r.mu.Lock()

	if r.workspaces == nil {
		r.mu.Unlock()
		return
	}

	ws, exists := r.workspaces[uuid]
	if !exists {
		r.mu.Unlock()
		return
	}
	workingDir := ws.WorkingDir

	delete(r.workspaces, uuid)

	if r.defaultWorkspace != nil && r.defaultWorkspace.UUID == uuid {
		r.defaultWorkspace = nil
		for _, ws := range r.workspaces {
			r.defaultWorkspace = ws
			break
		}
	}

	if r.logger != nil {
		r.logger.Info("Removed workspace",
			"uuid", uuid,
			"working_dir", workingDir,
			"total_workspaces", len(r.workspaces))
	}

	shouldSave := !r.fromCLI && r.onWorkspaceSave != nil
	var workspacesToSave []config.WorkspaceSettings
	if shouldSave {
		workspacesToSave = r.getWorkspacesLocked()
	}
	r.mu.Unlock()

	if shouldSave {
		if err := r.onWorkspaceSave(workspacesToSave); err != nil && r.logger != nil {
			r.logger.Error("Failed to save workspaces", "error", err)
		}
	}
}

// HasWorkspaces returns true if there are any configured workspaces.
func (r *WorkspaceRegistry) HasWorkspaces() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.workspaces) > 0
}

// IsFromCLI returns true if workspaces were loaded from CLI flags.
func (r *WorkspaceRegistry) IsFromCLI() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.fromCLI
}

// resolveWorkspaceACPLocked resolves the effective ACP command, cwd, and env for a workspace.
// Caller must hold r.mu (at least for read).
func (r *WorkspaceRegistry) resolveWorkspaceACPLocked(ws *config.WorkspaceSettings) (acpCommand, acpCwd string, acpEnv map[string]string) {
	if ws == nil {
		return "", "", nil
	}

	if ws.ACPServer != "" && r.mittoConfig != nil {
		if server, err := r.mittoConfig.GetServer(ws.ACPServer); err == nil {
			acpCommand = server.Command
			acpCwd = server.Cwd
			acpEnv = server.Env
		}
	}

	// Apply per-workspace command override (takes priority over server config)
	if ws.ACPCommandOverride != "" {
		acpCommand = ws.ACPCommandOverride
	}

	return
}

// ResolveWorkspaceACP is the self-locking variant of resolveWorkspaceACPLocked.
func (r *WorkspaceRegistry) ResolveWorkspaceACP(ws *config.WorkspaceSettings) (acpCommand, acpCwd string, acpEnv map[string]string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.resolveWorkspaceACPLocked(ws)
}

// buildAvailableACPServers returns the list of ACP servers that have workspaces
// configured for the given folder.
func (r *WorkspaceRegistry) buildAvailableACPServers(folder, currentACPServer string) []processors.AvailableACPServer {
	r.mu.RLock()
	mittoConfig := r.mittoConfig
	r.mu.RUnlock()

	if mittoConfig == nil || len(mittoConfig.ACPServers) == 0 {
		return nil
	}

	folderWorkspaces := r.GetWorkspacesForFolder(folder)
	if len(folderWorkspaces) == 0 {
		return nil
	}

	wsServerSet := make(map[string]bool, len(folderWorkspaces))
	for _, ws := range folderWorkspaces {
		wsServerSet[ws.ACPServer] = true
	}

	servers := make([]processors.AvailableACPServer, 0, len(folderWorkspaces))
	for _, srv := range mittoConfig.ACPServers {
		if wsServerSet[srv.Name] {
			servers = append(servers, processors.AvailableACPServer{
				Name:    srv.Name,
				Type:    srv.GetType(),
				Tags:    srv.Tags,
				Current: srv.Name == currentACPServer,
			})
		}
	}
	return servers
}

// LookupDirByUUID resolves a workspace UUID to its WorkingDir.
// When requireNonEmpty is true, only returns directories that are non-empty.
// When requireNonEmpty is false, returns the dir whenever the UUID is found
// (even if the dir is empty string).
func (r *WorkspaceRegistry) LookupDirByUUID(uuid string, requireNonEmpty bool) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, ws := range r.workspaces {
		if ws.UUID == uuid {
			if !requireNonEmpty || ws.WorkingDir != "" {
				return ws.WorkingDir, true
			}
		}
	}

	if r.defaultWorkspace != nil && r.defaultWorkspace.UUID == uuid {
		if !requireNonEmpty || r.defaultWorkspace.WorkingDir != "" {
			return r.defaultWorkspace.WorkingDir, true
		}
	}

	return "", false
}

// GetWorkspacePrompts returns prompts defined in the workspace's .mittorc file.
func (r *WorkspaceRegistry) GetWorkspacePrompts(workingDir string) []config.WebPrompt {
	if r.workspaceRCCache == nil || workingDir == "" {
		return nil
	}

	rc, err := r.workspaceRCCache.Get(workingDir)
	if err != nil {
		if r.logger != nil {
			r.logger.Warn("Failed to load workspace .mittorc",
				"working_dir", workingDir,
				"error", err)
		}
		return nil
	}

	if rc == nil {
		return nil
	}

	return rc.Prompts
}

// GetWorkspacePromptsDirs returns the prompts_dirs defined in the workspace's .mittorc file.
func (r *WorkspaceRegistry) GetWorkspacePromptsDirs(workingDir string) []string {
	if r.workspaceRCCache == nil || workingDir == "" {
		return nil
	}

	rc, err := r.workspaceRCCache.Get(workingDir)
	if err != nil {
		return nil
	}

	if rc == nil {
		return nil
	}

	return rc.PromptsDirs
}

// GetWorkspaceProcessorsDirs returns the processors_dirs defined in the workspace's .mittorc file.
func (r *WorkspaceRegistry) GetWorkspaceProcessorsDirs(workingDir string) []string {
	if r.workspaceRCCache == nil || workingDir == "" {
		return nil
	}

	rc, err := r.workspaceRCCache.Get(workingDir)
	if err != nil {
		return nil
	}

	if rc == nil {
		return nil
	}

	return rc.ProcessorsDirs
}

// GetWorkspaceProcessorOverrides returns the processor enabled/disabled overrides
// from the workspace's .mittorc file.
func (r *WorkspaceRegistry) GetWorkspaceProcessorOverrides(workingDir string) []config.ProcessorOverride {
	if r.workspaceRCCache == nil || workingDir == "" {
		return nil
	}

	rc, err := r.workspaceRCCache.Get(workingDir)
	if err != nil {
		return nil
	}

	if rc == nil {
		return nil
	}

	return rc.ProcessorOverrides
}

// GetWorkspaceAllProcessorDirs returns all processor directories applicable to a workspace:
// the default .mitto/processors/ dir plus any extras from .mittorc processors_dirs.
func (r *WorkspaceRegistry) GetWorkspaceAllProcessorDirs(workingDir string) []string {
	if workingDir == "" {
		return nil
	}

	var dirs []string

	// 1. Default .mitto/processors/ directory
	defaultDir := appdir.WorkspaceProcessorsDir(workingDir)
	dirs = append(dirs, defaultDir)

	// 2. Additional processors_dirs from .mittorc
	if extraDirs := r.GetWorkspaceProcessorsDirs(workingDir); len(extraDirs) > 0 {
		for _, dir := range extraDirs {
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(workingDir, dir)
			}
			dirs = append(dirs, dir)
		}
	}

	return dirs
}

// GetWorkspaceRCLastModified returns the last modification time of the workspace's .mittorc file.
func (r *WorkspaceRegistry) GetWorkspaceRCLastModified(workingDir string) time.Time {
	if r.workspaceRCCache == nil || workingDir == "" {
		return time.Time{}
	}
	return r.workspaceRCCache.GetLastModified(workingDir)
}

// InvalidateWorkspaceRC invalidates the cached workspace RC for the given directory,
// forcing a reload on the next access.
func (r *WorkspaceRegistry) InvalidateWorkspaceRC(workingDir string) {
	if r.workspaceRCCache == nil || workingDir == "" {
		return
	}
	r.workspaceRCCache.Invalidate(workingDir)
}

// GetUserDataSchema returns the user data schema defined in the workspace's .mittorc file.
func (r *WorkspaceRegistry) GetUserDataSchema(workingDir string) *config.UserDataSchema {
	if r.workspaceRCCache == nil || workingDir == "" {
		return nil
	}

	rc, err := r.workspaceRCCache.Get(workingDir)
	if err != nil {
		if r.logger != nil {
			r.logger.Warn("Failed to load workspace .mittorc for user data schema",
				"working_dir", workingDir,
				"error", err)
		}
		return nil
	}

	if rc == nil {
		return nil
	}

	if rc.Metadata == nil {
		return nil
	}
	return rc.Metadata.UserDataSchema
}
