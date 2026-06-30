package session

import (
	"testing"
	"time"
)

// =============================================================================
// FindSingletonCandidate (mitto-4mb.3/.8) — scan/decision logic in isolation
// =============================================================================

func TestFindSingletonCandidate_NoExistingSession(t *testing.T) {
	if _, found := FindSingletonCandidate(nil, "/work", "my-prompt"); found {
		t.Error("expected no candidate for empty metadata list")
	}
}

func TestFindSingletonCandidate_OneMatchingNonArchivedSession(t *testing.T) {
	metas := []Metadata{
		{SessionID: "s1", WorkingDir: "/work", OriginPromptName: "my-prompt"},
	}
	id, found := FindSingletonCandidate(metas, "/work", "my-prompt")
	if !found {
		t.Fatal("expected a candidate")
	}
	if id != "s1" {
		t.Errorf("SessionID = %q, want %q", id, "s1")
	}
}

func TestFindSingletonCandidate_ArchivedMatchIgnored(t *testing.T) {
	metas := []Metadata{
		{SessionID: "s1", WorkingDir: "/work", OriginPromptName: "my-prompt", Archived: true},
	}
	if _, found := FindSingletonCandidate(metas, "/work", "my-prompt"); found {
		t.Error("archived session should not be a candidate")
	}
}

func TestFindSingletonCandidate_DifferentWorkingDirIgnored(t *testing.T) {
	metas := []Metadata{
		{SessionID: "s1", WorkingDir: "/other", OriginPromptName: "my-prompt"},
	}
	if _, found := FindSingletonCandidate(metas, "/work", "my-prompt"); found {
		t.Error("session in a different working dir should not be a candidate")
	}
}

func TestFindSingletonCandidate_DifferentOriginPromptNameIgnored(t *testing.T) {
	metas := []Metadata{
		{SessionID: "s1", WorkingDir: "/work", OriginPromptName: "other-prompt"},
	}
	if _, found := FindSingletonCandidate(metas, "/work", "my-prompt"); found {
		t.Error("session from a different prompt should not be a candidate")
	}
}

func TestFindSingletonCandidate_CaseInsensitivePromptMatch(t *testing.T) {
	metas := []Metadata{
		{SessionID: "s1", WorkingDir: "/work", OriginPromptName: "My-Prompt"},
	}
	id, found := FindSingletonCandidate(metas, "/work", "my-prompt")
	if !found || id != "s1" {
		t.Errorf("expected case-insensitive match, got found=%v id=%q", found, id)
	}
}

func TestFindSingletonCandidate_MultipleMatches_MostRecentlyUpdatedWins(t *testing.T) {
	older := time.Now().Add(-1 * time.Hour)
	newer := time.Now()
	metas := []Metadata{
		{SessionID: "old", WorkingDir: "/work", OriginPromptName: "my-prompt", UpdatedAt: older},
		{SessionID: "new", WorkingDir: "/work", OriginPromptName: "my-prompt", UpdatedAt: newer},
	}
	id, found := FindSingletonCandidate(metas, "/work", "my-prompt")
	if !found {
		t.Fatal("expected a candidate")
	}
	if id != "new" {
		t.Errorf("SessionID = %q, want %q (most recently updated)", id, "new")
	}
}
