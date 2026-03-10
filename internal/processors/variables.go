package processors

import "strings"

// SubstituteVariables replaces @mitto:variable placeholders in the message
// with values from the processor input context.
//
// Supported variables:
//   - @mitto:session_id             — Current session ID
//   - @mitto:parent_session_id      — Parent conversation ID (empty if root session)
//   - @mitto:session_name           — Conversation title/name
//   - @mitto:working_dir            — Session working directory
//   - @mitto:acp_server             — ACP server name (e.g., "claude-code")
//   - @mitto:workspace_uuid         — Workspace identifier
//   - @mitto:available_acp_servers  — ACP servers with workspaces for this folder,
//     comma-separated with tags and current marker
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
		"@mitto:session_name":          input.SessionName,
		"@mitto:working_dir":           input.WorkingDir,
		"@mitto:acp_server":            input.ACPServer,
		"@mitto:workspace_uuid":        input.WorkspaceUUID,
		"@mitto:available_acp_servers": formatAvailableACPServers(input.AvailableACPServers),
	}

	result := message
	for placeholder, value := range replacements {
		if strings.Contains(result, placeholder) {
			result = strings.ReplaceAll(result, placeholder, value)
		}
	}
	return result
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
