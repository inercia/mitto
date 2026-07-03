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
