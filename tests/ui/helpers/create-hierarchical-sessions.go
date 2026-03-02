package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/session"
)

// Helper tool to create hierarchical sessions for testing.
// This creates sessions with parent_session_id relationships directly in the session store.
//
// Usage:
//   go run create-hierarchical-sessions.go -dir /tmp/mitto-test -parent "Parent Session" -children "Child 1,Child 2,Child 3"

func main() {
	var (
		mittoDir    string
		parentName  string
		childrenStr string
		workingDir  string
		acpServer   string
	)

	flag.StringVar(&mittoDir, "dir", "", "Mitto data directory (required)")
	flag.StringVar(&parentName, "parent", "Parent Session", "Name of parent session")
	flag.StringVar(&childrenStr, "children", "Child 1,Child 2", "Comma-separated child session names")
	flag.StringVar(&workingDir, "working-dir", "/tmp", "Working directory for sessions")
	flag.StringVar(&acpServer, "acp-server", "mock-acp", "ACP server name")
	flag.Parse()

	if mittoDir == "" {
		log.Fatal("Error: -dir flag is required")
	}

	// Set MITTO_DIR environment variable
	os.Setenv(appdir.MittoDirEnv, mittoDir)
	appdir.ResetCache()

	// Create session store
	sessionsDir := filepath.Join(mittoDir, "sessions")
	store, err := session.NewStore(sessionsDir)
	if err != nil {
		log.Fatalf("Failed to create session store: %v", err)
	}
	defer store.Close()

	// Create parent session
	parentID := session.GenerateSessionID()
	parentMeta := session.Metadata{
		SessionID:  parentID,
		Name:       parentName,
		ACPServer:  acpServer,
		WorkingDir: workingDir,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		Status:     session.SessionStatusActive,
	}

	if err := store.Create(parentMeta); err != nil {
		log.Fatalf("Failed to create parent session: %v", err)
	}

	fmt.Printf("Created parent session: %s (ID: %s)\n", parentName, parentID)

	// Parse child names
	childNames := []string{}
	if childrenStr != "" {
		// Simple comma-separated parsing
		current := ""
		for _, ch := range childrenStr {
			if ch == ',' {
				if current != "" {
					childNames = append(childNames, current)
					current = ""
				}
			} else {
				current += string(ch)
			}
		}
		if current != "" {
			childNames = append(childNames, current)
		}
	}

	// Create child sessions
	childIDs := []string{}
	for i, childName := range childNames {
		childID := session.GenerateSessionID()
		childMeta := session.Metadata{
			SessionID:       childID,
			Name:            childName,
			ACPServer:       acpServer,
			WorkingDir:      workingDir,
			ParentSessionID: parentID,
			CreatedAt:       time.Now().Add(time.Duration(i+1) * time.Millisecond),
			UpdatedAt:       time.Now().Add(time.Duration(i+1) * time.Millisecond),
			Status:          session.SessionStatusActive,
		}

		if err := store.Create(childMeta); err != nil {
			log.Fatalf("Failed to create child session %s: %v", childName, err)
		}

		childIDs = append(childIDs, childID)
		fmt.Printf("Created child session: %s (ID: %s, Parent: %s)\n", childName, childID, parentID)
	}

	// Output JSON summary for test consumption
	output := map[string]interface{}{
		"parent": map[string]string{
			"id":   parentID,
			"name": parentName,
		},
		"children": []map[string]string{},
	}

	for i, childID := range childIDs {
		output["children"] = append(output["children"].([]map[string]string), map[string]string{
			"id":   childID,
			"name": childNames[i],
		})
	}

	jsonOutput, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal JSON output: %v", err)
	}

	fmt.Println("\nJSON Output:")
	fmt.Println(string(jsonOutput))

	// Write to a file for test consumption
	outputFile := filepath.Join(mittoDir, "hierarchical-sessions.json")
	if err := os.WriteFile(outputFile, jsonOutput, 0644); err != nil {
		log.Fatalf("Failed to write output file: %v", err)
	}

	fmt.Printf("\nOutput written to: %s\n", outputFile)
}
