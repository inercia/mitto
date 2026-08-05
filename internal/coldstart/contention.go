package coldstart

import (
	"runtime"
	"sync"
)

// ContentionSnapshot is a cheap host-load sample.
type ContentionSnapshot struct {
	NumGoroutine        int `json:"num_goroutine"`
	NumCPU              int `json:"num_cpu"`
	ConcurrentPrompting int `json:"concurrent_prompting"`
	LiveACPProcesses    int `json:"live_acp_processes"`
	// ConnectedWSClients is the number of currently connected session
	// WebSocket clients, summed across all sessions. -1 when no provider is
	// registered (mitto-x3x).
	ConnectedWSClients int `json:"connected_ws_clients"`
	// OpenMCPSSEStreams is the number of currently open long-lived MCP
	// Streamable-HTTP GET (keepalive) streams. -1 when no provider is
	// registered (mitto-x3x).
	OpenMCPSSEStreams int     `json:"open_mcp_sse_streams"`
	Load1             float64 `json:"load1"`
	LoadAvailable     bool    `json:"load_available"`
}

var (
	providerMu    sync.RWMutex
	promptingFn   func() int
	liveACPFn     func() int
	connectedWSFn func() int
	openMCPSSEFn  func() int
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

// SetConnectedWSCounter registers a provider for ConnectedWSClients.
// Intended to be called once at startup. Thread-safe. Part of the
// per-category goroutine attribution added in mitto-x3x.
func SetConnectedWSCounter(fn func() int) {
	providerMu.Lock()
	connectedWSFn = fn
	providerMu.Unlock()
}

// SetOpenMCPStreamCounter registers a provider for OpenMCPSSEStreams.
// Intended to be called once at startup. Thread-safe. Part of the
// per-category goroutine attribution added in mitto-x3x.
func SetOpenMCPStreamCounter(fn func() int) {
	providerMu.Lock()
	openMCPSSEFn = fn
	providerMu.Unlock()
}

// Contention samples current host load. Cheap to call.
func Contention() ContentionSnapshot {
	s := ContentionSnapshot{
		NumGoroutine:        runtime.NumGoroutine(),
		NumCPU:              runtime.NumCPU(),
		ConcurrentPrompting: -1,
		LiveACPProcesses:    -1,
		ConnectedWSClients:  -1,
		OpenMCPSSEStreams:   -1,
	}
	providerMu.RLock()
	pf := promptingFn
	lf := liveACPFn
	wf := connectedWSFn
	mf := openMCPSSEFn
	providerMu.RUnlock()
	if pf != nil {
		s.ConcurrentPrompting = pf()
	}
	if lf != nil {
		s.LiveACPProcesses = lf()
	}
	if wf != nil {
		s.ConnectedWSClients = wf()
	}
	if mf != nil {
		s.OpenMCPSSEStreams = mf()
	}
	if l, ok := readLoad1(); ok {
		s.Load1 = l
		s.LoadAvailable = true
	}
	return s
}

// LogAttrs returns a flat []any of key/value pairs suitable to splat
// into slog. Omits load1 when unavailable and prompting/acp/ws/sse when -1.
func (c ContentionSnapshot) LogAttrs() []any {
	attrs := make([]any, 0, 16)
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
	if c.ConnectedWSClients >= 0 {
		attrs = append(attrs, "connected_ws_clients", c.ConnectedWSClients)
	}
	if c.OpenMCPSSEStreams >= 0 {
		attrs = append(attrs, "open_mcp_sse_streams", c.OpenMCPSSEStreams)
	}
	if c.LoadAvailable {
		attrs = append(attrs, "load1", c.Load1)
	}
	return attrs
}
