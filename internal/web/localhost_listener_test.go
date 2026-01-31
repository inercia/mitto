package web

import (
	"net"
	"testing"
)

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
