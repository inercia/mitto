package cel

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// TerminalBead is one open bead carrying a class-terminal label, classified by
// TerminalBeads. Exposed to prompt templates.
type TerminalBead struct {
	ID    string
	Label string
	// Stale is true when the label was carried through a reopen (or the
	// evidence is missing/malformed): the orchestrator must remove it.
	Stale bool
	// OpenChildren counts non-closed direct children (fail-closed: 1 when the
	// enumeration failed). Only meaningful for current beads.
	OpenChildren int
	// WorkStarted is the claimed_at value to promote into work_started_at
	// when work_started_at is absent; empty otherwise.
	WorkStarted string
}

// Closable reports whether the orchestrator may close the bead this pass.
func (t TerminalBead) Closable() bool { return !t.Stale && t.OpenChildren == 0 }

type bdListItem struct {
	ID        string `json:"id"`
	IssueType string `json:"issue_type"`
	Status    string `json:"status"`
	Parent    string `json:"parent"`
}

type bdHistoryEntry struct {
	CommitDate string `json:"CommitDate"`
	Issue      struct {
		Status string `json:"status"`
	} `json:"Issue"`
}

type bdLiveBead struct {
	Status   string   `json:"status"`
	Labels   []string `json:"labels"`
	Comments []struct {
		Text      string `json:"text"`
		CreatedAt string `json:"created_at"`
	} `json:"comments"`
	Metadata map[string]json.RawMessage `json:"metadata"`
}

func parseRFC3339(v string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, v)
	return t, err == nil
}

func isOpenStatus(s string) bool { return s == "open" || s == "in_progress" }

// terminalLabelCurrent reports whether label on bead id belongs to the current
// open episode. A never-reopened bead is current; a reopened one is current
// only when its newest terminal phase comment is strictly newer than the
// reopen transition. Missing/malformed evidence is stale (fail safe).
func terminalLabelCurrent(folder, id, label string) (current bool, live bdLiveBead) {
	histOut, ok1 := runBd(folder, "history", id, "--json")
	liveOut, ok2 := runBd(folder, "show", id, "--json", "--include-comments")
	if !ok1 || !ok2 {
		return false, live
	}
	var entries []bdHistoryEntry
	var lives []bdLiveBead
	if json.Unmarshal(histOut, &entries) != nil || json.Unmarshal(liveOut, &lives) != nil ||
		len(entries) == 0 || len(lives) == 0 {
		return false, live
	}
	live = lives[0]
	if !isOpenStatus(live.Status) {
		return false, live
	}
	hasLabel := false
	for _, l := range live.Labels {
		if l == label {
			hasLabel = true
		}
	}
	if !hasLabel {
		return false, live
	}
	reopenedAt := ""
	for i := 0; i < len(entries)-1; i++ {
		if isOpenStatus(entries[i].Issue.Status) && entries[i+1].Issue.Status == "closed" {
			reopenedAt = entries[i].CommitDate
			break
		}
	}
	if reopenedAt == "" {
		return true, live
	}
	prefix := "Review [tier:"
	if label == "fixed" {
		prefix = "Fix [tier:"
	}
	var newest time.Time
	found := false
	for _, c := range live.Comments {
		if !strings.HasPrefix(c.Text, prefix) {
			continue
		}
		t, ok := parseRFC3339(c.CreatedAt)
		if !ok {
			return false, live
		}
		if !found || t.After(newest) {
			newest, found = t, true
		}
	}
	reopened, ok := parseRFC3339(reopenedAt)
	if !found || !ok {
		return false, live
	}
	return newest.After(reopened), live
}

func openChildCount(folder, id string) int {
	out, ok := runBd(folder, "children", id, "--json")
	if !ok {
		return 1
	}
	var kids []bdListItem
	if json.Unmarshal(out, &kids) != nil {
		return 1
	}
	n := 0
	for _, k := range kids {
		if k.Status != "closed" {
			n++
		}
	}
	return n
}

func metadataString(m map[string]json.RawMessage, key string) string {
	raw, ok := m[key]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// terminalBeads lists open/in_progress beads carrying a class-terminal label
// (`fixed` on bugs when fixBugs, `verified` on non-bug non-epic beads when
// workFeatures) and classifies each. Read-only; fail-safe (errors => stale).
func terminalBeads(folder string, fixBugs, workFeatures bool) []TerminalBead {
	var result []TerminalBead
	collect := func(label string, args []string, keep func(bdListItem) bool) {
		out, ok := runBd(folder, args...)
		if !ok {
			return
		}
		var items []bdListItem
		if json.Unmarshal(out, &items) != nil {
			return
		}
		for _, it := range items {
			if !keep(it) {
				continue
			}
			current, live := terminalLabelCurrent(folder, it.ID, label)
			tb := TerminalBead{ID: it.ID, Label: label, Stale: !current}
			if current {
				tb.OpenChildren = openChildCount(folder, it.ID)
				if started := metadataString(live.Metadata, "claimed_at"); started != "" &&
					metadataString(live.Metadata, "work_started_at") == "" {
					tb.WorkStarted = started
				}
			}
			result = append(result, tb)
		}
	}
	if fixBugs {
		collect("fixed", []string{"list", "--type", "bug", "--status", "open,in_progress", "--label", "fixed", "--json"},
			func(bdListItem) bool { return true })
	}
	if workFeatures {
		collect("verified", []string{"list", "--status", "open,in_progress", "--label", "verified", "--json"},
			func(it bdListItem) bool { return it.IssueType != "bug" && it.IssueType != "epic" })
	}
	return result
}

// reapableEpics returns the open epics (capped at limit) among the candidate
// beads and their parents whose direct children are all closed, treating
// assumeClosed IDs as already closed.
func reapableEpics(folder string, candidates, assumeClosed []string, limit int) []string {
	closed := map[string]bool{}
	for _, id := range assumeClosed {
		closed[id] = true
	}
	set := map[string]bool{}
	for _, cid := range candidates {
		out, ok := runBd(folder, "show", cid, "--json")
		if !ok {
			continue
		}
		var beads []bdListItem
		if json.Unmarshal(out, &beads) != nil || len(beads) == 0 {
			continue
		}
		if beads[0].IssueType == "epic" {
			set[cid] = true
		}
		if beads[0].Parent != "" {
			set[beads[0].Parent] = true
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var result []string
	for _, id := range ids {
		if limit > 0 && len(result) >= limit {
			break
		}
		out, ok := runBd(folder, "show", id, "--json")
		if !ok {
			continue
		}
		var beads []bdListItem
		if json.Unmarshal(out, &beads) != nil || len(beads) == 0 ||
			beads[0].IssueType != "epic" || beads[0].Status != "open" {
			continue
		}
		cout, ok := runBd(folder, "children", id, "--json")
		if !ok {
			continue
		}
		var kids []bdListItem
		if json.Unmarshal(cout, &kids) != nil {
			continue
		}
		open := 0
		for _, k := range kids {
			if k.Status != "closed" && !closed[k.ID] {
				open++
			}
		}
		if open == 0 {
			result = append(result, id)
		}
	}
	return result
}
