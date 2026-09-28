package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/inercia/mitto/internal/appdir"
	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// HandleSessionMoveAgentPreflight handles
// GET /api/sessions/{id}/move-agent/preflight (mitto-f7yo.2). It reports move
// affordance for a conversation — current agent, valid target candidates,
// busy/archived state, loop info, and descendant count — without performing
// a move, so a UI (mitto-f7yo.6) can decide what to show/enable before the
// user commits to POST .../move-agent.
func (h *Handlers) HandleSessionMoveAgentPreflight(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if h.deps.SessionManager == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Session manager not available")
		return
	}

	preflight, err := h.deps.SessionManager.MoveSessionToAgentPreflight(sessionID)
	if err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "", "Session not found")
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("MoveSessionToAgentPreflight failed", "session_id", sessionID, "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to compute move-agent preflight")
		return
	}

	// Resolve per-candidate loop-prompt availability. This can only happen
	// here (not in SessionManager.MoveSessionToAgentPreflight): it needs the
	// full prompt-merge pipeline (PromptsCache, MittoConfig, per-workspace
	// prompt dirs) that only exists at this layer. See
	// loopPromptAvailableForAgent's doc comment for the name-only-resolution
	// limitation.
	if preflight.IsLoop && preflight.LoopPromptName != "" {
		for i := range preflight.Candidates {
			available := h.loopPromptAvailableForAgent(preflight.WorkingDir, preflight.Candidates[i].Name, preflight.LoopPromptName)
			preflight.Candidates[i].LoopPromptAvailable = &available
		}
	}

	writeJSONOK(w, preflight)
}

// loopPromptAvailableForAgent reports whether a workspace prompt named
// promptName resolves for acpServerName in workingDir, using the same
// sources and priority order as resolvePromptByName in internal/web/server.go
// (global file prompts < settings prompts < ACP-server-specific prompts <
// workspace directory prompts < workspace inline (.mittorc) prompts).
//
// LIMITATION (documented per mitto-f7yo.2's spec): this is a name-only
// resolution. It does NOT evaluate enabledWhen CEL gates, so a prompt that
// exists but would be hidden for the target agent's current permission/tool
// state is still reported as available. Full CEL evaluation would require
// building a per-target-agent PromptEnabledContext (workspace/ACP/tools
// namespaces resolved for an agent this conversation isn't currently running
// against), which needs live capability/tool data not available for a
// not-yet-selected candidate — deferred to a follow-up if this proves
// confusing in practice.
func (h *Handlers) loopPromptAvailableForAgent(workingDir, acpServerName, promptName string) bool {
	if promptName == "" || h.deps.SessionManager == nil {
		return false
	}

	var globalFilePrompts []configPkg.WebPrompt
	if h.deps.PromptsCache != nil {
		if gfp, err := h.deps.PromptsCache.GetWebPrompts(); err == nil {
			globalFilePrompts = gfp
		}
	}

	var settingsPrompts []configPkg.WebPrompt
	if h.deps.MittoConfig != nil {
		settingsPrompts = h.deps.MittoConfig.Prompts
	}

	acpServerType := acpServerName
	if acpServerName != "" && h.deps.MittoConfig != nil {
		if t := h.deps.MittoConfig.GetServerType(acpServerName); t != "" {
			acpServerType = t
		}
	}

	var serverPrompts []configPkg.WebPrompt
	if acpServerType != "" && h.deps.PromptsCache != nil {
		if sp, err := h.deps.PromptsCache.GetWebPromptsSpecificToACP(acpServerType); err == nil {
			serverPrompts = sp
		}
	}
	if acpServerName != "" && h.deps.MittoConfig != nil {
		for _, srv := range h.deps.MittoConfig.ACPServers {
			if srv.Name == acpServerName {
				serverPrompts = append(serverPrompts, srv.Prompts...)
				break
			}
		}
	}

	var dirPrompts []configPkg.WebPrompt
	if h.deps.LoadPromptsFromDirs != nil {
		dirs := append([]string{appdir.WorkspacePromptsDir(workingDir)}, h.deps.SessionManager.GetWorkspacePromptsDirs(workingDir)...)
		dirPrompts = h.deps.LoadPromptsFromDirs(workingDir, dirs)
	}

	inlinePrompts := h.deps.SessionManager.GetWorkspacePrompts(workingDir)

	merged := configPkg.MergePrompts(
		configPkg.MergePrompts(globalFilePrompts, settingsPrompts, serverPrompts),
		nil,
		configPkg.MergePrompts(nil, dirPrompts, inlinePrompts),
	)

	for _, p := range merged {
		if strings.EqualFold(p.Name, promptName) {
			return true
		}
	}
	return false
}

