package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
)

var cutoverPayload = json.RawMessage(`{"status":"stuck","summary":"client evidence","details":"","unknown":9007199254740993}`)

func TestChildReportCutoverMigrationAndReload(t *testing.T) {
	srv, store, parent, children := setupParentChildSessions(t, 1)
	legacy := json.RawMessage(fmt.Sprintf(`{"reports":{%q:{"completed":true,"timestamp":"2026-10-26T12:00:00Z","report":{"status":"completed","summary":"legacy","extra":9007199254740993}}}}`, children[0]))
	if err := store.WriteSessionSidecarJSON(parent, childReportsStateFile, legacy); err != nil {
		t.Fatal(err)
	}
	c, err := srv.getOrCreateCollector(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.entries) != 1 || c.entries[0].Association != childReportUnassociated {
		t.Fatalf("legacy association: %+v", c.entries)
	}
	var backup json.RawMessage
	if err := store.ReadSessionSidecarJSON(parent, childReportsLegacySnapshotFile, &backup); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(backup), "9007199254740993") {
		t.Fatal("snapshot lost raw values")
	}
	for _, payload := range []json.RawMessage{cutoverPayload, cutoverPayload, json.RawMessage(`{"status":"progress","summary":"distinct"}`)} {
		if _, err := c.addReport(children[0], "explicit", payload); err != nil {
			t.Fatal(err)
		}
	}
	// An ordinary wait persist must never replace history with the old DTO.
	c.startWait("next-task", children)
	c.clearWait()
	if err := c.persist(); err != nil {
		t.Fatal(err)
	}
	want := c.historyLocked()
	restarted, err := NewServer(Config{Port: 0}, Dependencies{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := restarted.getOrCreateCollector(parent)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStoredHistory(want, reloaded.historyLocked()) || len(reloaded.entries) != 4 {
		t.Fatal("history/order/identity changed after restart")
	}
	ids := map[string]bool{}
	for _, entry := range reloaded.entries {
		if ids[entry.ID] {
			t.Fatal("duplicate entry identity")
		}
		ids[entry.ID] = true
	}
}

func TestChildReportCutoverAssociation(t *testing.T) {
	for _, tc := range []struct {
		name, waitTask, supplied string
		active, member           bool
		association              childReportAssociation
		effective                string
	}{
		{"explicit", "wait", "supplied", true, true, childReportExplicit, "supplied"},
		{"inferred", "wait", "", true, true, childReportInferred, "wait"},
		{"outside", "wait", "", false, true, childReportUnassociated, ""},
		{"nonmember", "wait", "", true, false, childReportUnassociated, ""},
		{"unnamed", "", "", true, true, childReportUnassociated, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, store, parent, children := setupParentChildSessions(t, 1)
			c, err := loadChildReportCollector(store, parent)
			if err != nil {
				t.Fatal(err)
			}
			c.currentTaskID = tc.waitTask
			if tc.active {
				c.waitCh = make(chan struct{})
			}
			c.waitingFor = map[string]bool{children[0]: tc.member}
			payload := append(json.RawMessage(nil), cutoverPayload...)
			if _, err := c.addReport(children[0], tc.supplied, payload); err != nil {
				t.Fatal(err)
			}
			payload[0] = 'x'
			entry := c.entries[0]
			if entry.Association != tc.association || entry.EffectiveTaskID != tc.effective || entry.SuppliedTaskID != tc.supplied || !json.Valid(entry.Report) {
				t.Fatalf("bad association/ownership: %+v", entry)
			}
			c.clearWait()
			c.startWait("different", children)
			c.clearWait()
			if err := c.persist(); err != nil {
				t.Fatal(err)
			}
			again, err := loadChildReportCollector(store, parent)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(entry, again.entries[0]) { // Ignore JSON indentation only.
				a, _ := json.Marshal(entry)
				b, _ := json.Marshal(again.entries[0])
				if string(a) != string(b) {
					t.Fatal("association reassigned")
				}
			}
		})
	}
}

