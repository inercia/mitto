package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/config"
)

func TestStore_GetUserData_NonExistentSession(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Get user data from a session that doesn't exist
	data, err := store.GetUserData("nonexistent-session")
	if err != nil {
		t.Fatalf("GetUserData failed: %v", err)
	}

	// Should return empty attributes (not nil)
	if data == nil {
		t.Fatal("Expected non-nil UserData")
	}
	if len(data.Attributes) != 0 {
		t.Errorf("Expected empty attributes, got %d", len(data.Attributes))
	}
}

func TestStore_GetUserData_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := Metadata{
		SessionID:  "test-session-1",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create session failed: %v", err)
	}

	// Get user data when no user-data.json exists
	data, err := store.GetUserData("test-session-1")
	if err != nil {
		t.Fatalf("GetUserData failed: %v", err)
	}

	if data == nil {
		t.Fatal("Expected non-nil UserData")
	}
	if len(data.Attributes) != 0 {
		t.Errorf("Expected empty attributes, got %d", len(data.Attributes))
	}
}

func TestStore_SetAndGetUserData(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := Metadata{
		SessionID:  "test-session-2",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create session failed: %v", err)
	}

	// Set user data
	userData := &UserData{
		Attributes: []UserDataAttribute{
			{Name: "JIRA ticket", Value: "https://jira.example.com/PROJ-123"},
			{Name: "Description", Value: "Bug fix for login flow"},
		},
	}
	if err := store.SetUserData("test-session-2", userData); err != nil {
		t.Fatalf("SetUserData failed: %v", err)
	}

	// Verify file was created
	userDataPath := filepath.Join(tmpDir, "test-session-2", "user-data.json")
	if _, err := os.Stat(userDataPath); os.IsNotExist(err) {
		t.Error("user-data.json file was not created")
	}

	// Get user data back
	got, err := store.GetUserData("test-session-2")
	if err != nil {
		t.Fatalf("GetUserData failed: %v", err)
	}

	if len(got.Attributes) != 2 {
		t.Fatalf("Expected 2 attributes, got %d", len(got.Attributes))
	}

	if got.Attributes[0].Name != "JIRA ticket" || got.Attributes[0].Value != "https://jira.example.com/PROJ-123" {
		t.Errorf("Attribute 0 mismatch: got %+v", got.Attributes[0])
	}
	if got.Attributes[1].Name != "Description" || got.Attributes[1].Value != "Bug fix for login flow" {
		t.Errorf("Attribute 1 mismatch: got %+v", got.Attributes[1])
	}
}

func TestStore_SetUserData_NonExistentSession(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	userData := &UserData{
		Attributes: []UserDataAttribute{
			{Name: "test", Value: "value"},
		},
	}

	err = store.SetUserData("nonexistent-session", userData)
	if err != ErrSessionNotFound {
		t.Errorf("Expected ErrSessionNotFound, got %v", err)
	}
}

func TestUserData_Validate_NoSchema(t *testing.T) {
	userData := &UserData{
		Attributes: []UserDataAttribute{
			{Name: "anything", Value: "any value"},
		},
	}

	// With nil schema, attributes should be rejected
	err := userData.Validate(nil)
	if err == nil {
		t.Error("Expected error with nil schema, got nil")
	}

	// With empty schema, attributes should be rejected
	err = userData.Validate(&config.UserDataSchema{})
	if err == nil {
		t.Error("Expected error with empty schema, got nil")
	}

	// Empty user data should always be valid (even with no schema)
	emptyUserData := &UserData{Attributes: []UserDataAttribute{}}
	err = emptyUserData.Validate(nil)
	if err != nil {
		t.Errorf("Expected nil error for empty user data, got %v", err)
	}
}
