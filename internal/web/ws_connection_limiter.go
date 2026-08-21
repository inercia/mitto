package web

import (
	"net"
	"net/http"
	"sync"

	"github.com/inercia/mitto/internal/web/middleware"
)

const (
	// Preserve normal multi-session browser startup while bounding one source.
	defaultMaxWSConnectionsPerIP = 64
	defaultMaxWSConnections      = 512
)

// wsConnectionLimiter bounds authenticated external WebSocket connections.
// Requests through the internal listener bypass it in acquireExternalWebSocket.
type wsConnectionLimiter struct {
	mu       sync.Mutex
	perIP    map[string]int
	total    int
	maxPerIP int
	maxTotal int
}

func newWSConnectionLimiter(maxPerIP, maxTotal int) *wsConnectionLimiter {
	return &wsConnectionLimiter{
		perIP:    make(map[string]int),
		maxPerIP: maxPerIP,
		maxTotal: maxTotal,
	}
}

// acquire reserves capacity and returns an idempotent release function.
func (l *wsConnectionLimiter) acquire(ip string) (func(), bool) {
	l.mu.Lock()
	if l.total >= l.maxTotal || l.perIP[ip] >= l.maxPerIP {
		l.mu.Unlock()
		return nil, false
	}
	l.total++
	l.perIP[ip]++
	l.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() { l.release(ip) })
	}, true
}

func (l *wsConnectionLimiter) release(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.perIP[ip] <= 0 {
		return
	}
	l.total--
	l.perIP[ip]--
	if l.perIP[ip] == 0 {
		delete(l.perIP, ip)
	}
}

// acquireExternalWebSocket enforces limits only after authentication middleware
// has marked a request as external. Internal/local UI connections remain uncapped.
func (s *Server) acquireExternalWebSocket(w http.ResponseWriter, r *http.Request) (func(), bool) {
	if !middleware.IsExternalConnection(r) {
		return nil, true
	}
	if s.wsConnectionLimiter == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "server_error", "WebSocket admission unavailable")
		return nil, false
	}

	clientIP := normalizeWSClientIP(middleware.GetClientIPWithProxyCheck(r))
	release, ok := s.wsConnectionLimiter.acquire(clientIP)
	if !ok {
		w.Header().Set("Retry-After", "1")
		writeErrorJSON(w, http.StatusTooManyRequests, "", "Too Many Requests")
		return nil, false
	}
	return release, true
}

func normalizeWSClientIP(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
	}
	if ip := net.ParseIP(addr); ip != nil {
		return ip.String()
	}
	return addr
}
