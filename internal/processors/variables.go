package processors

import (
	"sort"
	"strconv"
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
//   - @mitto:mcp_children_count     — Number of MCP-created child sessions (integer as string)
//   - @mitto:mcp_children           — MCP-created child sessions only, comma-separated
//
// Unknown @mitto: variables are left as-is.
// Empty values substitute to empty string.
//
// To include a literal @mitto:variable without substitution, escape it with a
// backslash: \@mitto:variable. The backslash is stripped and the variable name is
// passed through as-is (e.g. \@mitto:session_id → @mitto:session_id).
//
// The @mitto: prefix is consistent with the existing @namespace:value convention
// used by processor triggers (e.g., @git:status, @file:path).
func SubstituteVariables(message string, input *ProcessorInput) string {
	if !strings.Contains(message, "@mitto:") {
		return message // Fast path: no variables to substitute
	}

	// Handle escape sequences before running substitutions.
	//
	// Two sentinels are used (both contain NUL bytes, which cannot appear in
	// any substitution value):
	//
	//   sentinelBackslash — marks a literal '\' that preceded \\ before @mitto:.
	//     \\@mitto:foo  →  sentinelBackslash + @mitto:foo
	//     After substitution sentinelBackslash is restored to '\', so the
	//     variable IS substituted and the literal backslash is preserved.
	//
	//   sentinelEscaped — marks a \@mitto: that should NOT be substituted.
	//     \@mitto:foo  →  sentinelEscapedfoo
	//     After substitution sentinelEscaped is restored to @mitto:, stripping
	//     the leading backslash.
	//
	// Double-backslash must be handled first to avoid the inner \@mitto: being
	// caught by the single-backslash rule.
	const (
		sentinelBackslash = "\x00MITTO_BACKSLASH\x00"
		sentinelEscaped   = "\x00MITTO_ESCAPED\x00"
	)
	message = strings.ReplaceAll(message, `\\@mitto:`, sentinelBackslash+"@mitto:")
	message = strings.ReplaceAll(message, `\@mitto:`, sentinelEscaped)

	replacements := map[string]string{
		"@mitto:session_id":            input.SessionID,
		"@mitto:parent_session_id":     input.ParentSessionID,
		"@mitto:parent":                formatParentSession(input.ParentSessionID, input.ParentSessionName),
		"@mitto:session_name":          input.SessionName,
		"@mitto:working_dir":           input.WorkingDir,
		"@mitto:acp_server":            input.ACPServer,
		"@mitto:workspace_uuid":        input.WorkspaceUUID,
		"@mitto:available_acp_servers": formatAvailableACPServers(input.AvailableACPServers),
		"@mitto:mcp_children_count":    formatMCPChildrenCount(input.ChildSessions),
		"@mitto:mcp_children":          formatMCPChildren(input.ChildSessions),
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

	// Restore sentinels: escaped variables become literal @mitto: (backslash
	// stripped); double-backslash prefix becomes a single literal backslash.
	result = strings.ReplaceAll(result, sentinelEscaped, "@mitto:")
	result = strings.ReplaceAll(result, sentinelBackslash, `\`)
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

// formatMCPChildrenCount returns the count of MCP-origin children as a string.
func formatMCPChildrenCount(children []ChildSession) string {
	count := 0
	for _, child := range children {
		if child.ChildOrigin == "mcp" {
			count++
		}
	}
	return strconv.Itoa(count)
}

// formatMCPChildren renders only MCP-origin children as a human-readable string.
//
// Format: "id (name) [acp-server], id2 (name2) [acp-server2]"
// If a child has no name, the parenthetical group is omitted.
// If a child has no ACP server, the bracket group is omitted.
func formatMCPChildren(children []ChildSession) string {
	var parts []string
	for _, child := range children {
		if child.ChildOrigin != "mcp" {
			continue
		}
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
