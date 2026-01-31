package web

import (
	"testing"
)

func TestNewGlobalEventsManager(t *testing.T) {
	m := NewGlobalEventsManager()

	if m == nil {
		t.Fatal("NewGlobalEventsManager returned nil")
	}

	if m.clients == nil {
		t.Error("clients map should not be nil")
	}

	if m.ClientCount() != 0 {
		t.Errorf("ClientCount = %d, want 0", m.ClientCount())
	}
}

func TestGlobalEventsManager_RegisterUnregister(t *testing.T) {
	m := NewGlobalEventsManager()

	client := &GlobalEventsClient{}

	// Register
	m.Register(client)
	if m.ClientCount() != 1 {
		t.Errorf("ClientCount = %d, want 1", m.ClientCount())
	}

	// Register same client again (should not duplicate)
	m.Register(client)
	if m.ClientCount() != 1 {
		t.Errorf("ClientCount = %d, want 1 (no duplicates)", m.ClientCount())
	}

	// Unregister
	m.Unregister(client)
	if m.ClientCount() != 0 {
		t.Errorf("ClientCount = %d, want 0", m.ClientCount())
	}
}

func TestGlobalEventsManager_MultipleClients(t *testing.T) {
	m := NewGlobalEventsManager()

	client1 := &GlobalEventsClient{}
	client2 := &GlobalEventsClient{}
	client3 := &GlobalEventsClient{}

	m.Register(client1)
	m.Register(client2)
	m.Register(client3)

	if m.ClientCount() != 3 {
		t.Errorf("ClientCount = %d, want 3", m.ClientCount())
	}

	m.Unregister(client2)
	if m.ClientCount() != 2 {
		t.Errorf("ClientCount = %d, want 2", m.ClientCount())
	}
}

func TestGlobalEventsManager_UnregisterNonExistent(t *testing.T) {
	m := NewGlobalEventsManager()

	client1 := &GlobalEventsClient{}
	client2 := &GlobalEventsClient{}

	m.Register(client1)

	// Unregister client that was never registered - should not panic
	m.Unregister(client2)

	if m.ClientCount() != 1 {
		t.Errorf("ClientCount = %d, want 1", m.ClientCount())
	}
}

func TestGlobalEventsManager_Broadcast_NoClients(t *testing.T) {
	m := NewGlobalEventsManager()

	// Broadcast with no clients should not panic
	m.Broadcast("test", map[string]string{"key": "value"})
}

func TestGlobalEventsManager_Broadcast_NilData(t *testing.T) {
	m := NewGlobalEventsManager()

	// Broadcast with nil data should not panic
	m.Broadcast("test", nil)
}
