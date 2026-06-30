package session

import "strings"

// FindSingletonCandidate scans persisted session metadata for a non-archived
// session in the given workingDir whose OriginPromptName matches promptName
// (case-insensitive). If multiple match, the most recently updated wins. It
// returns the matching session ID and true, or ("", false) when none match.
func FindSingletonCandidate(metas []Metadata, workingDir, promptName string) (string, bool) {
	var best Metadata
	found := false
	for _, m := range metas {
		if m.Archived || m.WorkingDir != workingDir || !strings.EqualFold(m.OriginPromptName, promptName) {
			continue
		}
		if !found || m.UpdatedAt.After(best.UpdatedAt) {
			best = m
			found = true
		}
	}
	if !found {
		return "", false
	}
	return best.SessionID, true
}
