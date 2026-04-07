package processors

import (
	"sort"
	"strings"
)

// SubstituteVariables replaces @mitto:variable placeholders in the message
// with values from the processor input context.
//
// Supported variables:
//   - @mitto:session_id             — Current session ID
//   - @mitto:parent_session_id      — Parent conversation ID (empty if root session)
//   - @mitto:parent                 — Parent session formatted as "id (name)" or empty
//   - @mitto:session_name           — Conversation title/name
//   - @mitto:working_dir            — Session working directory
//   - @mitto:acp_server             — ACP server name (e.g., "claude-code")
//   - @mitto:workspace_uuid         — Workspace identifier
//   - @mitto:available_acp_servers  — ACP servers with workspaces for this folder,
//     comma-separated with tags and current marker
//   - @mitto:children               — Child sessions, comma-separated with names and ACP servers
//
// Unknown @mitto: variables are left as-is.
// Empty values substitute to empty string.
//
// The @mitto: prefix is consistent with the existing @namespace:value convention
// used by processor triggers (e.g., @git:status, @file:path).
func SubstituteVariables(message string, input *ProcessorInput) string {
	if !strings.Contains(message, "@mitto:") {
		return message // Fast path: no variables to substitute
	}

	replacements := map[string]string{
		"@mitto:session_id":            input.SessionID,
		"@mitto:parent_session_id":     input.ParentSessionID,
		"@mitto:parent":                formatParentSession(input.ParentSessionID, input.ParentSessionName),
		"@mitto:session_name":          input.SessionName,
		"@mitto:working_dir":           input.WorkingDir,
		"@mitto:acp_server":            input.ACPServer,
		"@mitto:workspace_uuid":        input.WorkspaceUUID,
		"@mitto:available_acp_servers": formatAvailableACPServers(input.AvailableACPServers),
		"@mitto:children":              formatChildSessions(input.ChildSessions),
	}

	// Sort placeholders by length descending to prevent prefix collisions.
	// e.g., @mitto:parent_session_id must be substituted before @mitto:parent.
	keys := make([]string, 0, len(replacements))
	for k := range replacements {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return len(keys[i]) > len(keys[j])
	})

	result := message
	for _, placeholder := range keys {
		if strings.Contains(result, placeholder) {
			result = strings.ReplaceAll(result, placeholder, replacements[placeholder])
		}
	}
	return result
}

// formatParentSession renders the parent session reference as "id (name)".
// If the parent ID is empty, returns empty string.
// If the parent name is empty, returns just the ID.
func formatParentSession(parentID, parentName string) string {
	if parentID == "" {
		return ""
	}
	if parentName != "" {
		return parentID + " (" + parentName + ")"
	}
	return parentID
}

// formatChildSessions renders the child session list as a human-readable
// comma-separated string.
//
// Format: "id (name) [acp-server], id2 (name2) [acp-server2]"
// If a child has no name, the parenthetical group is omitted.
// If a child has no ACP server, the bracket group is omitted.
func formatChildSessions(children []ChildSession) string {
	if len(children) == 0 {
		return ""
	}
	parts := make([]string, 0, len(children))
	for _, child := range children {
		s := child.ID
		if child.Name != "" {
			s += " (" + child.Name + ")"
		}
		if child.ACPServer != "" {
			s += " [" + child.ACPServer + "]"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

// formatAvailableACPServers renders the available ACP server list as a human-readable
// comma-separated string, matching the structure reported by the MCP tool.
//
// Format: "name [tag1, tag2] (current), name2 [tag3]"
// If a server has no tags the bracket group is omitted.
// The "(current)" marker is appended to the entry for the active server.
func formatAvailableACPServers(servers []AvailableACPServer) string {
	if len(servers) == 0 {
		return ""
	}
	parts := make([]string, 0, len(servers))
	for _, srv := range servers {
		s := srv.Name
		if len(srv.Tags) > 0 {
			s += " [" + strings.Join(srv.Tags, ", ") + "]"
		}
		if srv.Current {
			s += " (current)"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}
