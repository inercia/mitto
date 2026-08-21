package web

import (
	"net"
	"strings"
	"testing"
)

func TestPrimaryListenerAddress(t *testing.T) {
	tests := []struct {
		name        string
		host        string
		authEnabled bool
		want        string
		wantError   string
	}{
		{name: "IPv4 loopback", host: "127.0.0.2", want: "127.0.0.2:8080"},
		{name: "IPv6 loopback", host: "::1", want: "[::1]:8080"},
		{name: "bracketed IPv6 loopback", host: "[::1]", want: "[::1]:8080"},
		{name: "localhost hostname", host: "localhost", want: "localhost:8080"},
		{name: "IPv4 wildcard", host: "0.0.0.0", wantError: "authentication"},
		{name: "IPv6 wildcard", host: "::", wantError: "authentication"},
		{name: "non-loopback IPv4", host: "192.0.2.10", wantError: "authentication"},
		{name: "non-loopback hostname", host: "mitto.example", wantError: "authentication"},
		{name: "authenticated non-loopback uses external listener", host: "192.0.2.10", authEnabled: true, wantError: "StartExternalListener"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PrimaryListenerAddress(tt.host, 8080, tt.authEnabled)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("PrimaryListenerAddress(%q) error = %v, want containing %q", tt.host, err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("PrimaryListenerAddress(%q) error = %v", tt.host, err)
			}
			if got != tt.want {
				t.Errorf("PrimaryListenerAddress(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}

func TestCreateLocalhostListener(t *testing.T) {
	// Create a localhost listener with random port
	listener, port, err := CreateLocalhostListener(0)
	if err != nil {
		t.Fatalf("CreateLocalhostListener failed: %v", err)
	}
	defer listener.Close()

	if port == 0 {
		t.Error("Expected non-zero port")
	}

	// Verify the listener address is localhost
	addr := listener.Addr().String()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("Failed to parse listener address: %v", err)
	}

	if host != "127.0.0.1" {
		t.Errorf("Expected listener on 127.0.0.1, got %s", host)
	}
}

func TestLocalhostListener_AcceptsLocalhost(t *testing.T) {
	// Create a localhost listener
	listener, port, err := CreateLocalhostListener(0)
	if err != nil {
		t.Fatalf("CreateLocalhostListener failed: %v", err)
	}
	defer listener.Close()

	// Connect from localhost
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		conn.Close()
		done <- nil
	}()

	// Make a connection from localhost
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	conn.Close()

	// Wait for accept to complete
	if err := <-done; err != nil {
		t.Errorf("Accept failed: %v", err)
	}

	t.Logf("Successfully accepted localhost connection on port %d", port)
}

func TestIsLocalhostConnection(t *testing.T) {
	// Create a test listener
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()

	// Accept connection in goroutine
	connChan := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		connChan <- conn
	}()

	// Connect from localhost
	clientConn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer clientConn.Close()

	// Get the server-side connection
	serverConn := <-connChan
	defer serverConn.Close()

	// Verify it's detected as localhost
	if !isLocalhostConnection(serverConn) {
		t.Errorf("Expected connection from %s to be detected as localhost",
			serverConn.RemoteAddr().String())
	}
}
