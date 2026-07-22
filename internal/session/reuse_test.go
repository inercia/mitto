package session

import (
	"testing"
	"time"
)

// =============================================================================
// FindConversationByBeadsIssue — scan/decision logic in isolation
// =============================================================================

func TestFindConversationByBeadsIssue_NoExistingSession(t *testing.T) {
	if _, found := FindConversationByBeadsIssue(nil, "/work", "mitto-123"); found {
		t.Error("expected no candidate for empty metadata list")
	}
}

func TestFindConversationByBeadsIssue_OneMatchingNonArchived(t *testing.T) {
	metas := []Metadata{
		{SessionID: "s1", WorkingDir: "/work", BeadsIssue: "mitto-123"},
	}
	id, found := FindConversationByBeadsIssue(metas, "/work", "mitto-123")
	if !found {
		t.Fatal("expected a candidate")
	}
	if id != "s1" {
		t.Errorf("SessionID = %q, want %q", id, "s1")
	}
}

func TestFindConversationByBeadsIssue_ArchivedMatchIgnored(t *testing.T) {
	metas := []Metadata{
		{SessionID: "s1", WorkingDir: "/work", BeadsIssue: "mitto-123", Archived: true},
	}
	if _, found := FindConversationByBeadsIssue(metas, "/work", "mitto-123"); found {
		t.Error("archived session should not be a candidate")
	}
}

func TestFindConversationByBeadsIssue_DifferentWorkingDirIgnored(t *testing.T) {
	metas := []Metadata{
		{SessionID: "s1", WorkingDir: "/other", BeadsIssue: "mitto-123"},
	}
	if _, found := FindConversationByBeadsIssue(metas, "/work", "mitto-123"); found {
		t.Error("session in a different working dir should not be a candidate")
	}
}

func TestFindConversationByBeadsIssue_DifferentBeadsIssueIgnored(t *testing.T) {
	metas := []Metadata{
		{SessionID: "s1", WorkingDir: "/work", BeadsIssue: "mitto-999"},
	}
	if _, found := FindConversationByBeadsIssue(metas, "/work", "mitto-123"); found {
		t.Error("session linked to a different beads issue should not be a candidate")
	}
}

func TestFindConversationByBeadsIssue_EmptyBeadsIssueReturnsFalse(t *testing.T) {
	metas := []Metadata{
		{SessionID: "s1", WorkingDir: "/work", BeadsIssue: ""},
	}
	if _, found := FindConversationByBeadsIssue(metas, "/work", ""); found {
		t.Error("empty beadsIssue should never match")
	}
}

func TestFindConversationByBeadsIssue_CaseSensitive(t *testing.T) {
	metas := []Metadata{
		{SessionID: "s1", WorkingDir: "/work", BeadsIssue: "Mitto-123"},
	}
	if _, found := FindConversationByBeadsIssue(metas, "/work", "mitto-123"); found {
		t.Error("expected case-sensitive match to fail")
	}
}

func TestFindConversationByBeadsIssue_MultipleMatches_MostRecentlyUpdatedWins(t *testing.T) {
	older := time.Now().Add(-1 * time.Hour)
	newer := time.Now()
	metas := []Metadata{
		{SessionID: "old", WorkingDir: "/work", BeadsIssue: "mitto-123", UpdatedAt: older},
		{SessionID: "new", WorkingDir: "/work", BeadsIssue: "mitto-123", UpdatedAt: newer},
	}
	id, found := FindConversationByBeadsIssue(metas, "/work", "mitto-123")
	if !found {
		t.Fatal("expected a candidate")
	}
	if id != "new" {
		t.Errorf("SessionID = %q, want %q (most recently updated)", id, "new")
	}
}

// =============================================================================
// FindConversationByTitle — scan/decision logic in isolation
// =============================================================================

func TestFindConversationByTitle_NoExistingSession(t *testing.T) {
	if _, found := FindConversationByTitle(nil, "/work", "Weekly triage"); found {
		t.Error("expected no candidate for empty metadata list")
	}
}

func TestFindConversationByTitle_OneMatchingNonArchived(t *testing.T) {
	metas := []Metadata{
		{SessionID: "s1", WorkingDir: "/work", Name: "Weekly triage"},
	}
	id, found := FindConversationByTitle(metas, "/work", "Weekly triage")
	if !found {
		t.Fatal("expected a candidate")
	}
	if id != "s1" {
		t.Errorf("SessionID = %q, want %q", id, "s1")
	}
}

func TestFindConversationByTitle_ArchivedMatchIgnored(t *testing.T) {
	metas := []Metadata{
		{SessionID: "s1", WorkingDir: "/work", Name: "Weekly triage", Archived: true},
	}
	if _, found := FindConversationByTitle(metas, "/work", "Weekly triage"); found {
		t.Error("archived session should not be a candidate")
	}
}

func TestFindConversationByTitle_DifferentWorkingDirIgnored(t *testing.T) {
	metas := []Metadata{
		{SessionID: "s1", WorkingDir: "/other", Name: "Weekly triage"},
	}
	if _, found := FindConversationByTitle(metas, "/work", "Weekly triage"); found {
		t.Error("session in a different working dir should not be a candidate")
	}
}

func TestFindConversationByTitle_DifferentTitleIgnored(t *testing.T) {
	metas := []Metadata{
		{SessionID: "s1", WorkingDir: "/work", Name: "Daily standup"},
	}
	if _, found := FindConversationByTitle(metas, "/work", "Weekly triage"); found {
		t.Error("session with a different title should not be a candidate")
	}
}

func TestFindConversationByTitle_EmptyTitleReturnsFalse(t *testing.T) {
	metas := []Metadata{
		{SessionID: "s1", WorkingDir: "/work", Name: ""},
	}
	if _, found := FindConversationByTitle(metas, "/work", ""); found {
		t.Error("empty title should never match")
	}
}

func TestFindConversationByTitle_CaseSensitive(t *testing.T) {
	metas := []Metadata{
		{SessionID: "s1", WorkingDir: "/work", Name: "Weekly Triage"},
	}
	if _, found := FindConversationByTitle(metas, "/work", "weekly triage"); found {
		t.Error("expected case-sensitive match to fail")
	}
}

func TestFindConversationByTitle_MultipleMatches_MostRecentlyUpdatedWins(t *testing.T) {
	older := time.Now().Add(-1 * time.Hour)
	newer := time.Now()
	metas := []Metadata{
		{SessionID: "old", WorkingDir: "/work", Name: "Weekly triage", UpdatedAt: older},
		{SessionID: "new", WorkingDir: "/work", Name: "Weekly triage", UpdatedAt: newer},
	}
	id, found := FindConversationByTitle(metas, "/work", "Weekly triage")
	if !found {
		t.Fatal("expected a candidate")
	}
	if id != "new" {
		t.Errorf("SessionID = %q, want %q (most recently updated)", id, "new")
	}
}
