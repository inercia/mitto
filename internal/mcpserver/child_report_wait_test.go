package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// Adapt old collector fixtures to the same reservation/activation API as the
// handler. Storage tests keep their original payload/durability assertions.
func (c *childReportCollector) startTestWait(taskID string, children []string) (chan struct{}, bool) {
	owner, err := c.reserveWait()
	if err != nil {
		panic(err)
	}
	ch, err := c.startWait(owner, taskID, children, false)
	if err != nil {
		panic(err)
	}
	select {
	case <-ch:
		return ch, true
	default:
		return ch, false
	}
}

func TestChildReportWait_ReservedBoundary(t *testing.T) {
	c, _ := loadChildReportCollector(nil, "parent")
	owner, err := c.reserveWait()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.addReport("child", "", []byte(`{"status":"completed","summary":"before activation"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.startWait(owner, "A", []string{"child"}, false); err != nil {
		t.Fatal(err)
	}
	if c.entries[0].Association != childReportUnassociated || c.projectionLocked("child").waitResolved() {
		t.Fatal("reserved wait inferred or reused unassociated evidence")
	}
	c.clearWait(owner)
}

func acceptWaitReport(t *testing.T, c *childReportCollector, child, task, status string) {
	t.Helper()
	payload, err := json.Marshal(ChildReportData{Status: status, Summary: status})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.addReport(child, task, payload); err != nil {
		t.Fatal(err)
	}
}

func assertWaitChannel(t *testing.T, ch <-chan struct{}, closed bool) {
	t.Helper()
	select {
	case <-ch:
		if !closed {
			t.Fatal("wait resolved prematurely")
		}
	default:
		if closed {
			t.Fatal("resolved child did not signal promptly")
		}
	}
}

func TestChildReportWait_Terminality(t *testing.T) {
	for _, status := range []string{"completed", "failed", "partial", "verification_pending", "in_progress", "done", "stuck", "progress", "Completed"} {
		t.Run(status, func(t *testing.T) {
			c, _ := loadChildReportCollector(nil, "parent")
			ch, _ := c.startTestWait("A", []string{"child"})
			acceptWaitReport(t, c, "child", "A", status)
			terminal := status == "completed" || status == "failed" || status == "partial" || status == "verification_pending"
			assertWaitChannel(t, ch, terminal)
			p := c.projectionLocked("child")
			if p.taskTerminal() != terminal || p.taskSucceeded() != (status == "completed") || p.Diagnostic != nil || p.Outcome.Status != status {
				t.Fatalf("incorrect live interpretation: %+v", p)
			}
		})
	}
}

func TestChildReportWait_HandlerTerminalOutcomesSkipDispatch(t *testing.T) {
	for _, status := range []string{"completed", "failed", "partial", "verification_pending"} {
		t.Run(status, func(t *testing.T) {
			srv, store, parent, children := setupParentChildSessions(t, 1)
			c, err := srv.getOrCreateCollector(parent)
			if err != nil {
				t.Fatal(err)
			}
			acceptWaitReport(t, c, children[0], "A", status)
			// Later other-task evidence cannot displace a matching terminal report.
			acceptWaitReport(t, c, children[0], "other", "in_progress")
			_, out, err := srv.handleChildrenTasksWait(context.Background(), nil, ChildrenTasksWaitInput{SelfID: parent, ChildrenList: children, TaskID: "A", Prompt: "do not redispatch", TimeoutSeconds: 1})
			info := out.Reports[children[0]]
			msgs, queueErr := store.Queue(children[0]).List()
			if err != nil || !out.Success || out.TimedOut || info.Completed != (status == "completed") || info.Status != status || info.Report == nil || info.Report.Status != status || queueErr != nil || len(msgs) != 0 {
				t.Fatalf("terminal outcome was collapsed or redispatched: %+v queue=%v err=%v", out, msgs, err)
			}
		})
	}
}

func TestChildReportWait_FrozenCorrectionAndBoundary(t *testing.T) {
	for _, reportFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "report-first", false: "deadline-first"}[reportFirst], func(t *testing.T) {
			c, _ := loadChildReportCollector(nil, "parent")
			ch, _ := c.startTestWait("A", []string{"child"})
			owner := c.waitOwner
			if !reportFirst {
				c.finalizeWait(owner, true)
			}
			acceptWaitReport(t, c, "child", "", "completed")
			snapshot, timedOut := c.finalizeWait(owner, true)
			assertWaitChannel(t, ch, true)
			if timedOut == reportFirst || snapshot["child"].taskSucceeded() != reportFirst {
				t.Fatalf("wrong boundary winner: %+v timedOut=%v", snapshot, timedOut)
			}
			entry := c.entries[0]
			if reportFirst && (entry.Association != childReportInferred || entry.EffectiveTaskID != "A") || !reportFirst && entry.Association != childReportUnassociated {
				t.Fatalf("boundary reassociation: %+v", entry)
			}
			// The channel remains closed exactly once; the successful frozen result
			// cannot be rewritten by a later matching progress correction.
			acceptWaitReport(t, c, "child", "A", "in_progress")
			acceptWaitReport(t, c, "child", "", "completed")
			if c.entries[2].Association != childReportUnassociated || snapshot["child"].taskSucceeded() != reportFirst {
				t.Fatal("finalized wait inferred or changed its snapshot")
			}
			if reportFirst && c.admitPrompt(owner, "child") {
				t.Fatal("later correction caused dispatch for frozen terminal result")
			}
			c.clearWait(owner)
			ch, done := c.startTestWait("A", []string{"child"})
			if done || c.projectionLocked("child").Outcome.Status != "in_progress" {
				t.Fatal("next wait did not use latest matching correction")
			}
			assertWaitChannel(t, ch, false)
		})
	}
}

func TestChildReportWait_InferredReloadAndTaskIsolation(t *testing.T) {
	_, store, parent, children := setupParentChildSessions(t, 1)
	c, err := loadChildReportCollector(store, parent)
	if err != nil {
		t.Fatal(err)
	}
	c.startTestWait("A", children)
	acceptWaitReport(t, c, children[0], "", "completed")
	want := c.entries[0]
	c.clearWait(c.waitOwner)
	// A later unassociated entry cannot displace A's matching evidence.
	acceptWaitReport(t, c, children[0], "", "failed")
	reloaded, err := loadChildReportCollector(store, parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range []string{"A", "", "B", "A"} {
		ch, done := reloaded.startTestWait(task, children)
		if done != (task != "B") {
			t.Fatalf("wrong terminality for %q", task)
		}
		assertWaitChannel(t, ch, task != "B")
		if task == "A" && reloaded.projectionLocked(children[0]).Entry.ID != want.ID {
			t.Fatal("unrelated append displaced matching inferred entry")
		}
		reloaded.clearWait(reloaded.waitOwner)
	}
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(reloaded.entries[0])
	if string(wantJSON) != string(gotJSON) {
		t.Fatal("retry/reload/unnamed/task switch reassigned evidence")
	}
}

func TestChildReportWait_CommitBoundaryOrdering(t *testing.T) {
	for _, activation := range []bool{false, true} {
		t.Run(map[bool]string{true: "activation", false: "report"}[activation], func(t *testing.T) {
			c, _ := loadChildReportCollector(nil, "parent")
			owner, err := c.reserveWait()
			if err != nil {
				t.Fatal(err)
			}
			var ch chan struct{}
			if !activation {
				ch, err = c.startWait(owner, "A", []string{"child"}, false)
				if err != nil {
					t.Fatal(err)
				}
			}
			entered, release := make(chan struct{}), make(chan struct{})
			c.writeHistory = func(candidate childReportHistoryState) error {
				if activation && candidate.CurrentTaskID == "A" && c.currentTaskID == "" || !activation && len(candidate.Entries) == 1 {
					if activation && (owner.active || c.waitCh != nil) || !activation && (owner.finalized || len(c.entries) != 0) {
						t.Error("candidate published before durable commit")
					}
					if !activation {
						assertWaitChannel(t, ch, false)
					}
					close(entered)
					<-release
				}
				return nil
			}
			committed := make(chan error, 1)
			go func() {
				if activation {
					_, err := c.startWait(owner, "A", []string{"child"}, false)
					committed <- err
				} else {
					_, err := c.addReport("child", "", []byte(`{"status":"completed","summary":"durable"}`))
					committed <- err
				}
			}()
			<-entered
			competing := make(chan struct{})
			started := make(chan struct{})
			var snapshot map[string]childReportProjection
			var timedOut bool
			go func() {
				close(started)
				if activation {
					_, err := c.addReport("child", "", []byte(`{"status":"completed","summary":"after activation"}`))
					if err != nil {
						t.Error(err)
					}
				} else {
					snapshot, timedOut = c.finalizeWait(owner, true)
				}
				close(competing)
			}()
			<-started
			select {
			case <-competing:
				t.Fatal("operation crossed uncommitted boundary")
			default:
			}
			close(release)
			if err := <-committed; err != nil {
				t.Fatal(err)
			}
			<-competing
			if c.entries[0].Association != childReportInferred || c.entries[0].EffectiveTaskID != "A" {
				t.Fatal("mutex winner did not govern association")
			}
			if !activation && (timedOut || !snapshot["child"].taskSucceeded()) {
				t.Fatal("deadline beat report while report held durable commit lock")
			}
		})
	}
}

func TestChildReportWait_DiagnosticCommitFailureBarrier(t *testing.T) {
	srv, _, parent, children := setupParentChildSessions(t, 1)
	srv.UnregisterSession(children[0])
	c, err := srv.getOrCreateCollector(parent)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var wait chan struct{}
	c.writeHistory = func(candidate childReportHistoryState) error {
		if len(candidate.Diagnostics) != 0 {
			wait = c.waitCh
			if len(c.diagnostics) != 0 || !c.waitOwner.active {
				t.Error("diagnostic published before commit")
			}
			close(entered)
			<-release
			return errors.New("blocked diagnostic commit failed")
		}
		return nil
	}
	done := make(chan ChildrenTasksWaitOutput, 1)
	go func() {
		_, out, _ := srv.handleChildrenTasksWait(context.Background(), nil, ChildrenTasksWaitInput{SelfID: parent, ChildrenList: children, TaskID: "A", TimeoutSeconds: 2})
		done <- out
	}()
	<-entered
	assertWaitChannel(t, wait, false)
	select {
	case <-done:
		t.Fatal("diagnostic returned before commit")
	default:
	}
	close(release)
	out := <-done
	assertWaitChannel(t, wait, false)
	if out.Success || !strings.Contains(out.Error, "retry later") || c.waitOwner != nil || len(c.diagnostics) != 0 {
		t.Fatalf("failed diagnostic published/signaled or leaked ownership: %+v", out)
	}
}

func TestChildReportWait_DiagnosticOriginsAndRetry(t *testing.T) {
	for _, origin := range []string{"agent_idle", "session_stopped", "startup recovery failed", "queued send failed", "enqueue failed", "stuck"} {
		t.Run(origin, func(t *testing.T) {
			c, _ := loadChildReportCollector(nil, "parent")
			ch, _ := c.startTestWait("A", []string{"child"})
			acceptWaitReport(t, c, "child", "old", "completed")
			before := c.entries[0]
			var err error
			switch origin {
			case "agent_idle", "session_stopped":
				err = c.markChildAutoCompleted("child", origin)
			case "stuck":
				err = c.markChildStuck("child", "A")
			default:
				err = c.markChildFailed("child", origin)
			}
			if err != nil {
				t.Fatal(err)
			}
			assertWaitChannel(t, ch, true)
			p := c.projectionLocked("child")
			if p.taskSucceeded() || p.taskTerminal() || !p.waitResolved() || !reflect.DeepEqual(before, c.entries[0]) {
				t.Fatal("diagnostic changed or satisfied evidence")
			}
			acceptWaitReport(t, c, "child", "unrelated", "completed")
			if c.projectionLocked("child").Diagnostic == nil {
				t.Fatal("unrelated report cleared current diagnostic")
			}
			c.clearWait(c.waitOwner)
			_, done := c.startTestWait("A", []string{"child"})
			if !done {
				t.Fatal("wait-only retry lost diagnostic")
			}
			c.clearWait(c.waitOwner)
			owner, err := c.reserveWait()
			if err != nil {
				t.Fatal(err)
			}
			ch, err = c.startWait(owner, "A", []string{"child"}, true)
			if err != nil {
				t.Fatal(err)
			}
			assertWaitChannel(t, ch, false)
			if !c.admitPrompt(owner, "child") || len(c.entries) != 2 || len(c.diagnostics) != 0 {
				t.Fatal("same-task correction not eligible or evidence deleted")
			}
			acceptWaitReport(t, c, "child", "A", "failed")
			if c.admitPrompt(owner, "child") {
				t.Fatal("terminal matching report did not prevent dispatch")
			}
		})
	}
}

func TestChildReportWait_ActivationAndDiagnosticFailure(t *testing.T) {
	for _, activationFailure := range []bool{true, false} {
		t.Run(map[bool]string{true: "activation", false: "diagnostic"}[activationFailure], func(t *testing.T) {
			srv, _, parent, children := setupParentChildSessions(t, 1)
			c, err := srv.getOrCreateCollector(parent)
			if err != nil {
				t.Fatal(err)
			}
			acceptWaitReport(t, c, children[0], "old", "completed")
			before := c.historyLocked()
			c.writeHistory = func(candidate childReportHistoryState) error {
				if activationFailure || len(candidate.Diagnostics) != 0 {
					if c.currentTaskID != "" && activationFailure || len(c.diagnostics) != 0 || c.waitOwner.finalized {
						t.Error("failed candidate published or signaled")
					}
					return errors.New("injected persistence failure")
				}
				return nil
			}
			srv.UnregisterSession(children[0]) // stopped diagnostic immediately after activation
			_, out, err := srv.handleChildrenTasksWait(context.Background(), nil, ChildrenTasksWaitInput{SelfID: parent, ChildrenList: children, TaskID: "A", TimeoutSeconds: 1})
			if err != nil || out.Success || !strings.Contains(out.Error, "retry later") || c.waitOwner != nil || c.waitCh != nil || len(c.diagnostics) != 0 || !reflect.DeepEqual(before.Entries, c.entries) {
				t.Fatalf("failure was not atomic/owner cleaned: %+v %v", out, err)
			}
			if activationFailure && !equalStoredHistory(before, c.historyLocked()) {
				t.Fatal("activation failure published task state")
			}
		})
	}
}

func TestChildReportWait_TaskScopedTimers(t *testing.T) {
	c, _ := loadChildReportCollector(nil, "parent")
	c.startTestWait("A", []string{"child", "other"})
	old := time.Now().Add(-2 * maxChildWaitDuration)
	c.firstWaitTime["child"], c.firstWaitTime["other"] = old, old
	c.clearWait(c.waitOwner)
	c.startTestWait("A", []string{"child"})
	if got := c.getStuckChildren(); len(got) != 1 || got[0] != "child" {
		t.Fatalf("same-task cap/membership lost: %v", got)
	}
	c.clearWait(c.waitOwner)
	c.startTestWait("B", []string{"child"})
	if len(c.getStuckChildren()) != 0 || !c.firstWaitTime["child"].After(old) {
		t.Fatal("old-task stuck timer bled into B")
	}
}

func TestChildReportWait_NotRunningEvidenceAndEnqueueFailure(t *testing.T) {
	for _, mode := range []string{"closed", "archived", "enqueue"} {
		t.Run(mode, func(t *testing.T) {
			srv, store, parent, children := setupParentChildSessions(t, 1)
			c, err := srv.getOrCreateCollector(parent)
			if err != nil {
				t.Fatal(err)
			}
			status := "completed"
			if mode == "enqueue" {
				status = "in_progress"
				// Real queue admission fails, without replacing queue/Store APIs.
				if err := os.WriteFile(filepath.Join(store.SessionDir(children[0]), "queue.json"), []byte(`invalid`), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				srv.UnregisterSession(children[0])
				if mode == "archived" {
					if err := store.UpdateMetadata(children[0], func(m *session.Metadata) { m.Archived = true }); err != nil {
						t.Fatal(err)
					}
				}
			}
			acceptWaitReport(t, c, children[0], "A", status)
			_, out, err := srv.handleChildrenTasksWait(context.Background(), nil, ChildrenTasksWaitInput{SelfID: parent, ChildrenList: children, TaskID: "A", Prompt: "correct A", TimeoutSeconds: 1})
			info := out.Reports[children[0]]
			if err != nil || !out.Success || out.TimedOut || info.Report == nil || info.Report.Status != status || info.Completed != (mode != "enqueue") || len(c.entries) != 1 {
				t.Fatalf("evidence hidden or diagnostic timeout: %+v %v", out, err)
			}
			if mode == "enqueue" && (info.Status != "failed" || !strings.Contains(info.Reason, "enqueue failed") || c.diagnostics[children[0]].Kind != childDiagnosticFailed) {
				t.Fatal("immediate send failure did not produce separate diagnostic")
			}
		})
	}
}

// Embed the existing manager fixture; hook only the lifecycle edges needed for
// deterministic interleavings. No production test hook or polling sleeps.
type reportWaitBarrierManager struct {
	*mockSessionManagerForAutoResume
	onResume  func() (BackgroundSession, error)
	onWaiting func(bool)
}

func (m *reportWaitBarrierManager) ResumeSession(_, _, _ string) (BackgroundSession, error) {
	return m.onResume()
}

func (m *reportWaitBarrierManager) BroadcastWaitingForChildren(_ string, waiting bool) {
	if m.onWaiting != nil {
		m.onWaiting(waiting)
	}
}

func TestChildReportWait_OverlapBeforeEffects(t *testing.T) {
	srv, store, parent, children := setupParentChildSessions(t, 1)
	srv.UnregisterSession(children[0])
	entered, release := make(chan struct{}), make(chan struct{})
	var resumes, broadcasts atomic.Int32
	srv.sessionManager = &reportWaitBarrierManager{
		mockSessionManagerForAutoResume: &mockSessionManagerForAutoResume{},
		onResume: func() (BackgroundSession, error) {
			resumes.Add(1)
			close(entered)
			<-release
			return nil, errors.New("permanent startup failure")
		},
		onWaiting: func(bool) { broadcasts.Add(1) },
	}
	first := make(chan ChildrenTasksWaitOutput, 1)
	go func() {
		_, out, _ := srv.handleChildrenTasksWait(context.Background(), nil, ChildrenTasksWaitInput{SelfID: parent, ChildrenList: children, TaskID: "A", TimeoutSeconds: 5})
		first <- out
	}()
	<-entered
	c, err := srv.getOrCreateCollector(parent)
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	owner := c.waitOwner
	c.mu.Unlock()
	acceptWaitReport(t, c, children[0], "", "completed")
	_, second, err := srv.handleChildrenTasksWait(context.Background(), nil, ChildrenTasksWaitInput{SelfID: parent, ChildrenList: children, TaskID: "B", Prompt: "must not enqueue", TimeoutSeconds: 1})
	msgs, queueErr := store.Queue(children[0]).List()
	if err != nil || second.Success || !strings.Contains(second.Error, "already in progress") || resumes.Load() != 1 || broadcasts.Load() != 0 || queueErr != nil || len(msgs) != 0 {
		t.Fatalf("overlap had side effects: %+v resumes=%d broadcasts=%d queue=%v", second, resumes.Load(), broadcasts.Load(), queueErr)
	}
	c.mu.Lock()
	if c.waitOwner != owner || owner.active || c.currentTaskID != "" || c.entries[0].Association != childReportUnassociated {
		t.Error("conflict mutated reservation/task or preparation inferred association")
	}
	c.mu.Unlock()
	close(release)
	out := <-first
	if !out.Success || out.TimedOut || out.Reports[children[0]].Completed || c.waitOwner != nil {
		t.Fatalf("first owner not completed/cleaned correctly: %+v", out)
	}
	newOwner, err := c.reserveWait()
	if err != nil {
		t.Fatal(err)
	}
	c.clearWait(owner) // delayed old cleanup must never clear a later owner
	if snapshot, _ := c.finalizeWait(owner, true); snapshot != nil {
		t.Fatal("old owner finalized a new reservation")
	}
	if c.waitOwner != newOwner {
		t.Fatal("old cleanup cleared new reservation")
	}
	c.clearWait(newOwner)
}

func TestChildReportWait_HandlerFrozenOutputAndCancellation(t *testing.T) {
	for _, cancelWait := range []bool{false, true} {
		t.Run(map[bool]string{true: "cancel", false: "frozen"}[cancelWait], func(t *testing.T) {
			srv, _, parent, children := setupParentChildSessions(t, 1)
			entered, release := make(chan struct{}), make(chan struct{})
			srv.sessionManager = &reportWaitBarrierManager{
				mockSessionManagerForAutoResume: &mockSessionManagerForAutoResume{},
				onWaiting: func(waiting bool) {
					if waiting {
						close(entered)
						<-release
					}
				},
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan ChildrenTasksWaitOutput, 1)
			go func() {
				_, out, _ := srv.handleChildrenTasksWait(ctx, nil, ChildrenTasksWaitInput{SelfID: parent, ChildrenList: children, TaskID: "A", TimeoutSeconds: 5})
				done <- out
			}()
			<-entered
			c, err := srv.getOrCreateCollector(parent)
			if err != nil {
				t.Fatal(err)
			}
			c.mu.Lock()
			oldOwner := c.waitOwner
			c.mu.Unlock()
			if cancelWait {
				cancel()
			} else {
				acceptWaitReport(t, c, children[0], "", "completed")
				acceptWaitReport(t, c, children[0], "A", "in_progress")
				acceptWaitReport(t, c, children[0], "", "failed")
			}
			close(release)
			out := <-done
			if c.waitOwner != nil || c.isWaiting() {
				t.Fatal("handler did not release wait ownership")
			}
			if cancelWait {
				if out.Success || !strings.Contains(out.Error, "cancelled") {
					t.Fatalf("wrong cancellation: %+v", out)
				}
			} else if !out.Success || out.TimedOut || !out.Reports[children[0]].Completed || out.Reports[children[0]].Report.Status != "completed" || c.entries[2].Association != childReportUnassociated {
				t.Fatalf("handler output wasn't frozen: %+v", out)
			}
			ch, _ := c.startTestWait("B", children)
			c.clearWait(oldOwner)
			if !c.isWaiting() {
				t.Fatal("old handler cleared next active owner")
			}
			assertWaitChannel(t, ch, false)
		})
	}
}
