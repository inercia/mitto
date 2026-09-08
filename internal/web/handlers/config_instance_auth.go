package handlers

import (
	"net/http"

	"github.com/inercia/mitto/internal/config/configsvc"
)

// validateInstanceBearer enforces the instance-bearer gate shared by
// HandleConfigSnapshot and HandleConfigPatch (mitto-4rz.3): see
// Deps.ValidateInstanceBearer's doc comment for the full contract. It writes
// the error response itself and returns false when the request must be
// rejected.
func (h *Handlers) validateInstanceBearer(w http.ResponseWriter, r *http.Request) bool {
	if h.deps.ValidateInstanceBearer == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "", "Instance-authenticated config API is not available")
		return false
	}
	if !h.deps.ValidateInstanceBearer(r) {
		writeErrorJSON(w, http.StatusUnauthorized, "", "Missing or invalid instance bearer token")
		return false
	}
	return true
}

// writeConfigsvcError maps a configsvc error to the canonical HTTP error
// envelope (docs/devel/rest-api-conventions.md §4). It never echoes a
// value: configsvc.Error.Msg is a fixed, non-value-carrying description and
// Path is safe to display (see internal/config/configsvc/errors.go).
func writeConfigsvcError(w http.ResponseWriter, err error) {
	cerr, ok := err.(*configsvc.Error)
	if !ok {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Configuration error: "+err.Error())
		return
	}

	status := http.StatusInternalServerError
	switch cerr.Kind {
	case configsvc.ErrKindRevisionMismatch, configsvc.ErrKindLocked:
		status = http.StatusConflict
	case configsvc.ErrKindRejected, configsvc.ErrKindReadOnly:
		status = http.StatusForbidden
	case configsvc.ErrKindUnknownField, configsvc.ErrKindValidation, configsvc.ErrKindConflict, configsvc.ErrKindStructure:
		status = http.StatusBadRequest
	case configsvc.ErrKindNotFound:
		status = http.StatusNotFound
	case configsvc.ErrKindIO:
		status = http.StatusInternalServerError
	}
	writeErrorJSON(w, status, "", cerr.Error())
}
