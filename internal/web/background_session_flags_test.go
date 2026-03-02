package web

import (
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

func TestDefaultFlags_AppliedToNewSession(t *testing.T) {
	// Create a temporary store
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Create a config with default flags
	cfg := &config.Config{
		Conversations: &config.ConversationsConfig{
			DefaultFlags: map[string]bool{
				session.FlagCanStartConversation: true,
				session.FlagCanPromptUser:        true,
				session.FlagCanSendPrompt:        false, // Explicitly set to false
			},
		},
	}

	// Create a new session metadata
	meta := session.Metadata{
		SessionID:  session.GenerateSessionID(),
		Name:       "Test Session",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Simulate what BackgroundSession does: apply default flags
	store.UpdateMetadata(meta.SessionID, func(m *session.Metadata) {
		if m.AdvancedSettings == nil {
			m.AdvancedSettings = make(map[string]bool)
		}

		// Apply configured default flags from config
		if cfg.Conversations != nil {
			for flagName, flagValue := range cfg.Conversations.DefaultFlags {
				if _, exists := m.AdvancedSettings[flagName]; !exists {
					m.AdvancedSettings[flagName] = flagValue
				}
			}
		}

		// Apply compile-time defaults for flags not explicitly configured
		for _, flagDef := range session.AvailableFlags {
			if _, exists := m.AdvancedSettings[flagDef.Name]; !exists {
				if flagDef.Default {
					m.AdvancedSettings[flagDef.Name] = true
				}
			}
		}
	})

	// Get the updated metadata
	updatedMeta, err := store.GetMetadata(meta.SessionID)
	if err != nil {
		t.Fatalf("Failed to get metadata: %v", err)
	}

	// Verify default flags were applied
	if updatedMeta.AdvancedSettings == nil {
		t.Fatal("AdvancedSettings should not be nil")
	}

	// Check that configured flags were applied
	if !updatedMeta.AdvancedSettings[session.FlagCanStartConversation] {
		t.Error("FlagCanStartConversation should be true (from config)")
	}
	if !updatedMeta.AdvancedSettings[session.FlagCanPromptUser] {
		t.Error("FlagCanPromptUser should be true (from config)")
	}
	if updatedMeta.AdvancedSettings[session.FlagCanSendPrompt] {
		t.Error("FlagCanSendPrompt should be false (explicitly set in config)")
	}

	// Check that compile-time defaults were applied for flags not in config
	// FlagCanDoIntrospection has compile-time default of false, so it should not be in the map
	if updatedMeta.AdvancedSettings[session.FlagCanDoIntrospection] {
		t.Error("FlagCanDoIntrospection should be false (compile-time default)")
	}
}

func TestDefaultFlags_PreservesExistingFlags(t *testing.T) {
	// Create a temporary store
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Create a session with existing flags
	existingMeta := session.Metadata{
		SessionID:  session.GenerateSessionID(),
		Name:       "Existing Session",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
		AdvancedSettings: map[string]bool{
			session.FlagCanDoIntrospection: true, // User explicitly enabled this
		},
	}
	if err := store.Create(existingMeta); err != nil {
		t.Fatalf("Failed to create existing session: %v", err)
	}

	// Create a config with different default flags
	cfg := &config.Config{
		Conversations: &config.ConversationsConfig{
			DefaultFlags: map[string]bool{
				session.FlagCanStartConversation: true,
				session.FlagCanDoIntrospection:   false, // Config says false, but user set it to true
			},
		},
	}

	// Simulate what BackgroundSession does when resuming: apply default flags
	store.UpdateMetadata(existingMeta.SessionID, func(m *session.Metadata) {
		if m.AdvancedSettings == nil {
			m.AdvancedSettings = make(map[string]bool)
		}

		// Apply configured default flags from config
		if cfg.Conversations != nil {
			for flagName, flagValue := range cfg.Conversations.DefaultFlags {
				// Only set the flag if it's not already set (preserve existing values)
				if _, exists := m.AdvancedSettings[flagName]; !exists {
					m.AdvancedSettings[flagName] = flagValue
				}
			}
		}

		// Apply compile-time defaults for flags not explicitly configured
		for _, flagDef := range session.AvailableFlags {
			if _, exists := m.AdvancedSettings[flagDef.Name]; !exists {
				if flagDef.Default {
					m.AdvancedSettings[flagDef.Name] = true
				}
			}
		}
	})

	// Get the updated metadata
	meta, err := store.GetMetadata(existingMeta.SessionID)
	if err != nil {
		t.Fatalf("Failed to get metadata: %v", err)
	}

	// Verify existing flag was preserved (not overwritten by config default)
	if !meta.AdvancedSettings[session.FlagCanDoIntrospection] {
		t.Error("FlagCanDoIntrospection should still be true (preserved from existing session)")
	}

	// Verify new default flags were applied
	if !meta.AdvancedSettings[session.FlagCanStartConversation] {
		t.Error("FlagCanStartConversation should be true (from config)")
	}
}
