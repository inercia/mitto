// Package eventprojectionsession adapts internal/session's per-session
// sidecar JSON API to eventprojection.CheckpointStore, so a Checkpoint can
// be durably persisted alongside a Mitto conversation's session directory.
//
// This package intentionally lives OUTSIDE internal/eventprojection (which
// enforces via imports_test.go that it never depends on internal/session)
// so that the pure projection core stays a leaf package; this is its
// session-backed seam implementation, mirroring how mitto-lrt.7 put its
// ACP-backed seam implementation in a higher package than the neutral
// contracts it implements.
package eventprojectionsession

import (
	"errors"
	"fmt"
	"os"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/eventprojection"
	"github.com/inercia/mitto/internal/session"
)

// sidecarName is the file name used for the JSON sidecar written alongside
// a session's other sidecars (queue.json, loop.json, ...).
const sidecarName = "upstream-projection.json"

// checkpointDTO is the on-disk representation of an
// eventprojection.Checkpoint. Kept distinct from the in-memory type so a
// future on-disk schema change doesn't leak into the pure core package.
type checkpointDTO struct {
	Backend         string           `json:"backend"`
	Provider        string           `json:"provider"`
	ProviderSession string           `json:"provider_session"`
	Epoch           int              `json:"epoch"`
	LastCursor      string           `json:"last_cursor"`
	Committed       []string         `json:"committed"`
	IdentitySeq     map[string]int64 `json:"identity_seq"`
}

func toDTO(cp *eventprojection.Checkpoint) checkpointDTO {
	return checkpointDTO{
		Backend:         string(cp.Source.Backend),
		Provider:        string(cp.Source.Provider),
		ProviderSession: string(cp.Source.ProviderSession),
		Epoch:           cp.Epoch,
		LastCursor:      cp.LastCursor,
		Committed:       cp.Committed,
		IdentitySeq:     cp.IdentitySeq,
	}
}

func fromDTO(dto checkpointDTO) *eventprojection.Checkpoint {
	identitySeq := dto.IdentitySeq
	if identitySeq == nil {
		identitySeq = make(map[string]int64)
	}
	return &eventprojection.Checkpoint{
		Source: eventprojection.SourceID{
			Backend:         agentbackend.BackendID(dto.Backend),
			Provider:        agentbackend.ProviderID(dto.Provider),
			ProviderSession: agentbackend.ProviderSessionID(dto.ProviderSession),
		},
		Epoch:       dto.Epoch,
		LastCursor:  dto.LastCursor,
		Committed:   dto.Committed,
		IdentitySeq: identitySeq,
	}
}

// Store implements eventprojection.CheckpointStore by persisting exactly
// one Checkpoint per Mitto session id, in that session's sidecar directory.
type Store struct {
	sessions  *session.Store
	sessionID string
}

var _ eventprojection.CheckpointStore = (*Store)(nil)

// New returns a Store that persists the Checkpoint for sessionID via
// sessions' sidecar JSON API.
func New(sessions *session.Store, sessionID string) *Store {
	return &Store{sessions: sessions, sessionID: sessionID}
}

// Load implements eventprojection.CheckpointStore. Returns
// eventprojection.ErrCheckpointNotFound when no sidecar has been written
// yet for this session (a fresh source, not an error condition); a
// genuinely missing/deleted session (session.ErrSessionNotFound) is
// propagated as-is so callers can distinguish "fresh" from "gone".
func (s *Store) Load(src eventprojection.SourceID) (*eventprojection.Checkpoint, error) {
	var dto checkpointDTO
	err := s.sessions.ReadSessionSidecarJSON(s.sessionID, sidecarName, &dto)
	if err == nil {
		return fromDTO(dto), nil
	}
	if errors.Is(err, session.ErrSessionNotFound) {
		return nil, err
	}
	// The sidecar file itself does not exist yet (fresh session, never
	// saved before) — store_sidecar.go's ReadJSON surfaces this as a raw
	// os.IsNotExist error, distinct from session.ErrSessionNotFound above
	// (which means the whole session directory is gone).
	if os.IsNotExist(err) {
		return nil, eventprojection.ErrCheckpointNotFound
	}
	return nil, fmt.Errorf("eventprojectionsession: loading checkpoint for session %s: %w", s.sessionID, err)
}

// Save implements eventprojection.CheckpointStore.
func (s *Store) Save(cp *eventprojection.Checkpoint) error {
	if err := s.sessions.WriteSessionSidecarJSON(s.sessionID, sidecarName, toDTO(cp)); err != nil {
		return fmt.Errorf("eventprojectionsession: saving checkpoint for session %s: %w", s.sessionID, err)
	}
	return nil
}
