package mcpserver

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func projectionTestEntry(id, child, supplied, effective string, association childReportAssociation, status string) childReportEntry {
	payload, _ := json.Marshal(struct {
		Status  string `json:"status"`
		Summary string `json:"summary"`
	}{status, "genuine evidence"})
	return childReportEntry{
		ID: id, ChildID: child, ReceivedAt: time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC),
		SuppliedTaskID: supplied, EffectiveTaskID: effective, Association: association, Report: payload,
	}
}

func TestChildReportProjection_Status(t *testing.T) {
	type statusCase struct {
		status     string
		known      bool
		terminal   bool
		successful bool
	}
	cases := []statusCase{
		{"completed", true, true, true},
		{"failed", true, true, false},
		{"partial", true, true, false},
		{"verification_pending", true, true, false},
		{"in_progress", true, false, false},
		{"", false, false, false},
		{"stuck", false, false, false},
		{"done", false, false, false},
		{"progress", false, false, false},
		{"blocked", false, false, false},
		{"auto_completed", false, false, false},
		{"unknown / 未完", false, false, false},
		{" completed", false, false, false},
		{"completed\n", false, false, false},
	}
	for _, status := range []string{"completed", "failed", "partial", "verification_pending", "in_progress"} {
		cases = append(cases, statusCase{status: strings.ToUpper(status)},
			statusCase{status: strings.ToUpper(status[:1]) + status[1:]})
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			want := childReportOutcome{Status: tc.status, Known: tc.known, Terminal: tc.terminal, Successful: tc.successful}
			if got := interpretChildReportStatus(tc.status); got != want {
				t.Fatalf("outcome = %+v, want %+v", got, want)
			}
			entry := projectionTestEntry("entry", "child", "task", "task", childReportExplicit, tc.status)
			p := projectChildReport([]childReportEntry{entry}, "child", "task", nil)
			if p.Outcome != want || p.taskSucceeded() != tc.successful || p.taskTerminal() != tc.terminal || p.waitResolved() != tc.terminal {
				t.Fatalf("projection decisions differ from outcome: %+v", p)
			}
			if p.Diagnostic != nil || p.Entry == nil || !bytes.Equal(p.Entry.Report, entry.Report) {
				t.Fatal("genuine client text became a diagnostic or changed payload")
			}
			if !tc.known && p.Reason != "unknown_status" {
				t.Fatalf("unknown status reason = %q", p.Reason)
			}
			if p.reconciliationEligible(true) != !tc.terminal || p.reconciliationEligible(false) {
				t.Fatal("reconciliation confused task success with terminality or wait-only mode")
			}
		})
	}
}