// MoveAgentExecuteRequest is the request body for
// POST /api/sessions/{id}/move-agent.
type MoveAgentExecuteRequest struct {
	TargetAgent     string `json:"target_agent"`
	IncludeChildren bool   `json:"include_children"`
}

// HandleSessionMoveAgentExecute handles POST /api/sessions/{id}/move-agent
// (mitto-f7yo.2). It rebinds sessionID (and, when include_children is set,
// its eligible descendants) to target_agent via
// SessionManager.MoveSessionToAgent, broadcasts a session_agent_moved event
// for every conversation actually moved, and returns the MoveAgentResult.
func (h *Handlers) HandleSessionMoveAgentExecute(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if h.deps.SessionManager == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Session manager not available")
		return
	}

	var req MoveAgentExecuteRequest
	if !parseJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.TargetAgent) == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "target_agent is required")
		return
	}

	result, err := h.deps.SessionManager.MoveSessionToAgent(sessionID, req.TargetAgent, conversation.MoveAgentOptions{
		IncludeChildren: req.IncludeChildren,
	})
	if err != nil {
		switch {
		case errors.Is(err, session.ErrSessionNotFound):
			writeErrorJSON(w, http.StatusNotFound, "", "Session not found")
		case errors.Is(err, conversation.ErrMoveAgentArchived):
			writeErrorJSON(w, http.StatusConflict, "", err.Error())
		case errors.Is(err, conversation.ErrMoveAgentBusy):
			writeErrorJSON(w, http.StatusConflict, "", err.Error())
		case errors.Is(err, conversation.ErrMoveAgentSameAgent),
			errors.Is(err, conversation.ErrMoveAgentUnknownTarget),
			errors.Is(err, conversation.ErrMoveAgentNoWorkspace):
			writeErrorJSON(w, http.StatusBadRequest, "", err.Error())
		default:
			if h.deps.Logger != nil {
				h.deps.Logger.Error("MoveSessionToAgent failed",
					"session_id", sessionID, "target_agent", req.TargetAgent, "error", err)
			}
			writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to move conversation to agent")
		}
		return
	}

	// Broadcast an agent-moved event for the moved session and every moved
	// child so every connected client can refresh acp_server for that row.
	// The frontend handler for this message type is added by the UI bead
	// (mitto-f7yo.6); until then this is a harmless no-op broadcast.
	if h.deps.BroadcastSessionAgentMoved != nil {
		for _, movedID := range result.Moved {
			h.deps.BroadcastSessionAgentMoved(movedID, req.TargetAgent, result.PreviousAgent)
		}
	}

	writeJSONOK(w, moveAgentResultJSON(result))
}

// moveAgentResultJSON builds the wire representation of a MoveAgentResult.
// A plain map (rather than adding json tags to MoveAgentResult itself) keeps
// ResumeError's non-nil-error-to-string translation local to the REST layer;
// MoveAgentResult.ResumeError is deliberately `json:"-"` since an error value
// doesn't marshal usefully on its own.
func moveAgentResultJSON(result conversation.MoveAgentResult) map[string]interface{} {
	resp := map[string]interface{}{
		"moved":                   result.Moved,
		"skipped":                 result.Skipped,
		"previous_agent":          result.PreviousAgent,
		"previous_baseline_model": result.PreviousBaselineModel,
	}
	if result.ResumeError != nil {
		resp["resume_error"] = result.ResumeError.Error()
	}
	return resp
}
