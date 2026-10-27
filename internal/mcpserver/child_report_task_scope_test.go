package mcpserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// A child that was never told the parent's task_id cannot scope its report.
// Before this fix such a report was treated as non-matching, overwritten by
// markChildAutoCompleted, and surfaced as agent_not_responding even though the
// child's mitto_children_tasks_report call had returned success:true.
func TestChildrenTasksWait_UnscopedReportDuringWaitCounts(t *testing.T) {
	srv, _, parentID, childIDs := setupParentChildSessions(t, 1)
	ctx := context.Background()

	type waitResult struct {
		output ChildrenTasksWaitOutput
		err    error
	}
	resultCh := make(chan waitResult, 1)
	go func() {
		_, output, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
			SelfID:         parentID,
			ChildrenList:   childIDs,
			TaskID:         "task-A",
			TimeoutSeconds: 5,
		})
		resultCh <- waitResult{output: output, err: err}
	}()

	time.Sleep(100 * time.Millisecond)

	// Report carries no task_id: the child never received one.
	_, reportOutput, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID:  childIDs[0],
		Status:  "completed",
		Summary: "Unscoped evidence",
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksReport returned error: %v", err)
	}
	if !reportOutput.Success {
		t.Fatalf("Report failed: %s", reportOutput.Error)
	}

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("handleChildrenTasksWait returned error: %v", result.err)
		}
		if result.output.TimedOut {
			t.Fatal("wait timed out although the child reported")
		}
		report := result.output.Reports[childIDs[0]]
		if !report.Completed {
			t.Fatalf("unscoped report was not accepted: status=%q reason=%q", report.Status, report.Reason)
		}
		if report.Status == "agent_not_responding" {
			t.Fatal("genuine report was replaced by a synthetic agent_not_responding result")
		}
		if report.Report == nil || report.Report.Summary != "Unscoped evidence" {
			t.Fatalf("expected the child's evidence, got %+v", report.Report)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for wait result")
	}
}

// Only a real idle observation resolves the wait, not mismatched evidence alone.
func TestChildReportCollector_TaskMismatchIsSurfacedNotAutoCompleted(t *testing.T) {
	collector := &childReportCollector{
		reports:       make(map[string]*childReport),
		currentTaskID: "task-B",
		waitingFor:    map[string]bool{"child": true},
	}

	collector.addReport("child", "task-A", json.RawMessage(`{"status":"completed","summary":"stale task report"}`))
	collector.markChildAutoCompleted("child", "agent_idle")

	r := collector.reports["child"]
	if r == nil {
		t.Fatal("report disappeared")
	}
	if r.AutoCompleted {
		t.Error("genuine report was replaced by auto-completion")
	}
	if !r.TaskMismatch {
		t.Error("expected the report to be flagged as a task mismatch")
	}
	p := collector.projectionLocked("child")
	if !p.waitResolved() || p.taskSucceeded() || p.taskTerminal() {
		t.Error("idle diagnostic must resolve without completing the requested task")
	}
}

// Between-wait evidence survives as history, never as completion of a new task.
func TestChildrenTasksWait_UnscopedReportBeforeNewTaskIsPreserved(t *testing.T) {
	srv, _, parentID, childIDs := setupParentChildSessions(t, 1)
	ctx := context.Background()

	// Previous wait ends without evidence.
	_, first, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   childIDs,
		TaskID:         "task-A",
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("first wait returned error: %v", err)
	}
	if !first.TimedOut {
		t.Fatal("expected the first wait to time out")
	}

	// Child reports without a task_id after that wait ended.
	_, reportOutput, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID:  childIDs[0],
		Status:  "completed",
		Summary: "Reported between waits",
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksReport returned error: %v", err)
	}
	if !reportOutput.Success {
		t.Fatalf("Report failed: %s", reportOutput.Error)
	}

	// New task: unassociated evidence cannot count or suppress a corrective prompt.
	_, second, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   childIDs,
		TaskID:         "task-B",
		Prompt:         "Work on B",
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("second wait returned error: %v", err)
	}
	if !second.TimedOut {
		t.Fatal("between-wait unscoped evidence completed B")
	}
	report := second.Reports[childIDs[0]]
	if report.Completed || report.Report == nil || report.Report.Summary != "Reported between waits" {
		t.Fatalf("expected the preserved report, got %+v", report)
	}
	msgs, err := srv.store.Queue(childIDs[0]).List()
	if err != nil || len(msgs) != 1 || msgs[0].ClientID != parentID {
		t.Fatalf("B correction not admitted: %+v %v", msgs, err)
	}
}

// An unscoped report older than the previous wait must not satisfy a new task:
// task scoping still protects against stale evidence.
func TestChildrenTasksWait_StaleUnscopedReportDoesNotSatisfyNewTask(t *testing.T) {
	srv, _, parentID, childIDs := setupParentChildSessions(t, 1)
	ctx := context.Background()

	// Report arrives before any active wait; it is never associated retroactively.
	_, reportOutput, err := srv.handleChildrenTasksReport(ctx, nil, ChildrenTasksReportInput{
		SelfID:  childIDs[0],
		Status:  "completed",
		Summary: "Old unscoped report",
	})
	if err != nil {
		t.Fatalf("handleChildrenTasksReport returned error: %v", err)
	}
	if !reportOutput.Success {
		t.Fatalf("Report failed: %s", reportOutput.Error)
	}

	_, first, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   childIDs,
		TaskID:         "task-A",
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("first wait returned error: %v", err)
	}
	if !first.TimedOut || first.Reports[childIDs[0]].Completed {
		t.Fatalf("first named wait consumed unassociated evidence: %+v", first.Reports[childIDs[0]])
	}

	// New task: the report predates the previous wait's end, so it is stale.
	_, second, err := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{
		SelfID:         parentID,
		ChildrenList:   childIDs,
		TaskID:         "task-B",
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("second wait returned error: %v", err)
	}
	if !second.TimedOut {
		t.Fatal("stale unscoped report satisfied a new task")
	}
	if report := second.Reports[childIDs[0]]; report.Completed {
		t.Fatalf("stale report must not be completed for the new task: %+v", report)
	}
}
