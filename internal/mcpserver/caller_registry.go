package mcpserver

import (
	"context"
	"strings"
	"time"
)

const (
	maxACPChildRecords     = 1024
	maxACPChildQuarantines = 1024
	maxACPChildDepth       = 32
	maxACPChildIDBytes     = 256
	maxACPChildWait        = 250 * time.Millisecond
	maxACPChildWaiters     = 128
)

type acpRootBinding struct {
	rootID     string
	generation uint64
}

type acpChildBinding struct {
	owner      string
	rootID     string
	parentID   string
	generation uint64
	depth      int
	terminal   bool
}

// ACPChildStatus is the caller-registry view of a validated child lifecycle
// event. Keeping the registry independent of conversation avoids an import
// cycle: conversation handlers already depend on mcpserver.
type ACPChildStatus struct {
	RootSessionID   string
	ChildSessionID  string
	ParentSessionID string
	Depth           int
	Type            string
	Status          string
}

// BindACPRootGeneration binds the conversation's current ACP root and returns
// a generation token for child lifecycle updates. A child session must never
// be passed to BindACPSession; doing so would replace this root binding.
func (s *Server) BindACPRootGeneration(convID, rootID string) (uint64, bool) {
	if convID == "" || !validACPChildID(rootID) {
		return 0, false
	}
	s.sessionsMu.RLock()
	owner := s.sessions[convID]
	if owner == nil {
		s.sessionsMu.RUnlock()
		return 0, false
	}
	s.acpSessionsMu.Lock()
	defer s.acpSessionsMu.Unlock()
	s.sessionsMu.RUnlock()

	if s.acpIDQuarantinedLocked(rootID) {
		return 0, false
	}
	if s.acpChildRegistryFull {
		return 0, false
	}
	if child := s.acpChildren[rootID]; child != nil {
		s.quarantineACPChildIDLocked(rootID, convID, child.owner)
		return 0, false
	}
	if !s.bindACPSessionLocked(convID, rootID) {
		return 0, false
	}
	generation := s.acpGeneration.Add(1)
	s.acpRootGenerations[convID] = acpRootBinding{rootID: rootID, generation: generation}
	s.dropACPChildClaimsLocked(convID)
	for id, child := range s.acpChildren {
		if child.owner == convID {
			delete(s.acpChildren, id)
		}
	}
	return generation, true
}

// ApplyACPChildStatus applies a validated lifecycle event only to the current
// root generation. Identity is admitted only on created; terminal records
// remain as ancestry for live descendants but cannot authorize calls.
func (s *Server) ApplyACPChildStatus(convID string, generation uint64, update ACPChildStatus) bool {
	rootID, childID, parentID := update.RootSessionID, update.ChildSessionID, update.ParentSessionID
	if convID == "" || generation == 0 || !validACPChildID(rootID) || !validACPChildID(childID) ||
		!validACPChildID(parentID) || rootID == childID || childID == parentID || update.Depth < 1 || update.Depth > maxACPChildDepth {
		return false
	}
	s.sessionsMu.RLock()
	owner := s.sessions[convID]
	if owner == nil {
		s.sessionsMu.RUnlock()
		return false
	}
	s.acpSessionsMu.Lock()
	defer s.acpSessionsMu.Unlock()
	s.sessionsMu.RUnlock()

	root, ok := s.acpRootGenerations[convID]
	if !ok || root.rootID != rootID || root.generation != generation || s.acpSessions[rootID] != convID {
		return false
	}
	if s.acpIDQuarantinedLocked(childID) {
		return false
	}
	if s.acpSessions[childID] != "" {
		s.quarantineACPChildIDLocked(childID, convID, s.acpSessions[childID])
		return false
	}
	if existing := s.acpChildren[childID]; existing != nil {
		if existing.owner != convID || existing.rootID != rootID || existing.generation != generation || existing.parentID != parentID || existing.depth != update.Depth {
			s.quarantineACPChildIDLocked(childID, convID, existing.owner)
			return false
		}
		if update.Type == "update" {
			return !existing.terminal
		}
		switch update.Status {
		case "created", "running":
			return !existing.terminal
		case "completed", "failed", "interrupted":
			existing.terminal = true
			return true
		default:
			return false
		}
	}
	if update.Type != "status" || update.Status != "created" || s.childCountForGenerationLocked(convID, generation) >= maxACPChildRecords {
		return false
	}
	if update.Depth == 1 {
		if parentID != rootID {
			return false
		}
	} else {
		parent := s.acpChildren[parentID]
		if parent == nil || parent.owner != convID || parent.rootID != rootID || parent.generation != generation || parent.depth+1 != update.Depth {
			return false
		}
	}
	s.acpChildren[childID] = &acpChildBinding{owner: convID, rootID: rootID, parentID: parentID, generation: generation, depth: update.Depth}
	return true
}

