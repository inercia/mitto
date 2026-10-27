package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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

type taskScopeWaitResult struct {
	output ChildrenTasksWaitOutput
	err    error
}

func startTaskScopeWait(t *testing.T, srv *Server, ctx context.Context, input ChildrenTasksWaitInput) <-chan taskScopeWaitResult {
	t.Helper()
	result := make(chan taskScopeWaitResult, 1)
	go func() {
		_, output, err := srv.handleChildrenTasksWait(ctx, nil, input)
		result <- taskScopeWaitResult{output: output, err: err}
	}()
	return result
}

func taskScopeEntries(t *testing.T, srv *Server, parent string) []childReportEntry {
	t.Helper()
	collector, err := srv.getOrCreateCollector(parent)
	if err != nil {
		t.Fatal(err)
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	entries := make([]childReportEntry, len(collector.entries))
	copy(entries, collector.entries)
	return entries
}

func TestChildrenTasksWait_TaskBoundaryEvidenceDoesNotContaminateNextTask(t *testing.T) {
	srv, store, parent, children := setupParentChildSessions(t, 1)
	child := children[0]

	// Task A genuinely reaches its deadline before the child supplies an
	// unscoped report. That later report is retained, but it cannot be inferred
	// after A's finalized boundary.
	_, first, err := srv.handleChildrenTasksWait(context.Background(), nil, ChildrenTasksWaitInput{
		SelfID: parent, ChildrenList: children, TaskID: "A", TimeoutSeconds: 1,
	})
	if err != nil || !first.Success || !first.TimedOut {
		t.Fatalf("A timeout: %+v %v", first, err)
	}
	if _, report, err := srv.handleChildrenTasksReport(context.Background(), nil, ChildrenTasksReportInput{
		SelfID: child, Status: "completed", Summary: "A evidence after deadline",
	}); err != nil || !report.Success {
		t.Fatalf("post-A report: %+v %v", report, err)
	}

	// B must still prompt the child and time out. The retained A evidence is
	// visible as context, never B completion.
	_, second, err := srv.handleChildrenTasksWait(context.Background(), nil, ChildrenTasksWaitInput{
		SelfID: parent, ChildrenList: children, TaskID: "B", Prompt: "reconcile B", TimeoutSeconds: 1,
	})
	if err != nil || !second.Success || !second.TimedOut {
		t.Fatalf("B timeout: %+v %v", second, err)
	}
	info := second.Reports[child]
	if info.Completed || info.TaskMatches || info.EffectiveTaskID != "" || info.Association != string(childReportUnassociated) || info.Report == nil || info.Report.Summary != "A evidence after deadline" {
		t.Fatalf("A evidence contaminated B: %+v", info)
	}
	_, history, err := srv.handleChildrenTasksHistory(context.Background(), nil, ChildrenTasksHistoryInput{SelfID: parent, ChildID: child})
	if err != nil || !history.Success || len(history.Entries) != 1 || !contains(history.Entries[0].RawReport, "A evidence after deadline") ||
		history.Entries[0].Association != string(childReportUnassociated) {
		t.Fatalf("A evidence was not retrievable as parent-owned history: %+v %v", history, err)
	}
	msgs, err := store.Queue(child).List()
	if err != nil || len(msgs) != 1 || msgs[0].ClientID != parent || !contains(msgs[0].Message, `task_id: "B"`) {
		t.Fatalf("B corrective prompt was not queued: %v %v", msgs, err)
	}
	entries := taskScopeEntries(t, srv, parent)
	if len(entries) != 1 || entries[0].Association != childReportUnassociated || entries[0].EffectiveTaskID != "" {
		t.Fatalf("post-deadline report was reassociated: %+v", entries)
	}
}

func TestChildrenTasksWait_MismatchIdleReturnsEvidenceAndKeepsBPrompt(t *testing.T) {
	srv, store, parent, children := setupParentChildSessions(t, 1)
	child := children[0]
	started, release := make(chan struct{}), make(chan struct{})
	srv.sessionManager = &reportWaitBarrierManager{
		mockSessionManagerForAutoResume: &mockSessionManagerForAutoResume{},
		onWaiting: func(waiting bool) {
			if waiting {
				close(started)
				<-release
			}
		},
	}
	result := startTaskScopeWait(t, srv, context.Background(), ChildrenTasksWaitInput{
		SelfID: parent, ChildrenList: children, TaskID: "B", Prompt: "correct task B", TimeoutSeconds: 5,
	})
	<-started // B activation and prompt admission happen before the broadcast.
	_, report, err := srv.handleChildrenTasksReport(context.Background(), nil, ChildrenTasksReportInput{
		SelfID: child, TaskID: "A", Status: "completed", Summary: "finished A",
	})
	if err != nil || !report.Success {
		t.Fatalf("explicit A report: %+v %v", report, err)
	}
	collector, err := srv.getOrCreateCollector(parent)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the real idle observation after genuine mismatched ingress. The
	// handler's returned output, rather than this collector flag, is asserted.
	if err := collector.markChildAutoCompleted(child, "agent_idle"); err != nil {
		t.Fatal(err)
	}
	close(release)
	got := <-result
	if got.err != nil || !got.output.Success || got.output.TimedOut {
		t.Fatalf("B wait: %+v %v", got.output, got.err)
	}
	info := got.output.Reports[child]
	if info.Completed || info.TaskTerminal || !info.WaitResolved || info.Status != "agent_not_responding" ||
		info.Report == nil || info.Report.Status != "completed" || info.ReportID == "" ||
		info.SuppliedTaskID != "A" || info.EffectiveTaskID != "A" || info.TaskMatches ||
		info.Diagnostic == nil || info.Diagnostic.Kind != string(childDiagnosticTaskMismatch) || !contains(info.Reason, `requested="B"`) {
		t.Fatalf("mismatch/idle output lost reconciliation evidence: %+v", info)
	}
	msgs, err := store.Queue(child).List()
	if err != nil || len(msgs) != 1 || msgs[0].ClientID != parent || !contains(msgs[0].Message, `task_id: "B"`) {
		t.Fatalf("B reconciliation prompt was not queued: %v %v", msgs, err)
	}
}

func TestChildrenTasksWait_UnscopedInferenceOnlyWithinActiveBoundary(t *testing.T) {
	srv, _, parent, children := setupParentChildSessions(t, 2)
	inside, outside := children[0], children[1]

	// Before activation, an unscoped report remains permanently unassociated.
	if _, report, err := srv.handleChildrenTasksReport(context.Background(), nil, ChildrenTasksReportInput{SelfID: inside, Status: "completed", Summary: "before"}); err != nil || !report.Success {
		t.Fatalf("before report: %+v %v", report, err)
	}

	started, release := make(chan struct{}), make(chan struct{})
	srv.sessionManager = &reportWaitBarrierManager{
		mockSessionManagerForAutoResume: &mockSessionManagerForAutoResume{},
		onWaiting: func(waiting bool) {
			if waiting {
				close(started)
				<-release
			}
		},
	}
	result := startTaskScopeWait(t, srv, context.Background(), ChildrenTasksWaitInput{
		SelfID: parent, ChildrenList: []string{inside}, TaskID: "A", TimeoutSeconds: 5,
	})
	<-started
	if _, report, err := srv.handleChildrenTasksReport(context.Background(), nil, ChildrenTasksReportInput{SelfID: outside, Status: "completed", Summary: "outside"}); err != nil || !report.Success {
		t.Fatalf("outside report: %+v %v", report, err)
	}
	if _, report, err := srv.handleChildrenTasksReport(context.Background(), nil, ChildrenTasksReportInput{SelfID: inside, Status: "completed", Summary: "inside"}); err != nil || !report.Success {
		t.Fatalf("inside report: %+v %v", report, err)
	}
	close(release)
	active := <-result
	if active.err != nil || !active.output.Success || active.output.TimedOut || !active.output.Reports[inside].Completed || active.output.Reports[inside].Association != string(childReportInferred) {
		t.Fatalf("active inference: %+v %v", active.output, active.err)
	}

	// Both a finalized wait and a cancelled wait end the inference window.
	if _, report, err := srv.handleChildrenTasksReport(context.Background(), nil, ChildrenTasksReportInput{SelfID: inside, Status: "completed", Summary: "finalized"}); err != nil || !report.Success {
		t.Fatalf("finalized report: %+v %v", report, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancelStarted, cancelRelease := make(chan struct{}), make(chan struct{})
	srv.sessionManager = &reportWaitBarrierManager{
		mockSessionManagerForAutoResume: &mockSessionManagerForAutoResume{},
		onWaiting: func(waiting bool) {
			if waiting {
				close(cancelStarted)
				<-cancelRelease
			}
		},
	}
	cancelledResult := startTaskScopeWait(t, srv, cancelled, ChildrenTasksWaitInput{SelfID: parent, ChildrenList: []string{inside}, TaskID: "C", TimeoutSeconds: 5})
	<-cancelStarted
	cancel()
	close(cancelRelease)
	if got := <-cancelledResult; got.err != nil || got.output.Success || !contains(got.output.Error, "cancelled") {
		t.Fatalf("cancelled wait: %+v %v", got.output, got.err)
	}
	if _, report, err := srv.handleChildrenTasksReport(context.Background(), nil, ChildrenTasksReportInput{SelfID: inside, Status: "completed", Summary: "cancelled"}); err != nil || !report.Success {
		t.Fatalf("cancelled-boundary report: %+v %v", report, err)
	}
	entries := taskScopeEntries(t, srv, parent)
	if len(entries) != 5 || entries[0].Association != childReportUnassociated || entries[1].Association != childReportUnassociated ||
		entries[2].Association != childReportInferred || entries[3].Association != childReportUnassociated || entries[4].Association != childReportUnassociated {
		t.Fatalf("inference crossed a lifecycle boundary: %+v", entries)
	}
}

func TestChildrenTasksWait_ReloadAndLegacyEvidenceDoNotCompleteB(t *testing.T) {
	srv, store, parent, children := setupParentChildSessions(t, 1)
	child := children[0]
	if _, report, err := srv.handleChildrenTasksReport(context.Background(), nil, ChildrenTasksReportInput{SelfID: child, TaskID: "A", Status: "completed", Summary: "A evidence"}); err != nil || !report.Success {
		t.Fatalf("A report: %+v %v", report, err)
	}
	_, aWait, err := srv.handleChildrenTasksWait(context.Background(), nil, ChildrenTasksWaitInput{SelfID: parent, ChildrenList: children, TaskID: "A", TimeoutSeconds: 1})
	if err != nil || !aWait.Success || aWait.TimedOut || !aWait.Reports[child].Completed {
		t.Fatalf("consume A: %+v %v", aWait, err)
	}

	// A fresh server recreates the collector from the same parent sidecar.
	restarted, err := NewServer(Config{Port: 0}, Dependencies{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.RegisterSession(parent, nil, slog.Default()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.RegisterSession(child, nil, slog.Default()); err != nil {
		t.Fatal(err)
	}
	_, bWait, err := restarted.handleChildrenTasksWait(context.Background(), nil, ChildrenTasksWaitInput{SelfID: parent, ChildrenList: children, TaskID: "B", TimeoutSeconds: 1})
	if err != nil || !bWait.Success || !bWait.TimedOut || bWait.Reports[child].Completed || bWait.Reports[child].TaskMatches {
		t.Fatalf("reloaded A evidence completed B: %+v %v", bWait, err)
	}

	legacySrv, legacyStore, legacyParent, legacyChildren := setupParentChildSessions(t, 1)
	legacyChild := legacyChildren[0]
	legacy := json.RawMessage(fmt.Sprintf(`{"reports":{%q:{"completed":true,"timestamp":"2026-10-27T12:00:00Z","report":{"status":"completed","summary":"legacy unscoped"}}}}`, legacyChild))
	if err := legacyStore.WriteSessionSidecarJSON(legacyParent, childReportsStateFile, legacy); err != nil {
		t.Fatal(err)
	}
	_, legacyB, err := legacySrv.handleChildrenTasksWait(context.Background(), nil, ChildrenTasksWaitInput{SelfID: legacyParent, ChildrenList: legacyChildren, TaskID: "B", TimeoutSeconds: 1})
	if err != nil || !legacyB.Success || !legacyB.TimedOut || legacyB.Reports[legacyChild].Completed || legacyB.Reports[legacyChild].TaskMatches {
		t.Fatalf("legacy unscoped evidence completed B: %+v %v", legacyB, err)
	}
	entries := taskScopeEntries(t, legacySrv, legacyParent)
	if len(entries) != 1 || entries[0].Association != childReportUnassociated || entries[0].EffectiveTaskID != "" {
		t.Fatalf("legacy evidence was reassociated: %+v", entries)
	}
}
