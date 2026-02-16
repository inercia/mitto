package defense

import (
	"log/slog"
	"net"
)

// BlockedCallback is called when a blocked IP attempts to connect.
// Parameters: ip address, reason for block.
type BlockedCallback func(ip, reason string)

// FilteredListener wraps a net.Listener and rejects connections from blocked IPs.
type FilteredListener struct {
	net.Listener
	defense         *ScannerDefense
	logger          *slog.Logger
	blockedCallback BlockedCallback
}

// NewFilteredListener wraps a listener with scanner defense.
func NewFilteredListener(l net.Listener, defense *ScannerDefense, logger *slog.Logger) *FilteredListener {
	return &FilteredListener{
		Listener: l,
		defense:  defense,
		logger:   logger,
	}
}

// SetBlockedCallback sets a callback to be invoked when a blocked IP is rejected.
func (l *FilteredListener) SetBlockedCallback(cb BlockedCallback) {
	l.blockedCallback = cb
}

// Accept accepts connections, rejecting blocked IPs immediately.
func (l *FilteredListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}

		ip := ExtractIP(conn.RemoteAddr())

		if l.defense.IsBlocked(ip) {
			// Get block reason for logging
			reason := l.defense.GetBlockReason(ip)

			conn.Close() // Close immediately, no response
			l.logger.Debug("connection_rejected",
				"component", "defense",
				"ip", ip,
				"reason", reason,
			)

			// Invoke callback for access logging
			if l.blockedCallback != nil {
				l.blockedCallback(ip, reason)
			}
			continue
		}

		return conn, nil
	}
}

// ExtractIP gets the IP address from a net.Addr (handles IPv4, IPv6, with/without port).
func ExtractIP(addr net.Addr) string {
	if addr == nil {
		return ""
	}

	// Try to get the string representation
	addrStr := addr.String()

	// Try to split host:port
	host, _, err := net.SplitHostPort(addrStr)
	if err != nil {
		// No port, return as is (but parse to normalize)
		ip := net.ParseIP(addrStr)
		if ip != nil {
			return ip.String()
		}
		return addrStr
	}

	// Parse and normalize the IP
	ip := net.ParseIP(host)
	if ip != nil {
		return ip.String()
	}
	return host
}
