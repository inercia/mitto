package web

import (
	"net"
	"net/http"
	"strings"
	"sync"
)

// TrustedProxyChecker validates whether requests come from trusted proxies.
// It is safe for concurrent use.
type TrustedProxyChecker struct {
	mu          sync.RWMutex
	trustedNets []*net.IPNet
	trustedIPs  []net.IP
}

// NewTrustedProxyChecker creates a new trusted proxy checker from a list of
// IP addresses and CIDR ranges.
func NewTrustedProxyChecker(trustedProxies []string) *TrustedProxyChecker {
	tpc := &TrustedProxyChecker{}

	for _, entry := range trustedProxies {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		// Try parsing as CIDR first
		if strings.Contains(entry, "/") {
			_, network, err := net.ParseCIDR(entry)
			if err == nil {
				tpc.trustedNets = append(tpc.trustedNets, network)
				continue
			}
		}

		// Try parsing as individual IP
		ip := net.ParseIP(entry)
		if ip != nil {
			tpc.trustedIPs = append(tpc.trustedIPs, ip)
		}
	}

	return tpc
}

// IsTrusted checks if the given IP address is from a trusted proxy.
func (tpc *TrustedProxyChecker) IsTrusted(ipStr string) bool {
	if len(tpc.trustedNets) == 0 && len(tpc.trustedIPs) == 0 {
		return false
	}

	ip := parseClientIP(ipStr)
	if ip == nil {
		return false
	}

	// Check against individual IPs
	for _, trustedIP := range tpc.trustedIPs {
		if trustedIP.Equal(ip) {
			return true
		}
	}

	// Check against CIDR networks
	for _, network := range tpc.trustedNets {
		if network.Contains(ip) {
			return true
		}
	}

	return false
}

// HasTrustedProxies returns true if any trusted proxies are configured.
func (tpc *TrustedProxyChecker) HasTrustedProxies() bool {
	return len(tpc.trustedNets) > 0 || len(tpc.trustedIPs) > 0
}

// GetClientIP extracts the real client IP from the request.
// It only trusts X-Forwarded-For and X-Real-IP headers if the direct
// connection comes from a trusted proxy.
func (tpc *TrustedProxyChecker) GetClientIP(r *http.Request) string {
	// Get the direct connection IP
	directIP := r.RemoteAddr

	// If no trusted proxies configured, always use direct IP
	if !tpc.HasTrustedProxies() {
		return directIP
	}

	// Check if the direct connection is from a trusted proxy
	if !tpc.IsTrusted(directIP) {
		// Not from a trusted proxy - don't trust forwarded headers
		return directIP
	}

	// Connection is from a trusted proxy - check forwarded headers
	// Check X-Forwarded-For header (may contain multiple IPs)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP (original client)
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			clientIP := strings.TrimSpace(parts[0])
			if clientIP != "" {
				return clientIP
			}
		}
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	// Fall back to direct IP
	return directIP
}

// defaultProxyChecker is the global trusted proxy checker.
// It is initialized when the server starts.
var (
	defaultProxyChecker   *TrustedProxyChecker
	defaultProxyCheckerMu sync.RWMutex
)

// SetDefaultProxyChecker sets the global trusted proxy checker.
func SetDefaultProxyChecker(tpc *TrustedProxyChecker) {
	defaultProxyCheckerMu.Lock()
	defer defaultProxyCheckerMu.Unlock()
	defaultProxyChecker = tpc
}

// getClientIPWithProxyCheck extracts the client IP using the global proxy checker.
// This replaces the old getClientIP function when trusted proxies are configured.
func getClientIPWithProxyCheck(r *http.Request) string {
	defaultProxyCheckerMu.RLock()
	tpc := defaultProxyChecker
	defaultProxyCheckerMu.RUnlock()

	if tpc != nil {
		return tpc.GetClientIP(r)
	}

	// Fall back to old behavior if no proxy checker configured
	return getClientIP(r)
}
