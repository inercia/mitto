package web

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseMessage(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		want    WSMessage
		wantErr bool
	}{
		{
			name:  "valid message with data",
			input: []byte(`{"type":"test","data":{"key":"value"}}`),
			want: WSMessage{
				Type: "test",
				Data: json.RawMessage(`{"key":"value"}`),
			},
			wantErr: false,
		},
		{
			name:  "valid message without data",
			input: []byte(`{"type":"ping"}`),
			want: WSMessage{
				Type: "ping",
				Data: nil,
			},
			wantErr: false,
		},
		{
			name:    "invalid json",
			input:   []byte(`{invalid`),
			want:    WSMessage{},
			wantErr: true,
		},
		{
			name:    "empty input",
			input:   []byte(``),
			want:    WSMessage{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMessage(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseMessage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if got.Type != tt.want.Type {
					t.Errorf("ParseMessage() Type = %v, want %v", got.Type, tt.want.Type)
				}
				if tt.want.Data != nil && string(got.Data) != string(tt.want.Data) {
					t.Errorf("ParseMessage() Data = %v, want %v", string(got.Data), string(tt.want.Data))
				}
			}
		})
	}
}

func TestWSConn_SendRaw_NonBlocking(t *testing.T) {
	// Create a WSConn with a small buffer to test non-blocking behavior
	w := &WSConn{
		send: make(chan []byte, 1),
	}

	// First send should succeed
	w.SendRaw([]byte("first"))

	// Second send should not block (buffer full, message dropped)
	done := make(chan bool)
	go func() {
		w.SendRaw([]byte("second"))
		done <- true
	}()

	select {
	case <-done:
		// Good - SendRaw returned without blocking
	case <-time.After(100 * time.Millisecond):
		t.Error("SendRaw blocked when buffer was full")
	}

	// Verify first message is in buffer
	msg := <-w.send
	if string(msg) != "first" {
		t.Errorf("Expected 'first', got %s", string(msg))
	}
}

func TestWSConn_ReleaseConnectionSlot(t *testing.T) {
	tracker := NewConnectionTracker(10)
	clientIP := "192.168.1.1"

	// Add a connection
	if !tracker.TryAdd(clientIP) {
		t.Fatal("TryAdd should succeed")
	}

	w := &WSConn{
		tracker:  tracker,
		clientIP: clientIP,
	}

	// Release should remove from tracker
	w.ReleaseConnectionSlot()

	// Should be able to add again
	if !tracker.TryAdd(clientIP) {
		t.Error("TryAdd should succeed after ReleaseConnectionSlot")
	}
}

func TestWSConn_ReleaseConnectionSlot_NilTracker(t *testing.T) {
	w := &WSConn{
		tracker:  nil,
		clientIP: "192.168.1.1",
	}

	// Should not panic with nil tracker
	w.ReleaseConnectionSlot()
}

func TestWSConn_ReleaseConnectionSlot_EmptyClientIP(t *testing.T) {
	tracker := NewConnectionTracker(10)

	w := &WSConn{
		tracker:  tracker,
		clientIP: "",
	}

	// Should not panic with empty client IP
	w.ReleaseConnectionSlot()
}

func TestWSConnConfig_Fields(t *testing.T) {
	// Test that WSConnConfig fields are properly defined
	tracker := NewConnectionTracker(10)

	cfg := WSConnConfig{
		Conn:     nil,
		Config:   DefaultWebSocketSecurityConfig(),
		Logger:   nil,
		ClientIP: "192.168.1.1",
		Tracker:  tracker,
		SendSize: 128,
	}

	if cfg.ClientIP != "192.168.1.1" {
		t.Error("ClientIP not set correctly")
	}
	if cfg.SendSize != 128 {
		t.Error("SendSize not set correctly")
	}
	if cfg.Tracker != tracker {
		t.Error("Tracker not set correctly")
	}
}