func (s *Server) childCountForGenerationLocked(convID string, generation uint64) int {
	count := 0
	for _, child := range s.acpChildren {
		if child.owner == convID && child.generation == generation {
			count++
		}
	}
	return count
}

// UnbindACPRootGeneration removes a root and its generation-scoped child data.
func (s *Server) UnbindACPRootGeneration(convID string, generation uint64) {
	s.acpSessionsMu.Lock()
	defer s.acpSessionsMu.Unlock()
	root, ok := s.acpRootGenerations[convID]
	if !ok || root.generation != generation {
		return
	}
	delete(s.acpRootGenerations, convID)
	s.dropACPChildClaimsLocked(convID)
	for id, child := range s.acpChildren {
		if child.owner == convID && child.generation == generation {
			delete(s.acpChildren, id)
		}
	}
	s.unmapConvLocked(convID)
	s.dropACPClaimLocked(convID, "")
}

// dropACPChildClaimsLocked removes a conversation from child-ID quarantine
// claimant sets, clearing a quarantined ID only after every claimant leaves.
func (s *Server) dropACPChildClaimsLocked(convID string) {
	for id, claimants := range s.acpChildQuarantine {
		delete(claimants, convID)
		if len(claimants) == 0 {
			delete(s.acpChildQuarantine, id)
		}
	}
	if len(s.acpChildQuarantine) < maxACPChildQuarantines {
		s.acpChildRegistryFull = false
	}
}

func validACPChildID(id string) bool {
	return strings.TrimSpace(id) == id && id != "" && len(id) <= maxACPChildIDBytes
}

func (s *Server) acpIDQuarantinedLocked(id string) bool {
	_, rootQuarantined := s.acpAmbiguous[id]
	_, childQuarantined := s.acpChildQuarantine[id]
	return rootQuarantined || childQuarantined
}

func (s *Server) quarantineACPChildIDLocked(id string, owners ...string) {
	set := s.acpChildQuarantine[id]
	if set == nil {
		if len(s.acpChildQuarantine) >= maxACPChildQuarantines {
			s.acpChildRegistryFull = true
			return
		}
		set = make(map[string]struct{})
		s.acpChildQuarantine[id] = set
	}
	for _, owner := range owners {
		if owner != "" {
			set[owner] = struct{}{}
		}
	}
	delete(s.acpChildren, id)
	if rootOwner := s.acpSessions[id]; rootOwner != "" {
		delete(s.acpSessions, id)
		delete(s.acpByConv, rootOwner)
		s.acpAmbiguous[id] = map[string]struct{}{rootOwner: {}}
	}
}

func (s *Server) resolveACPCaller(acpID string) string {
	s.acpSessionsMu.Lock()
	defer s.acpSessionsMu.Unlock()
	if s.acpChildRegistryFull {
		return ""
	}
	if s.acpIDQuarantinedLocked(acpID) {
		return ""
	}
	if owner := s.acpSessions[acpID]; owner != "" {
		return owner
	}
	child := s.acpChildren[acpID]
	if child == nil || child.terminal {
		return ""
	}
	root, ok := s.acpRootGenerations[child.owner]
	if !ok || root.rootID != child.rootID || root.generation != child.generation || s.acpSessions[child.rootID] != child.owner {
		return ""
	}
	return child.owner
}

func (s *Server) waitForACPChild(ctx context.Context, acpID string) string {
	if !validACPChildID(acpID) {
		return ""
	}
	if s.acpChildWaiters.Add(1) > maxACPChildWaiters {
		s.acpChildWaiters.Add(-1)
		return ""
	}
	defer s.acpChildWaiters.Add(-1)
	timer := time.NewTimer(maxACPChildWait)
	defer timer.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if owner := s.resolveACPCaller(acpID); owner != "" {
			return owner
		}
		select {
		case <-ctx.Done():
			return ""
		case <-timer.C:
			return ""
		case <-tick.C:
		}
	}
}
