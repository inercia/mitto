package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/appdir"
)

var (
	cleanupDryRun  bool
	cleanupVerbose bool
)

// toolsSessionCleanupCmd removes duplicate session_end events from old sessions.
var toolsSessionCleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Remove duplicate session_end events from sessions",
	Long: `Remove duplicate session_end events from old sessions.

This command scans all sessions and removes duplicate session_end events,
keeping only the first one. This fixes a bug where multiple session_end
events could be written to the same session.

The cleanup process:
1. Scans all sessions in the sessions directory
2. Identifies sessions with duplicate session_end events
3. Removes duplicates, keeping only the first session_end
4. Resequences all events with correct sequence numbers
5. Updates the metadata.json event count

Use --dry-run to preview changes without modifying files.`,
	RunE: runSessionCleanup,
}

func init() {
	toolsSessionCmd.AddCommand(toolsSessionCleanupCmd)

	toolsSessionCleanupCmd.Flags().BoolVar(&cleanupDryRun, "dry-run", false,
		"Show what would be changed without modifying files")
	toolsSessionCleanupCmd.Flags().BoolVarP(&cleanupVerbose, "verbose", "v", false,
		"Show detailed information about each session")
}

func runSessionCleanup(_ *cobra.Command, _ []string) error {
	sessionsDir, err := appdir.SessionsDir()
	if err != nil {
		return fmt.Errorf("error getting sessions directory: %w", err)
	}

	fmt.Printf("📁 Sessions directory: %s\n", sessionsDir)
	if cleanupDryRun {
		fmt.Println("🔍 Dry-run mode - no files will be modified")
	}
	fmt.Println()

	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return fmt.Errorf("error reading sessions directory: %w", err)
	}

	var totalSessions, cleanedSessions, duplicatesRemoved int

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		sessionID := entry.Name()
		eventsPath := filepath.Join(sessionsDir, sessionID, "events.jsonl")

		if _, err := os.Stat(eventsPath); os.IsNotExist(err) {
			continue
		}

		totalSessions++
		removed, err := cleanupSession(eventsPath, sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Error processing %s: %v\n", sessionID, err)
			continue
		}
		if removed > 0 {
			cleanedSessions++
			duplicatesRemoved += removed
		}
	}

	fmt.Println()
	fmt.Printf("📊 Summary:\n")
	fmt.Printf("   Total sessions scanned: %d\n", totalSessions)
	fmt.Printf("   Sessions with duplicates: %d\n", cleanedSessions)
	fmt.Printf("   Duplicate session_end events removed: %d\n", duplicatesRemoved)
	if cleanupDryRun && duplicatesRemoved > 0 {
		fmt.Println("\n💡 Run without --dry-run to apply changes")
	}

	return nil
}

// cleanupEvent represents a session event (minimal for cleanup purposes).
type cleanupEvent struct {
	Seq       int64           `json:"seq"`
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// cleanupSession removes duplicate session_end events from a session.
// Returns the number of duplicates removed.
func cleanupSession(eventsPath, sessionID string) (int, error) {
	events, err := readCleanupEvents(eventsPath)
	if err != nil {
		return 0, err
	}

	// Find session_end events
	var sessionEndIndices []int
	for i, event := range events {
		if event.Type == "session_end" {
			sessionEndIndices = append(sessionEndIndices, i)
		}
	}

	// No duplicates
	if len(sessionEndIndices) <= 1 {
		if cleanupVerbose {
			fmt.Printf("✓ %s: OK (%d events)\n", sessionID, len(events))
		}
		return 0, nil
	}

	// Found duplicates
	duplicates := len(sessionEndIndices) - 1
	fmt.Printf("🔧 %s: Found %d duplicate session_end events (keeping first at seq %d)\n",
		sessionID, duplicates, events[sessionEndIndices[0]].Seq)

	if cleanupVerbose {
		for _, idx := range sessionEndIndices {
			fmt.Printf("   - seq %d at index %d\n", events[idx].Seq, idx)
		}
	}

	if cleanupDryRun {
		return duplicates, nil
	}

	// Remove all but the first session_end
	keepFirstSessionEnd := true
	var newEvents []cleanupEvent
	for _, event := range events {
		if event.Type == "session_end" {
			if keepFirstSessionEnd {
				newEvents = append(newEvents, event)
				keepFirstSessionEnd = false
			}
			// Skip subsequent session_end events
		} else {
			newEvents = append(newEvents, event)
		}
	}

	// Reassign sequence numbers
	for i := range newEvents {
		newEvents[i].Seq = int64(i + 1)
	}

	// Write back atomically
	if err := writeCleanupEvents(eventsPath, newEvents); err != nil {
		return 0, fmt.Errorf("failed to write events: %w", err)
	}

	// Update metadata event count
	if err := updateCleanupMetadata(eventsPath, len(newEvents)); err != nil {
		fmt.Fprintf(os.Stderr, "   ⚠️  Warning: could not update metadata: %v\n", err)
	}

	fmt.Printf("   ✅ Removed %d duplicates, updated sequences\n", duplicates)
	return duplicates, nil
}

// readCleanupEvents reads all events from a JSONL file.
func readCleanupEvents(path string) ([]cleanupEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var events []cleanupEvent
	scanner := bufio.NewScanner(f)
	const maxScannerBuffer = 10 * 1024 * 1024
	scanner.Buffer(make([]byte, 0, 64*1024), maxScannerBuffer)

	for scanner.Scan() {
		var event cleanupEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("failed to unmarshal event: %w", err)
		}
		events = append(events, event)
	}

	return events, scanner.Err()
}

// writeCleanupEvents writes events to a JSONL file atomically.
func writeCleanupEvents(path string, events []cleanupEvent) error {
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			f.Close()
			os.Remove(tmpPath)
			return err
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			f.Close()
			os.Remove(tmpPath)
			return err
		}
	}

	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	return os.Rename(tmpPath, path)
}

// updateCleanupMetadata updates the event_count in metadata.json.
func updateCleanupMetadata(eventsPath string, count int) error {
	metadataPath := filepath.Join(filepath.Dir(eventsPath), "metadata.json")

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return err
	}

	var meta map[string]interface{}
	if err := json.Unmarshal(data, &meta); err != nil {
		return err
	}

	meta["event_count"] = count

	newData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(metadataPath, append(newData, '\n'), 0644)
}
