package session

// FindConversationByBeadsIssue scans persisted session metadata for a
// non-archived session in the given workingDir whose BeadsIssue equals
// beadsIssue. If multiple match, the most recently updated wins. Returns
// the matching session ID and true, or ("", false) when none match or
// when beadsIssue is empty.
//
// Comparison is case-sensitive (issue IDs like "mitto-4mb.3" are canonical),
// unlike FindSingletonCandidate which folds case on prompt names.
func FindConversationByBeadsIssue(metas []Metadata, workingDir, beadsIssue string) (string, bool) {
	if beadsIssue == "" {
		return "", false
	}
	var best Metadata
	found := false
	for _, m := range metas {
		if m.Archived || m.WorkingDir != workingDir || m.BeadsIssue != beadsIssue {
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

// FindConversationByTitle scans persisted session metadata for a
// non-archived session in the given workingDir whose canonical reuseTitle
// lookup key equals title. If multiple match, the most recently updated
// wins. Returns the matching session ID and true, or ("", false) when none
// match or when title is empty.
//
// The lookup key is ReuseTitleKey (mitto-9vng), NOT the displayed Name: a
// caller-supplied title now survives as Name instead of being clobbered to
// target.title, so matching must not use Name for conversations that set
// ReuseTitleKey. For conversations created before this field existed (empty
// ReuseTitleKey), fall back to matching on Name — those rows still have
// Name == target.title because the old clobbering behavior set it that way.
//
// Comparison is case-sensitive: target.title is an author-chosen canonical
// identifier declared in prompt frontmatter (like BeadsIssue), not user
// free-text like FindSingletonCandidate's prompt-name key.
func FindConversationByTitle(metas []Metadata, workingDir, title string) (string, bool) {
	if title == "" {
		return "", false
	}
	var best Metadata
	found := false
	for _, m := range metas {
		if m.Archived || m.WorkingDir != workingDir {
			continue
		}
		matches := m.ReuseTitleKey == title
		if m.ReuseTitleKey == "" {
			matches = m.Name == title
		}
		if !matches {
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