func TestChildReportProjection_Selection(t *testing.T) {
	explicit := projectionTestEntry("explicit", "child", "A", "A", childReportExplicit, "completed")
	inferred := projectionTestEntry("inferred", "child", "", "A", childReportInferred, "failed")
	unassociated := projectionTestEntry("unassociated", "child", "", "", childReportUnassociated, "completed")
	otherTask := projectionTestEntry("other-task", "child", "B", "B", childReportExplicit, "completed")
	otherChild := projectionTestEntry("other-child", "other", "A", "A", childReportExplicit, "completed")
	progress := projectionTestEntry("correction-progress", "child", "A", "A", childReportExplicit, "in_progress")
	malformedAssociation := projectionTestEntry("unassociated-with-ID", "child", "", "A", childReportUnassociated, "completed")
	unknownAssociation := projectionTestEntry("unknown-association", "child", "A", "A", "future", "completed")
	exact := projectionTestEntry("exact-ID", "child", " A ", " A ", childReportExplicit, "completed")
	cases := []struct {
		name, task, selected            string
		entries                         []childReportEntry
		matches, terminal, success      bool
		moreHistory, unassociatedExists bool
	}{
		{name: "empty", task: "A"},
		{name: "other-child-only", task: "A", entries: []childReportEntry{otherChild}},
		{name: "explicit-named", task: "A", entries: []childReportEntry{explicit}, selected: explicit.ID, matches: true, terminal: true, success: true},
		{name: "inferred-named", task: "A", entries: []childReportEntry{inferred}, selected: inferred.ID, matches: true, terminal: true},
		{name: "unassociated-not-named", task: "A", entries: []childReportEntry{unassociated}, selected: unassociated.ID, unassociatedExists: true},
		{name: "mismatch-not-named", task: "A", entries: []childReportEntry{otherTask}, selected: otherTask.ID},
		{name: "requires-associated-provenance", task: "A", entries: []childReportEntry{malformedAssociation}, selected: malformedAssociation.ID, unassociatedExists: true},
		{name: "unknown-provenance-not-named", task: "A", entries: []childReportEntry{unknownAssociation}, selected: unknownAssociation.ID},
		{name: "later-inferred-authoritative", task: "A", entries: []childReportEntry{explicit, inferred}, selected: inferred.ID, matches: true, terminal: true, moreHistory: true},
		{name: "success-to-progress", task: "A", entries: []childReportEntry{explicit, progress}, selected: progress.ID, matches: true, moreHistory: true},
		{name: "failure-to-success", task: "A", entries: []childReportEntry{inferred, explicit}, selected: explicit.ID, matches: true, terminal: true, success: true, moreHistory: true},
		{name: "later-unrelated-does-not-displace", task: "A", entries: []childReportEntry{explicit, otherTask, unassociated, otherChild}, selected: explicit.ID, matches: true, terminal: true, success: true, moreHistory: true, unassociatedExists: true},
		{name: "fallback-latest-evidence", task: "C", entries: []childReportEntry{explicit, otherTask, unassociated, otherChild}, selected: unassociated.ID, moreHistory: true, unassociatedExists: true},
		{name: "unnamed-explicit", entries: []childReportEntry{explicit}, selected: explicit.ID, matches: true, terminal: true, success: true},
		{name: "unnamed-inferred", entries: []childReportEntry{inferred}, selected: inferred.ID, matches: true, terminal: true},
		{name: "unnamed-unassociated", entries: []childReportEntry{unassociated}, selected: unassociated.ID, matches: true, terminal: true, success: true, unassociatedExists: true},
		{name: "unnamed-latest-any-task", entries: []childReportEntry{explicit, unassociated, otherTask}, selected: otherTask.ID, matches: true, terminal: true, success: true, moreHistory: true, unassociatedExists: true},
		{name: "no-ID-trimming", task: "A", entries: []childReportEntry{exact}, selected: exact.ID},
		{name: "no-ID-case-folding", task: "a", entries: []childReportEntry{explicit}, selected: explicit.ID},
		{name: "exact-whitespace-ID", task: " A ", entries: []childReportEntry{exact}, selected: exact.ID, matches: true, terminal: true, success: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Later appends have EARLIER receipt times. No clock sorting may win.
			entries := append([]childReportEntry(nil), tc.entries...)
			for i := range entries {
				entries[i].ReceivedAt = explicit.ReceivedAt.Add(-time.Duration(i) * time.Hour)
			}
			before, err := json.Marshal(entries)
			if err != nil {
				t.Fatal(err)
			}
			p := projectChildReport(entries, "child", tc.task, nil)
			selected := ""
			if p.Entry != nil {
				selected = p.Entry.ID
			}
			if selected != tc.selected || p.RequestedTaskID != tc.task || p.TaskMatches != tc.matches || p.taskTerminal() != tc.terminal || p.taskSucceeded() != tc.success || p.waitResolved() != tc.terminal {
				t.Fatalf("selected=%q projection=%+v, want %+v", selected, p, tc)
			}
			if p.HasMoreHistory != tc.moreHistory || p.HasUnassociatedEvidence != tc.unassociatedExists {
				t.Fatalf("history indicators = %+v", p)
			}
			if p.reconciliationEligible(true) != !tc.terminal || p.reconciliationEligible(false) {
				t.Fatal("nonapplicable evidence suppressed reconciliation")
			}
			after, err := json.Marshal(entries)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("projection modified history/association")
			}
		})
	}
}

