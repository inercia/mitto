package cel

import "strings"

// NormalizeTasksUpstream normalizes a raw beads upstream value (as read from
// folders.json, e.g. via internal/workspaces.FolderBeadsUpstream) into the
// canonical form stored in WorkspaceContext.TasksUpstream: lowercased,
// trimmed, and "none" mapped to "". Every call site that populates
// Workspace.TasksUpstream must pass the raw value through this helper so the
// field is always normalized regardless of what folders.json contains.
func NormalizeTasksUpstream(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "none" {
		return ""
	}
	return s
}
