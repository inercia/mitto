package web

import (
	configPkg "github.com/inercia/mitto/internal/config"
)

// loopDelayFloor returns the configured global floor for the on-completion delay.
// Falls back to the package default when the loop runner is unavailable (e.g. tests).
//
// This server-internal lifecycle helper stays in the web package and is wired into the
// handlers sub-package via Deps.LoopDelayFloor; the HTTP handlers themselves live in
// internal/web/handlers/session_loop*.go.
func (s *Server) loopDelayFloor() int {
	if s.loopRunner != nil {
		return s.loopRunner.MinLoopCompletionDelaySeconds()
	}
	return configPkg.DefaultMinLoopCompletionDelaySeconds
}
