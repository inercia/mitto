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

// Helper tool (mitto-sus.1.2) that seeds one session per history snapshot
// JSON file (see scripts/gen-perf-histories.mjs) directly into the session
// store, bypassing ACP/UI entirely. history-load.perf.spec.ts uses this to
// measure DOM/frame cost against an already-large conversation without
// spending test time sending thousands of messages one at a time through the
// chat UI — the same "write directly into the session store" technique
// ../create-hierarchical-sessions.go uses for parent/child fixtures. Lives in
// its own subdirectory (unlike that sibling) because both are `package main`
// with their own `main()`; two at the same directory level would collide
// under `go build ./...`.
//
// Usage:
//
//	go build -o seed-perf-history ./tests/ui/helpers/seed-perf-history
//	./seed-perf-history -dir /tmp/mitto-test \
//	  -snapshots-dir tests/ui/perf/fixtures/histories \
//	  -working-dir /path/to/project-alpha
//
// Writes $MITTO_DIR/perf-history-sessions.json mapping snapshot name (small/
// medium/max) to the seeded session ID, for the spec to read.

type historySnapshot struct {
	Name     string `json:"name"`
	Messages []struct {
		Role string `json:"role"` // "user" | "agent"
		Text string `json:"text"`
	} `json:"messages"`
}

func main() {
	var mittoDir, snapshotsDir, workingDir, acpServer string
	flag.StringVar(&mittoDir, "dir", "", "Mitto data directory (required)")
	flag.StringVar(&snapshotsDir, "snapshots-dir", "", "Directory of history snapshot JSON files (required)")
	flag.StringVar(&workingDir, "working-dir", "/tmp", "Working directory for seeded sessions")
	flag.StringVar(&acpServer, "acp-server", "mock-acp", "ACP server name")
	flag.Parse()

	if mittoDir == "" || snapshotsDir == "" {
		log.Fatal("Error: -dir and -snapshots-dir flags are required")
	}

	os.Setenv(appdir.MittoDirEnv, mittoDir)
	appdir.ResetCache()

	sessionsDir := filepath.Join(mittoDir, "sessions")
	store, err := session.NewStore(sessionsDir)
	if err != nil {
		log.Fatalf("Failed to create session store: %v", err)
	}
	defer store.Close()

	entries, err := os.ReadDir(snapshotsDir)
	if err != nil {
		log.Fatalf("Failed to read snapshots dir %s: %v", snapshotsDir, err)
	}

	sessionIDs := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		snapshotPath := filepath.Join(snapshotsDir, entry.Name())
		raw, err := os.ReadFile(snapshotPath)
		if err != nil {
			log.Fatalf("Failed to read snapshot %s: %v", snapshotPath, err)
		}
		var snap historySnapshot
		if err := json.Unmarshal(raw, &snap); err != nil {
			log.Fatalf("Failed to parse snapshot %s: %v", snapshotPath, err)
		}
		if snap.Name == "" {
			snap.Name = entry.Name()
		}

		sessionID := session.GenerateSessionID()
		meta := session.Metadata{
			SessionID:  sessionID,
			Name:       fmt.Sprintf("perf-history-%s", snap.Name),
			ACPServer:  acpServer,
			WorkingDir: workingDir,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
			Status:     session.SessionStatusActive,
		}
		if err := store.Create(meta); err != nil {
			log.Fatalf("Failed to create session for %s: %v", snap.Name, err)
		}

		for _, m := range snap.Messages {
			var evt session.Event
			switch m.Role {
			case "agent":
				evt = session.Event{Type: session.EventTypeAgentMessage, Data: session.AgentMessageData{Text: m.Text}}
			default:
				evt = session.Event{Type: session.EventTypeUserPrompt, Data: session.UserPromptData{Message: m.Text}}
			}
			if err := store.AppendEvent(sessionID, evt); err != nil {
				log.Fatalf("Failed to append event for %s: %v", snap.Name, err)
			}
		}

		sessionIDs[snap.Name] = sessionID
		fmt.Printf("Seeded %s history: %d messages (session %s)\n", snap.Name, len(snap.Messages), sessionID)
	}

	outPath := filepath.Join(mittoDir, "perf-history-sessions.json")
	out, err := json.MarshalIndent(sessionIDs, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal output: %v", err)
	}
	if err := os.WriteFile(outPath, out, 0644); err != nil {
		log.Fatalf("Failed to write output file: %v", err)
	}
	fmt.Printf("\nOutput written to: %s\n", outPath)
}
