package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/session"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Inspect schemas obtained through tools/list, not a parallel hand-built schema.
func TestChildrenTasksContract_RegisteredSchema(t *testing.T) {
	s, _, parent, _ := setupParentChildSessions(t, 1)
	cs := connectBoundProtocolSession(t, historyProtocolServer(t, s), sessionBindingToken(t, s, parent))
	listed, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := make(map[string]*mcp.Tool)
	for _, tool := range listed.Tools {
		tools[tool.Name] = tool
	}
	history := tools["mitto_children_tasks_history"]
	if history == nil {
		t.Fatal("history tool not registered")
	}
	schemaMap := func(schema any) map[string]any {
		raw, err := json.Marshal(schema)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	input := schemaMap(history.InputSchema)
	props := input["properties"].(map[string]any)
	for _, key := range []string{"self_id", "child_id", "task_id", "association", "report_id", "before_report_id", "limit"} {
		if props[key] == nil {
			t.Fatalf("missing history input %s", key)
		}
	}
	if len(props) != 7 {
		t.Fatalf("unexpected namespace selector in history input: %+v", props)
	}
	output := schemaMap(history.OutputSchema)
	entries := output["properties"].(map[string]any)["entries"].(map[string]any)
	entryProps := entries["items"].(map[string]any)["properties"].(map[string]any)
	if entryProps["raw_report"].(map[string]any)["type"] != "string" {
		t.Fatal("raw JSON must be a string, not an untyped object/base64 byte slice")
	}
	for _, key := range []string{"report_id", "child_id", "received_at", "supplied_task_id", "effective_task_id", "association"} {
		if entryProps[key] == nil {
			t.Fatalf("missing history metadata %s", key)
		}
	}
	wait := schemaMap(tools["mitto_children_tasks_wait"].OutputSchema)
	reportProps := wait["properties"].(map[string]any)["reports"].(map[string]any)["additionalProperties"].(map[string]any)["properties"].(map[string]any)
	for _, key := range []string{"completed", "task_terminal", "wait_resolved", "outcome", "report_id", "timestamp", "requested_task_id", "supplied_task_id", "effective_task_id", "association", "task_matches", "diagnostic", "has_more_history", "has_unassociated_evidence"} {
		if reportProps[key] == nil {
			t.Fatalf("missing compact contract field %s", key)
		}
	}
	for _, key := range []string{"completed", "task_terminal", "wait_resolved", "task_matches", "has_more_history", "has_unassociated_evidence"} {
		if reportProps[key].(map[string]any)["type"] != "boolean" {
			t.Fatalf("%s is not a typed boolean", key)
		}
	}
}

func TestChildrenTasksContract_CompactBoundOutput(t *testing.T) {
	for _, status := range []string{"completed", "failed", "partial", "verification_pending", "in_progress", "Unknown", "mismatch", "none"} {
		t.Run(status, func(t *testing.T) {
			s, store, parent, children := setupParentChildSessions(t, 1)
			child := children[0]
			c, _ := s.getOrCreateCollector(parent)
			endpoint := historyProtocolServer(t, s)
			parentClient := connectBoundProtocolSession(t, endpoint, sessionBindingToken(t, s, parent))
			childClient := connectBoundProtocolSession(t, endpoint, sessionBindingToken(t, s, child))
			task, rawStatus := "A", status
			if status == "mismatch" {
				task, rawStatus = "other", "completed"
			}
			if status != "none" {
				historyAdd(t, c, child, "", `{"status":"failed","summary":"earlier evidence"}`)
				var accepted ChildrenTasksReportOutput
				historyMCPCall(t, childClient, "mitto_children_tasks_report", map[string]any{
					"self_id": "init", "status": rawStatus, "summary": "primary evidence", "task_id": task,
				}, nil, &accepted)
				if !accepted.Success || accepted.ParentSessionID != parent {
					t.Fatalf("bound report failed: %+v", accepted)
				}
			}
			// Closed children supply real liveness diagnostics, not fake payloads;
			// applicable terminal evidence must still win over that classification.
			if err := store.UpdateMetadata(child, func(m *session.Metadata) { m.Archived = true }); err != nil {
				t.Fatal(err)
			}
			s.UnregisterSession(child)
			var out ChildrenTasksWaitOutput
			result := historyMCPCall(t, parentClient, "mitto_children_tasks_wait", map[string]any{
				"self_id": "init", "children_list": children, "task_id": "A", "timeout_seconds": 1,
			}, nil, &out)
			info := out.Reports[child]
			terminal := status == "completed" || status == "failed" || status == "partial" || status == "verification_pending"
			if !out.Success || out.TimedOut || !info.WaitResolved || info.TaskTerminal != terminal || info.Completed != (status == "completed") || info.RequestedTaskID != "A" {
				t.Fatalf("operation success conflated with child outcome: %+v", out)
			}
			if (info.Diagnostic == nil) != terminal {
				t.Fatalf("governing diagnostic missing or masking terminal evidence: %+v", info)
			}
			if status == "none" {
				if info.Report != nil || info.Outcome != nil || info.ReportID != "" || info.Timestamp != "" || info.TaskMatches || info.HasMoreHistory || info.HasUnassociatedEvidence {
					t.Fatalf("diagnostic fabricated genuine evidence: %+v", info)
				}
				return
			}
			if info.Report == nil || info.Report.Status != rawStatus || info.Outcome == nil || info.Outcome.Status != rawStatus || info.SuppliedTaskID != task || info.EffectiveTaskID != task || info.Association != "explicit" || info.TaskMatches != (status != "mismatch") || !info.HasMoreHistory || !info.HasUnassociatedEvidence {
				t.Fatalf("compact identity/status/association lost: %+v", info)
			}
			if status == "Unknown" && (!strings.Contains(info.Reason, "unknown_status") || info.Outcome.Known || info.Outcome.Terminal) {
				t.Fatalf("unknown status silently became known/terminal: %+v", info)
			}
			if status == "mismatch" && (!strings.Contains(info.Reason, `requested="A"`) || !strings.Contains(info.Reason, `supplied="other"`)) {
				t.Fatalf("mismatch lacks IDs: %+v", info)
			}
			raw, _ := json.Marshal(result.StructuredContent)
			if strings.Count(string(raw), `"summary"`) != 1 || strings.Contains(string(raw), "earlier evidence") || strings.Contains(string(raw), "raw_report") {
				t.Fatalf("compact result exceeded one genuine payload: %s", raw)
			}
			var history ChildrenTasksHistoryOutput
			historyMCPCall(t, parentClient, "mitto_children_tasks_history", map[string]any{"self_id": "init", "report_id": info.ReportID}, nil, &history)
			if !history.Success || len(history.Entries) != 1 || history.Entries[0].ReportID != info.ReportID || history.Entries[0].ReceivedAt != info.Timestamp || history.Entries[0].SuppliedTaskID != info.SuppliedTaskID || history.Entries[0].EffectiveTaskID != info.EffectiveTaskID || history.Entries[0].Association != info.Association {
				t.Fatalf("compact reference cannot retrieve same evidence: %+v %+v", info, history)
			}
		})
	}
}
