package conversation

// resolveResumeTargetLocked resolves the ACP server, command, cwd, env, workspace,
// and ACP session ID to use when resuming a persisted session. Extracted from
// resumeSessionWithConstraint as part of the file-decomposition epic (mitto-90f.1);
// the logic is behavior-preserving — every log line, fallback ordering, and
// store.UpdateMetadata call is identical to the pre-extraction inline code.
//
// Locking contract: the caller MUST already hold sm.mu (write lock). This helper
// does not lock or unlock anything itself; it only reads sm.wsRegistry / sm.mittoConfig
// and (best-effort) writes rescued metadata back to the passed store.

import (
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// resolveResumeTargetLocked picks the ACP command/cwd/env, ACP server name,
// workspace UUID + settings, and ACP session ID for a resume attempt.
//
// The returned err is currently always nil — GetMetadata failures fall through
// with zero values, matching the pre-extraction behavior. The signature reserves
// err for future callers that want to distinguish "no metadata" from "orphaned
// no rescue" without inspecting acpCommand.
func (sm *SessionManager) resolveResumeTargetLocked(sessionID, workingDir string, store *session.Store) (
	acpServer, acpCommand, acpCwd string,
	acpEnv map[string]string,
	workspaceUUID string,
	foundWs *config.WorkspaceSettings,
	acpSessionID string,
	err error,
) {
	// Determine ACP command, cwd, server, and workspace UUID from workspace configuration.
	// Command/cwd/env are always resolved from global ACP server config at runtime via
	// resolveWorkspaceACPLocked; ACPCommandOverride (per-workspace) takes priority.
	// Try to find a workspace by working directory. If the session metadata later
	// identifies a specific ACP server, this provisional choice will be replaced
	// with the exact workspace for that server.
	foundWs = sm.wsRegistry.GetWorkspaceByDirAndACP(workingDir, "")
	if foundWs != nil {
		acpCommand, acpCwd, acpEnv = sm.wsRegistry.ResolveWorkspaceACP(foundWs)
		acpServer = foundWs.ACPServer
		workspaceUUID = foundWs.UUID
	} else {
		defWs := sm.wsRegistry.GetDefaultWorkspace()
		if defWs != nil {
			acpCommand, acpCwd, acpEnv = sm.wsRegistry.ResolveWorkspaceACP(defWs)
			acpServer = defWs.ACPServer
			workspaceUUID = defWs.UUID
		}
	}

	// Get session metadata for ACP session ID and server name
	if store != nil {
		if meta, err := store.GetMetadata(sessionID); err == nil {
			// Get ACP session ID for potential resumption
			acpSessionID = meta.ACPSessionID

			// On the final retry attempt before archiving, skip ACP session resume
			// and try a fresh session instead. The resume itself may be causing the failure.
			if meta.ACPStartFailureCount >= ACPStartFailureThreshold-1 && acpSessionID != "" {
				if sm.logger != nil {
					sm.logger.Info("Final retry: skipping ACP resume, trying fresh session",
						"session_id", sessionID,
						"failure_count", meta.ACPStartFailureCount,
						"threshold", ACPStartFailureThreshold)
				}
				acpSessionID = ""
			}

			// IMPORTANT: Use ACP server from session metadata, not workspace config.
			// The session was created with a specific ACP server, and resuming it
			// should use the same server regardless of current workspace defaults.
			// Only fall back to workspace config if metadata doesn't have the server.
			if meta.ACPServer != "" {
				acpServer = meta.ACPServer

				// IMPORTANT: Re-resolve the workspace using BOTH working directory and
				// ACP server. The provisional workspace chosen above may point to the
				// same directory but a different ACP server, which would incorrectly
				// reuse the wrong shared ACP process.
				foundWs = sm.wsRegistry.resolveWorkspaceForACP(workingDir, acpServer)
				if foundWs != nil {
					workspaceUUID = foundWs.UUID
					// Resolve command/cwd/env from the re-resolved workspace.
					// ResolveWorkspaceACP applies ACPCommandOverride if set,
					// otherwise looks up from global config.
					acpCommand, acpCwd, acpEnv = sm.wsRegistry.ResolveWorkspaceACP(foundWs)
					if sm.logger != nil && foundWs.ACPCommandOverride != "" {
						sm.logger.Debug("Using workspace command override",
							"session_id", sessionID,
							"acp_server", acpServer,
							"acp_command", acpCommand,
							"acp_command_override", foundWs.ACPCommandOverride)
					} else if sm.logger != nil {
						sm.logger.Debug("Using ACP command from session metadata server",
							"session_id", sessionID,
							"acp_server", acpServer,
							"acp_command", acpCommand)
					}
				} else {
					// No workspace matches the session's stored ACP server. Two cases:
					//   1. The server still exists in global config but no workspace
					//      references it — resolve the command directly from config and
					//      keep the stored agent (but disable shared workspace resolution,
					//      since no workspace owns it).
					//   2. The server was renamed or removed (orphaned conversation) —
					//      rescue it by adopting a workspace configured for the same
					//      working directory, so the conversation remains resumable
					//      instead of failing with "empty command".
					workspaceUUID = ""

					var serverExists bool
					if sm.mittoConfig != nil {
						if server, err := sm.mittoConfig.GetServer(acpServer); err == nil {
							acpCommand = server.Command
							acpCwd = server.Cwd
							acpEnv = server.Env
							serverExists = true
						}
					}

					if serverExists {
						// Case 1: stored server still configured, just no workspace owns
						// it. Do NOT keep a mismatched workspace from the same directory,
						// otherwise shared ACP process lookup can mix different agents.
						if sm.logger != nil {
							sm.logger.Warn("No matching workspace for resumed session ACP server; disabling shared workspace resolution",
								"session_id", sessionID,
								"working_dir", workingDir,
								"acp_server", acpServer)
						}
					} else {
						// Case 2: orphaned conversation — the stored ACP server no longer
						// exists in config. Rescue it by adopting any workspace configured
						// for the same working directory. We fully adopt the rescue
						// workspace's identity (server name + command), so shared ACP
						// process lookup stays consistent and does not mix agents.
						rescueWs := sm.wsRegistry.resolveWorkspaceForACP(workingDir, "")
						var rescueCmd, rescueCwd string
						var rescueEnv map[string]string
						if rescueWs != nil {
							rescueCmd, rescueCwd, rescueEnv = sm.wsRegistry.ResolveWorkspaceACP(rescueWs)
						}
						if rescueWs != nil && rescueCmd != "" {
							foundWs = rescueWs
							workspaceUUID = rescueWs.UUID
							acpCommand, acpCwd, acpEnv = rescueCmd, rescueCwd, rescueEnv
							if sm.logger != nil {
								sm.logger.Warn("Orphaned conversation: stored ACP server not found in config; rescuing with a workspace for the same folder",
									"session_id", sessionID,
									"working_dir", workingDir,
									"missing_acp_server", acpServer,
									"rescued_acp_server", rescueWs.ACPServer)
							}
							acpServer = rescueWs.ACPServer
							// Persist the rescued ACP server name so the next resume resolves
							// directly instead of re-rescuing (and re-emitting the orphaned WARN)
							// on every loop/queue sweep. Best-effort: a failure here does not
							// block the resume itself.
							if store != nil {
								if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
									m.ACPServer = rescueWs.ACPServer
								}); err != nil && sm.logger != nil {
									sm.logger.Warn("Failed to persist rescued ACP server name to metadata",
										"session_id", sessionID,
										"rescued_acp_server", rescueWs.ACPServer,
										"error", err)
								}
							}
						} else {
							// Nothing to rescue with — no workspace for this folder.
							// Leave the command empty; resume will fail with a clear error.
							acpCommand = ""
							acpCwd = ""
							acpEnv = nil
							if sm.logger != nil {
								sm.logger.Warn("Orphaned conversation: stored ACP server not found and no workspace for folder; cannot resume",
									"session_id", sessionID,
									"working_dir", workingDir,
									"acp_server", acpServer)
							}
						}
					}
				}
			}
		}
	}

	return
}
