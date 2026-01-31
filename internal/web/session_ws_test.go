package web

import (
	"testing"
)

func TestGenerateClientID(t *testing.T) {
	id1 := generateClientID()
	id2 := generateClientID()

	// IDs should not be empty
	if id1 == "" {
		t.Error("generateClientID returned empty string")
	}

	// IDs should be unique
	if id1 == id2 {
		t.Errorf("generateClientID returned duplicate IDs: %s", id1)
	}

	// IDs should be 16 characters (8 bytes hex encoded)
	if len(id1) != 16 {
		t.Errorf("generateClientID returned ID of length %d, want 16", len(id1))
	}
}
