package coldstart

import (
	"runtime"
	"sync"
)

// ContentionSnapshot is a cheap host-load sample.
type ContentionSnapshot struct {
	NumGoroutine        int     `json:"num_goroutine"`
	NumCPU              int     `json:"num_cpu"`
	ConcurrentPrompting int     `json:"concurrent_prompting"`
	LiveACPProcesses    int     `json:"live_acp_processes"`
	Load1               float64 `json:"load1"`
	LoadAvailable       bool    `json:"load_available"`
}

var (
	providerMu  sync.RWMutex
	promptingFn func() int
	liveACPFn   func() int
)

// SetPromptingCounter registers a provider for ConcurrentPrompting.
// Intended to be called once at startup. Thread-safe.
func SetPromptingCounter(fn func() int) {
	providerMu.Lock()
	promptingFn = fn
	providerMu.Unlock()
}

// SetLiveACPCounter registers a provider for LiveACPProcesses.
// Intended to be called once at startup. Thread-safe.
func SetLiveACPCounter(fn func() int) {
	providerMu.Lock()
	liveACPFn = fn
	providerMu.Unlock()
}

// Contention samples current host load. Cheap to call.
func Contention() ContentionSnapshot {
	s := ContentionSnapshot{
		NumGoroutine:        runtime.NumGoroutine(),
		NumCPU:              runtime.NumCPU(),
		ConcurrentPrompting: -1,
		LiveACPProcesses:    -1,
	}
	providerMu.RLock()
	pf := promptingFn
	lf := liveACPFn
	providerMu.RUnlock()
	if pf != nil {
		s.ConcurrentPrompting = pf()
	}
	if lf != nil {
		s.LiveACPProcesses = lf()
	}
	if l, ok := readLoad1(); ok {
		s.Load1 = l
		s.LoadAvailable = true
	}
	return s
}

// LogAttrs returns a flat []any of key/value pairs suitable to splat
// into slog. Omits load1 when unavailable and prompting/acp when -1.
func (c ContentionSnapshot) LogAttrs() []any {
	attrs := make([]any, 0, 12)
	attrs = append(attrs,
		"num_goroutine", c.NumGoroutine,
		"num_cpu", c.NumCPU,
	)
	if c.ConcurrentPrompting >= 0 {
		attrs = append(attrs, "concurrent_prompting", c.ConcurrentPrompting)
	}
	if c.LiveACPProcesses >= 0 {
		attrs = append(attrs, "live_acp_processes", c.LiveACPProcesses)
	}
	if c.LoadAvailable {
		attrs = append(attrs, "load1", c.Load1)
	}
	return attrs
}
