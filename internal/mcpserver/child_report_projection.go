package mcpserver

import (
	"bytes"
	"encoding/json"
)

// childReportOutcome interprets genuine payload status, not server diagnostics
// or acceptance PASS. Unknown status text remains evidence, never completion.
type childReportOutcome struct {
	Status     string
	Known      bool
	Terminal   bool
	Successful bool
}

func interpretChildReportStatus(status string) childReportOutcome {
	outcome := childReportOutcome{Status: status}
	switch status {
	case "completed":
		outcome.Known, outcome.Terminal, outcome.Successful = true, true, true
	case "failed", "partial", "verification_pending":
		outcome.Known, outcome.Terminal = true, true
	case "in_progress":
		outcome.Known = true
	}
	return outcome
}

// childReportProjection is an internal, owned compact view, deliberately not a
// public MCP schema. It is currently inactive: live waits still use the legacy
// compatibility slots. Entry carries the unchanged ID, receipt time, supplied
// identity, effective association and raw payload together. Nonapplicable
// evidence may be shown, but cannot satisfy the requested task.
type childReportProjection struct {
	RequestedTaskID         string
	Entry                   *childReportEntry
	TaskMatches             bool
	Outcome                 childReportOutcome
	Reason                  string
	Diagnostic              *childWaitDiagnostic
	HasMoreHistory          bool
	HasUnassociatedEvidence bool
}

// childReportAppliesToTask deliberately preserves unnamed legacy queries:
// they select any genuine evidence without changing its durable association.
func childReportAppliesToTask(entry childReportEntry, taskID string) bool {
	if taskID == "" {
		return true
	}
	return entry.EffectiveTaskID == taskID &&
		(entry.Association == childReportExplicit || entry.Association == childReportInferred)
}

// projectChildReport uses append order only. The caller owns synchronization of
// the inputs; the returned view shares no mutable payload bytes with them.
// Diagnostics are supplied separately and never inferred from client text.
func projectChildReport(entries []childReportEntry, childID, taskID string, diagnostic *childWaitDiagnostic) childReportProjection {
	projection := childReportProjection{RequestedTaskID: taskID}
	latest, applicable, count := -1, -1, 0
	for i, entry := range entries {
		if entry.ChildID != childID {
			continue
		}
		latest, count = i, count+1
		if entry.Association == childReportUnassociated {
			projection.HasUnassociatedEvidence = true
		}
		if childReportAppliesToTask(entry, taskID) {
			applicable = i
		}
	}
	selected := applicable
	if selected == -1 {
		selected = latest
	}
	projection.TaskMatches = applicable != -1
	projection.HasMoreHistory = count > 1
	if selected != -1 {
		entry := entries[selected]
		entry.Report = bytes.Clone(entry.Report)
		projection.Entry = &entry
		var payload struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(entry.Report, &payload); err != nil {
			projection.Reason = "invalid_report_status"
		} else {
			projection.Outcome = interpretChildReportStatus(payload.Status)
			if !projection.Outcome.Known {
				projection.Reason = "unknown_status"
			}
		}
	}
	if diagnostic != nil && diagnostic.ChildID == childID && diagnostic.TaskID == taskID {
		owned := *diagnostic
		owned.Legacy = bytes.Clone(diagnostic.Legacy)
		projection.Diagnostic = &owned
	}
	return projection
}

func (p childReportProjection) taskSucceeded() bool {
	return p.TaskMatches && p.Outcome.Successful
}

func (p childReportProjection) taskTerminal() bool {
	return p.TaskMatches && p.Outcome.Terminal
}

func (p childReportProjection) waitResolved() bool {
	return p.taskTerminal() || p.Diagnostic != nil
}

// reconciliationEligible is only the evidence decision for an explicit prompt,
// not queue admission, a timer reset or a replacement for delivery safeguards.
func (p childReportProjection) reconciliationEligible(explicitPrompt bool) bool {
	return explicitPrompt && !p.taskTerminal()
}