func TestChildReportCutoverFailureBeforePublication(t *testing.T) {
	srv, store, parent, children := setupParentChildSessions(t, 2)
	c, err := srv.getOrCreateCollector(parent)
	if err != nil {
		t.Fatal(err)
	}
	wait, _ := c.startWait("task", children)
	if _, err := c.addReport(children[0], "task", cutoverPayload); err != nil {
		t.Fatal(err)
	}
	prior := c.historyLocked()
	entered, release := make(chan struct{}), make(chan struct{})
	injected := errors.New("injected durable failure")
	c.writeHistory = func(candidate childReportHistoryState) error {
		if len(c.entries) != 1 || c.reports[children[1]] != nil {
			t.Error("candidate published before write")
		}
		if len(candidate.Entries) != 2 {
			t.Error("incomplete candidate")
		}
		select {
		case <-wait:
			t.Error("wait signaled before write")
		default:
		}
		close(entered)
		<-release
		return injected
	}
	done := make(chan ChildrenTasksReportOutput, 1)
	go func() {
		_, out, _ := srv.handleChildrenTasksReport(context.Background(), nil, ChildrenTasksReportInput{SelfID: children[1], Status: "completed", Summary: "last", TaskID: "task"})
		done <- out
	}()
	<-entered
	select {
	case <-done:
		t.Fatal("returned while write blocked")
	default:
	}
	select {
	case <-wait:
		t.Fatal("signaled while write blocked")
	default:
	}
	close(release)
	out := <-done
	if out.Success || !strings.Contains(out.Error, "retry later") {
		t.Fatalf("not retryable: %+v", out)
	}
	if !equalStoredHistory(prior, c.historyLocked()) || c.reports[children[1]] != nil {
		t.Fatal("failed candidate exposed")
	}
	select {
	case <-wait:
		t.Fatal("failed acceptance signaled")
	default:
	}
	again, err := loadChildReportCollector(store, parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.entries) != 1 {
		t.Fatal("prior acknowledged history lost")
	}
	c.writeHistory = nil
	if _, err := c.addReport(children[1], "task", cutoverPayload); err != nil {
		t.Fatal(err)
	}
	select {
	case <-wait:
	default:
		t.Fatal("durable success failed to signal")
	}
}

func TestChildReportCutoverDeletedParent(t *testing.T) {
	srv, store, parent, children := setupParentChildSessions(t, 1)
	c, err := srv.getOrCreateCollector(parent)
	if err != nil {
		t.Fatal(err)
	}
	wait, _ := c.startWait("task", children)
	if err := store.Delete(parent); err != nil {
		t.Fatal(err)
	}
	if _, err := c.addReport(children[0], "task", cutoverPayload); err == nil {
		t.Fatal("accepted deleted parent")
	}
	if len(c.entries) != 0 {
		t.Fatal("published after deletion")
	}
	select {
	case <-wait:
		t.Fatal("signaled after deletion")
	default:
	}
	if _, err := loadChildReportCollector(store, parent); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("missing parent: %v", err)
	}
}

func TestChildReportCutoverConcurrentLifetime(t *testing.T) {
	srv, store, parent, children := setupParentChildSessions(t, 2)
	c, err := srv.getOrCreateCollector(parent)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	c.writeHistory = func(state childReportHistoryState) error {
		once.Do(func() { close(entered); <-release })
		return writeChildReportHistory(store, parent, state)
	}
	errs := make(chan error, 2)
	go func() { _, err := c.addReport(children[0], "task", cutoverPayload); errs <- err }()
	<-entered
	srv.UnregisterSession(parent)
	retained, err := srv.getOrCreateCollector(parent)
	if err != nil || retained != c {
		t.Fatal("unregister split collector identity")
	}
	secondReady := make(chan struct{})
	go func() {
		second, getErr := srv.getOrCreateCollector(parent)
		if getErr != nil || second != c {
			t.Error("second writer did not retain collector")
		}
		close(secondReady)
		_, out, err := srv.handleChildrenTasksReport(context.Background(), nil, ChildrenTasksReportInput{SelfID: children[1], Status: "completed", Summary: "suspended", TaskID: "task"})
		if err == nil && !out.Success {
			err = errors.New(out.Error)
		}
		errs <- err
	}()
	<-secondReady
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	retained, err = srv.getOrCreateCollector(parent)
	if err != nil || retained != c {
		t.Fatal("suspended report evicted collector")
	}
	again, err := loadChildReportCollector(store, parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.entries) != 2 || again.entries[0].ChildID != children[0] {
		t.Fatal("concurrent accepted history lost/reordered")
	}
}

