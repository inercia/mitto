package chatui

import (
	"path/filepath"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/fileutil"
)

// maxInputHistoryEntries caps the number of persisted input-history entries
// per conversation (mitto-pscc.11 Plan decision).
const maxInputHistoryEntries = 200

// inputHistoryFileVersion is the on-disk schema version for
// inputHistoryFile, bumped if the shape ever changes incompatibly.
const inputHistoryFileVersion = 1

// inputHistoryFile is the on-disk JSON shape persisted per conversation at
// $MITTO_DIR/chat-history/<conversation-id>.json.
type inputHistoryFile struct {
	Version int      `json:"version"`
	Entries []string `json:"entries"`
}

// inputHistory implements readline-style up/down recall over a list of
// previously submitted lines, mirroring reeflective/readline's semantics
// (internal/cmd/cli.go) so `mitto conversation chat` does not regress
// relative to `mitto cli`.
//
// cursor indexes into entries: len(entries) means "not currently recalling"
// (the live draft is shown). Prev walks backwards from there, stashing the
// in-progress draft on the first step; Next walks forward and restores the
// draft once the cursor walks past the newest entry. Any ordinary edit
// resets the cursor via ResetCursor, abandoning recall — matching readline,
// where typing while browsing history starts editing a fresh copy of the
// recalled line rather than mutating the stored entry.
type inputHistory struct {
	entries []string
	cursor  int
	draft   string
}

func newInputHistory() *inputHistory {
	return &inputHistory{cursor: 0}
}

// SeedInputHistory replays previously persisted entries, oldest first, in
// the same order Add would have produced them. Called once before the
// program starts (see Model.SeedInputHistory) — never seeds mid-run, so no
// race with a live Add.
func (h *inputHistory) Seed(entries []string) {
	for _, e := range entries {
		h.Add(e)
	}
}

// Add records a newly submitted line, skipping empty lines and immediate
// duplicates of the most recent entry (both readline behaviors), then
// trims to the most recent maxInputHistoryEntries and resets the recall
// cursor to "not recalling".
func (h *inputHistory) Add(line string) {
	if line == "" {
		return
	}
	if n := len(h.entries); n > 0 && h.entries[n-1] == line {
		h.ResetCursor()
		return
	}
	h.entries = append(h.entries, line)
	if n := len(h.entries); n > maxInputHistoryEntries {
		h.entries = h.entries[n-maxInputHistoryEntries:]
	}
	h.ResetCursor()
}

// ResetCursor abandons any in-progress recall, discarding the stashed
// draft. Called whenever the user edits the input normally instead of
// walking history.
func (h *inputHistory) ResetCursor() {
	h.cursor = len(h.entries)
	h.draft = ""
}

// Prev recalls the entry before the current cursor position, stashing
// current (the live textarea value) as the draft on the first step so Next
// can restore it later. Returns ("", false) when there is nothing older to
// recall (already at the oldest entry, or history is empty).
func (h *inputHistory) Prev(current string) (string, bool) {
	if len(h.entries) == 0 || h.cursor <= 0 {
		return "", false
	}
	if h.cursor == len(h.entries) {
		h.draft = current
	}
	h.cursor--
	return h.entries[h.cursor], true
}

// Next recalls the entry after the current cursor position, or restores
// the stashed draft once the cursor walks past the newest entry. Returns
// ("", false) when not currently recalling (cursor already at
// len(entries)).
func (h *inputHistory) Next(current string) (string, bool) {
	if h.cursor >= len(h.entries) {
		return "", false
	}
	h.cursor++
	if h.cursor == len(h.entries) {
		return h.draft, true
	}
	return h.entries[h.cursor], true
}

// chatHistoryPath returns the on-disk path for a conversation's persisted
// input history ($MITTO_DIR/chat-history/<conversationID>.json).
func chatHistoryPath(conversationID string) (string, error) {
	dir, err := appdir.ChatHistoryDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, conversationID+".json"), nil
}

// LoadInputHistory reads a conversation's persisted input history from
// disk, oldest first. A missing file or any read/parse error yields an
// empty, non-fatal result (history degrades to in-memory only) — callers
// should not treat the error as fatal to the program.
func LoadInputHistory(conversationID string) ([]string, error) {
	path, err := chatHistoryPath(conversationID)
	if err != nil {
		return nil, err
	}
	var f inputHistoryFile
	if err := fileutil.ReadJSON(path, &f); err != nil {
		return nil, err
	}
	return f.Entries, nil
}

// SaveInputHistory persists a conversation's input history to disk,
// atomically. Called from a tea.Cmd (never inline in Update) after each
// accepted submit.
func SaveInputHistory(conversationID string, entries []string) error {
	path, err := chatHistoryPath(conversationID)
	if err != nil {
		return err
	}
	return fileutil.WriteJSONAtomic(path, &inputHistoryFile{
		Version: inputHistoryFileVersion,
		Entries: entries,
	}, 0644)
}
