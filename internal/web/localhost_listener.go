package web

import (
	"fmt"
	"net"

	"github.com/inercia/mitto/internal/logging"
)

// LocalhostListener wraps a net.Listener and only accepts connections from localhost.
// This provides socket-level security by rejecting non-localhost connections before
// any HTTP processing occurs.
//
// SECURITY: This is a defense-in-depth measure. Even if the listener is somehow
// bound to a non-localhost address (which shouldn't happen), connections from
// external IPs will be rejected at the socket level.
type LocalhostListener struct {
	net.Listener
}

// NewLocalhostListener creates a new localhost-only listener.
// The underlying listener should already be bound to 127.0.0.1, but this wrapper
// provides an additional layer of security by validating the source IP of each
// incoming connection.
func NewLocalhostListener(l net.Listener) *LocalhostListener {
	return &LocalhostListener{Listener: l}
}

// Accept waits for and returns the next connection to the listener.
// It only accepts connections from localhost (127.0.0.0/8 or ::1).
// Connections from other IPs are immediately closed.
func (l *LocalhostListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}

		// Validate that the connection is from localhost
		if !isLocalhostConnection(conn) {
			remoteAddr := conn.RemoteAddr().String()
			logging.Web().Warn("Rejected non-localhost connection on internal port",
				"remote_addr", remoteAddr)
			conn.Close()
			continue
		}

		return conn, nil
	}
}

// isLocalhostConnection checks if a connection originates from localhost.
func isLocalhostConnection(conn net.Conn) bool {
	remoteAddr := conn.RemoteAddr()
	if remoteAddr == nil {
		return false
	}

	// Extract the IP from the address
	host, _, err := net.SplitHostPort(remoteAddr.String())
	if err != nil {
		// Try parsing as IP directly (no port)
		host = remoteAddr.String()
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	return ip.IsLoopback()
}

// CreateLocalhostListener creates a new TCP listener bound exclusively to localhost.
// If port is 0, a random available port is selected.
// Returns the listener and the actual port used.
//
// SECURITY: This function ensures the listener is bound to 127.0.0.1 only,
// and wraps it with LocalhostListener for additional connection validation.
func CreateLocalhostListener(port int) (*LocalhostListener, int, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	actualPort := listener.Addr().(*net.TCPAddr).Port
	return NewLocalhostListener(listener), actualPort, nil
}