func TestChildReportCutoverDiagnostics(t *testing.T) {
	for _, origin := range []string{"agent_idle", "session_stopped", "send_failure", "stuck"} {
		t.Run(origin, func(t *testing.T) {
			_, store, parent, children := setupParentChildSessions(t, 1)
			c, err := loadChildReportCollector(store, parent)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.addReport(children[0], "old", cutoverPayload); err != nil {
				t.Fatal(err)
			}
			original := c.entries[0]
			c.startWait("new", children)
			switch origin {
			case "send_failure":
				err = c.markChildFailed(children[0], "send failed")
			case "stuck":
				err = c.markChildStuck(children[0], "new")
			default:
				err = c.markChildAutoCompleted(children[0], origin)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(c.entries) != 1 || len(c.diagnostics) != 1 {
				t.Fatal("diagnostic overwritten/appended evidence")
			}
			again, err := loadChildReportCollector(store, parent)
			if err != nil {
				t.Fatal(err)
			}
			if len(again.diagnostics) != 1 || len(again.entries) != 1 {
				t.Fatal("diagnostic lost on reload")
			}
			c.resetAutoCompletedForRetry(children)
			if _, err := c.addReport(children[0], "new", cutoverPayload); err != nil {
				t.Fatal(err)
			}
			r := c.reports[children[0]]
			if r.AutoCompleted || r.Failed || r.TaskMismatch || r.AutoReason != "" || r.FailMessage != "" || len(c.diagnostics) != 0 {
				t.Fatal("genuine inherited diagnostic flags")
			}
			if len(c.entries) != 2 || !reflect.DeepEqual(original, c.entries[0]) {
				t.Fatal("diagnostic/reset altered history")
			}
			c.clearWait()
			c.startWait("later", children)
			c.clearWait()
			if err := c.persist(); err != nil {
				t.Fatal(err)
			}
			again, err = loadChildReportCollector(store, parent)
			if err != nil {
				t.Fatal(err)
			}
			if len(again.entries) != 2 || len(again.diagnostics) != 0 {
				t.Fatal("transition lost entries or revived diagnostics")
			}
		})
	}
}

func TestChildReportCutoverDiagnosticFailure(t *testing.T) {
	c, _ := loadChildReportCollector(nil, "parent")
	wait, _ := c.startWait("task", []string{"child"})
	c.writeHistory = func(childReportHistoryState) error { return errors.New("failed") }
	if err := c.markChildStuck("child", "task"); err == nil {
		t.Fatal("missing error")
	}
	if len(c.diagnostics) != 0 || c.reports["child"] != nil {
		t.Fatal("failed diagnostic published")
	}
	select {
	case <-wait:
		t.Fatal("failed diagnostic signaled")
	default:
	}
}

func TestChildReportCutoverAppendOrderNotClockOrder(t *testing.T) {
	c, _ := loadChildReportCollector(nil, "parent")
	if _, err := c.addReport("child", "task", cutoverPayload); err != nil {
		t.Fatal(err)
	}
	// Simulate a wall clock adjustment without relying on timing or sleeps.
	c.entries[0].ReceivedAt = time.Now().Add(time.Hour)
	first := c.entries[0].ID
	if _, err := c.addReport("child", "task", cutoverPayload); err != nil {
		t.Fatal(err)
	}
	if c.entries[0].ID != first || !c.entries[0].ReceivedAt.After(c.entries[1].ReceivedAt) {
		t.Fatal("entries sorted by clock")
	}
}

func TestChildReportCutoverLoadFailsClosed(t *testing.T) {
	legacy := json.RawMessage(`{"reports":{"c":{"report":{"status":"done","summary":"legacy"},"completed":true,"timestamp":"2026-10-26T12:00:00Z"}}}`)
	for _, tc := range []struct {
		name            string
		primary, backup json.RawMessage
	}{
		{"future", json.RawMessage(`{"version":99}`), nil},
		{"corrupt", json.RawMessage(`{"version":1,"entries":null,"diagnostics":{}}`), nil},
		{"corrupt-backup", legacy, json.RawMessage(`null`)},
		{"conflicting-backup", legacy, json.RawMessage(`{"reports":{}}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, store, parent, _ := setupParentChildSessions(t, 1)
			if err := store.WriteSessionSidecarJSON(parent, childReportsStateFile, tc.primary); err != nil {
				t.Fatal(err)
			}
			if tc.backup != nil {
				if err := store.WriteSessionSidecarJSON(parent, childReportsLegacySnapshotFile, tc.backup); err != nil {
					t.Fatal(err)
				}
			}
			var before, after json.RawMessage
			if err := store.ReadSessionSidecarJSON(parent, childReportsStateFile, &before); err != nil {
				t.Fatal(err)
			}
			if _, err := srv.getOrCreateCollector(parent); err == nil {
				t.Fatal("invalid history accepted")
			}
			if srv.childReportCollectors[parent] != nil {
				t.Fatal("failed migration cached")
			}
			if err := store.ReadSessionSidecarJSON(parent, childReportsStateFile, &after); err != nil || string(before) != string(after) {
				t.Fatal("failed migration overwrote primary")
			}
			var backup json.RawMessage
			err := store.ReadSessionSidecarJSON(parent, childReportsLegacySnapshotFile, &backup)
			if tc.backup == nil {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatal("failed load created snapshot")
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestChildReportCutoverLegacyDiagnostics(t *testing.T) {
	_, store, parent, _ := setupParentChildSessions(t, 1)
	legacy := json.RawMessage(`{"current_task_id":"B","reports":{
		"scoped":{"report":{"status":"done","summary":"scoped"},"completed":true,"task_id":"A","timestamp":"2026-10-26T12:00:00Z"},
		"mismatch":{"report":{"status":"done","summary":"mismatch"},"completed":true,"task_id":"A","task_mismatch":true,"timestamp":"2026-10-26T12:00:01Z"},
		"idle":{"report":{"status":"auto_completed","summary":"idle"},"completed":true,"auto_completed":true,"auto_reason":"agent_idle","timestamp":"2026-10-26T12:00:02Z"},
		"failed":{"report":{"status":"failed","summary":"send"},"completed":true,"failed":true,"fail_message":"send failed","timestamp":"2026-10-26T12:00:03Z"}}}`)
	if err := store.WriteSessionSidecarJSON(parent, childReportsStateFile, legacy); err != nil {
		t.Fatal(err)
	}
	c, err := loadChildReportCollector(store, parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.entries) != 2 || len(c.diagnostics) != 3 || !c.reports["mismatch"].TaskMismatch || !c.reports["idle"].AutoCompleted || !c.reports["failed"].Failed {
		t.Fatal("legacy evidence/origin mapping lost")
	}
	before := c.historyLocked()
	if err := c.persist(); err != nil {
		t.Fatal(err)
	}
	again, err := loadChildReportCollector(store, parent)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStoredHistory(before, again.historyLocked()) {
		t.Fatal("legacy diagnostic reload changed history")
	}
}

func TestChildReportCutoverCumulativeStuckOrigin(t *testing.T) {
	srv, _, parent, children := setupParentChildSessions(t, 1)
	c, err := srv.getOrCreateCollector(parent)
	if err != nil {
		t.Fatal(err)
	}
	c.currentTaskID = "task"
	c.firstWaitTime = map[string]time.Time{children[0]: time.Now().Add(-2 * maxChildWaitDuration)}
	_, out, err := srv.handleChildrenTasksWait(context.Background(), nil, ChildrenTasksWaitInput{SelfID: parent, ChildrenList: children, TaskID: "task", TimeoutSeconds: 1})
	if err != nil || !out.Success || out.TimedOut {
		t.Fatalf("stuck wait: %+v %v", out, err)
	}
	if len(c.entries) != 0 || c.diagnostics[children[0]].Kind != childDiagnosticStuck {
		t.Fatal("synthetic stuck entered genuine history")
	}
	_, accepted, err := srv.handleChildrenTasksReport(context.Background(), nil, ChildrenTasksReportInput{SelfID: children[0], Status: "stuck", Summary: "client stuck", TaskID: "task"})
	if err != nil || !accepted.Success {
		t.Fatalf("client stuck: %+v %v", accepted, err)
	}
	if len(c.entries) != 1 || len(c.diagnostics) != 0 {
		t.Fatal("client status text misclassified as diagnostic")
	}
}