func TestChildReportProjection_Diagnostics(t *testing.T) {
	cases := []struct {
		name, task, status, effective     string
		association                       childReportAssociation
		diagnosticChild, diagnosticTask   string
		wantDiagnostic, terminal, success bool
	}{
		{"no-evidence", "A", "", "", childReportUnassociated, "child", "A", true, false, false},
		{"mismatch", "A", "completed", "B", childReportExplicit, "child", "A", true, false, false},
		{"unassociated", "A", "completed", "", childReportUnassociated, "child", "A", true, false, false},
		{"progress", "A", "in_progress", "A", childReportExplicit, "child", "A", true, false, false},
		{"success", "A", "completed", "A", childReportExplicit, "child", "A", true, true, true},
		{"failure", "A", "failed", "A", childReportExplicit, "child", "A", true, true, false},
		{"old-task-diagnostic", "A", "in_progress", "A", childReportExplicit, "child", "B", false, false, false},
		{"other-child-diagnostic", "A", "in_progress", "A", childReportExplicit, "other", "A", false, false, false},
		{"unnamed-does-not-borrow-named-diagnostic", "", "in_progress", "A", childReportExplicit, "child", "A", false, false, false},
		{"unnamed-diagnostic", "", "in_progress", "", childReportUnassociated, "child", "", true, false, false},
	}
	for _, kind := range []childWaitDiagnosticKind{childDiagnosticAutoCompleted, childDiagnosticFailed, childDiagnosticStuck} {
		for _, tc := range cases {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				var entries []childReportEntry
				if tc.status != "" {
					entries = []childReportEntry{projectionTestEntry("evidence", "child", tc.effective, tc.effective, tc.association, tc.status)}
				}
				diagnostic := childWaitDiagnostic{ChildID: tc.diagnosticChild, TaskID: tc.diagnosticTask, Kind: kind, Reason: "observed", Message: "server observation"}
				p := projectChildReport(entries, "child", tc.task, &diagnostic)
				if (p.Diagnostic != nil) != tc.wantDiagnostic || p.taskSucceeded() != tc.success || p.taskTerminal() != tc.terminal || p.waitResolved() != (tc.terminal || tc.wantDiagnostic) {
					t.Fatalf("diagnostic conflated with task outcome: %+v", p)
				}
				if p.reconciliationEligible(true) != !tc.terminal || p.reconciliationEligible(false) {
					t.Fatal("diagnostic suppressed explicit reconciliation or created wait-only dispatch")
				}
			})
		}
	}
}

func TestChildReportProjection_Ownership(t *testing.T) {
	raw := json.RawMessage(`{ "status":"stuck", "summary":"Résumé 🦉", "unknown":9007199254740993, "details":"unchanged" }`)
	originalRaw := bytes.Clone(raw)
	entry := projectionTestEntry("stable-ID", "child", " Task-A ", " Task-A ", childReportExplicit, "completed")
	entry.Report = raw
	entries := []childReportEntry{entry}
	diagnostic := childWaitDiagnostic{ChildID: "child", TaskID: " Task-A ", Kind: childDiagnosticFailed, Legacy: json.RawMessage(`{"unknown":9007199254740993}`)}
	p := projectChildReport(entries, "child", " Task-A ", &diagnostic)
	if !reflect.DeepEqual(*p.Entry, entry) || !reflect.DeepEqual(*p.Diagnostic, diagnostic) {
		t.Fatal("projection changed raw payload, supplied identity, receipt, ID or provenance")
	}
	// Unnamed selection must not consume or reassign an entry for a later query.
	_ = projectChildReport(entries, "child", "", nil)
	if next := projectChildReport(entries, "child", "B", nil); next.TaskMatches || next.taskSucceeded() {
		t.Fatal("unnamed query reassigned evidence to a new named task")
	}
	p.Entry.Report[0] = '['
	p.Entry.SuppliedTaskID = "changed"
	p.Diagnostic.Legacy[0] = '['
	if !bytes.Equal(entries[0].Report, originalRaw) || entries[0].SuppliedTaskID != " Task-A " || diagnostic.Legacy[0] != '{' {
		t.Fatal("returned projection aliases source state")
	}
	second := projectChildReport(entries, "child", " Task-A ", &diagnostic)
	entries[0].Report[0] = '['
	diagnostic.Legacy[0] = '['
	if second.Entry.Report[0] != '{' || second.Diagnostic.Legacy[0] != '{' {
		t.Fatal("source mutations changed a previously returned view")
	}
}

func TestChildReportProjection_InvalidStatusPayload(t *testing.T) {
	for _, raw := range []string{`{"status":1}`, `{"status":{"completed":true}}`, `["completed"]`, `{"status":"completed",`} {
		t.Run(raw, func(t *testing.T) {
			entry := projectionTestEntry("evidence", "child", "A", "A", childReportExplicit, "completed")
			entry.Report = json.RawMessage(raw)
			p := projectChildReport([]childReportEntry{entry}, "child", "A", nil)
			if p.Reason != "invalid_report_status" || p.Outcome.Known || p.taskSucceeded() || p.taskTerminal() || p.waitResolved() {
				t.Fatalf("invalid status claimed an outcome: %+v", p)
			}
			if p.Entry == nil || string(p.Entry.Report) != raw || p.Diagnostic != nil {
				t.Fatal("invalid status payload was modified, discarded or turned into a diagnostic")
			}
		})
	}
}
