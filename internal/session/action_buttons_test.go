package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestActionButtonsStore_SetAndGet(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "action-buttons-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store := NewActionButtonsStore(tmpDir)

	// Initially empty
	buttons, err := store.Get()
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if len(buttons) != 0 {
		t.Errorf("Expected empty buttons, got %d", len(buttons))
	}

	// Set some buttons
	testButtons := []ActionButton{
		{Label: "Yes", Response: "Yes, please proceed"},
		{Label: "No", Response: "No, cancel the operation"},
	}
	if err := store.Set(testButtons, 42); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Get them back
	buttons, err = store.Get()
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if len(buttons) != 2 {
		t.Errorf("Expected 2 buttons, got %d", len(buttons))
	}
	if buttons[0].Label != "Yes" {
		t.Errorf("Expected label 'Yes', got '%s'", buttons[0].Label)
	}
	if buttons[1].Response != "No, cancel the operation" {
		t.Errorf("Expected response 'No, cancel the operation', got '%s'", buttons[1].Response)
	}

	// Verify file exists
	filePath := filepath.Join(tmpDir, actionButtonsFileName)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Error("Expected action_buttons.json file to exist")
	}
}

func TestActionButtonsStore_Clear(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "action-buttons-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store := NewActionButtonsStore(tmpDir)

	// Set some buttons
	testButtons := []ActionButton{
		{Label: "Test", Response: "Test response"},
	}
	if err := store.Set(testButtons, 1); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Clear
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	// Should be empty now
	buttons, err := store.Get()
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if len(buttons) != 0 {
		t.Errorf("Expected empty buttons after clear, got %d", len(buttons))
	}

	// File should be removed
	filePath := filepath.Join(tmpDir, actionButtonsFileName)
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("Expected action_buttons.json file to be removed after clear")
	}
}

func TestActionButtonsStore_IsEmpty(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "action-buttons-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store := NewActionButtonsStore(tmpDir)

	// Initially empty
	empty, err := store.IsEmpty()
	if err != nil {
		t.Fatalf("IsEmpty failed: %v", err)
	}
	if !empty {
		t.Error("Expected IsEmpty to return true initially")
	}

	// Set some buttons
	testButtons := []ActionButton{
		{Label: "Test", Response: "Test response"},
	}
	if err := store.Set(testButtons, 1); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Not empty now
	empty, err = store.IsEmpty()
	if err != nil {
		t.Fatalf("IsEmpty failed: %v", err)
	}
	if empty {
		t.Error("Expected IsEmpty to return false after setting buttons")
	}
}

func TestActionButtonsStore_Delete(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "action-buttons-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store := NewActionButtonsStore(tmpDir)

	// Set some buttons
	testButtons := []ActionButton{
		{Label: "Test", Response: "Test response"},
	}
	if err := store.Set(testButtons, 1); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Delete
	if err := store.Delete(); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// File should be removed
	filePath := filepath.Join(tmpDir, actionButtonsFileName)
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("Expected action_buttons.json file to be removed after delete")
	}
}
