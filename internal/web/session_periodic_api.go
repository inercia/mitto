package web

import (
	configPkg "github.com/inercia/mitto/internal/config"
)

// periodicDelayFloor returns the configured global floor for the on-completion delay.
// Falls back to the package default when the periodic runner is unavailable (e.g. tests).
//
// This server-internal lifecycle helper stays in the web package and is wired into the
// handlers sub-package via Deps.PeriodicDelayFloor; the HTTP handlers themselves live in
// internal/web/handlers/session_periodic*.go.
func (s *Server) periodicDelayFloor() int {
	if s.periodicRunner != nil {
		return s.periodicRunner.MinPeriodicCompletionDelaySeconds()
	}
	return configPkg.DefaultMinPeriodicCompletionDelaySeconds
}
