package web

// buildCallbackIndex scans all sessions at startup and builds the in-memory token index.
// This is called once during server initialization.
//
// This is a server-lifecycle helper (not an HTTP handler), so it stays in the web
// package while the callback REST handlers live in internal/web/handlers.
func (s *Server) buildCallbackIndex() {
	store := s.Store()
	if store == nil {
		return
	}

	sessions, err := store.List()
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to list sessions for callback index", "error", err)
		}
		return
	}

	for _, meta := range sessions {
		cs := store.Callback(meta.SessionID)
		if cb, err := cs.Get(); err == nil {
			s.callbackIndex.Register(cb.Token, meta.SessionID)
		}
	}

	if s.logger != nil {
		s.logger.Info("Callback index built", "tokens", s.callbackIndex.Count())
	}
}
