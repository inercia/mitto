package acp

import (
	"os"
	"strings"

	"github.com/inercia/mitto/internal/appdir"
)

// BuildMittoEnv returns a map of MITTO_* environment variables for use in ACP server commands.
// The map contains context about the current session, working directory, and data paths.
func BuildMittoEnv(sessionID, workingDir, acpServer, workspaceUUID string) map[string]string {
	dataDir := ""
	if d, err := appdir.Dir(); err == nil {
		dataDir = d
	}

	logsDir := ""
	if d, err := appdir.LogsDir(); err == nil {
		logsDir = d
	}

	env := map[string]string{
		"MITTO_SESSION_ID":     sessionID,
		"MITTO_WORKING_DIR":    workingDir,
		"MITTO_ACP_SERVER":     acpServer,
		"MITTO_WORKSPACE_UUID": workspaceUUID,
		"MITTO_DATA_DIR":       dataDir,
		"MITTO_LOGS_DIR":       logsDir,
	}
	// Stamp bd (beads) writes the agent makes in this conversation with a stable
	// per-conversation actor so the audit trail records which Mitto conversation
	// made each change. bd reads BEADS_ACTOR as the default --actor. Only set it
	// when we have a session ID (shared/process-less env builds pass "").
	if sessionID != "" {
		env["BEADS_ACTOR"] = "mitto:" + sessionID
	}
	return env
}

// BuildAgentHintEnv returns a map of "agent hint" environment variables that
// announce agent presence to shells the ACP subprocess later spawns for tool
// use (Bash tool, run_command, hooks, …). Common shell rc snippets use
// AGENT_MODE (and vendor-specific sentinels like AUGMENT_AGENT) to skip heavy
// interactive plugins so agent-issued shell invocations stay fast and quiet.
//
// These are Mitto-authored but user-overridable: BuildACPProcessEnv layers
// them below serverEnv, so a user can set e.g. AGENT_MODE="" in settings.json
// acp_servers[].env to disable them per-server. This is intentionally distinct
// from BuildMittoEnv (MITTO_* identity vars) which stays at the highest
// precedence layer and must not be spoofed.
//
// Currently emits AGENT_MODE=1 unconditionally. AUGMENT_AGENT and other
// agent-specific hints may be added later once acpServer is reliably plumbed
// to every call site (today several callers pass empty acpServer).
func BuildAgentHintEnv(acpServer string) map[string]string {
	_ = acpServer // reserved for future per-agent hints (e.g. AUGMENT_AGENT)
	return map[string]string{
		"AGENT_MODE": "1",
	}
}

// ExpandCommand expands $MITTO_* and ${MITTO_*} references in a command string.
// Non-MITTO variables (e.g. $HOME) are left untouched as literal "$KEY" strings.
// MITTO_* variables not present in mittoEnv are expanded to empty string.
func ExpandCommand(command string, mittoEnv map[string]string) string {
	return os.Expand(command, func(key string) string {
		if !strings.HasPrefix(key, "MITTO_") {
			// Passthrough: preserve the original reference
			return "$" + key
		}
		// MITTO_ variable: return value or empty string if not defined
		return mittoEnv[key]
	})
}

// ExpandArgs expands $MITTO_* and ${MITTO_*} references in each argument individually.
// This should be called AFTER ParseCommand to preserve paths with spaces as single args.
// Non-MITTO variables are left untouched, just like ExpandCommand.
func ExpandArgs(args []string, mittoEnv map[string]string) []string {
	result := make([]string, len(args))
	for i, arg := range args {
		result[i] = ExpandCommand(arg, mittoEnv)
	}
	return result
}
