package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"
)

const (
	TrustedProxyHeaderXForwardedFor  = "x-forwarded-for"
	TrustedProxyHeaderXRealIP        = "x-real-ip"
	TrustedProxyHeaderCFConnectingIP = "cf-connecting-ip"
)

// TrustedProxyChecker validates whether requests come from trusted proxies.
// It is safe for concurrent use.
type TrustedProxyChecker struct {
	trustedNets    []*net.IPNet
	trustedIPs     []net.IP
	trustedHeaders map[string]bool
}

// NewTrustedProxyChecker creates a new trusted proxy checker from a list of
// IP addresses and CIDR ranges. X-Forwarded-For is the safe default; other
// single-IP headers must be explicitly enabled.
func NewTrustedProxyChecker(trustedProxies []string, trustedHeaders ...string) *TrustedProxyChecker {
	tpc := &TrustedProxyChecker{trustedHeaders: make(map[string]bool)}
	if len(trustedHeaders) == 0 {
		tpc.trustedHeaders[TrustedProxyHeaderXForwardedFor] = true
	} else {
		for _, header := range trustedHeaders {
			tpc.trustedHeaders[strings.ToLower(strings.TrimSpace(header))] = true
		}
	}

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

// GetClientIP extracts the real client IP from explicitly trusted headers when
// the direct connection comes from a trusted proxy.
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

	if tpc.trustedHeaders[TrustedProxyHeaderCFConnectingIP] {
		if ip, present, valid := singleIPHeader(r, "Cf-Connecting-IP"); present {
			if !valid {
				return directIP
			}
			return ip
		}
	}

	if tpc.trustedHeaders[TrustedProxyHeaderXRealIP] {
		if ip, present, valid := singleIPHeader(r, "X-Real-IP"); present {
			if !valid {
				return directIP
			}
			return ip
		}
	}

	if tpc.trustedHeaders[TrustedProxyHeaderXForwardedFor] {
		values := r.Header.Values("X-Forwarded-For")
		if len(values) > 0 {
			parts := strings.Split(strings.Join(values, ","), ",")
			leftmost := ""
			for i := len(parts) - 1; i >= 0; i-- {
				ip := net.ParseIP(strings.TrimSpace(parts[i]))
				if ip == nil {
					return directIP
				}
				leftmost = ip.String()
				if !tpc.IsTrusted(leftmost) {
					return leftmost
				}
			}
			return leftmost
		}
	}

	// Fall back to direct IP
	return directIP
}

func singleIPHeader(r *http.Request, name string) (ip string, present, valid bool) {
	values := r.Header.Values(name)
	if len(values) == 0 {
		return "", false, false
	}
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return "", true, false
	}
	parsed := net.ParseIP(strings.TrimSpace(values[0]))
	if parsed == nil {
		return "", true, false
	}
	return parsed.String(), true, true
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

// GetClientIPWithProxyCheck extracts the client IP using the global proxy checker.
// This replaces the old getClientIP function when trusted proxies are configured.
func GetClientIPWithProxyCheck(r *http.Request) string {
	defaultProxyCheckerMu.RLock()
	tpc := defaultProxyChecker
	defaultProxyCheckerMu.RUnlock()

	if tpc != nil {
		return tpc.GetClientIP(r)
	}

	// Fall back to old behavior if no proxy checker configured
	return getClientIP(r)
}
