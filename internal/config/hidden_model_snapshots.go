package config

import (
	"sync"
	"sync/atomic"
)

// HiddenModelSnapshot is an immutable, compiled configuration of hidden-model
// policies. A nil policy explicitly means no rules for that configured server.
// Its version is allocated before compilation so a slow older preparation cannot
// replace an already published newer configuration.
type HiddenModelSnapshot struct {
	version  uint64
	policies map[string]*HiddenModelPolicy
}

func (s *HiddenModelSnapshot) Version() uint64 {
	if s == nil {
		return 0
	}
	return s.version
}

func (s *HiddenModelSnapshot) Policy(server string) *HiddenModelPolicy {
	if s == nil {
		return nil
	}
	return s.policies[server]
}

// HiddenModelPolicies owns runtime publication, independently of mutable Config
// slices. The zero value is ready for use. No caller-owned slices are retained.
type HiddenModelPolicies struct {
	next    atomic.Uint64
	mu      sync.RWMutex
	current *HiddenModelSnapshot
}

func (p *HiddenModelPolicies) Prepare(servers []ACPServer) (*HiddenModelSnapshot, error) {
	snapshot := &HiddenModelSnapshot{
		version: p.next.Add(1), policies: make(map[string]*HiddenModelPolicy, len(servers)),
	}
	for _, server := range servers {
		policy, err := CompileHiddenModels(server.Name, server.HiddenModels)
		if err != nil {
			return nil, err
		}
		if len(NormalizeHiddenModels(server.HiddenModels)) == 0 {
			policy = nil
		}
		snapshot.policies[server.Name] = policy
	}
	return snapshot, nil
}

// Publish rejects stale (including duplicate) publications. It invokes no code
// supplied by callers; refreshes/notifications must run after it returns.
func (p *HiddenModelPolicies) Publish(snapshot *HiddenModelSnapshot) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if snapshot == nil || snapshot.Version() <= p.current.Version() {
		return false
	}
	p.current = snapshot
	return true
}

func (p *HiddenModelPolicies) Snapshot() *HiddenModelSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.current
}

// Read pins the published snapshot for a short in-memory view commit. The caller
// must release it before any callback, RPC, storage, or observer invocation. This
// is not a prompt-admission token and must never span a turn or model operation.
func (p *HiddenModelPolicies) Read() (*HiddenModelSnapshot, func()) {
	p.mu.RLock()
	return p.current, p.mu.RUnlock
}

// Admit runs check against the current immutable publication while excluding a
// concurrent Publish.  It is deliberately limited to an in-memory final
// handoff: check must not invoke callbacks, storage, or an RPC.  Callers use it
// immediately before handing work to their transport, so work prepared before a
// settings save is never admitted under an older snapshot.
func (p *HiddenModelPolicies) Admit(check func(*HiddenModelSnapshot) error) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return check(p.current)
}
