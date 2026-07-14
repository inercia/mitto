package conversation

import (
	"log/slog"
	"sort"
)

// DuplicateDirGroup describes a group of workspace UUIDs that share the same
// WorkingDir. Used by DuplicateWorkingDirs to surface cold-start MCP contention
// risk when several UUIDs point at one folder.
type DuplicateDirGroup struct {
	WorkingDir string
	UUIDs      []string
	ACPServers []string
	SameACP    bool
}

// DuplicateWorkingDirs returns one DuplicateDirGroup per WorkingDir that has
// more than one registered workspace UUID. WorkingDir=="" is ignored (CLI default).
// SameACP is true iff all UUIDs in the group share the same non-empty ACPServer.
// Pure/read-only; safe to call from anywhere.
func (r *WorkspaceRegistry) DuplicateWorkingDirs() []DuplicateDirGroup {
	r.mu.RLock()
	defer r.mu.RUnlock()

	byDir := make(map[string]*DuplicateDirGroup)
	acpSeen := make(map[string]map[string]struct{})
	acpAllSame := make(map[string]bool)
	acpFirst := make(map[string]string)

	for _, ws := range r.workspaces {
		dir := ws.WorkingDir
		if dir == "" {
			continue
		}
		g, ok := byDir[dir]
		if !ok {
			g = &DuplicateDirGroup{WorkingDir: dir}
			byDir[dir] = g
			acpSeen[dir] = make(map[string]struct{})
			acpAllSame[dir] = true
			acpFirst[dir] = ws.ACPServer
		} else {
			if ws.ACPServer != acpFirst[dir] {
				acpAllSame[dir] = false
			}
		}
		g.UUIDs = append(g.UUIDs, ws.UUID)
		if _, seen := acpSeen[dir][ws.ACPServer]; !seen {
			acpSeen[dir][ws.ACPServer] = struct{}{}
			g.ACPServers = append(g.ACPServers, ws.ACPServer)
		}
	}

	out := make([]DuplicateDirGroup, 0)
	for dir, g := range byDir {
		if len(g.UUIDs) < 2 {
			continue
		}
		sort.Strings(g.UUIDs)
		sort.Strings(g.ACPServers)
		g.SameACP = acpAllSame[dir] && acpFirst[dir] != ""
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WorkingDir < out[j].WorkingDir })
	return out
}

// LogDuplicateWorkingDirs emits one line per duplicate group. No-op when none.
// Severity depends on whether the duplication is expected:
//   - SameACP=false → INFO ("multiple workspaces registered for folder"): the
//     supported multi-agent-per-folder configuration (one folder registered under
//     several ACP servers → several UUIDs).
//   - SameACP=true  → WARN ("workspace duplication detected"): same folder+ACP
//     registered under different UUIDs, which is a redundant registration.
func (r *WorkspaceRegistry) LogDuplicateWorkingDirs(logger *slog.Logger) {
	if logger == nil {
		return
	}
	for _, g := range r.DuplicateWorkingDirs() {
		attrs := []any{
			"working_dir", g.WorkingDir,
			"uuid_count", len(g.UUIDs),
			"uuids", g.UUIDs,
			"acp_servers", g.ACPServers,
			"same_acp", g.SameACP,
		}
		if g.SameACP {
			logger.Warn("workspace duplication detected", attrs...)
		} else {
			logger.Info("multiple workspaces registered for folder", attrs...)
		}
	}
}
